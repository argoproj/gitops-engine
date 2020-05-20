package cache

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/pager"

	"github.com/argoproj/gitops-engine/pkg/utils/kube"
)

const (
	clusterSyncTimeout         = 24 * time.Hour
	watchResourcesRetryTimeout = 1 * time.Second
	ClusterRetryTimeout        = 10 * time.Second
)

type apiMeta struct {
	namespaced      bool
	resourceVersion string
	watchCancel     context.CancelFunc
}

// ClusterInfo holds cluster cache stats
type ClusterInfo struct {
	// Server holds cluster API server URL
	Server string
	// K8SVersion holds Kubernetes version
	K8SVersion string
	// ResourcesCount holds number of observed Kubernetes resources
	ResourcesCount int
	// APIsCount holds number of observed Kubernetes API count
	APIsCount int
	// LastCacheSyncTime holds time of most recent cache synchronization
	LastCacheSyncTime *time.Time
	// SyncError holds most recent cache synchronization error
	SyncError error
}

var handlerKey uint64

// OnEventHandler is a function that handles Kubernetes event
type OnEventHandler func(event watch.EventType, un *unstructured.Unstructured)

// OnPopulateResourceInfoHandler returns additional resource metadata that should be stored in cache
type OnPopulateResourceInfoHandler func(un *unstructured.Unstructured, isRoot bool) (info interface{}, cacheManifest bool)

// OnResourceUpdatedHandler handlers resource update event
type OnResourceUpdatedHandler func(newRes *Resource, oldRes *Resource, namespaceResources map[kube.ResourceKey]*Resource)
type Unsubscribe func()

type ClusterCache interface {
	// EnsureSynced checks cache state and synchronizes it if necessary
	EnsureSynced() error
	// GetServerVersion returns observed cluster version
	GetServerVersion() string
	// GetAPIGroups returns information about observed API groups
	GetAPIGroups() []metav1.APIGroup
	// Invalidate cache and executes callback that optionally might update cache settings
	Invalidate(opts ...UpdateSettingsFunc)
	// GetNamespaceTopLevelResources returns top level resources in the specified namespace
	GetNamespaceTopLevelResources(namespace string) map[kube.ResourceKey]*Resource
	// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
	IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources map[kube.ResourceKey]*Resource))
	// IsNamespaced answers if specified group/kind is a namespaced resource API or not
	IsNamespaced(gk schema.GroupKind) (bool, error)
	// GetManagedLiveObjs helps finding matching live K8S resources for a given resources list.
	// The function returns all resources from cache for those `isManaged` function returns true and resources
	// specified in targetObjs list.
	GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error)
	// GetClusterInfo returns cluster cache statistics
	GetClusterInfo() ClusterInfo
	// SetPopulateResourceInfoHandler sets callback that populates resource information in the cache
	SetPopulateResourceInfoHandler(handler OnPopulateResourceInfoHandler)
	// OnResourceUpdated register event handler that is executed every time when resource get's updated in the cache
	OnResourceUpdated(handler OnResourceUpdatedHandler) Unsubscribe
	// OnEvent register event handler that is executed every time when new K8S event received
	OnEvent(handler OnEventHandler) Unsubscribe
}

// NewClusterCache creates new instance of cluster cache
func NewClusterCache(config *rest.Config, opts ...func(cache *clusterCache)) *clusterCache {
	cache := &clusterCache{
		settings:                Settings{ResourceHealthOverride: &noopSettings{}, ResourcesFilter: &noopSettings{}},
		apisMeta:                make(map[schema.GroupKind]*apiMeta),
		resources:               make(map[kube.ResourceKey]*Resource),
		nsIndex:                 make(map[string]map[kube.ResourceKey]*Resource),
		config:                  config,
		kubectl:                 &kube.KubectlCmd{},
		syncTime:                nil,
		resourceUpdatedHandlers: map[uint64]OnResourceUpdatedHandler{},
		eventHandlers:           map[uint64]OnEventHandler{},
		log:                     log.WithField("server", config.Host),
	}
	for i := range opts {
		opts[i](cache)
	}
	return cache
}

type clusterCache struct {
	syncTime      *time.Time
	syncError     error
	apisMeta      map[schema.GroupKind]*apiMeta
	serverVersion string
	apiGroups     []metav1.APIGroup
	// namespacedResources is a simple map which indicates a groupKind is namespaced
	namespacedResources map[schema.GroupKind]bool

	// lock is a rw lock which protects the fields of clusterInfo
	lock      sync.RWMutex
	resources map[kube.ResourceKey]*Resource
	nsIndex   map[string]map[kube.ResourceKey]*Resource

	kubectl    kube.Kubectl
	log        *log.Entry
	config     *rest.Config
	namespaces []string
	settings   Settings

	handlersLock                sync.Mutex
	populateResourceInfoHandler OnPopulateResourceInfoHandler
	resourceUpdatedHandlers     map[uint64]OnResourceUpdatedHandler
	eventHandlers               map[uint64]OnEventHandler
}

// SetPopulateResourceInfoHandler sets callback that populates resource information in the cache
func (c *clusterCache) SetPopulateResourceInfoHandler(handler OnPopulateResourceInfoHandler) {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	c.populateResourceInfoHandler = handler
}

// OnResourceUpdated register event handler that is executed every time when resource get's updated in the cache
func (c *clusterCache) OnResourceUpdated(handler OnResourceUpdatedHandler) Unsubscribe {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	key := atomic.AddUint64(&handlerKey, 1)
	c.resourceUpdatedHandlers[key] = handler
	return func() {
		c.handlersLock.Lock()
		defer c.handlersLock.Unlock()
		delete(c.resourceUpdatedHandlers, key)
	}
}

func (c *clusterCache) getResourceUpdatedHandlers() []OnResourceUpdatedHandler {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	var handlers []OnResourceUpdatedHandler
	for _, h := range c.resourceUpdatedHandlers {
		handlers = append(handlers, h)
	}
	return handlers
}

// OnEvent register event handler that is executed every time when new K8S event received
func (c *clusterCache) OnEvent(handler OnEventHandler) Unsubscribe {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	key := atomic.AddUint64(&handlerKey, 1)
	c.eventHandlers[key] = handler
	return func() {
		c.lock.Lock()
		defer c.lock.Unlock()
		delete(c.eventHandlers, key)
	}
}

func (c *clusterCache) getEventHandlers() []OnEventHandler {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	var handlers []OnEventHandler
	for _, h := range c.eventHandlers {
		handlers = append(handlers, h)
	}
	return handlers
}

// GetServerVersion returns observed cluster version
func (c *clusterCache) GetServerVersion() string {
	return c.serverVersion
}

// GetAPIGroups returns information about observed API groups
func (c *clusterCache) GetAPIGroups() []metav1.APIGroup {
	return c.apiGroups
}

func (c *clusterCache) replaceResourceCache(gk schema.GroupKind, resourceVersion string, objs []unstructured.Unstructured, ns string) {
	info, ok := c.apisMeta[gk]
	if ok {
		objByKey := make(map[kube.ResourceKey]*unstructured.Unstructured)
		for i := range objs {
			objByKey[kube.GetResourceKey(&objs[i])] = &objs[i]
		}

		// update existing nodes
		for i := range objs {
			obj := &objs[i]
			key := kube.GetResourceKey(&objs[i])
			c.onNodeUpdated(c.resources[key], obj)
		}

		for key := range c.resources {
			if key.Kind != gk.Kind || key.Group != gk.Group || ns != "" && key.Namespace != ns {
				continue
			}

			if _, ok := objByKey[key]; !ok {
				c.onNodeRemoved(key)
			}
		}
		info.resourceVersion = resourceVersion
	}
}

func isServiceAccountTokenSecret(un *unstructured.Unstructured) (bool, metav1.OwnerReference) {
	ref := metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       kube.ServiceAccountKind,
	}
	if un.GetKind() != kube.SecretKind || un.GroupVersionKind().Group != "" {
		return false, ref
	}

	if typeVal, ok, err := unstructured.NestedString(un.Object, "type"); !ok || err != nil || typeVal != "kubernetes.io/service-account-token" {
		return false, ref
	}

	annotations := un.GetAnnotations()
	if annotations == nil {
		return false, ref
	}

	id, okId := annotations["kubernetes.io/service-account.uid"]
	name, okName := annotations["kubernetes.io/service-account.name"]
	if okId && okName {
		ref.Name = name
		ref.UID = types.UID(id)
	}
	return ref.Name != "" && ref.UID != "", ref
}

func (c *clusterCache) newResource(un *unstructured.Unstructured) *Resource {
	ownerRefs := un.GetOwnerReferences()
	gvk := un.GroupVersionKind()
	// Special case for endpoint. Remove after https://github.com/kubernetes/kubernetes/issues/28483 is fixed
	if gvk.Group == "" && gvk.Kind == kube.EndpointsKind && len(un.GetOwnerReferences()) == 0 {
		ownerRefs = append(ownerRefs, metav1.OwnerReference{
			Name:       un.GetName(),
			Kind:       kube.ServiceKind,
			APIVersion: "v1",
		})
	}

	// Special case for Operator Lifecycle Manager ClusterServiceVersion:
	if un.GroupVersionKind().Group == "operators.coreos.com" && un.GetKind() == "ClusterServiceVersion" {
		if un.GetAnnotations()["olm.operatorGroup"] != "" {
			ownerRefs = append(ownerRefs, metav1.OwnerReference{
				Name:       un.GetAnnotations()["olm.operatorGroup"],
				Kind:       "OperatorGroup",
				APIVersion: "operators.coreos.com/v1",
			})
		}
	}

	// edge case. Consider auto-created service account tokens as a child of service account objects
	if yes, ref := isServiceAccountTokenSecret(un); yes {
		ownerRefs = append(ownerRefs, ref)
	}

	cacheManifest := false
	var info interface{}
	if c.populateResourceInfoHandler != nil {
		info, cacheManifest = c.populateResourceInfoHandler(un, len(ownerRefs) == 0)
	}
	resource := &Resource{
		ResourceVersion: un.GetResourceVersion(),
		Ref:             kube.GetObjectRef(un),
		OwnerRefs:       ownerRefs,
		Info:            info,
	}
	if cacheManifest {
		resource.Resource = un
	}

	return resource
}

func (c *clusterCache) setNode(n *Resource) {
	key := n.ResourceKey()
	c.resources[key] = n
	ns, ok := c.nsIndex[key.Namespace]
	if !ok {
		ns = make(map[kube.ResourceKey]*Resource)
		c.nsIndex[key.Namespace] = ns
	}
	ns[key] = n
}

// Invalidate cache and executes callback that optionally might update cache settings
func (c *clusterCache) Invalidate(opts ...UpdateSettingsFunc) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.syncTime = nil
	for i := range c.apisMeta {
		c.apisMeta[i].watchCancel()
	}
	for i := range opts {
		opts[i](c)
	}
	c.apisMeta = nil
	c.namespacedResources = nil
	c.log.Warnf("invalidated cluster")
}

func (c *clusterCache) synced() bool {
	syncTime := c.syncTime
	if syncTime == nil {
		return false
	}
	if c.syncError != nil {
		return time.Now().Before(syncTime.Add(ClusterRetryTimeout))
	}
	return time.Now().Before(syncTime.Add(clusterSyncTimeout))
}

func (c *clusterCache) stopWatching(gk schema.GroupKind, ns string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if info, ok := c.apisMeta[gk]; ok {
		info.watchCancel()
		delete(c.apisMeta, gk)
		c.replaceResourceCache(gk, "", []unstructured.Unstructured{}, ns)
		c.log.Warnf("Stop watching: %s not found", gk)
	}
}

// startMissingWatches lists supported cluster resources and start watching for changes unless watch is already running
func (c *clusterCache) startMissingWatches() error {
	apis, err := c.kubectl.GetAPIResources(c.config, c.settings.ResourcesFilter)
	if err != nil {
		return err
	}
	client, err := c.kubectl.NewDynamicClient(c.config)
	if err != nil {
		return err
	}
	namespacedResources := make(map[schema.GroupKind]bool)
	for i := range apis {
		api := apis[i]
		namespacedResources[api.GroupKind] = api.Meta.Namespaced
		if _, ok := c.apisMeta[api.GroupKind]; !ok {
			ctx, cancel := context.WithCancel(context.Background())
			info := &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel}
			c.apisMeta[api.GroupKind] = info

			err = c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {
				go c.watchEvents(ctx, api, info, resClient, ns)
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	c.namespacedResources = namespacedResources
	return nil
}

func runSynced(lock sync.Locker, action func() error) error {
	lock.Lock()
	defer lock.Unlock()
	return action()
}

func (c *clusterCache) watchEvents(ctx context.Context, api kube.APIResourceInfo, info *apiMeta, resClient dynamic.ResourceInterface, ns string) {
	kube.RetryUntilSucceed(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("Recovered from panic: %+v\n%s", r, debug.Stack())
			}
		}()

		err = runSynced(&c.lock, func() error {
			if info.resourceVersion == "" {
				listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
					res, err := resClient.List(opts)
					if err == nil {
						info.resourceVersion = res.GetResourceVersion()
					}
					return res, err
				})
				var items []unstructured.Unstructured
				err = listPager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
					if un, ok := obj.(*unstructured.Unstructured); !ok {
						return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
					} else {
						items = append(items, *un)
					}
					return nil
				})
				if err != nil {
					return fmt.Errorf("failed to load initial state of resource %s: %v", api.GroupKind.String(), err)
				}
				c.replaceResourceCache(api.GroupKind, info.resourceVersion, items, ns)
			}
			return nil
		})

		if err != nil {
			return err
		}

		w, err := resClient.Watch(metav1.ListOptions{ResourceVersion: info.resourceVersion})
		if errors.IsNotFound(err) {
			c.stopWatching(api.GroupKind, ns)
			return nil
		}

		if errors.IsGone(err) {
			info.resourceVersion = ""
			c.log.Warnf("Resource version of %s is too old", api.GroupKind)
		}

		if err != nil {
			return err
		}

		defer w.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case event, ok := <-w.ResultChan():
				if ok {
					obj := event.Object.(*unstructured.Unstructured)
					info.resourceVersion = obj.GetResourceVersion()
					c.processEvent(event.Type, obj)
					if kube.IsCRD(obj) {
						if event.Type == watch.Deleted {
							group, groupOk, groupErr := unstructured.NestedString(obj.Object, "spec", "group")
							kind, kindOk, kindErr := unstructured.NestedString(obj.Object, "spec", "names", "kind")

							if groupOk && groupErr == nil && kindOk && kindErr == nil {
								gk := schema.GroupKind{Group: group, Kind: kind}
								c.stopWatching(gk, ns)
							}
						} else {
							err = runSynced(&c.lock, func() error {
								return c.startMissingWatches()
							})

						}
					}
					if err != nil {
						c.log.Warnf("Failed to start missing watch: %v", err)
					}
				} else {
					return fmt.Errorf("Watch %s on %s has closed", api.GroupKind, c.config.Host)
				}
			}
		}

	}, fmt.Sprintf("watch %s on %s", api.GroupKind, c.config.Host), ctx, watchResourcesRetryTimeout)
}

func (c *clusterCache) processApi(client dynamic.Interface, api kube.APIResourceInfo, callback func(resClient dynamic.ResourceInterface, ns string) error) error {
	resClient := client.Resource(api.GroupVersionResource)
	if len(c.namespaces) == 0 {
		return callback(resClient, "")
	}

	if !api.Meta.Namespaced {
		return nil
	}

	for _, ns := range c.namespaces {
		err := callback(resClient.Namespace(ns), ns)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *clusterCache) sync() (err error) {
	c.log.Info("Start syncing cluster")

	for i := range c.apisMeta {
		c.apisMeta[i].watchCancel()
	}
	c.apisMeta = make(map[schema.GroupKind]*apiMeta)
	c.resources = make(map[kube.ResourceKey]*Resource)
	c.namespacedResources = make(map[schema.GroupKind]bool)
	config := c.config
	version, err := c.kubectl.GetServerVersion(config)

	if err != nil {
		return err
	}
	c.serverVersion = version
	groups, err := c.kubectl.GetAPIGroups(config)
	if err != nil {
		return err
	}
	c.apiGroups = groups
	apis, err := c.kubectl.GetAPIResources(c.config, c.settings.ResourcesFilter)

	if err != nil {
		return err
	}
	client, err := c.kubectl.NewDynamicClient(c.config)
	if err != nil {
		return err
	}
	lock := sync.Mutex{}
	err = kube.RunAllAsync(len(apis), func(i int) error {
		api := apis[i]

		lock.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		info := &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel}
		c.apisMeta[api.GroupKind] = info
		c.namespacedResources[api.GroupKind] = api.Meta.Namespaced
		lock.Unlock()

		return c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {

			listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
				res, err := resClient.List(opts)
				if err == nil {
					lock.Lock()
					info.resourceVersion = res.GetResourceVersion()
					lock.Unlock()
				}
				return res, err
			})

			err = listPager.EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
				if un, ok := obj.(*unstructured.Unstructured); !ok {
					return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
				} else {
					lock.Lock()
					c.setNode(c.newResource(un))
					lock.Unlock()
				}
				return nil
			})

			if err != nil {
				return fmt.Errorf("failed to load initial state of resource %s: %v", api.GroupKind.String(), err)
			}

			go c.watchEvents(ctx, api, info, resClient, ns)

			return nil
		})
	})

	if err != nil {
		log.Errorf("Failed to sync cluster %s: %v", c.config.Host, err)
		return err
	}

	c.log.Info("Cluster successfully synced")
	return nil
}

// EnsureSynced checks cache state and synchronizes it if necessary
func (c *clusterCache) EnsureSynced() error {
	// first check if cluster is synced *without lock*
	if c.synced() {
		return c.syncError
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	// before doing any work, check once again now that we have the lock, to see if it got
	// synced between the first check and now
	if c.synced() {
		return c.syncError
	}
	err := c.sync()
	syncTime := time.Now()
	c.syncTime = &syncTime
	c.syncError = err
	return c.syncError
}

// GetNamespaceTopLevelResources returns top level resources in the specified namespace
func (c *clusterCache) GetNamespaceTopLevelResources(namespace string) map[kube.ResourceKey]*Resource {
	c.lock.RLock()
	defer c.lock.RUnlock()
	resources := make(map[kube.ResourceKey]*Resource)
	for _, res := range c.nsIndex[namespace] {
		if len(res.OwnerRefs) == 0 {
			resources[res.ResourceKey()] = res
		}
	}
	return resources
}

// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
func (c *clusterCache) IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources map[kube.ResourceKey]*Resource)) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if res, ok := c.resources[key]; ok {
		nsNodes := c.nsIndex[key.Namespace]
		action(res, nsNodes)
		childrenByUID := make(map[types.UID][]*Resource)
		for _, child := range nsNodes {
			if res.isParentOf(child) {
				childrenByUID[child.Ref.UID] = append(childrenByUID[child.Ref.UID], child)
			}
		}
		// make sure children has no duplicates
		for _, children := range childrenByUID {
			if len(children) > 0 {
				// The object might have multiple children with the same UID (e.g. replicaset from apps and extensions group). It is ok to pick any object but we need to make sure
				// we pick the same child after every refresh.
				sort.Slice(children, func(i, j int) bool {
					key1 := children[i].ResourceKey()
					key2 := children[j].ResourceKey()
					return strings.Compare(key1.String(), key2.String()) < 0
				})
				child := children[0]
				action(child, nsNodes)
				child.iterateChildren(nsNodes, map[kube.ResourceKey]bool{res.ResourceKey(): true}, action)
			}
		}
	}
}

// IsNamespaced answers if specified group/kind is a namespaced resource API or not
func (c *clusterCache) IsNamespaced(gk schema.GroupKind) (bool, error) {
	if isNamespaced, ok := c.namespacedResources[gk]; ok {
		return isNamespaced, nil
	}
	return false, errors.NewNotFound(schema.GroupResource{Group: gk.Group}, "")
}

// GetManagedLiveObjs helps finding matching live K8S resources for a given resources list.
// The function returns all resources from cache for those `isManaged` function returns true and resources
// specified in targetObjs list.
func (c *clusterCache) GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	managedObjs := make(map[kube.ResourceKey]*unstructured.Unstructured)
	// iterate all objects in live state cache to find ones associated with app
	for key, o := range c.resources {
		if isManaged(o) && o.Resource != nil && len(o.OwnerRefs) == 0 {
			managedObjs[key] = o.Resource
		}
	}
	// but are simply missing our label
	lock := &sync.Mutex{}
	err := kube.RunAllAsync(len(targetObjs), func(i int) error {
		targetObj := targetObjs[i]
		key := kube.GetResourceKey(targetObj)
		lock.Lock()
		managedObj := managedObjs[key]
		lock.Unlock()

		if managedObj == nil {
			if existingObj, exists := c.resources[key]; exists {
				if existingObj.Resource != nil {
					managedObj = existingObj.Resource
				} else {
					var err error
					managedObj, err = c.kubectl.GetResource(c.config, targetObj.GroupVersionKind(), existingObj.Ref.Name, existingObj.Ref.Namespace)
					if err != nil {
						if errors.IsNotFound(err) {
							return nil
						}
						return err
					}
				}
			} else if _, watched := c.apisMeta[key.GroupKind()]; !watched {
				var err error
				managedObj, err = c.kubectl.GetResource(c.config, targetObj.GroupVersionKind(), targetObj.GetName(), targetObj.GetNamespace())
				if err != nil {
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}
			}
		}

		if managedObj != nil {
			converted, err := c.kubectl.ConvertToVersion(managedObj, targetObj.GroupVersionKind().Group, targetObj.GroupVersionKind().Version)
			if err != nil {
				// fallback to loading resource from kubernetes if conversion fails
				log.Warnf("Failed to convert resource: %v", err)
				managedObj, err = c.kubectl.GetResource(c.config, targetObj.GroupVersionKind(), managedObj.GetName(), managedObj.GetNamespace())
				if err != nil {
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}
			} else {
				managedObj = converted
			}
			lock.Lock()
			managedObjs[key] = managedObj
			lock.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return managedObjs, nil
}

func (c *clusterCache) processEvent(event watch.EventType, un *unstructured.Unstructured) {
	for _, h := range c.getEventHandlers() {
		h(event, un)
	}
	key := kube.GetResourceKey(un)
	if event == watch.Modified && skipAppRequeing(key) {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	existingNode, exists := c.resources[key]
	if event == watch.Deleted {
		if exists {
			c.onNodeRemoved(key)
		}
	} else if event != watch.Deleted {
		c.onNodeUpdated(existingNode, un)
	}
}

func (c *clusterCache) onNodeUpdated(oldRes *Resource, un *unstructured.Unstructured) {
	newRes := c.newResource(un)
	c.setNode(newRes)
	for _, h := range c.getResourceUpdatedHandlers() {
		h(newRes, oldRes, c.nsIndex[newRes.Ref.Namespace])
	}
}

func (c *clusterCache) onNodeRemoved(key kube.ResourceKey) {
	existing, ok := c.resources[key]
	if ok {
		delete(c.resources, key)
		ns, ok := c.nsIndex[key.Namespace]
		if ok {
			delete(ns, key)
			if len(ns) == 0 {
				delete(c.nsIndex, key.Namespace)
			}
		}
		for _, h := range c.getResourceUpdatedHandlers() {
			h(nil, existing, ns)
		}
	}
}

var (
	ignoredRefreshResources = map[string]bool{
		"/" + kube.EndpointsKind: true,
	}
)

// GetClusterInfo returns cluster cache statistics
func (c *clusterCache) GetClusterInfo() ClusterInfo {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return ClusterInfo{
		APIsCount:         len(c.apisMeta),
		K8SVersion:        c.serverVersion,
		ResourcesCount:    len(c.resources),
		Server:            c.config.Host,
		LastCacheSyncTime: c.syncTime,
		SyncError:         c.syncError,
	}
}

// skipAppRequeing checks if the object is an API type which we want to skip requeuing against.
// We ignore API types which have a high churn rate, and/or whose updates are irrelevant to the app
func skipAppRequeing(key kube.ResourceKey) bool {
	return ignoredRefreshResources[key.Group+"/"+key.Kind]
}

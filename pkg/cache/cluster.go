package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
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

var (
	// listSemaphore is used to limit the number of concurrent k8s list queries.
	// Global limit is required to avoid memory spikes during cache initialization.
	// The default limit of 50 is chosen based on experiments.
	listSemaphore = semaphore.NewWeighted(50)
)

// SetMaxConcurrentList set maximum number of concurrent K8S list calls.
// If set to 0 then no limit is enforced.
// Note: method is not thread safe. Use it during initialization before executing ClusterCache.EnsureSynced method.
func SetMaxConcurrentList(val int64) {
	if val > 0 {
		listSemaphore = semaphore.NewWeighted(val)
	} else {
		listSemaphore = nil
	}
}

func list(resClient dynamic.ResourceInterface, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if listSemaphore != nil {
		if err := listSemaphore.Acquire(context.Background(), 1); err != nil {
			return nil, err
		}
		defer listSemaphore.Release(1)
	}
	return resClient.List(opts)
}

type apiMeta struct {
	namespaced      bool
	resourceVersion atomic.Value
	watchCancel     context.CancelFunc
}

func (a *apiMeta) LoadResourceVersion() (string, error) {
	v := a.resourceVersion.Load()
	version, ok := v.(string)
	if ok {
		return version, nil
	}

	return "", fmt.Errorf("stored type is invalid in resourceVersion")
}

func (a *apiMeta) StoreResourceVersion(resourceVersion string) {
	a.resourceVersion.Store(resourceVersion)
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
type OnResourceUpdatedHandler func(newRes *Resource, oldRes *Resource, namespaceResources *Resources)
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
	GetNamespaceTopLevelResources(namespace string) *Resources
	// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
	IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources *Resources))
	// IsNamespaced answers if specified group/kind is a namespaced resource API or not
	IsNamespaced(gk schema.GroupKind) (bool, error)
	// GetManagedLiveObjs helps finding matching live K8S resources for a given resources list.
	// The function returns all resources from cache for those `isManaged` function returns true and resources
	// specified in targetObjs list.
	GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error)
	// GetClusterInfo returns cluster cache statistics
	GetClusterInfo() ClusterInfo
	// OnResourceUpdated register event handler that is executed every time when resource get's updated in the cache
	OnResourceUpdated(handler OnResourceUpdatedHandler) Unsubscribe
	// OnEvent register event handler that is executed every time when new K8S event received
	OnEvent(handler OnEventHandler) Unsubscribe
}

// NewClusterCache creates new instance of cluster cache
func NewClusterCache(config *rest.Config, opts ...UpdateSettingsFunc) *clusterCache {
	cache := &clusterCache{
		settings:                Settings{ResourceHealthOverride: &noopSettings{}, ResourcesFilter: &noopSettings{}},
		apisMeta:                sync.Map{},
		resources:               Resources{},
		nsIndex:                 sync.Map{},
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

type Resources struct {
	sync.Map
}

func (r *Resources) StoreResources(key kube.ResourceKey, value *Resource) {
	r.Store(key, value)
}

func (r *Resources) LoadResources(key kube.ResourceKey) (*Resource, error) {
	v, ok := r.Load(key)
	if !ok {
		return nil, fmt.Errorf("not found key: %s in Resources map", key.String())
	}

	res, ok := v.(*Resource)
	if !ok {
		return nil, fmt.Errorf("stored type is invalid in Resources map")
	}

	if res == nil {
		return nil, fmt.Errorf("stored type is nil in Resources map")
	}

	return res, nil
}

func (r *Resources) DeleteResources(key kube.ResourceKey) {
	r.Delete(key)
}

func (r *Resources) Length() int {
	var cnt int
	r.Range(func(key, value interface{}) bool {
		cnt++
		return true
	})
	return cnt
}

type clusterCache struct {
	syncTime      *time.Time
	syncError     error
	apisMeta      sync.Map
	serverVersion string
	apiGroups     []metav1.APIGroup
	// namespacedResources is a simple map which indicates a groupKind is namespaced
	namespacedResources map[schema.GroupKind]bool

	resources Resources
	nsIndex   sync.Map

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

func (c *clusterCache) StoreApisMeta(key schema.GroupKind, value *apiMeta) {
	c.apisMeta.Store(key, value)
}

func (c *clusterCache) LoadApisMeta(key schema.GroupKind) (*apiMeta, error) {
	v, ok := c.apisMeta.Load(key)
	if !ok {
		return nil, fmt.Errorf("not found key: %s in apiMeta map", key.String())
	}

	meta, ok := v.(*apiMeta)
	if !ok {
		return nil, fmt.Errorf("stored type is invalid in apiMeta map")
	}

	if meta == nil {
		return nil, fmt.Errorf("stored type is nil in apiMeta map")
	}

	return meta, nil
}

func (c *clusterCache) DeleteApisMeta(key schema.GroupKind) {
	c.apisMeta.Delete(key)
}

func (c *clusterCache) ApisMetaLength() int {
	var cnt int
	c.apisMeta.Range(func(key, value interface{}) bool {
		cnt++
		return true
	})
	return cnt
}

func (c *clusterCache) StoreNsIndex(key string, value *Resources) {
	c.nsIndex.Store(key, value)
}

func (c *clusterCache) LoadNsIndex(key string) (*Resources, error) {
	v, ok := c.nsIndex.Load(key)
	if !ok {
		return nil, fmt.Errorf("not found key: %s in nsIndex map", key)
	}

	res, ok := v.(*Resources)
	if !ok {
		return nil, fmt.Errorf("stored type is invalid in nsIndex map")
	}

	if res == nil {
		return nil, fmt.Errorf("stored type is nil in nsIndex map")
	}

	return res, nil
}

func (c *clusterCache) DeleteNsIndex(key string) {
	c.nsIndex.Delete(key)
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
		c.handlersLock.Lock()
		defer c.handlersLock.Unlock()
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

func (c *clusterCache) replaceResourceCache(gk schema.GroupKind, resourceVersion string, objs []unstructured.Unstructured, ns string) error {
	info, err := c.LoadApisMeta(gk)
	if err != nil {
		return err
	}
	if info != nil {
		objByKey := make(map[kube.ResourceKey]*unstructured.Unstructured)
		for i := range objs {
			objByKey[kube.GetResourceKey(&objs[i])] = &objs[i]
		}

		// update existing nodes
		for i := range objs {
			obj := &objs[i]
			key := kube.GetResourceKey(&objs[i])
			res, _ := c.resources.LoadResources(key)
			c.onNodeUpdated(res, obj)
		}

		var err error
		c.resources.Range(func(key, value interface{}) bool {
			k, ok := key.(kube.ResourceKey)
			if !ok {
				err = fmt.Errorf("stored type is invalid in apiMeta map")
				return false
			}

			if k.Kind != gk.Kind || k.Group != gk.Group || ns != "" && k.Namespace != ns {
				return true
			}

			if _, ok := objByKey[k]; !ok {
				err = c.onNodeRemoved(k)
				if err != nil {
					return false
				}
			}

			return true
		})
		if err != nil {
			return err
		}
		info.StoreResourceVersion(resourceVersion)
	}

	return nil
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
	c.resources.StoreResources(key, n)
	ns, _ := c.LoadNsIndex(key.Namespace)
	if ns == nil {
		ns = &Resources{}
		c.StoreNsIndex(key.Namespace, ns)
	}
	ns.StoreResources(key, n)
}

// Invalidate cache and executes callback that optionally might update cache settings
func (c *clusterCache) Invalidate(opts ...UpdateSettingsFunc) {
	c.syncTime = nil
	c.apisMeta.Range(func(key, value interface{}) bool {
		meta, ok := value.(*apiMeta)
		if !ok {
			c.log.Warnf("stored type is invalid in apiMeta map")
		}
		meta.watchCancel()
		return true
	})
	for i := range opts {
		opts[i](c)
	}
	c.apisMeta = sync.Map{}
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

func (c *clusterCache) stopWatching(gk schema.GroupKind, ns string) error {
	info, err := c.LoadApisMeta(gk)
	if err != nil {
		return err
	}

	info.watchCancel()
	c.DeleteApisMeta(gk)
	if err := c.replaceResourceCache(gk, "", []unstructured.Unstructured{}, ns); err != nil {
		return err
	}
	c.log.Warnf("Stop watching: %s not found", gk)

	return nil
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
		if _, ok := c.apisMeta.Load(api.GroupKind); !ok {
			ctx, cancel := context.WithCancel(context.Background())
			info := &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel}
			c.apisMeta.Store(api.GroupKind, info)

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

func runSynced(action func() error) error {
	return action()
}

func (c *clusterCache) watchEvents(ctx context.Context, api kube.APIResourceInfo, info *apiMeta, resClient dynamic.ResourceInterface, ns string) {
	kube.RetryUntilSucceed(func() error {
		err := runSynced(func() error {
			version, err := info.LoadResourceVersion()
			if err != nil {
				return err
			}

			if version == "" {
				listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
					res, err := list(resClient, opts)
					if err == nil {
						info.StoreResourceVersion(res.GetResourceVersion())
					}
					return res, err
				})
				var items []unstructured.Unstructured
				err := listPager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
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

				version, err := info.LoadResourceVersion()
				if err != nil {
					return err
				}

				if err := c.replaceResourceCache(api.GroupKind, version, items, ns); err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			return err
		}

		version, err := info.LoadResourceVersion()
		if err != nil {
			return err
		}

		w, err := resClient.Watch(metav1.ListOptions{ResourceVersion: version})
		if errors.IsNotFound(err) {
			if err := c.stopWatching(api.GroupKind, ns); err != nil {
				return err
			}
			return nil
		}

		if errors.IsGone(err) {
			info.StoreResourceVersion("")
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
					info.StoreResourceVersion(obj.GetResourceVersion())
					c.processEvent(event.Type, obj)
					if kube.IsCRD(obj) {
						if event.Type == watch.Deleted {
							group, groupOk, groupErr := unstructured.NestedString(obj.Object, "spec", "group")
							kind, kindOk, kindErr := unstructured.NestedString(obj.Object, "spec", "names", "kind")

							if groupOk && groupErr == nil && kindOk && kindErr == nil {
								gk := schema.GroupKind{Group: group, Kind: kind}
								if err := c.stopWatching(gk, ns); err != nil {
									return err
								}
							}
						} else {
							err = runSynced(func() error {
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

func (c *clusterCache) sync() error {
	c.log.Info("Start syncing cluster")

	c.apisMeta.Range(func(key, value interface{}) bool {
		meta, ok := value.(*apiMeta)
		if !ok {
			c.log.Warnf("stored type is invalid in apiMeta map")
		}
		meta.watchCancel()
		return true
	})

	c.apisMeta = sync.Map{}
	c.resources = Resources{}
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
		c.StoreApisMeta(api.GroupKind, info)
		c.namespacedResources[api.GroupKind] = api.Meta.Namespaced
		lock.Unlock()

		return c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {

			listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
				res, err := list(resClient, opts)
				if err == nil {
					info.StoreResourceVersion(res.GetResourceVersion())
				}
				return res, err
			})

			err := listPager.EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
				if un, ok := obj.(*unstructured.Unstructured); !ok {
					return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
				} else {
					c.setNode(c.newResource(un))
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
func (c *clusterCache) GetNamespaceTopLevelResources(namespace string) *Resources {
	res, _ := c.LoadNsIndex(namespace)
	res.Range(func(key, value interface{}) bool {
		k, ok := key.(kube.ResourceKey)
		if !ok {
			return false
		}

		r, ok := value.(*Resource)
		if !ok {
			return false
		}

		if len(r.OwnerRefs) == 0 {
			res.StoreResources(k, r)
		}

		return true
	})

	return res
}

// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
func (c *clusterCache) IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources *Resources)) {
	res, _ := c.resources.LoadResources(key)
	if res != nil {
		nsNodes, _ := c.LoadNsIndex(key.Namespace)
		action(res, nsNodes)
		childrenByUID := make(map[types.UID][]*Resource)

		nsNodes.Range(func(key, value interface{}) bool {
			child, ok := value.(*Resource)
			if !ok {
				return false
			}

			if res.isParentOf(child) {
				childrenByUID[child.Ref.UID] = append(childrenByUID[child.Ref.UID], child)
			}

			return true
		})

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
	managedObjs := make(map[kube.ResourceKey]*unstructured.Unstructured)
	// iterate all objects in live state cache to find ones associated with app
	var err error
	c.resources.Range(func(key, value interface{}) bool {
		k, ok := key.(kube.ResourceKey)
		if !ok {
			err = fmt.Errorf("stored type is invalid in Resources map")
			return false
		}

		r, ok := value.(*Resource)
		if !ok {
			err = fmt.Errorf("stored type is invalid in Resources map")
			return false
		}

		if isManaged(r) && r.Resource != nil && len(r.OwnerRefs) == 0 {
			managedObjs[k] = r.Resource
		}

		return true
	})
	if err != nil {
		return nil, err
	}

	// but are simply missing our label
	lock := &sync.Mutex{}
	err = kube.RunAllAsync(len(targetObjs), func(i int) error {
		targetObj := targetObjs[i]
		key := kube.GetResourceKey(targetObj)
		lock.Lock()
		managedObj := managedObjs[key]
		lock.Unlock()

		if managedObj == nil {
			existingObj, err := c.resources.LoadResources(key)
			if err != nil {
				return err
			}
			if existingObj != nil {
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
			} else {
				meta, err := c.LoadApisMeta(key.GroupKind())
				if err != nil {
					return err
				}
				if meta == nil {
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

	existingNode, _ := c.resources.LoadResources(key)
	if event == watch.Deleted {
		if existingNode != nil {
			_ = c.onNodeRemoved(key)
		}
	} else if event != watch.Deleted {
		c.onNodeUpdated(existingNode, un)
	}
}

func (c *clusterCache) onNodeUpdated(oldRes *Resource, un *unstructured.Unstructured) {
	newRes := c.newResource(un)
	c.setNode(newRes)
	for _, h := range c.getResourceUpdatedHandlers() {
		r, _ := c.LoadNsIndex(newRes.Ref.Namespace)
		h(newRes, oldRes, r)
	}
}

func (c *clusterCache) onNodeRemoved(key kube.ResourceKey) error {
	existing, err := c.resources.LoadResources(key)
	if err != nil {
		return err
	}
	if existing != nil {
		c.resources.DeleteResources(key)
		ns, err := c.LoadNsIndex(key.Namespace)
		if err != nil {
			return err
		}

		if ns != nil {
			ns.DeleteResources(key)
			if ns.Length() == 0 {
				c.DeleteNsIndex(key.Namespace)
			}
		}
		for _, h := range c.getResourceUpdatedHandlers() {
			h(nil, existing, ns)
		}
	}

	return nil
}

var (
	ignoredRefreshResources = map[string]bool{
		"/" + kube.EndpointsKind: true,
	}
)

// GetClusterInfo returns cluster cache statistics
func (c *clusterCache) GetClusterInfo() ClusterInfo {
	return ClusterInfo{
		APIsCount:         c.ApisMetaLength(),
		K8SVersion:        c.serverVersion,
		ResourcesCount:    c.resources.Length(),
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

package cache

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
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
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/pager"
	watchutil "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2/klogr"

	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/gitops-engine/pkg/utils/tracing"
)

const (
	clusterResyncTimeout       = 24 * time.Hour
	watchResyncTimeout         = 10 * time.Minute
	watchResourcesRetryTimeout = 1 * time.Second
	ClusterRetryTimeout        = 10 * time.Second

	// Same page size as in k8s.io/client-go/tools/pager/pager.go
	defaultListPageSize = 100
	// Prefetch only a single page
	defaultListPageBufferSize = 1
	// listSemaphore is used to limit the number of concurrent memory consuming operations on the
	// k8s list queries results.
	// Limit is required to avoid memory spikes during cache initialization.
	// The default limit of 50 is chosen based on experiments.
	defaultListSemaphoreWeight = 50
)

type apiMeta struct {
	namespaced  bool
	watchCancel context.CancelFunc
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
	// OnResourceUpdated register event handler that is executed every time when resource get's updated in the cache
	OnResourceUpdated(handler OnResourceUpdatedHandler) Unsubscribe
	// OnEvent register event handler that is executed every time when new K8S event received
	OnEvent(handler OnEventHandler) Unsubscribe
}

type WeightedSemaphore interface {
	Acquire(ctx context.Context, n int64) error
	TryAcquire(n int64) bool
	Release(n int64)
}

// NewClusterCache creates new instance of cluster cache
func NewClusterCache(config *rest.Config, opts ...UpdateSettingsFunc) *clusterCache {
	log := klogr.New()
	cache := &clusterCache{
		resyncTimeout:      clusterResyncTimeout,
		settings:           Settings{ResourceHealthOverride: &noopSettings{}, ResourcesFilter: &noopSettings{}},
		apisMeta:           make(map[schema.GroupKind]*apiMeta),
		listPageSize:       defaultListPageSize,
		listPageBufferSize: defaultListPageBufferSize,
		listSemaphore:      semaphore.NewWeighted(defaultListSemaphoreWeight),
		resources:          make(map[kube.ResourceKey]*Resource),
		nsIndex:            make(map[string]map[kube.ResourceKey]*Resource),
		config:             config,
		kubectl: &kube.KubectlCmd{
			Log:    log,
			Tracer: tracing.NopTracer{},
		},
		syncTime:                nil,
		resourceUpdatedHandlers: map[uint64]OnResourceUpdatedHandler{},
		eventHandlers:           map[uint64]OnEventHandler{},
		log:                     log,
	}
	for i := range opts {
		opts[i](cache)
	}
	return cache
}

type clusterCache struct {
	resyncTimeout time.Duration
	syncTime      *time.Time
	syncError     error
	apisMeta      map[schema.GroupKind]*apiMeta
	serverVersion string
	apiGroups     []metav1.APIGroup
	// namespacedResources is a simple map which indicates a groupKind is namespaced
	namespacedResources map[schema.GroupKind]bool

	// size of a page for list operations pager.
	listPageSize int64
	// number of pages to prefetch for list pager.
	listPageBufferSize int32
	listSemaphore      WeightedSemaphore

	// lock is a rw lock which protects the fields of clusterInfo
	lock      sync.RWMutex
	resources map[kube.ResourceKey]*Resource
	nsIndex   map[string]map[kube.ResourceKey]*Resource

	kubectl    kube.Kubectl
	log        logr.Logger
	config     *rest.Config
	namespaces []string
	settings   Settings

	handlersLock                sync.Mutex
	handlerKey                  uint64
	populateResourceInfoHandler OnPopulateResourceInfoHandler
	resourceUpdatedHandlers     map[uint64]OnResourceUpdatedHandler
	eventHandlers               map[uint64]OnEventHandler
}

// OnResourceUpdated register event handler that is executed every time when resource get's updated in the cache
func (c *clusterCache) OnResourceUpdated(handler OnResourceUpdatedHandler) Unsubscribe {
	c.handlersLock.Lock()
	defer c.handlersLock.Unlock()
	key := c.handlerKey
	c.handlerKey++
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
	key := c.handlerKey
	c.handlerKey++
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
	handlers := make([]OnEventHandler, 0, len(c.eventHandlers))
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

func (c *clusterCache) replaceResourceCache(gk schema.GroupKind, resources []*Resource, ns string) {
	objByKey := make(map[kube.ResourceKey]*Resource)
	for i := range resources {
		objByKey[resources[i].ResourceKey()] = resources[i]
	}

	// update existing nodes
	for i := range resources {
		res := resources[i]
		oldRes := c.resources[res.ResourceKey()]
		if oldRes == nil || oldRes.ResourceVersion != res.ResourceVersion {
			c.onNodeUpdated(oldRes, res)
		}
	}

	for key := range c.resources {
		if key.Kind != gk.Kind || key.Group != gk.Group || ns != "" && key.Namespace != ns {
			continue
		}

		if _, ok := objByKey[key]; !ok {
			c.onNodeRemoved(key)
		}
	}
}

func (c *clusterCache) newResource(un *unstructured.Unstructured) *Resource {
	ownerRefs, isInferredParentOf := c.resolveResourceReferences(un)

	cacheManifest := false
	var info interface{}
	if c.populateResourceInfoHandler != nil {
		info, cacheManifest = c.populateResourceInfoHandler(un, len(ownerRefs) == 0)
	}
	var creationTimestamp *metav1.Time
	ct := un.GetCreationTimestamp()
	if !ct.IsZero() {
		creationTimestamp = &ct
	}
	resource := &Resource{
		ResourceVersion:    un.GetResourceVersion(),
		Ref:                kube.GetObjectRef(un),
		OwnerRefs:          ownerRefs,
		Info:               info,
		CreationTimestamp:  creationTimestamp,
		isInferredParentOf: isInferredParentOf,
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

	// update inferred parent references
	if n.isInferredParentOf != nil || mightHaveInferredOwner(n) {
		for k, v := range ns {
			// update child resource owner references
			if n.isInferredParentOf != nil && mightHaveInferredOwner(v) {
				v.setOwnerRef(n.toOwnerRef(), n.isInferredParentOf(k))
			}
			if mightHaveInferredOwner(n) && v.isInferredParentOf != nil {
				n.setOwnerRef(v.toOwnerRef(), v.isInferredParentOf(n.ResourceKey()))
			}
		}
	}
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
	c.log.Info("Invalidated cluster")
}

func (c *clusterCache) synced() bool {
	syncTime := c.syncTime
	if syncTime == nil {
		return false
	}
	if c.syncError != nil {
		return time.Now().Before(syncTime.Add(ClusterRetryTimeout))
	}
	return time.Now().Before(syncTime.Add(c.resyncTimeout))
}

func (c *clusterCache) stopWatching(gk schema.GroupKind, ns string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if info, ok := c.apisMeta[gk]; ok {
		info.watchCancel()
		delete(c.apisMeta, gk)
		c.replaceResourceCache(gk, nil, ns)
		c.log.Info(fmt.Sprintf("Stop watching: %s not found", gk))
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
			c.apisMeta[api.GroupKind] = &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel}

			err = c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {
				go c.watchEvents(ctx, api, resClient, ns, "")
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

// listResources creates list pager and enforces number of concurrent list requests
func (c *clusterCache) listResources(ctx context.Context, resClient dynamic.ResourceInterface, callback func(*pager.ListPager) error) (string, error) {
	if err := c.listSemaphore.Acquire(ctx, 1); err != nil {
		return "", err
	}
	defer c.listSemaphore.Release(1)
	resourceVersion := ""
	listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		start := time.Now()
		res, err := resClient.List(ctx, opts)
		elapsed := time.Since(start)
		if err == nil {
			resourceVersion = res.GetResourceVersion()
			c.log.Info("cluster cache list resources", "elapsed(s)", elapsed.Seconds(), "resource", res.GetResourceVersion())
		} else {
			c.log.Error(err, "cluster cache list resources error", "timeout", opts.TimeoutSeconds)
		}
		return res, err
	})
	listPager.PageBufferSize = c.listPageBufferSize
	listPager.PageSize = c.listPageSize

	return resourceVersion, callback(listPager)
}

func (c *clusterCache) watchEvents(ctx context.Context, api kube.APIResourceInfo, resClient dynamic.ResourceInterface, ns string, resourceVersion string) {
	kube.RetryUntilSucceed(ctx, watchResourcesRetryTimeout, fmt.Sprintf("watch %s on %s", api.GroupKind, c.config.Host), c.log, func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("Recovered from panic: %+v\n%s", r, debug.Stack())
			}
		}()

		// load API initial state if no resource version provided
		if resourceVersion == "" {
			resourceVersion, err = c.listResources(ctx, resClient, func(listPager *pager.ListPager) error {
				var items []*Resource
				err := listPager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
					if un, ok := obj.(*unstructured.Unstructured); !ok {
						return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
					} else {
						items = append(items, c.newResource(un))
					}
					return nil
				})

				if err != nil {
					return fmt.Errorf("failed to load initial state of resource %s: %v", api.GroupKind.String(), err)
				}

				return runSynced(&c.lock, func() error {
					c.replaceResourceCache(api.GroupKind, items, ns)
					return nil
				})
			})

			if err != nil {
				return err
			}
		}

		w, err := watchutil.NewRetryWatcher(resourceVersion, &cache.ListWatch{
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				res, err := resClient.Watch(ctx, options)
				if errors.IsNotFound(err) {
					c.stopWatching(api.GroupKind, ns)
				}
				return res, err
			},
		})
		if err != nil {
			return err
		}

		defer func() {
			w.Stop()
			resourceVersion = ""
		}()

		shouldResync := time.NewTimer(watchResyncTimeout)
		defer shouldResync.Stop()

		for {
			select {
			// stop watching when parent context got cancelled
			case <-ctx.Done():
				return nil

			// re-synchronize API state and restart watch periodically
			case <-shouldResync.C:
				return fmt.Errorf("Resyncing %s on %s during to timeout", api.GroupKind, c.config.Host)

			// re-synchronize API state and restart watch if retry watcher failed to continue watching using provided resource version
			case <-w.Done():
				return fmt.Errorf("Watch %s on %s has closed", api.GroupKind, c.config.Host)

			case event, ok := <-w.ResultChan():
				if !ok {
					return fmt.Errorf("Watch %s on %s has closed", api.GroupKind, c.config.Host)
				}

				obj, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					return fmt.Errorf("Failed to convert to *unstructured.Unstructured: %v", event.Object)
				}

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
						if err != nil {
							c.log.Error(err, "Failed to start missing watch")
						}
					}
				}
			}
		}
	})
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

		var listTimeout int64 = 300
		return c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {
			resourceVersion, err := c.listResources(ctx, resClient, func(listPager *pager.ListPager) error {
				return listPager.EachListItem(context.Background(), metav1.ListOptions{TimeoutSeconds: &listTimeout}, func(obj runtime.Object) error {
					if un, ok := obj.(*unstructured.Unstructured); !ok {
						return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
					} else {
						lock.Lock()
						c.setNode(c.newResource(un))
						lock.Unlock()
					}
					return nil
				})
			})
			if err != nil {
				return fmt.Errorf("failed to load initial state of resource %s: %v", api.GroupKind.String(), err)
			}

			go c.watchEvents(ctx, api, resClient, ns, resourceVersion)

			return nil
		})
	})

	if err != nil {
		return fmt.Errorf("failed to sync cluster %s: %v", c.config.Host, err)
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
	c.lock.RLock()
	defer c.lock.RUnlock()
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
				child.iterateChildren(nsNodes, map[kube.ResourceKey]bool{res.ResourceKey(): true}, func(err error, child *Resource, namespaceResources map[kube.ResourceKey]*Resource) {
					if err != nil {
						c.log.V(2).Info(err.Error())
						return
					}
					action(child, namespaceResources)
				})
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

func (c *clusterCache) managesNamespace(namespace string) bool {
	if len(c.namespaces) == 0 {
		return true
	}
	if namespace == "" {
		return false
	}
	for _, ns := range c.namespaces {
		if ns == namespace {
			return true
		}
	}
	return false
}

// GetManagedLiveObjs helps finding matching live K8S resources for a given resources list.
// The function returns all resources from cache for those `isManaged` function returns true and resources
// specified in targetObjs list.
func (c *clusterCache) GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for _, o := range targetObjs {
		if !c.managesNamespace(o.GetNamespace()) {
			if o.GetNamespace() == "" {
				return nil, fmt.Errorf("Cluster level %s %q can not be managed when in namespaced mode", o.GetKind(), o.GetName())
			}
			return nil, fmt.Errorf("Namespace %q for %s %q is not managed", o.GetNamespace(), o.GetKind(), o.GetName())
		}
	}

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
					managedObj, err = c.kubectl.GetResource(context.TODO(), c.config, targetObj.GroupVersionKind(), existingObj.Ref.Name, existingObj.Ref.Namespace)
					if err != nil {
						if errors.IsNotFound(err) {
							return nil
						}
						return err
					}
				}
			} else if _, watched := c.apisMeta[key.GroupKind()]; !watched {
				var err error
				managedObj, err = c.kubectl.GetResource(context.TODO(), c.config, targetObj.GroupVersionKind(), targetObj.GetName(), targetObj.GetNamespace())
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
				c.log.V(1).Info(fmt.Sprintf("Failed to convert resource: %v", err))
				managedObj, err = c.kubectl.GetResource(context.TODO(), c.config, targetObj.GroupVersionKind(), managedObj.GetName(), managedObj.GetNamespace())
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
		c.onNodeUpdated(existingNode, c.newResource(un))
	}
}

func (c *clusterCache) onNodeUpdated(oldRes *Resource, newRes *Resource) {
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
			// remove ownership references from children with inferred references
			if existing.isInferredParentOf != nil {
				for k, v := range ns {
					if mightHaveInferredOwner(v) && existing.isInferredParentOf(k) {
						v.setOwnerRef(existing.toOwnerRef(), false)
					}
				}
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

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
	"k8s.io/kubectl/pkg/util/openapi"

	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/argoproj/gitops-engine/pkg/utils/tracing"
)

const (
	clusterResyncTimeout       = 24 * time.Hour
	watchResyncTimeout         = 10 * time.Minute
	watchResourcesRetryTimeout = 1 * time.Second
	ClusterRetryTimeout        = 10 * time.Second
	ratioDisplayDelayInSync    = 0.1

	// Same page size as in k8s.io/client-go/tools/pager/pager.go
	defaultListPageSize = 500
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
	// APIGroups holds list of API groups supported by the cluster
	APIGroups []metav1.APIGroup
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
	// GetOpenAPISchema returns open API schema of supported API resources
	GetOpenAPISchema() openapi.Resources
	// Invalidate cache and executes callback that optionally might update cache settings
	Invalidate(opts ...UpdateSettingsFunc)
	// FindResources returns resources that matches given list of predicates from specified namespace or everywhere if specified namespace is empty
	FindResources(namespace string, predicates ...func(r *Resource) bool) map[kube.ResourceKey]*Resource
	// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
	// TODO pointer leaks here, replace of get snapshot of each Resource
	IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources map[kube.ResourceKey]*Resource))
	// IsNamespaced answers if specified group/kind is a namespaced resource API or not
	IsNamespaced(gk schema.GroupKind) (bool, error)
	// GetManagedLiveObjs helps finding matching live K8S resources for a given resources list.
	// The function returns all resources from cache for those `isManaged` function returns true and resources
	// specified in targetObjs list.
	GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error)
	// GetClusterInfo returns cluster cache statistics
	GetClusterInfo() ClusterInfo
	// GetClusterInfoSnapshot returns cluster cache statistics instantly, not thread safe
	GetClusterInfoSnapshot() ClusterInfo
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
		settings:           Settings{ResourceHealthOverride: &noopSettings{}, ResourcesFilter: &noopSettings{}},
		apisMeta:           ApisMetaMap{},
		listPageSize:       defaultListPageSize,
		listPageBufferSize: defaultListPageBufferSize,
		listSemaphore:      semaphore.NewWeighted(defaultListSemaphoreWeight),
		resources:          ResourceMap{},
		nsIndex:            NamespaceResourcesMap{},
		config:             config,
		kubectl: &kube.KubectlCmd{
			Log:    log,
			Tracer: tracing.NopTracer{},
		},
		syncStatus: clusterCacheSync{
			resyncTimeout:      clusterResyncTimeout,
			watchResyncTimeout: watchResyncTimeout,
			syncTime:           nil,
		},
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
	syncStatus clusterCacheSync

	apisMeta      ApisMetaMap
	serverVersion string
	apiGroups     APIGroupList
	// namespacedResources is a simple map which indicates a groupKind is namespaced
	namespacedResources GroupKindBoolMap

	// size of a page for list operations pager.
	listPageSize int64
	// number of pages to prefetch for list pager.
	listPageBufferSize int32
	listSemaphore      WeightedSemaphore

	// lock only using when fields of clusterCache are being modified(re-assigned)
	lock      sync.RWMutex
	resources ResourceMap
	// TODO rename to nsResourcesMap
	nsIndex NamespaceResourcesMap

	kubectl          kube.Kubectl
	log              logr.Logger
	config           *rest.Config
	namespaces       StringList
	clusterResources bool
	settings         Settings

	handlersLock                sync.Mutex
	handlerKey                  uint64
	populateResourceInfoHandler OnPopulateResourceInfoHandler
	resourceUpdatedHandlers     map[uint64]OnResourceUpdatedHandler
	eventHandlers               map[uint64]OnEventHandler

	openAPISchemaLock sync.Mutex
	openAPISchema     openapi.Resources
}

type clusterCacheSync struct {
	// When using this struct:
	// 1) 'lock' mutex should be acquired when reading/writing from fields of this struct.
	// 2) The parent 'clusterCache.lock' does NOT need to be owned to r/w from fields of this struct (if it is owned, that is fine, but see below)
	// 3) To prevent deadlocks, do not acquire parent 'clusterCache.lock' after acquiring this lock; if you need both locks, always acquire the parent lock first
	lock               sync.Mutex
	syncTime           *time.Time
	syncError          error
	resyncTimeout      time.Duration
	watchResyncTimeout time.Duration
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
	return c.apiGroups.All()
}

// GetOpenAPISchema returns open API schema of supported API resources
func (c *clusterCache) GetOpenAPISchema() openapi.Resources {
	return c.openAPISchema
}

func (c *clusterCache) appendAPIGroups(apiGroup metav1.APIGroup) {
	c.apiGroups.AddIfAbsent(apiGroup)
}

func (c *clusterCache) DeleteAPIGroup(apiGroup metav1.APIGroup) {
	c.apiGroups.Remove(apiGroup)
}

func (c *clusterCache) replaceResourceCache(gk schema.GroupKind, resources []*Resource, ns string) {
	objByKey := make(map[kube.ResourceKey]*Resource)
	for i := range resources {
		objByKey[resources[i].ResourceKey()] = resources[i]
	}

	// update existing nodes
	for i := range resources {
		res := resources[i]

		oldRes, loaded := c.resources.LoadOrStore(res.ResourceKey(), res)
		if !loaded {
			continue
		}
		if oldRes == nil || oldRes.ResourceVersion != res.ResourceVersion {
			c.onNodeUpdated(oldRes, res)
		}
	}
	c.resources.Range(func(key kube.ResourceKey, value *Resource) bool {
		if key.Kind != gk.Kind || key.Group != gk.Group || ns != "" && key.Namespace != ns {
			return true
		}

		if _, ok := objByKey[key]; !ok {
			c.onNodeRemoved(key)
		}
		return true
	})
}

func (c *clusterCache) newResource(un *unstructured.Unstructured) *Resource {
	ownerRefs, isInferredParentOf := c.resolveResourceReferences(un)

	cacheManifest := false
	var info interface{}
	// TODO handler should decoupling from newResource, using events
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

func (c *clusterCache) setNode(res *Resource) {
	key := res.ResourceKey()
	c.resources.Store(key, res)

	nsRes, _ := c.nsIndex.LoadOrStore(key.Namespace, &ResourceMap{})
	nsRes.Store(key, res)

	// update inferred parent references
	if res.isInferredParentOf != nil || mightHaveInferredOwner(res) {
		nsRes.Range(func(k kube.ResourceKey, v *Resource) bool {
			// update child resource owner references
			if res.isInferredParentOf != nil && mightHaveInferredOwner(v) {
				v.setOwnerRef(res.toOwnerRef(), res.isInferredParentOf(k))
			}
			if mightHaveInferredOwner(res) && v.isInferredParentOf != nil {
				res.setOwnerRef(v.toOwnerRef(), v.isInferredParentOf(res.ResourceKey()))
			}
			return true
		})
	}
}

// Invalidate cache and executes callback that optionally might update cache settings
func (c *clusterCache) Invalidate(opts ...UpdateSettingsFunc) {
	c.lock.Lock()
	defer c.lock.Unlock()

	// TODO update syncStatus refactor to function
	c.syncStatus.lock.Lock()
	c.syncStatus.syncTime = nil
	c.syncStatus.lock.Unlock()

	c.apisMeta.Range(func(key schema.GroupKind, value *apiMeta) bool {
		value.watchCancel()
		return true
	})
	for i := range opts {
		opts[i](c)
	}
	c.apisMeta = ApisMetaMap{}
	c.namespacedResources = GroupKindBoolMap{}
	c.log.Info("Invalidated cluster")
}

// clusterCacheSync's lock should be held before calling this method
func (syncStatus *clusterCacheSync) synced() bool {
	syncTime := syncStatus.syncTime

	if syncTime == nil {
		return false
	}
	if syncStatus.syncError != nil {
		return time.Now().Before(syncTime.Add(ClusterRetryTimeout))
	}
	return time.Now().Before(syncTime.Add(syncStatus.resyncTimeout))
}

func (c *clusterCache) stopWatching(gk schema.GroupKind, ns string) {
	c.apisMeta.Range(func(key schema.GroupKind, value *apiMeta) bool {
		value.watchCancel()
		c.apisMeta.Delete(key)
		c.replaceResourceCache(gk, nil, ns)
		c.log.Info(fmt.Sprintf("Stop watching: %s not found", gk))
		return true
	})
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
	namespacedResources := map[schema.GroupKind]bool{}
	for i := range apis {
		api := apis[i]
		namespacedResources[api.GroupKind] = api.Meta.Namespaced
		ctx, cancel := context.WithCancel(context.Background())
		_, loaded := c.apisMeta.LoadOrStore(api.GroupKind, &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel})
		if !loaded {
			err = c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {
				go c.watchEvents(ctx, api, resClient, ns, "")
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	c.namespacedResources.Reload(namespacedResources)
	return nil
}

// listResources creates list pager and enforces number of concurrent list requests
func (c *clusterCache) listResources(ctx context.Context, resClient dynamic.ResourceInterface, callback func(*pager.ListPager) error) (string, error) {
	resourceVersion := ""
	listPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		if err := c.listSemaphore.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		res, err := resClient.List(ctx, opts)
		c.listSemaphore.Release(1)
		if err == nil {
			resourceVersion = res.GetResourceVersion()
		}
		return res, err
	})
	listPager.PageBufferSize = c.listPageBufferSize
	listPager.PageSize = c.listPageSize

	return resourceVersion, callback(listPager)
}

func (c *clusterCache) watchEvents(ctx context.Context, api kube.APIResourceInfo, resClient dynamic.ResourceInterface, ns string, resourceVersion string) {
	kube.RetryUntilSucceed(ctx, watchResourcesRetryTimeout, fmt.Sprintf("watch %s in ns %s on %s", api.GroupKind, ns, c.config.Host), c.log, func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("Recovered from panic: %+v\n%s", r, debug.Stack())
			}
		}()

		// load API initial state if no resource version provided
		if resourceVersion == "" {
			resourceVersion, err = c.listResources(ctx, resClient, func(listPager *pager.ListPager) error {
				var items []*Resource
				err = listPager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
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

				c.replaceResourceCache(api.GroupKind, items, ns)
				return nil
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

		shouldResync := time.NewTimer(c.syncStatus.watchResyncTimeout)
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
					var apiGroup metav1.APIGroup
					name, nameOK, nameErr := unstructured.NestedString(obj.Object, "metadata", "name")
					group, groupOk, groupErr := unstructured.NestedString(obj.Object, "spec", "group")
					version, versionOK, versionErr := unstructured.NestedString(obj.Object, "spec", "version")
					if nameOK && nameErr == nil {
						apiGroup.Name = name
						var groupVersions []metav1.GroupVersionForDiscovery
						if groupOk && groupErr == nil && versionOK && versionErr == nil {
							groupVersion := metav1.GroupVersionForDiscovery{
								GroupVersion: group + "/" + version,
								Version:      version,
							}
							groupVersions = append(groupVersions, groupVersion)
						}
						apiGroup.Versions = groupVersions
					}

					if event.Type == watch.Deleted {
						kind, kindOk, kindErr := unstructured.NestedString(obj.Object, "spec", "names", "kind")
						if groupOk && groupErr == nil && kindOk && kindErr == nil {
							gk := schema.GroupKind{Group: group, Kind: kind}
							c.stopWatching(gk, ns)
						}
						// remove CRD's groupkind from c.apigroups
						if nameOK && nameErr == nil {
							c.DeleteAPIGroup(apiGroup)
						}
					} else {
						// add new CRD's groupkind to c.apigroups
						if event.Type == watch.Added && nameOK && nameErr == nil {
							c.appendAPIGroups(apiGroup)
						}
						err = c.startMissingWatches()
						if err != nil {
							c.log.Error(err, "Failed to start missing watch")
						}
					}
					c.openAPISchemaLock.Lock()
					openAPISchema, err := c.kubectl.LoadOpenAPISchema(c.config)
					if err != nil {
						return err
					}
					c.openAPISchema = openAPISchema
					c.openAPISchemaLock.Unlock()
					if err != nil {
						c.log.Error(err, "Failed to reload open api schema")
					}
				}
			}
		}
	})
}

func (c *clusterCache) processApi(client dynamic.Interface, api kube.APIResourceInfo, callback func(resClient dynamic.ResourceInterface, ns string) error) error {
	resClient := client.Resource(api.GroupVersionResource)
	switch {
	// if manage whole cluster or resource is cluster level and cluster resources enabled
	case c.namespaces.Len() == 0 || !api.Meta.Namespaced && c.clusterResources:
		return callback(resClient, "")
	// if manage some namespaces and resource is namespaced
	case c.namespaces.Len() != 0 && api.Meta.Namespaced:
		var err error
		c.namespaces.Range(func(ns string) bool {
			err = callback(resClient.Namespace(ns), ns)
			return err == nil // break loop of Range
		})
		return err
	}

	return nil
}

func (c *clusterCache) sync() error {
	c.log.Info("Start syncing cluster")

	c.lock.Lock()
	defer c.lock.Unlock()
	// stop all running watches
	c.apisMeta.Range(func(key schema.GroupKind, value *apiMeta) bool {
		value.watchCancel()
		return true
	})

	c.apisMeta = ApisMetaMap{}
	c.resources = ResourceMap{}
	c.namespacedResources = GroupKindBoolMap{}
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
	c.apiGroups = APIGroupList{list: groups}
	openAPISchema, err := c.kubectl.LoadOpenAPISchema(config)
	if err != nil {
		return err
	}
	c.openAPISchemaLock.Lock()
	c.openAPISchema = openAPISchema
	c.openAPISchemaLock.Unlock()
	apis, err := c.kubectl.GetAPIResources(c.config, c.settings.ResourcesFilter)
	if err != nil {
		return err
	}
	client, err := c.kubectl.NewDynamicClient(c.config)
	if err != nil {
		return err
	}

	// analytics of RunAllAsync
	resDoneCount := int32(0)
	resWaitingList := sync.Map{}
	for _, api := range apis {
		resWaitingList.Store(api.GroupKind.String(), "")
	}
	// TODO failure toleration, allow some resources failed but continue sync
	// start sync all resources
	err = kube.RunAllAsync(len(apis), func(i int) error {
		api := apis[i]

		ctx, cancel := context.WithCancel(context.Background())
		info := &apiMeta{namespaced: api.Meta.Namespaced, watchCancel: cancel}
		c.apisMeta.Store(api.GroupKind, info)
		c.namespacedResources.Store(api.GroupKind, api.Meta.Namespaced)

		err := c.processApi(client, api, func(resClient dynamic.ResourceInterface, ns string) error {
			resourceVersion, err := c.listResources(ctx, resClient, func(listPager *pager.ListPager) error {
				return listPager.EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
					if un, ok := obj.(*unstructured.Unstructured); !ok {
						return fmt.Errorf("object %s/%s has an unexpected type", un.GroupVersionKind().String(), un.GetName())
					} else {
						c.setNode(c.newResource(un))
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

		resWaitingList.Delete(api.GroupKind.String())
		atomic.AddInt32(&resDoneCount, 1)

		c.log.V(1).Info("Finished syncing resource", "resource", api.GroupKind.String(), "done", atomic.LoadInt32(&resDoneCount), "total", len(apis))
		if len(apis)-int(atomic.LoadInt32(&resDoneCount)) < int(ratioDisplayDelayInSync*float64(len(apis))) {
			var left []string
			resWaitingList.Range(func(key, value interface{}) bool {
				left = append(left, key.(string))
				return true
			})
			c.log.V(1).Info("syncing apis left", "left apis", left)
		}
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to sync cluster %s: %v", c.config.Host, err)
	}

	c.log.Info("Cluster successfully synced")
	return nil
}

// EnsureSynced checks cache state and synchronizes it if necessary
func (c *clusterCache) EnsureSynced() error {
	syncStatus := &c.syncStatus

	// first check if cluster is synced *without acquiring the full clusterCache lock*
	syncStatus.lock.Lock()
	if syncStatus.synced() {
		syncError := syncStatus.syncError
		syncStatus.lock.Unlock()
		return syncError
	}
	syncStatus.lock.Unlock() // release the lock, so that we can acquire the parent lock (see struct comment re: lock acquisition ordering)

	syncStatus.lock.Lock()
	defer syncStatus.lock.Unlock()

	// before doing any work, check once again now that we have the lock, to see if it got
	// synced between the first check and now
	if syncStatus.synced() {
		return syncStatus.syncError
	}
	err := c.sync()
	syncTime := time.Now()
	// TODO update syncStatus refactor to function
	syncStatus.syncTime = &syncTime
	syncStatus.syncError = err
	return syncStatus.syncError
}

// FindResources find the resources by name from cluster-wide resources and namespaced resources.
func (c *clusterCache) FindResources(namespace string, predicates ...func(r *Resource) bool) map[kube.ResourceKey]*Resource {
	result := map[kube.ResourceKey]*Resource{}
	var resources *ResourceMap
	if namespace != "" {
		nsRes, load := c.nsIndex.Load(namespace)
		if load {
			resources = nsRes
		}
	} else {
		resources = &c.resources
	}

	resources.Range(func(k kube.ResourceKey, v *Resource) bool {
		matches := true
		for i := range predicates {
			if !predicates[i](v) {
				matches = false
				break
			}
		}

		if matches {
			result[k] = v
		}
		return true
	})

	return result
}

// IterateHierarchy iterates resource tree starting from the specified top level resource and executes callback for each resource in the tree
func (c *clusterCache) IterateHierarchy(key kube.ResourceKey, action func(resource *Resource, namespaceResources map[kube.ResourceKey]*Resource)) {
	res, ok := c.resources.Load(key)
	if !ok {
		return
	}
	nsNodes, _ := c.nsIndex.Load(key.Namespace)
	nsNodesAll := nsNodes.All()
	action(res, nsNodesAll)
	childrenByUID := make(map[types.UID][]*Resource)
	nsNodes.Range(func(_ kube.ResourceKey, child *Resource) bool {
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
			action(child, nsNodesAll)
			child.iterateChildren(nsNodesAll, map[kube.ResourceKey]bool{res.ResourceKey(): true}, func(err error, child *Resource, namespaceResources map[kube.ResourceKey]*Resource) {
				if err != nil {
					c.log.V(2).Info(err.Error())
					return
				}
				action(child, namespaceResources)
			})
		}
	}
}

// IsNamespaced answers if specified group/kind is a namespaced resource API or not
func (c *clusterCache) IsNamespaced(gk schema.GroupKind) (bool, error) {
	if isNamespaced, ok := c.namespacedResources.Load(gk); ok {
		return isNamespaced, nil
	}
	return false, errors.NewNotFound(schema.GroupResource{Group: gk.Group}, "")
}

func (c *clusterCache) managesNamespace(namespace string) bool {
	return c.namespaces.Contains(namespace)
}

// GetManagedLiveObjs helps find matching live K8S resources for a given resources list.
// The function returns all resources from cache for those `isManaged` function returns true and resources
// specified in targetObjs list.
func (c *clusterCache) GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(r *Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for _, o := range targetObjs {
		if c.namespaces.Len() > 0 {
			if o.GetNamespace() == "" && !c.clusterResources {
				return nil, fmt.Errorf("Cluster level %s %q can not be managed when in namespaced mode", o.GetKind(), o.GetName())
			} else if o.GetNamespace() != "" && !c.managesNamespace(o.GetNamespace()) {
				return nil, fmt.Errorf("Namespace %q for %s %q is not managed", o.GetNamespace(), o.GetKind(), o.GetName())
			}
		}
	}

	managedObjs := make(map[kube.ResourceKey]*unstructured.Unstructured)
	// iterate all objects in live state cache to find ones associated with app
	c.resources.Range(func(key kube.ResourceKey, o *Resource) bool {
		if isManaged(o) && o.Resource != nil && len(o.OwnerRefs) == 0 {
			managedObjs[key] = o.Resource
		}
		return true
	})
	// but are simply missing our label
	lock := &sync.Mutex{}
	err := kube.RunAllAsync(len(targetObjs), func(i int) error {
		targetObj := targetObjs[i]
		key := kube.GetResourceKey(targetObj)
		lock.Lock()
		managedObj := managedObjs[key]
		lock.Unlock()

		if managedObj == nil {
			if existingObj, exists := c.resources.Load(key); exists {
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
			} else if _, watched := c.apisMeta.Load(key.GroupKind()); !watched {
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

// processEvent handles events from the k8s watcher
func (c *clusterCache) processEvent(event watch.EventType, un *unstructured.Unstructured) {
	for _, h := range c.getEventHandlers() {
		h(event, un)
	}
	key := kube.GetResourceKey(un)
	if event == watch.Modified && skipAppRequeing(key) {
		return
	}

	existingNode, exists := c.resources.Load(key)
	// TODO node update not atomic here
	if event == watch.Deleted {
		if exists {
			c.onNodeRemoved(key)
		}
	} else if event != watch.Deleted {
		c.onNodeUpdated(existingNode, c.newResource(un))
	}
}

// onNodeUpdated updates the cache with the new resource
func (c *clusterCache) onNodeUpdated(oldRes *Resource, newRes *Resource) {
	c.setNode(newRes)
	for _, h := range c.getResourceUpdatedHandlers() {
		res, ok := c.nsIndex.Load(newRes.Ref.Namespace)
		if !ok {
			continue
		}
		h(newRes, oldRes, res.All())
	}
}

func (c *clusterCache) onNodeRemoved(key kube.ResourceKey) {
	// TODO load and delete is not atomic here
	existing, ok := c.resources.Load(key)
	if ok {
		c.resources.Delete(key)
		ns, ok := c.nsIndex.Load(key.Namespace)
		// clear resources in namespace
		nsAll := ns.All()
		if ok {
			ns.Delete(key)
			if ns.Len() == 0 {
				c.nsIndex.Delete(key.Namespace)
			}
			// remove ownership references from children with inferred references
			if existing.isInferredParentOf != nil {
				ns.Range(func(k kube.ResourceKey, v *Resource) bool {
					if mightHaveInferredOwner(v) && existing.isInferredParentOf(k) {
						v.setOwnerRef(existing.toOwnerRef(), false)
					}
					return true
				})
			}
		}
		for _, h := range c.getResourceUpdatedHandlers() {
			h(nil, existing, nsAll)
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
	c.syncStatus.lock.Lock()
	defer c.syncStatus.lock.Unlock()

	return ClusterInfo{
		APIsCount:         c.apisMeta.Len(),
		K8SVersion:        c.serverVersion,
		ResourcesCount:    c.resources.Len(),
		Server:            c.config.Host,
		LastCacheSyncTime: c.syncStatus.syncTime,
		SyncError:         c.syncStatus.syncError,
		APIGroups:         c.apiGroups.GetReferrerList(), // TODO using snapshots, references cause leaks
	}
}

// GetClusterInfoSnapshot returns cluster cache statistics
func (c *clusterCache) GetClusterInfoSnapshot() ClusterInfo {
	return ClusterInfo{
		APIsCount:         c.apisMeta.Len(),
		K8SVersion:        c.serverVersion,
		ResourcesCount:    c.resources.Len(),
		Server:            c.config.Host,
		LastCacheSyncTime: c.syncStatus.syncTime,
		SyncError:         c.syncStatus.syncError,
		APIGroups:         c.apiGroups.GetReferrerList(), // TODO using snapshots, references cause leaks
	}
}

// skipAppRequeing checks if the object is an API type which we want to skip requeuing against.
// We ignore API types which have a high churn rate, and/or whose updates are irrelevant to the app
func skipAppRequeing(key kube.ResourceKey) bool {
	return ignoredRefreshResources[key.Group+"/"+key.Kind]
}

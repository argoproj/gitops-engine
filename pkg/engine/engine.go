/*
The package provides high-level interface that leverages "pkg/cache", "pkg/sync", "pkg/health" and "pkg/diff" packages
and "implements" GitOps.

Example

The https://github.com/namix-io/sync-engine/tree/master/agent demonstrates how to use the engine.
*/

package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/namix-io/sync-engine/pkg/cache"
	"github.com/namix-io/sync-engine/pkg/diff"
	"github.com/namix-io/sync-engine/pkg/sync"
	"github.com/namix-io/sync-engine/pkg/sync/common"
	"github.com/namix-io/sync-engine/pkg/utils/kube"
)

const (
	operationRefreshTimeout = time.Second * 1
)

type StopFunc func()

type GitOpsEngine interface {
	// Run initializes engine
	Run() (StopFunc, StopFunc, error)
	// Synchronizes resources in the cluster
	Sync(ctx context.Context, isManaged func(r *cache.Resource) bool, namespace string, opts ...sync.SyncOpt) ([]common.ResourceSyncResult, error)
}

type gitOpsEngine struct {
	vcConfig     *rest.Config
	vcCache      cache.ClusterCache
	targetConfig *rest.Config
	targetCache  cache.ClusterCache
	kubectl      kube.Kubectl
	log          logr.Logger
}

// NewEngine creates new instances of the GitOps engine
func NewEngine(config *rest.Config, clusterCache cache.ClusterCache, targetConfig *rest.Config, targetClusterCache cache.ClusterCache, opts ...Option) GitOpsEngine {
	o := applyOptions(opts)
	return &gitOpsEngine{
		vcConfig:     config,
		vcCache:      clusterCache,
		targetConfig: targetConfig,
		targetCache:  targetClusterCache,
		kubectl:      o.kubectl,
		log:          o.log,
	}
}

func (e *gitOpsEngine) Run() (StopFunc, StopFunc, error) {
	err := e.vcCache.EnsureSynced()
	if err != nil {
		return nil, nil, err
	}

	err = e.targetCache.EnsureSynced()
	if err != nil {
		return nil, nil, err
	}

	return func() {
			e.vcCache.Invalidate()
		}, func() {
			e.targetCache.Invalidate()
		},
		nil
}

func (e *gitOpsEngine) Sync(ctx context.Context,
	isManaged func(r *cache.Resource) bool,
	namespace string,
	opts ...sync.SyncOpt,
) ([]common.ResourceSyncResult, error) {
	resources := e.targetCache.GetUnstructuredResources(namespace, func(r *cache.Resource) bool {
		return  r.Resource != nil  && len(r.OwnerRefs) == 0
	})
	for idx := range resources {
		resources[idx] = sanitizeObjByKind(resources[idx])
	}
	managedResources, err := e.vcCache.GetManagedLiveObjs(resources, isManaged)
	if err != nil {
		return nil, err
	}
	result := sync.Reconcile(resources, managedResources, namespace, e.vcCache)
	diffRes, err := diff.DiffArray(result.Target, result.Live, diff.WithLogr(e.log))
	if err != nil {
		return nil, err
	}
	opts = append(opts, sync.WithSkipHooks(!diffRes.Modified))
	syncCtx, cleanup, err := sync.NewSyncContext("Nmaix", result, e.vcConfig, e.vcConfig, e.kubectl, namespace, e.vcCache.GetOpenAPISchema(), opts...)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	resUpdated := make(chan bool)
	resIgnore := make(chan struct{})
	unsubscribe := e.vcCache.OnResourceUpdated(func(newRes *cache.Resource, oldRes *cache.Resource, namespaceResources map[kube.ResourceKey]*cache.Resource) {
		var key kube.ResourceKey
		if newRes != nil {
			key = newRes.ResourceKey()
		} else {
			key = oldRes.ResourceKey()
		}
		if _, ok := managedResources[key]; ok {
			select {
			case resUpdated <- true:
			case <-resIgnore:
			}
		}
	})
	defer close(resIgnore)
	defer unsubscribe()
	for {
		syncCtx.Sync()
		phase, message, resources := syncCtx.GetState()
		if phase.Completed() {
			if phase == common.OperationError {
				err = fmt.Errorf("sync operation failed: %s", message)
			}
			return resources, err
		}
		select {
		case <-ctx.Done():
			syncCtx.Terminate()
			return resources, ctx.Err()
		case <-time.After(operationRefreshTimeout):
		case <-resUpdated:
		}
	}
}

func sanitizeObjByKind(obj *unstructured.Unstructured) *unstructured.Unstructured {

	dc := obj.DeepCopy()
	switch obj.GetKind() {
	case "Service":
		sanitizeService(dc)
		sanitizeObj(dc)
	case "ServiceAccount":
		sanitizeServiceAccount(dc)
		sanitizeObj(dc)
	default:
		sanitizeObj(dc)
	}

	// annon := dc.GetAnnotations()
	// delete(annon, "kubectl.kubernetes.io/last-applied-configuration")
	// dc.SetAnnotations(annon)
	return dc
}

func sanitizeObj(obj *unstructured.Unstructured) {

	obj.SetUID("")
	obj.SetGeneration(0)
	obj.SetResourceVersion("")
	obj.SetClusterName("")
	obj.SetDeletionTimestamp(nil)
	obj.SetCreationTimestamp(metav1.Time{})
	obj.SetManagedFields(nil)
	unstructured.RemoveNestedField(obj.Object, "status")
	obj.SetAnnotations(nil)
}

func sanitizeService(obj *unstructured.Unstructured)  {
	unstructured.RemoveNestedField(obj.Object, "spec", "clusterIP")
	unstructured.RemoveNestedField(obj.Object, "spec", "clusterIPs")

}

func sanitizeServiceAccount(obj *unstructured.Unstructured)  {
	unstructured.RemoveNestedField(obj.Object, "secrets")
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
}
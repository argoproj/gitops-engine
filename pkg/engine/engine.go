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

type SyncEngine interface {
	// Synchronizes resources in the cluster
	Sync(ctx context.Context, opts ...sync.SyncOpt) ([]common.ResourceSyncResult, error)
}

type gitOpsEngine struct {
	src, dest cache.ClusterCache
	kubectl   kube.Kubectl
	log       logr.Logger
}

// NewEngine creates new instances of the GitOps engine
func NewEngine(src, dest cache.ClusterCache, opts ...Option) SyncEngine {
	o := applyOptions(opts)
	return &gitOpsEngine{
		src:     src,
		dest:    dest,
		kubectl: o.kubectl,
		log:     o.log,
	}
}

func (e *gitOpsEngine) Sync(ctx context.Context,
	opts ...sync.SyncOpt,
) ([]common.ResourceSyncResult, error) {
	if err := e.src.EnsureSynced(ctx); err != nil {
		return nil, fmt.Errorf("failed to sync src cluster: %w", err)
	}

	if err := e.dest.EnsureSynced(ctx); err != nil {
		return nil, fmt.Errorf("failed to sync dest cluster: %w", err)
	}

	resources := e.src.GetUnstructuredResources(func(r *cache.Resource) bool {
		return r.Resource != nil && len(r.OwnerRefs) == 0
	})

	for idx := range resources {
		resources[idx] = sanitizeObjByKind(resources[idx])
	}

	managedResources, err := e.dest.GetManagedLiveObjs(resources, func(r *cache.Resource) bool { return true })
	if err != nil {
		return nil, err
	}

	result := sync.Reconcile(resources, managedResources, e.dest)
	diffRes, err := diff.DiffArray(result.Target, result.Live, diff.WithLogr(e.log))
	if err != nil {
		return nil, err
	}

	opts = append(opts, sync.WithSkipHooks(!diffRes.Modified))
	syncCtx, cleanup, err := sync.NewSyncContext(
		result,
		e.dest.Config(),
		e.dest.Config(),
		e.kubectl,
		e.dest.GetOpenAPISchema(),
		opts...,
	)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	resUpdated := make(chan bool)
	resIgnore := make(chan struct{})

	unsubscribe := e.dest.OnResourceUpdated(func(newRes *cache.Resource, oldRes *cache.Resource, namespaceResources map[kube.ResourceKey]*cache.Resource) {
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
		syncCtx.Sync(ctx)
		phase, message, resources := syncCtx.GetState()
		if phase.Completed() {
			if phase == common.OperationError {
				err = fmt.Errorf("sync operation failed: %s", message)
			}
			return resources, err
		}
		select {
		case <-ctx.Done():
			syncCtx.Terminate(context.Background())
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

func sanitizeService(obj *unstructured.Unstructured) {
	unstructured.RemoveNestedField(obj.Object, "spec", "clusterIP")
	unstructured.RemoveNestedField(obj.Object, "spec", "clusterIPs")

}

func sanitizeServiceAccount(obj *unstructured.Unstructured) {
	unstructured.RemoveNestedField(obj.Object, "secrets")
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
}

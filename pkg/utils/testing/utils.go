package testing

import (
	synccommon "github.com/argoproj/gitops-engine/pkg/sync/common"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
)

func GetResourceResult(resources []synccommon.ResourceSyncResult, resourceKey kube.ResourceKey) *synccommon.ResourceSyncResult {
	for _, res := range resources {
		if res.ResourceKey == resourceKey {
			return &res
		}
	}
	return nil
}

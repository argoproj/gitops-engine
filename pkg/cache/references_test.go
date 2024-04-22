package cache

import (
	"encoding/json"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func Test_isStatefulSetChild(t *testing.T) {
	type args struct {
		un *unstructured.Unstructured
	}

	statefulsetJSON := `{
        "apiVersion": "apps/v1",
        "kind": "StatefulSet",
        "metadata": {
            "name": "sw-broker"
        },
        "spec": {
            "volumeClaimTemplates": [{
                "metadata": {
                    "name": "emqx-data"
                }
            }]
        }
    }`

	// Create a new unstructured object from the JSON string
	un := unstructured.Unstructured{}
	json.Unmarshal([]byte(statefulsetJSON), &un)

	tests := []struct {
		name      string
		args      args
		wantErr   bool
		checkFunc func(func(kube.ResourceKey) bool) bool
	}{
		{
			name:    "Valid PVC for sw-broker",
			args:    args{un: &un},
			wantErr: false,
			checkFunc: func(fn func(kube.ResourceKey) bool) bool {
				// Check a valid PVC name for "sw-broker"
				return fn(kube.ResourceKey{Kind: "PersistentVolumeClaim", Name: "emqx-data-sw-broker-0"})
			},
		},
		{
			name:    "Invalid PVC for sw-broker",
			args:    args{un: &un},
			wantErr: false,
			checkFunc: func(fn func(kube.ResourceKey) bool) bool {
				// Check an invalid PVC name that should belong to "sw-broker-internal"
				return !fn(kube.ResourceKey{Kind: "PersistentVolumeClaim", Name: "emqx-data-sw-broker-internal-0"})
			},
		},
	}

	// Execute test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isStatefulSetChild(tt.args.un)
			assert.Equal(t, tt.wantErr, err != nil, "isStatefulSetChild() error = %v, wantErr %v", err, tt.wantErr)
			if err == nil {
				assert.True(t, tt.checkFunc(got), "Check function failed for %v", tt.name)
			}
		})
	}
}

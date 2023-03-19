package sync

import (
	testingutils "github.com/argoproj/gitops-engine/pkg/utils/testing"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"
)

func Test_taskDependency_match(t *testing.T) {
	tests := []struct {
		name    string
		taskDep taskDependency
		obj     *unstructured.Unstructured
		want    bool
	}{
		{"name/mismatch", taskDependency{Name: "foo"}, testingutils.Unstructured(`{"kind": "Pod", "metadata": {"name": "bar"}}`), false},
		{"name/match", taskDependency{Name: "foo"}, testingutils.Unstructured(`{"kind": "Pod", "metadata": {"name": "foo"}}`), true},
		{"generateName/mismatch", taskDependency{Name: "foo"}, testingutils.Unstructured(`{"kind": "Pod", "metadata": {"generateName": "bar"}}`), false},
		{"generateName/match", taskDependency{Name: "foo"}, testingutils.Unstructured(`{"kind": "Pod", "metadata": {"generateName": "foo"}}`), true},
		{"namespace/mismatch", taskDependency{Namespace: "bar"}, testingutils.Unstructured(`{"kind": "Pod"}`), false},
		{"namespace/match", taskDependency{Namespace: "bar"}, testingutils.Unstructured(`{"kind": "Pod", "metadata": {"namespace": "bar"}}`), true},
		{"kind/mismatch", taskDependency{Kind: "no"}, testingutils.Unstructured(`{"kind": "Pod"}`), false},
		{"kind/match", taskDependency{Kind: "Pod"}, testingutils.Unstructured(`{"kind": "Pod"}`), true},
		{"group/mismatch", taskDependency{Group: "no"}, testingutils.Unstructured(`{"kind": "Pod"}`), false},
		{"group/match", taskDependency{Group: "core"}, testingutils.Unstructured(`{"kind": "Pod", "apiVersion": "core/v1"}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.taskDep.match(tt.obj), "match(%v)", tt.obj)
		})
	}
}

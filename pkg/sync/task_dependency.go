package sync

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

type taskDependency struct {
	Namespace string
	Name      string
	Kind      string
	Group     string
}

// match returns true if the given object matches the dependency
func (d taskDependency) match(obj *unstructured.Unstructured) bool {
	return (obj.GetKind() == d.Kind || d.Kind == "") &&
		(strings.HasPrefix(obj.GetAPIVersion(), d.Group+"/") || d.Group == "") &&
		(obj.GetNamespace() == d.Namespace || d.Namespace == "") &&
		(obj.GetGenerateName() == d.Name || obj.GetName() == d.Name || d.Name == "")
}

package diff

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Normalizer updates resource before comparing it
type Normalizer interface {
	Normalize(un *unstructured.Unstructured) error
}

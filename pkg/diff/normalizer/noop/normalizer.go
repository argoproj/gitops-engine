package noop

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

type NoopNormalizer struct {
}

func (n *NoopNormalizer) Normalize(un *unstructured.Unstructured) error {
	return nil
}

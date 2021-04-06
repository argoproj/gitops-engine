package diff

import (
	"github.com/argoproj/gitops-engine/pkg/diff/normalizer/knowntypes"
	"github.com/argoproj/gitops-engine/pkg/diff/normalizer/noop"
)

// GetNoopNormalizer returns normalizer that does not apply any resource modifications
func GetNoopNormalizer() Normalizer {
	return &noop.NoopNormalizer{}
}

// getNewKnownTypesNormalizer returns a normalizer that supports normalizing CRD fields
// that use known built-in K8S types
func getNewKnownTypesNormalizer() Normalizer {
	return &knowntypes.KnownTypesNormalizer{}
}

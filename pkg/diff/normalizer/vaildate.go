package normalizer

import (
	"github.com/argoproj/gitops-engine/pkg/diff"
	"github.com/argoproj/gitops-engine/pkg/diff/normalizer/knowntypes"
	"github.com/argoproj/gitops-engine/pkg/diff/normalizer/noop"
)

// compile time validation of adherance to the "interface"
var _ diff.Normalizer = &noop.NoopNormalizer{}
var _ diff.Normalizer = &knowntypes.KnownTypesNormalizer{}

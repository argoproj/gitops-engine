package helm

import (
	"testing"

	. "github.com/namix-io/gitops-engine/pkg/utils/testing"

	"github.com/stretchr/testify/assert"
)

func TestWeight(t *testing.T) {
	assert.Equal(t, Weight(NewPod()), 0)
	assert.Equal(t, Weight(Annotate(NewPod(), "helm.sh/hook-weight", "1")), 1)
}

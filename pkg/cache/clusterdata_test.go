package cache

import (
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"testing"
)

func TestGroupKindBoolMap_Reload(t *testing.T) {
	m := &GroupKindBoolMap{}
	m.Store(schema.GroupKind{Group: "group", Kind: "k1"}, true)
	m.Store(schema.GroupKind{Group: "group", Kind: "k2"}, true)
	m.Reload(map[schema.GroupKind]bool{
        {Group: "group", Kind: "k3"}: true,
    })

	assert.Equal(t, 1, m.Len())
}

func TestNamespaceResourcesMap_LoadAndDelete(t *testing.T) {
	m := NamespaceResourcesMap{}
	m.Store("a", &ResourceMap{})
	m.Store("b", &ResourceMap{})
	existed, _ := m.Load("a")
	m.Delete("a")
	assert.NotNil(t, existed)
	assert.NotNil(t, existed.All())
}

func TestResourceMap_LoadAndDelete(t *testing.T) {
	m := ResourceMap{}
	keyA := kube.ResourceKey{
		Group:     "g",
		Kind:      "k",
		Namespace: "n",
		Name:      "a",
	}
	m.Store(keyA, &Resource{})
	keyB := kube.ResourceKey{
		Group:     "g",
		Kind:      "k",
		Namespace: "n",
		Name:      "b",
	}
	m.Store(keyB, &Resource{})
	existed, _ := m.LoadAndDelete(keyA)
	assert.NotNil(t, existed)
}

package knowntypes

import (
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	someCRDYaml = `apiVersion: some.io/v1alpha1
kind: TestCRD
metadata:
  name: canary-demo
spec:
  template:
    metadata:
      labels:
        app: canary-demo
    spec:
      containers:
      - image: something:latest
        name: canary-demo
        volumeMounts:
        - name: config-volume
          mountPath: /etc/config
          readOnly: false	
        resources:
          requests:
            cpu: 2000m
            memory: 32Mi`
	crdGroupKind = "some.io/TestCRD"
)

func mustUnmarshalYAML(yamlStr string) *unstructured.Unstructured {
	un := &unstructured.Unstructured{}
	err := yaml.Unmarshal([]byte(yamlStr), un)
	if err != nil {
		log.Fatal(err)
	}
	return un
}

// nolint:unparam
func nestedSliceMap(obj map[string]interface{}, i int, path ...string) (map[string]interface{}, error) {
	items, ok, err := unstructured.NestedSlice(obj, path...)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("field %s not found", strings.Join(path, "."))
	}
	if len(items) < i {
		return nil, fmt.Errorf("field %s has less than %d items", strings.Join(path, "."), i)
	}
	if item, ok := items[i].(map[string]interface{}); !ok {
		return nil, fmt.Errorf("field %s[%d] is not map", strings.Join(path, "."), i)
	} else {
		return item, nil
	}
}

func TestNormalize_MapField(t *testing.T) {

	n := &KnownTypesNormalizer{
		typeFields: map[schema.GroupKind][]KnownTypeField{},
	}
	err := n.addKnownField(schema.GroupKind{Group: "some.io", Kind: "TestCRD"}, "spec.template.spec", "core/v1/PodSpec")
	if !assert.NoError(t, err) {
		return
	}

	rollout := mustUnmarshalYAML(someCRDYaml)

	err = n.Normalize(rollout)
	if !assert.NoError(t, err) {
		return
	}

	container, err := nestedSliceMap(rollout.Object, 0, "spec", "template", "spec", "containers")
	if !assert.NoError(t, err) {
		return
	}

	cpu, ok, err := unstructured.NestedFieldNoCopy(container, "resources", "requests", "cpu")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}

	assert.Equal(t, "2", cpu)

	volumeMount, err := nestedSliceMap(container, 0, "volumeMounts")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}

	_, ok, err = unstructured.NestedBool(volumeMount, "readOnly")
	assert.NoError(t, err)
	assert.False(t, ok)
}

func TestNormalize_FieldInNestedSlice(t *testing.T) {
	rollout := mustUnmarshalYAML(someCRDYaml)

	n := &KnownTypesNormalizer{
		typeFields: map[schema.GroupKind][]KnownTypeField{},
	}
	err := n.addKnownField(schema.GroupKind{Group: "some.io", Kind: "TestCRD"}, "spec.template.spec.containers", "core/v1/Container")
	if !assert.NoError(t, err) {
		return
	}

	err = n.Normalize(rollout)
	if !assert.NoError(t, err) {
		return
	}

	container, err := nestedSliceMap(rollout.Object, 0, "spec", "template", "spec", "containers")
	if !assert.NoError(t, err) {
		return
	}

	cpu, ok, err := unstructured.NestedFieldNoCopy(container, "resources", "requests", "cpu")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}

	assert.Equal(t, "2", cpu)
}

func TestNormalize_FieldInDoubleNestedSlice(t *testing.T) {
	rollout := mustUnmarshalYAML(`apiVersion: some.io/v1alpha1
kind: TestCRD
metadata:
  name: canary-demo
spec:
  templates:
    - metadata:
       labels:
         app: canary-demo
      spec:
        containers:
        - image: argoproj/rollouts-demo:yellow
          name: canary-demo
          volumeMounts:
          - name: config-volume
            mountPath: /etc/config
            readOnly: false
          resources:
            requests:
              cpu: 2000m
              memory: 32Mi`)

	n := &KnownTypesNormalizer{
		typeFields: map[schema.GroupKind][]KnownTypeField{},
	}
	err := n.addKnownField(schema.GroupKind{Group: "some.io", Kind: "TestCRD"}, "spec.templates.spec.containers", "core/v1/Container")
	if !assert.NoError(t, err) {
		return
	}
	err = n.Normalize(rollout)
	if !assert.NoError(t, err) {
		return
	}

	template, err := nestedSliceMap(rollout.Object, 0, "spec", "templates")
	if !assert.NoError(t, err) {
		return
	}

	container, err := nestedSliceMap(template, 0, "spec", "containers")
	if !assert.NoError(t, err) {
		return
	}

	cpu, ok, err := unstructured.NestedFieldNoCopy(container, "resources", "requests", "cpu")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}
	assert.Equal(t, "2", cpu)
}

func TestNormalize_Quantity(t *testing.T) {
	rollout := mustUnmarshalYAML(`apiVersion: some.io/v1alpha1
kind: TestCRD
metadata:
  name: canary-demo
spec:
  ram: 1.25G`)

	n := &KnownTypesNormalizer{
		typeFields: map[schema.GroupKind][]KnownTypeField{},
	}
	err := n.addKnownField(schema.GroupKind{Group: "some.io", Kind: "TestCRD"}, "spec.ram", "core/Quantity")
	if !assert.NoError(t, err) {
		return
	}

	if !assert.NoError(t, err) {
		return
	}

	err = n.Normalize(rollout)
	if !assert.NoError(t, err) {
		return
	}

	ram, ok, err := unstructured.NestedFieldNoCopy(rollout.Object, "spec", "ram")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}
	assert.Equal(t, "1250M", ram)
}

func TestFieldDoesNotExist(t *testing.T) {
	rollout := mustUnmarshalYAML(someCRDYaml)

	n := &KnownTypesNormalizer{
		typeFields: map[schema.GroupKind][]KnownTypeField{},
	}
	err := n.addKnownField(schema.GroupKind{Group: "some.io", Kind: "TestCRD"}, "fieldDoesNotExist", "core/v1/PodSpec")
	if !assert.NoError(t, err) {
		return
	}

	err = n.Normalize(rollout)
	if !assert.NoError(t, err) {
		return
	}

	container, err := nestedSliceMap(rollout.Object, 0, "spec", "template", "spec", "containers")
	if !assert.NoError(t, err) {
		return
	}

	cpu, ok, err := unstructured.NestedFieldNoCopy(container, "resources", "requests", "cpu")
	if !assert.NoError(t, err) || !assert.True(t, ok) {
		return
	}

	assert.Equal(t, "2000m", cpu)
}

func TestKnownTypes(t *testing.T) {
	typesData, err := ioutil.ReadFile("./diffing_known_types.txt")
	if !assert.NoError(t, err) {
		return
	}
	for _, typeName := range strings.Split(string(typesData), "\n") {
		if typeName = strings.TrimSpace(typeName); typeName == "" {
			continue
		}
		fn, ok := knownTypes[typeName]
		if !assert.True(t, ok) {
			continue
		}
		assert.NotNil(t, fn())
	}
}

package knowntypes

import (
	"encoding/json"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

//go:generate go run github.com/argoproj/gitops-engine/generate/knowntypes corev1 k8s.io/api/core/v1 corev1_known_types.go --docs diffing_known_types.txt
var knownTypes = map[string]func() interface{}{}

type KnownTypeField struct {
	fieldPath  []string
	newFieldFn func() interface{}
}

type KnownTypesNormalizer struct {
	typeFields map[schema.GroupKind][]KnownTypeField
}

func init() {
	knownTypes["core/v1/PodSpec"] = func() interface{} {
		return &v1.PodSpec{}
	}
	knownTypes["core/v1/Container"] = func() interface{} {
		return &v1.Container{}
	}
	knownTypes["core/Quantity"] = func() interface{} {
		return &resource.Quantity{}
	}

}

func (n *KnownTypesNormalizer) addKnownField(gk schema.GroupKind, fieldPath string, typePath string) error {
	newFieldFn, ok := knownTypes[typePath]
	if !ok {
		return fmt.Errorf("type '%s' is not supported", typePath)
	}
	n.typeFields[gk] = append(n.typeFields[gk], KnownTypeField{
		fieldPath:  strings.Split(fieldPath, "."),
		newFieldFn: newFieldFn,
	})
	return nil
}

func normalize(obj map[string]interface{}, field KnownTypeField, fieldPath []string) error {
	for i := range fieldPath {
		if nestedField, ok, err := unstructured.NestedFieldNoCopy(obj, fieldPath[:i+1]...); err == nil && ok {
			items, ok := nestedField.([]interface{})
			if !ok {
				continue
			}
			for j := range items {
				item, ok := items[j].(map[string]interface{})
				if !ok {
					continue
				}

				subPath := fieldPath[i+1:]
				if len(subPath) == 0 {
					newItem, err := remarshal(item, field)
					if err != nil {
						return err
					}
					items[j] = newItem
				} else {
					if err = normalize(item, field, subPath); err != nil {
						return err
					}
				}
			}
			return unstructured.SetNestedSlice(obj, items, fieldPath[:i+1]...)
		}
	}

	if fieldVal, ok, err := unstructured.NestedFieldNoCopy(obj, fieldPath...); ok && err == nil {
		newFieldVal, err := remarshal(fieldVal, field)
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj, newFieldVal, fieldPath...)
		if err != nil {
			return err
		}
	}

	return nil
}

func remarshal(fieldVal interface{}, field KnownTypeField) (interface{}, error) {
	data, err := json.Marshal(fieldVal)
	if err != nil {
		return nil, err
	}
	typedValue := field.newFieldFn()
	err = json.Unmarshal(data, typedValue)
	if err != nil {
		return nil, err
	}
	data, err = json.Marshal(typedValue)
	if err != nil {
		return nil, err
	}
	var newFieldVal interface{}
	err = json.Unmarshal(data, &newFieldVal)
	if err != nil {
		return nil, err
	}
	return newFieldVal, nil
}

func (n *KnownTypesNormalizer) Normalize(un *unstructured.Unstructured) error {
	if fields, ok := n.typeFields[un.GroupVersionKind().GroupKind()]; ok {
		for _, field := range fields {
			err := normalize(un.Object, field, field.fieldPath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

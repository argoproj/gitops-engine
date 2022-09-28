package fieldmanager

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"sigs.k8s.io/structured-merge-diff/v4/merge"
)

func NewVersionConverter(gvkParser *managedfields.GvkParser, o runtime.ObjectConvertor, h schema.GroupVersion) merge.Converter {
	tc := &typeConverter{parser: gvkParser}
	return newVersionConverter(tc, o, h)
}

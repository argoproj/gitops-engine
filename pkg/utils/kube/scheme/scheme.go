package scheme

import (
	"k8s.io/apimachinery/pkg/runtime"
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/client-go/kubernetes/scheme"
)

var Scheme = runtime.NewScheme()

func init() {
	runtimeutil.Must(scheme.AddToScheme(Scheme))
}

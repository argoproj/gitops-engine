module github.com/argoproj/gitops-engine

go 1.15

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/go-logr/logr v0.3.0
	github.com/golang/mock v1.4.4
	github.com/spf13/cobra v1.1.1
	github.com/stretchr/testify v1.6.1
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	k8s.io/api v0.20.4
	k8s.io/apiextensions-apiserver v0.20.4
	k8s.io/apimachinery v0.20.4
	k8s.io/cli-runtime v0.20.4
	k8s.io/client-go v0.20.4
	k8s.io/klog/v2 v2.4.0
	k8s.io/kube-aggregator v0.20.4
	k8s.io/kubectl v0.20.4
	sigs.k8s.io/yaml v1.2.0
)

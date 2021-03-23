module github.com/argoproj/gitops-engine

go 1.15

require (
	github.com/chai2010/gettext-go v0.0.0-20170215093142-bf70f2a70fb1 // indirect
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/ghodss/yaml v1.0.0
	github.com/go-logr/logr v0.4.0
	github.com/golang/mock v1.4.4
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/moby/term v0.0.0-20201110203204-bea5bbe245bf // indirect
	github.com/onsi/ginkgo v1.14.2 // indirect
	github.com/onsi/gomega v1.10.3 // indirect
	github.com/spf13/cobra v1.1.1
	github.com/stretchr/testify v1.6.1
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	google.golang.org/appengine v1.6.7 // indirect
	k8s.io/api v0.20.1
	k8s.io/apiextensions-apiserver v0.20.1
	k8s.io/apimachinery v0.20.1
	k8s.io/cli-runtime v0.20.1
	k8s.io/client-go v11.0.1-0.20190816222228-6d55c1b1f1ca+incompatible
	k8s.io/klog/v2 v2.5.0
	k8s.io/kube-aggregator v0.20.1
	k8s.io/kubectl v0.20.1
	k8s.io/kubernetes v1.20.1
	sigs.k8s.io/yaml v1.2.0
)

replace (
	// https://github.com/kubernetes/kubernetes/issues/79384#issuecomment-505627280
	k8s.io/api => k8s.io/api v0.20.1
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.1 // indirect
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.1 // indirect
	k8s.io/apiserver => k8s.io/apiserver v0.20.1
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.20.1
	k8s.io/client-go => k8s.io/client-go v0.20.1
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.20.1
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.20.1
	k8s.io/code-generator => k8s.io/code-generator v0.20.1
	k8s.io/component-base => k8s.io/component-base v0.20.1
	k8s.io/component-helpers => k8s.io/component-helpers v0.20.1
	k8s.io/controller-manager => k8s.io/controller-manager v0.20.1
	k8s.io/cri-api => k8s.io/cri-api v0.20.1
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.20.1
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.20.1
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.20.1
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.20.1
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.20.1
	k8s.io/kubectl => k8s.io/kubectl v0.20.1
	k8s.io/kubelet => k8s.io/kubelet v0.20.1
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.20.1
	k8s.io/metrics => k8s.io/metrics v0.20.1
	k8s.io/mount-utils => k8s.io/mount-utils v0.20.1
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.20.1
)

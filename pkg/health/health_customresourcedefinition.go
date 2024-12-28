package health

import (
	"fmt"

	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func getCustomResourceDefinitionHealth(obj *unstructured.Unstructured) (*HealthStatus, error) {
	gvk := obj.GroupVersionKind()
	switch gvk {
	case apiextensionsv1.SchemeGroupVersion.WithKind(kube.CustomResourceDefinitionKind):
		var crd apiextensionsv1.CustomResourceDefinition
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured CustomResourceDefinition to typed: %v", err)
		}
		return getApiExtenstionsV1CustomResourceDefinitionHealth(&crd)
	default:
		return nil, fmt.Errorf("unsupported CustomResourceDefinition GVK: %s", gvk)
	}
}

func getApiExtenstionsV1CustomResourceDefinitionHealth(crd *apiextensionsv1.CustomResourceDefinition) (*HealthStatus, error) {
	if crd.ObjectMeta.DeletionTimestamp != nil {
		return &HealthStatus{
			Status:  HealthStatusProgressing,
			Message: "CRD is being terminated",
		}, nil
	}
	if crd.Status.Conditions == nil {
		return &HealthStatus{
			Status:  HealthStatusProgressing,
			Message: "Status conditions not found",
		}, nil
	}

	var (
		isEstablished  bool
		hasViolations  bool
		violationMsg   string
		establishedMsg string
	)

	// Check conditions
	for _, condition := range crd.Status.Conditions {
		switch condition.Type {
		case apiextensionsv1.Established:
			if condition.Status == apiextensionsv1.ConditionTrue {
				isEstablished = true
			} else {
				establishedMsg = condition.Message
			}
		case "NonStructuralSchema":
			if condition.Status == apiextensionsv1.ConditionTrue {
				hasViolations = true
				violationMsg = condition.Message
			}
		}
	}

	// Return appropriate health status
	switch {
	case !isEstablished:
		return &HealthStatus{
			Status:  HealthStatusDegraded,
			Message: fmt.Sprintf("CRD is not established: %s", establishedMsg),
		}, nil
	case hasViolations:
		return &HealthStatus{
			Status:  HealthStatusDegraded,
			Message: fmt.Sprintf("Schema violations found: %s", violationMsg),
		}, nil
	default:
		return &HealthStatus{
			Status:  HealthStatusHealthy,
			Message: "CRD is healthy",
		}, nil
	}
}

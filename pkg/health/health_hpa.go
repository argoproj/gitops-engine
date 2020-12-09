package health

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type hpaConditions struct {
	Type    string
	Reason  string
	Message string
}

func is_degraded(condition *hpaConditions) bool {
	degraded_states := []hpaConditions{
		{Type: "AbleToScale", Reason: "FailedGetScale"},
		{Type: "AbleToScale", Reason: "FailedUpdateScale"},
		{Type: "ScalingActive", Reason: "FailedGetResourceMetric"},
		{Type: "ScalingActive", Reason: "InvalidSelector"},
	}
	for _, degraded_state := range degraded_states {
		if condition.Type == degraded_state.Type && condition.Reason == degraded_state.Reason {
			return true
		}
	}
	return false
}

func getAutoscalingv1HPAHealth(obj *unstructured.Unstructured) (*HealthStatus, error) {
	healthyStatus := &HealthStatus{
		Status:  HealthStatusHealthy,
		Message: "",
	}

	annotation, ok := obj.GetAnnotations()["autoscaling.alpha.kubernetes.io/conditions"]
	if !ok {
		return healthyStatus, nil
	}

	var conditions []hpaConditions
	err := json.Unmarshal([]byte(annotation), &conditions)
	if err != nil {
		return healthyStatus, nil
	}

	if len(conditions) == 0 {
		return healthyStatus, nil
	}

	for _, condition := range conditions {
		if is_degraded(&condition) {
			return &HealthStatus{
				Status:  HealthStatusDegraded,
				Message: condition.Message,
			}, nil
		}
		if (condition.Type == "AbleToScale" && condition.Reason == "SucceededRescale") ||
			(condition.Type == "ScalingLimited" && condition.Reason == "DesiredWithinRange") {
			return &HealthStatus{
				Status:  HealthStatusHealthy,
				Message: condition.Message,
			}, nil
		}
	}
	return &HealthStatus{
		Status:  HealthStatusProgressing,
		Message: "Waiting to Autoscale",
	}, nil
}

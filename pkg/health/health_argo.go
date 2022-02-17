package health

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type workflowPhase string

// Workflow statuses
// See: https://github.com/argoproj/argo-workflows/blob/master/pkg/apis/workflow/v1alpha1/workflow_phase.go
const (
	workflowPending workflowPhase = "Pending"
	workflowRunning   workflowPhase = "Running"
	workflowSucceeded workflowPhase = "Succeeded"
	workflowFailed workflowPhase = "Failed"
	workflowError  workflowPhase = "Error"
)

// An agnostic Workflow object only considers Status.Phase and Status.Message. It is agnostic to the API version or any
// other fields.
type argoWorkflow struct {
	Status struct {
		Phase   workflowPhase
		Message string
	}
}

func getArgoWorkflowHealth(obj *unstructured.Unstructured) (*HealthStatus, error) {
	var wf argoWorkflow
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &wf)
	if err != nil {
		return nil, err
	}
	switch wf.Status.Phase {
	case "", workflowPending, workflowRunning:
		return &HealthStatus{Status: HealthStatusProgressing, Message: wf.Status.Message}, nil
	case workflowSucceeded:
		return &HealthStatus{Status: HealthStatusHealthy, Message: wf.Status.Message}, nil
	case workflowFailed, workflowError:
		return &HealthStatus{Status: HealthStatusDegraded, Message: wf.Status.Message}, nil
	}
	return &HealthStatus{Status: HealthStatusUnknown, Message: wf.Status.Message}, nil
}

// An agnostic CronWorkflow object only considers Status.Phase and Status.Message. It is agnostic to the API version or any
// other fields.
type argoCronWorkflow struct {
	Status struct {
		Conditions   Conditions
		Message string
	}
}

// Conditions for CronWorkflow Status: https://github.com/argoproj/argo-workflows/blob/11890b4cc14405902ee336e9197dd153df27c36b/pkg/apis/workflow/v1alpha1/workflow_types.go#L1700-L1711
type Conditions []Condition

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type Condition struct {
	// Type is the type of condition
	Type ConditionType
	// Status is the status of the condition
	Status ConditionStatus
	// Message is the condition message
	Message string
}

type ConditionType string

const (
	// ConditionTypeCompleted is a signifies the workflow has completed
	ConditionTypeCompleted ConditionType = "Completed"
	// ConditionTypePodRunning any workflow pods are currently running
	ConditionTypePodRunning ConditionType = "PodRunning"
	// ConditionTypeSpecWarning is a warning on the current application spec
	ConditionTypeSpecWarning ConditionType = "SpecWarning"
	// ConditionTypeSpecError is an error on the current application spec
	ConditionTypeSpecError ConditionType = "SpecError"
	// ConditionTypeMetricsError is an error during metric emission
	ConditionTypeMetricsError ConditionType = "MetricsError"
	// ConditionTypeSubmissionError signifies that there was an error when submitting the CronWorkflow as a Workflow
	ConditionTypeSubmissionError ConditionType = "SubmissionError"
)

func getArgoCronWorkflowHealth(obj *unstructured.Unstructured) (*HealthStatus, error) {
	var cronWf argoCronWorkflow
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &cronWf)
	if err != nil {
		return nil, err
	}
	failed := false
	var failMsg string
	complete := false
	var message string
	for _, condition := range cronWf.Status.Conditions {
		// We treat the following as unhealthy conditions:
		// 1. ConditionTypeSpecError when the CronWorkflow spec is invalid.
		// 2. ConditionTypeSubmissionError when:
		//		* The ConcurrencyPolicy is invalid, or
		//		* There is an error stopping a current workflow, or
		// 		* There is an error submitting a workflow with Argo Server
		switch condition.Type {
		case ConditionTypeSpecError, ConditionTypeSubmissionError:
			if condition.Status == ConditionTrue {
				failed = true
				complete = true
				failMsg = condition.Message
			}
		case ConditionTypeCompleted:
			if condition.Status == ConditionTrue {
				complete = true
				message = condition.Message
			}
		default:
			if condition.Status == ConditionTrue {
				message = condition.Message
			}
		}
	}
	if !complete {
		return &HealthStatus{
			Status:  HealthStatusProgressing,
			Message: message,
		}, nil
	} else if failed {
		return &HealthStatus{
			Status:  HealthStatusDegraded,
			Message: failMsg,
		}, nil
	} else {
		return &HealthStatus{
			Status:  HealthStatusHealthy,
			Message: message,
		}, nil
	}
}

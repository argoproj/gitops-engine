package tracing

import (
	"os"
	"time"

	"k8s.io/klog/v2/klogr"
)

/*
	Poor Mans OpenTracing.

	Standardizes logging of operation duration.
*/

var enabled = false
var logger = klogr.New()

func init() {
	enabled = os.Getenv("ARGOCD_TRACING_ENABLED") == "1"
}

type Span struct {
	operationName string
	baggage       map[string]interface{}
	start         time.Time
}

func (s Span) Finish() {
	if enabled {
		logger.WithValues(baggageToVals(s.baggage)).
			WithValues("operation_name", s.operationName, "time_ms", time.Since(s.start).Seconds()*1e3).
			Info("Trace")
	}
}

func (s Span) SetBaggageItem(key string, value interface{}) {
	s.baggage[key] = value
}

func StartSpan(operationName string) Span {
	return Span{operationName, make(map[string]interface{}), time.Now()}
}

func baggageToVals(baggage map[string]interface{}) []interface{} {
	result := make([]interface{}, 0, len(baggage)*2)
	for k, v := range baggage {
		result = append(result, k, v)
	}
	return result
}

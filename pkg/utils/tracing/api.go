package tracing

/*
	Poor Mans OpenTracing.

	Standardizes logging of operation duration.
*/

type Tracer interface {
	StartSpan(operationName string) Span
	StartSpanFromTraceParent(operationName string, parentTraceId, parentSpanId string) Span
}

type Span interface {
	SetBaggageItem(key string, value any)
	Finish()
	SpanID() string
	TraceID() string
}

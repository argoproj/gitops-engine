package tracing

import "context"

/*
	Poor Mans OpenTracing.

	Standardizes logging of operation duration.
*/

type Tracer interface {
	StartSpan(_ context.Context, operationName string) Span
	StartSpanFromTraceParent(ctx context.Context, operationName string, parentTraceId, parentSpanId string) Span
}

type Span interface {
	SetBaggageItem(key string, value any)
	Finish()
	SpanID() string
	TraceID() string
}

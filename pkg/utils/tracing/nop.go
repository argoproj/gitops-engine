package tracing

import "context"

var (
	_ Tracer = NopTracer{}
	_ Span   = nopSpan{}
)

type NopTracer struct{}

func (n NopTracer) StartSpan(_ context.Context, _ string) Span {
	return nopSpan{}
}

func (n NopTracer) StartSpanFromTraceParent(_ context.Context, _, _, _ string) Span {
	return nopSpan{}
}

type nopSpan struct{}

func (n nopSpan) SetBaggageItem(_ string, _ any) {
}

func (n nopSpan) Finish() {
}

func (n nopSpan) TraceID() string {
	return ""
}

func (n nopSpan) SpanID() string {
	return ""
}

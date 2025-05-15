package tracing

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type OpenTelemetryTracer struct {
	realTracer trace.Tracer
}

func NewOpenTelemetryTracer(t trace.Tracer) Tracer {
	return &OpenTelemetryTracer{
		realTracer: t,
	}
}

func (t OpenTelemetryTracer) StartSpan(ctx context.Context, operationName string) Span {
	_, realspan := t.realTracer.Start(ctx, operationName)
	return openTelemetrySpan{realSpan: realspan}
}

func (t OpenTelemetryTracer) StartSpanFromTraceParent(ctx context.Context, operationName string, parentTraceId, parentSpanId string) Span {
	traceID, _ := trace.TraceIDFromHex(parentTraceId)
	parentSpanID, _ := trace.SpanIDFromHex(parentSpanId)
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     parentSpanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx = trace.ContextWithSpanContext(ctx, spanCtx)
	_, realSpan := t.realTracer.Start(ctx, operationName)
	return openTelemetrySpan{realSpan: realSpan}
}

type openTelemetrySpan struct {
	realSpan trace.Span
}

func (s openTelemetrySpan) SetBaggageItem(key string, value any) {
	s.realSpan.SetAttributes(attribute.Key(key).String(value.(string)))
}

func (s openTelemetrySpan) Finish() {
	s.realSpan.End()
}

func (s openTelemetrySpan) TraceID() string {
	return s.realSpan.SpanContext().TraceID().String()
}

func (s openTelemetrySpan) SpanID() string {
	return s.realSpan.SpanContext().SpanID().String()
}

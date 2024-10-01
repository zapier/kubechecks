package telemetry

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"strconv"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type otelSpanInfo struct {
	spanID  trace.SpanID
	traceID trace.TraceID
}

func GetOtelSpanInfoFromContext(ctx context.Context) otelSpanInfo {
	s := trace.SpanFromContext(ctx)

	return otelSpanInfo{
		spanID:  s.SpanContext().SpanID(),
		traceID: s.SpanContext().TraceID(),
	}
}

func (o otelSpanInfo) SpanIDValid() bool {
	return o.spanID.IsValid()
}

func (o otelSpanInfo) SpanID() string {
	return o.spanID.String()
}

func (o otelSpanInfo) TraceID() string {
	return o.traceID.String()
}

func GetTraceID(ctx context.Context) string {
	tID, err := decodeTraceID(GetOtelSpanInfoFromContext(ctx).TraceID())
	if err != nil {
		return ""
	}
	return strconv.FormatUint(traceIDToUint64(tID), 10)
}

// traceIDToUint64 converts 128bit traceId to 64 bit uint64.
func traceIDToUint64(b [16]byte) uint64 {
	return binary.BigEndian.Uint64(b[len(b)-8:])
}

func decodeTraceID(traceID string) ([16]byte, error) {
	var ret [16]byte
	_, err := hex.Decode(ret[:], []byte(traceID))
	return ret, err
}

func SetError(span trace.Span, err error, event string) {
	span.RecordError(err)
	span.SetStatus(codes.Error, event)
}

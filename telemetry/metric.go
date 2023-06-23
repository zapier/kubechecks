package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	otelMetric "go.opentelemetry.io/otel/metric"
)

type MetricType int

const (
	GaugeInt MetricType = iota
	GaugeFloat
	HistogramInt
	HistogramFloat
	CounterInt
	CounterFloat
)

func (mt MetricType) String() string {
	switch mt {
	case GaugeInt:
		return "GaugeInt"
	case GaugeFloat:
		return "GaugeFloat"
	case HistogramInt:
		return "HistogramInt"
	case HistogramFloat:
		return "HistogramFloat"
	case CounterInt:
		return "CounterInt"
	case CounterFloat:
		return "CounterFloat"
	}

	return "TypeUnknown"
}

type AttributeType uint

const (
	// INVALID is used for a Value with no value set.
	INVALID AttributeType = iota
	// BOOL is a boolean Type Value.
	BOOL
	// INT64 is a 64-bit signed integral Type Value.
	INT64
	// FLOAT64 is a 64-bit floating point Type Value.
	FLOAT64
	// STRING is a string Type Value.
	STRING
	// BOOLSLICE is a slice of booleans Type Value.
	BOOLSLICE
	// INT64SLICE is a slice of 64-bit signed integral numbers Type Value.
	INT64SLICE
	// FLOAT64SLICE is a slice of 64-bit floating point numbers Type Value.
	FLOAT64SLICE
	// STRINGSLICE is a slice of strings Type Value.
	STRINGSLICE
)

type Attrs struct {
	Key   string
	Value AttrsValue
}

type AttrsValue struct {
	Type     AttributeType
	Numberic int64
	Stringly string
	Slice    interface{}
}

func intToBool(i int64) bool {
	return i == 1
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func BoolAttribute(key string, value bool) Attrs {
	return Attrs{
		Key: key,
		Value: AttrsValue{
			Type:     BOOL,
			Numberic: boolToInt(value),
		},
	}
}

func StringAttribute(key, value string) Attrs {
	return Attrs{
		Key: key,
		Value: AttrsValue{
			Type:     STRING,
			Stringly: value,
		},
	}
}

func StringSliceAttribute(key string, value []string) Attrs {
	return Attrs{
		Key: key,
		Value: AttrsValue{
			Type:  STRINGSLICE,
			Slice: value,
		},
	}
}

type MetricData struct {
	Name       string
	MetricType MetricType
	Value      interface{}
	Attrs      []Attrs
}

func NewMetricData(name string, metricType MetricType, value interface{}, attrs ...Attrs) *MetricData {
	return &MetricData{
		Name:       name,
		MetricType: metricType,
		Value:      value,
		Attrs:      attrs,
	}
}

func (md MetricData) ConvertAttrs() []attribute.KeyValue {
	attrs := []attribute.KeyValue{}

	for _, attr := range md.Attrs {
		switch attr.Value.Type {
		case STRING:
			attrs = append(attrs, attribute.String(attr.Key, attr.Value.Stringly))
		case BOOL:
			attrs = append(attrs, attribute.Bool(attr.Key, intToBool(attr.Value.Numberic)))
		case STRINGSLICE:
			attrs = append(attrs, attribute.StringSlice(attr.Key, attr.Value.Slice.([]string)))
		default:
			attrs = append(attrs, attribute.String(attr.Key, attr.Value.Stringly))
		}
	}

	return attrs
}

func RecordGaugeFloat(ctx context.Context, meter otelMetric.Meter, metricName string, value float64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncFloat64().UpDownCounter(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s gauge: %s", metricName, err.Error())
	}
	h.Add(ctx, value, attrs...)
	return nil
}

func RecordGaugeInt(ctx context.Context, meter otelMetric.Meter, metricName string, value int64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncInt64().UpDownCounter(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s gauge: %s", metricName, err.Error())
	}
	h.Add(ctx, value, attrs...)
	return nil
}

func RecordCounterFloat(ctx context.Context, meter otelMetric.Meter, metricName string, value float64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncFloat64().Counter(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s counter: %s", metricName, err.Error())
	}
	h.Add(ctx, value, attrs...)
	return nil
}

func RecordCounterInt(ctx context.Context, meter otelMetric.Meter, metricName string, value int64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncInt64().Counter(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s counter: %s", metricName, err.Error())
	}
	h.Add(ctx, value, attrs...)
	return nil
}

func RecordHistogramInt(ctx context.Context, meter otelMetric.Meter, metricName string, value int64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncInt64().Histogram(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s counter: %s", metricName, err.Error())
	}
	h.Record(ctx, value, attrs...)
	return nil
}

func RecordHistogramFloat(ctx context.Context, meter otelMetric.Meter, metricName string, value float64, attrs ...attribute.KeyValue) error {
	h, err := meter.SyncFloat64().Histogram(metricName)
	if err != nil {
		return fmt.Errorf("error creating %s counter: %s", metricName, err.Error())
	}
	h.Record(ctx, value, attrs...)
	return nil
}

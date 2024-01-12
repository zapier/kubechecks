package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
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

type MetricData struct {
	Name       string
	MetricType MetricType
	Value      interface{}
	Attrs      []Attrs
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

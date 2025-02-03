package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const DefaultMetricInterval = 2

type BaseTelemetry struct {
	c             context.Context
	traceProvider *sdktrace.TracerProvider
	metric        *metric.MeterProvider
}

func (bt *BaseTelemetry) Shutdown() {
	if bt.traceProvider != nil {
		_ = bt.traceProvider.Shutdown(bt.c)
	}
	if bt.metric != nil {
		_ = bt.metric.Shutdown(bt.c)
	}
}

type OperatorTelemetry struct {
	*BaseTelemetry
}

func (ot *OperatorTelemetry) StartMetricCollectors() error {
	log.Debug().Msg("Starting runtime instrumentation")
	err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second * DefaultMetricInterval))
	if err != nil {
		log.Error().Err(err).Msg("runtime instrumentation failure")
		return err
	}
	return nil
}

func Init(ctx context.Context, serviceName, gitTag, gitCommit string, otelEnabled bool, otelHost, otelPort string) (*OperatorTelemetry, error) {
	log.Info().Msg("Initializing telemetry")
	bt := &OperatorTelemetry{
		BaseTelemetry: &BaseTelemetry{
			c: ctx,
		}}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(gitTag),
			attribute.String("SHA", gitCommit),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	err = bt.initProviders(res, otelEnabled, otelHost, otelPort)
	if err != nil {
		return bt, err
	}

	// setup propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return bt, nil
}

func createGRPCConn(enabled bool, otelHost string, otelPort string) (*grpc.ClientConn, error) {
	if !enabled {
		log.Info().Msg("otel disabled")
		return nil, nil
	}

	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%s", otelHost, otelPort),
		grpc.WithTransportCredentials(
			insecure.NewCredentials(),
		),
	)
	if err != nil {
		log.Error().Err(err).Msg("unable to dial grpc")
		return conn, err
	}

	log.Debug().Str("host", otelHost).Str("port", otelPort).Msg("grpc conn created")

	return conn, err
}

func (bt *BaseTelemetry) initProviders(res *resource.Resource, enabled bool, otelHost string, otelPort string) error {
	conn, err := createGRPCConn(enabled, otelHost, otelPort)
	if err != nil {
		return err
	}

	err = bt.initTrace(conn, res)
	if err != nil {
		return err
	}

	err = bt.initMetric(conn, res)
	if err != nil {
		return err
	}

	return nil
}

func (bt *BaseTelemetry) initTrace(conn *grpc.ClientConn, res *resource.Resource) error {
	if conn == nil {
		return nil
	}

	err := bt.initOTLPTrace(conn, res)
	if err != nil {
		log.Error().Err(err).Msg("unable to init tracer")
	}
	log.Debug().Msg("tracer initialized")
	return nil
}

func (bt *BaseTelemetry) initMetric(conn *grpc.ClientConn, res *resource.Resource) error {
	if conn == nil {
		return nil
	}

	err := bt.initOTLPMetric(conn, res)
	if err != nil {
		log.Error().Err(err).Msg("unable to init metrics")
		return err
	}
	log.Debug().Msg("metric provider initialized")
	return nil
}

func (bt *BaseTelemetry) initOTLPTrace(conn *grpc.ClientConn, res *resource.Resource) error {
	// tracer grpc client
	traceExporter, err := otlptracegrpc.New(bt.c,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithGRPCConn(conn),
	)
	if err != nil {
		return err
	}

	spanProcessor := sdktrace.NewBatchSpanProcessor(traceExporter)
	bt.traceProvider = sdktrace.NewTracerProvider(
		// allow sampling to be configurable
		// sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(spanProcessor),
	)

	otel.SetTracerProvider(bt.traceProvider)

	return nil
}

func (bt *BaseTelemetry) initOTLPMetric(conn *grpc.ClientConn, res *resource.Resource) error {
	mClient, err := otlpmetricgrpc.New(
		bt.c,
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithGRPCConn(conn),
	)
	if err != nil {
		return fmt.Errorf("failed to create metric client: %w", err)
	}

	otelReader := metric.NewPeriodicReader(
		mClient,
		metric.WithInterval(DefaultMetricInterval*time.Second),
	)

	bt.metric = metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(otelReader),
	)

	otel.SetMeterProvider(bt.metric)

	return nil
}

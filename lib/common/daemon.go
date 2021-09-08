package common

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gogo/status"
	logger "github.com/textileio/go-log/v2"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric/global"
	export "go.opentelemetry.io/otel/sdk/export/metric"
	"go.opentelemetry.io/otel/sdk/metric/aggregator/histogram"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	selector "go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// SetupInstrumentation starts a metrics endpoint.
func SetupInstrumentation(prometheusAddr string) error {
	config := prometheus.Config{
		DefaultHistogramBoundaries: []float64{1e-3, 1e-2, 1e-1, 1},
	}
	c := controller.New(
		processor.New(
			selector.NewWithHistogramDistribution(
				histogram.WithExplicitBoundaries(config.DefaultHistogramBoundaries),
			),
			export.CumulativeExportKindSelector(),
			processor.WithMemory(true),
		),
	)
	exporter, err := prometheus.New(config, c)
	if err != nil {
		return fmt.Errorf("failed to initialize prometheus exporter %v", err)
	}
	global.SetMeterProvider(exporter.MeterProvider())
	http.HandleFunc("/metrics", exporter.ServeHTTP)
	go func() {
		_ = http.ListenAndServe(prometheusAddr, nil)
	}()

	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		return fmt.Errorf("starting Go runtime metrics: %s", err)
	}

	return nil
}

// GrpcLoggerInterceptor logs any error produced by processing requests, and catches/recovers
// from panics.
func GrpcLoggerInterceptor(log *logger.ZapEventLogger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (res interface{}, err error) {
		// Recover from any panic caused by this request processing.
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("panic: %s", r)
				err = status.Errorf(codes.Internal, "panic: %s", r)
			}
		}()

		res, err = handler(ctx, req)
		grpcErrCode := status.Code(err)
		if grpcErrCode != codes.OK {
			log.Error(err)
		}
		return
	}
}

package main

import (
	"context"
	log_ "log"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

var logger = log_.New(os.Stderr, "checkoutsvc", log_.Ldate|log_.Ltime|log_.Llongfile)

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer(url string) func() {
	// Create Zipkin Exporter and install it as a global tracer.
	//
	// For demoing purposes, always sample. In a production application, you should
	// configure the sampler to a trace.ParentBased(trace.TraceIDRatioBased) set at the desired
	// ratio.
	exporter, err := zipkin.New(
		url,
		zipkin.WithLogger(logger),
	)
	if err != nil {
		log.Fatal(err)
	}

	// batcher := sdktrace.NewBatchSpanProcessor(exporter)
	simpler := sdktrace.NewSimpleSpanProcessor(exporter)

	tp := sdktrace.NewTracerProvider(
		// sdktrace.WithSpanProcessor(batcher),
		sdktrace.WithSpanProcessor(simpler),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("checkout-service"),
		)),
	)
	otel.SetTracerProvider(tp)

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

package main

import (
	"context"
	"encoding/json"
	log_ "log"
	"net/http"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"google.golang.org/grpc/metadata"
)

var logger = log_.New(os.Stderr, "checkoutsvc", log_.Ldate|log_.Ltime|log_.Llongfile)

// initTracer creates a new trace provider instance and registers it as global trace provider.
func initTracer(url string, name string) func() {
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
			semconv.ServiceNameKey.String(name),
		)),
	)
	otel.SetTracerProvider(tp)

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

func getTraceCarrier(ctx context.Context) *propagation.MapCarrier {
	propgator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

	// Serialize the context into carrier
	carrier := propagation.MapCarrier{}
	propgator.Inject(ctx, carrier)

	return &carrier
}

func traceCtxFromGrpcCtx(ctx context.Context) context.Context {
	propgator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}

	traceparentHdrs := md["x-traceparent"]
	if len(traceparentHdrs) == 0 {
		log.Warnln("empty traceparent")
		return ctx
	}
	traceparentHdr := traceparentHdrs[0]
	if traceparentHdr == "" {
		log.Warnln("empty traceparent")
		return ctx
	}

	carrier := propagation.MapCarrier{"traceparent": traceparentHdr}

	return propgator.Extract(ctx, carrier)
}

func injectTrace2GrpcCtx(ctx context.Context, traceCtx context.Context) context.Context {
	carrier := getTraceCarrier(traceCtx)

	return metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{"x-traceparent": carrier.Get("traceparent")}))
}

func traceCtxFromHttpReq(ctx context.Context, r *http.Request) context.Context {
	propgator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

	// from header
	traceparentHdr := r.Header.Get("x-traceparent")
	if traceparentHdr == "" {
		// from body
		bodyJson := map[string]string{}
		err := json.NewDecoder(r.Body).Decode(&bodyJson)
		if err != nil {
			log.Errorf("cannot decode request body: %v\n", err)
			return ctx
		}
		traceparentHdr = bodyJson["x-traceparent"]
	}

	if traceparentHdr == "" {
		log.Warnln("empty traceparent")
		return ctx
	}

	carrier := propagation.MapCarrier{"traceparent": traceparentHdr}

	return propgator.Extract(ctx, carrier)

}

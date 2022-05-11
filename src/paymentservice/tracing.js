const { trace, ROOT_CONTEXT, defaultTextMapGetter, defaultTextMapSetter } = require('@opentelemetry/api');
const { W3CTraceContextPropagator } = require('@opentelemetry/core');
// const { registerInstrumentations } = require('@opentelemetry/instrumentation');
const { NodeTracerProvider } = require('@opentelemetry/sdk-trace-node');
const { Resource } = require('@opentelemetry/resources');
const { SemanticResourceAttributes } = require('@opentelemetry/semantic-conventions');
const { SimpleSpanProcessor, ConsoleSpanExporter } = require('@opentelemetry/sdk-trace-base');
const { ZipkinExporter } = require('@opentelemetry/exporter-zipkin');
// const { GrpcInstrumentation } = require('@opentelemetry/instrumentation-grpc');
const grpc = require('@grpc/grpc-js');

exports.initTrace = (url, serviceName) => {
  const provider = new NodeTracerProvider({
    resource: new Resource({
      [SemanticResourceAttributes.SERVICE_NAME]: serviceName,
    }),
  });

  let exporter = new ConsoleSpanExporter()
  if (url) {
    exporter = new ZipkinExporter({
      url: url || 'http://localhost:9411/api/v2/spans',
      serviceName: serviceName || 'service',
    });
  }

  provider.addSpanProcessor(new SimpleSpanProcessor(exporter));

  // Initialize the OpenTelemetry APIs to use the NodeTracerProvider bindings
  provider.register();

  // registerInstrumentations({
  //   instrumentations: [
  //     new GrpcInstrumentation(),
  //   ],
  // });

  // return trace.getTracer('zipkin');
};

if (process.env.ZIPKIN_URL) {
  exports.initTrace(process.env.ZIPKIN_URL, "payment-service")
}

exports.traceCtxFromHttpReq = (ctx, r) => {
  ctx = ctx || ROOT_CONTEXT
  const propagator = new W3CTraceContextPropagator()
  // from header
  let traceparentHdr = r.headers['x-traceparent']
  if (!traceparentHdr) {
    // from body
    const reqBody = r.body
    traceparentHdr = reqBody["x-traceparent"]
  }

  if (!traceparentHdr) {
    console.warn(`empty traceparent from body`)
    return ctx
  }

  return propagator.extract(
    ctx,
    { traceparent: traceparentHdr },
    defaultTextMapGetter
  )
}

exports.traceCtx2GrpcMetadata = (traceCtx) => {
  const carrier = exports.getTraceCarrier(traceCtx)

  const mt = new grpc.Metadata()
  mt.set("x-traceparent", carrier.traceparent)

  return mt
}

exports.traceCtxFromGrpcCall = (ctx, call) => {
  ctx = ctx || ROOT_CONTEXT
  const propagator = new W3CTraceContextPropagator()

  const mt = call.metadata.internalRepr
  let traceparentHdrs = mt.has('x-traceparent') && mt.get('x-traceparent')
  if (!traceparentHdrs || !traceparentHdrs.length) {
    console.warn('empty traceparent from grpc metadata')
    return ctx
  }

  const traceparentHdr = traceparentHdrs[0]
  if (!traceparentHdr) {
    console.warn(`empty traceparent`)
    return ctx
  }

  return propagator.extract(
    ctx,
    { traceparent: traceparentHdr },
    defaultTextMapGetter
  )

}

exports.getTraceCarrier = (ctx) => {
  const propagator = new W3CTraceContextPropagator();
  const carrier = {};

  propagator.inject(
    ctx,
    carrier,
    defaultTextMapSetter
  );

  return carrier
}

exports.test = async () => {
  exports.initTrace('http://localhost:9411/api/v2/spans', 'test-svc')
  const tracer = trace.getTracer("charge-card")

  const parentSpan = tracer.startSpan('main');

  // In upstream service
  // const propagator = new W3CTraceContextPropagator();
  // const carrier = {};
  //
  // propagator.inject(
  //   trace.setSpanContext(ROOT_CONTEXT, parentSpan.spanContext()),
  //   carrier,
  //   defaultTextMapSetter
  // );
  // console.log("carrier", carrier); // transport this carrier info to other service via headers or some other way 
  //
  // // In downstream service
  //
  // const parentCtx = propagator.extract(
  //   ROOT_CONTEXT,
  //   carrier,
  //   defaultTextMapGetter
  // );
  //
  // const childSpan = tracer.startSpan("child", undefined, parentCtx);
  //
  // childSpan.end()
  //
  // parentSpan.end()
  //
  const carrier = exports.getTraceCarrier(
    trace.setSpanContext(ROOT_CONTEXT, parentSpan.spanContext()),
  )
  // console.log(carrier)

  const req = {
    headers: {
      "x-traceparent": carrier.traceparent
    },
    body: {
      "x-traceparent": carrier.traceparent
    }
  }

  const call = {
    metadata: {
      "x-traceparent": [carrier.traceparent],
      internalRepr: {
        get: (key) => {
          return call.metadata[key]
        },
        has: (key) => {
          return !!call.metadata[key]
        },
      }
    }
  }

  const pCtx1 = exports.traceCtxFromHttpReq(ROOT_CONTEXT, req)
  const pCtx2 = exports.traceCtxFromGrpcCall(ROOT_CONTEXT, call)

  const s2 = tracer.startSpan("child-http", undefined, pCtx1);
  tracer.startSpan("child-http-1", undefined, trace.setSpanContext(pCtx1, s2.spanContext())).end()

  s2.end()
  tracer.startSpan("child-grpc", undefined, pCtx2).end();

  parentSpan.end()
}

// exports.test()

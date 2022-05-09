// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/profiler"
	"contrib.go.opencensus.io/exporter/jaeger"
	"contrib.go.opencensus.io/exporter/stackdriver"
	"github.com/barrydevp/transco"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/shippingservice/genproto"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	defaultPort = "50051"
)

var (
	log       *logrus.Logger
	txnClient *transco.Client
)

func init() {
	log = logrus.New()
	log.Level = logrus.DebugLevel
	log.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "severity",
			logrus.FieldKeyMsg:   "message",
		},
		TimestampFormat: time.RFC3339Nano,
	}
	log.Out = os.Stdout
	trans, err := transco.New(os.Getenv("TRANSCOORDITOR_URI"))
	txnClient = trans
	log.Info("load Transco:", err)
}

func main() {
	if os.Getenv("ZIPKIN_URL") != "" {
		log.Info("Zipkin enabled")
		shutdown := initTracer(os.Getenv("ZIPKIN_URL"), "shipping-service")
		defer shutdown()
	}

	if os.Getenv("DISABLE_TRACING") == "" {
		log.Info("Tracing enabled.")
		go initTracing()
	} else {
		log.Info("Tracing disabled.")
	}

	if os.Getenv("DISABLE_PROFILER") == "" {
		log.Info("Profiling enabled.")
		go initProfiling("shippingservice", "1.0.0")
	} else {
		log.Info("Profiling disabled.")
	}

	port := defaultPort
	if value, ok := os.LookupEnv("PORT"); ok {
		port = value
	}
	port = fmt.Sprintf(":%s", port)

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	var srv *grpc.Server
	if os.Getenv("DISABLE_STATS") == "" {
		log.Info("Stats enabled.")
		srv = grpc.NewServer(grpc.StatsHandler(&ocgrpc.ServerHandler{}))
	} else {
		log.Info("Stats disabled.")
		srv = grpc.NewServer()
	}
	svc := &server{}
	pb.RegisterShippingServiceServer(srv, svc)
	healthpb.RegisterHealthServer(srv, svc)

	r := mux.NewRouter()
	r.HandleFunc("/tracking/{id}/compensate", svc.compensateShipHandler).Methods(http.MethodPost)

	go func() {
		httpPort := "8181"
		if os.Getenv("HTTP_PORT") != "" {
			httpPort = os.Getenv("HTTP_PORT")
		}
		addr := os.Getenv("LISTEN_ADDR")
		log.Infof("starting http server on " + addr + ":" + httpPort)
		log.Fatal(http.ListenAndServe(addr+":"+httpPort, r))
	}()

	// Register reflection service on gRPC server.
	reflection.Register(srv)
	log.Infof("Shipping Service listening on port %s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// server controls RPC service responses.
type server struct{}

// Check is for health checking.
func (s *server) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (s *server) Watch(req *healthpb.HealthCheckRequest, ws healthpb.Health_WatchServer) error {
	return status.Errorf(codes.Unimplemented, "health check via Watch not implemented")
}

// GetQuote produces a shipping quote (cost) in USD.
func (s *server) GetQuote(ctx context.Context, in *pb.GetQuoteRequest) (*pb.GetQuoteResponse, error) {
	log.Info("[GetQuote] received request")
	defer log.Info("[GetQuote] completed request")

	// 1. Our quote system requires the total number of items to be shipped.
	count := 0
	for _, item := range in.Items {
		count += int(item.Quantity)
	}

	// 2. Generate a quote based on the total number of items to be shipped.
	quote := CreateQuoteFromCount(count)

	// 3. Generate a response.
	return &pb.GetQuoteResponse{
		CostUsd: &pb.Money{
			CurrencyCode: "USD",
			Units:        int64(quote.Dollars),
			Nanos:        int32(quote.Cents * 10000000)},
	}, nil

}

// ShipOrder mocks that the requested items will be shipped.
// It supplies a tracking ID for notional lookup of shipment delivery status.
func (s *server) ShipOrder(ctx context.Context, in *pb.ShipOrderRequest) (*pb.ShipOrderResponse, error) {
	log.Info("[ShipOrder] received request")
	defer log.Info("[ShipOrder] completed request")

	// tracing
	ctx = traceCtxFromGrpcCtx(ctx)
	tr := otel.GetTracerProvider().Tracer("ship-order")
	ctx, span := tr.Start(ctx, "ship order")
	defer span.End()

	// 1. Create a Tracking ID
	baseAddress := fmt.Sprintf("%s, %s, %s", in.Address.StreetAddress, in.Address.City, in.Address.State)
	id := CreateTrackingId(baseAddress)

	// 2. Generate a response.
	return &pb.ShipOrderResponse{
		TrackingId: id,
	}, nil
}

func (s *server) ShipOrderTxn(ctx context.Context, in *pb.ShipOrderRequestTxn) (*pb.ShipOrderResponse, error) {
	log.Info("[ShipOrderTxn] received request")
	defer log.Info("[ShipOrderTxn] completed request")

	// tracing
	ctx = traceCtxFromGrpcCtx(ctx)
	tr := otel.GetTracerProvider().Tracer("ship-order-txn")
	ctx, span := tr.Start(ctx, "ship order txn")
	defer span.End()
	traceCarrier := getTraceCarrier(ctx)

	sendTrace := func(label string, fn func() error) {
		_, span_ := tr.Start(ctx, label)
		// span_.SetAttributes(attribute.String("session_id", in.SessionId))
		defer span_.End()
		err := fn()
		if err != nil {
			span_.AddEvent(fmt.Sprintf("%v failed: %v", label, err))
		}
	}

	// span.AddEvent("retrive session")
	var session *transco.Session
	var err error
	sendTrace("retrive session", func() error {
		session, err = txnClient.SessionFromId(in.SessionId)
		return err
	})
	// session, err := txnClient.SessionFromId(in.SessionId)
	if err != nil {
		// span.AddEvent(fmt.Sprintf("retrive session failed: %v", err))
		return nil, status.Errorf(codes.Internal, "failed to retrive session: %v", err)
	}
	span.SetAttributes(attribute.String("session_id", session.Id))
	// _, span1 := tr.Start(ctx, "join session")
	// span1.SetAttributes(attribute.String("session_id", session.Id))
	// span.AddEvent("join session")
	var participant *transco.Participant
	sendTrace("join session", func() error {
		participant, err = session.JoinSession(&transco.ParticipantJoinBody{
			ClientId:  "shippingservice",
			RequestId: in.SessionId,
		})

		return err
	})
	// participant, err := session.JoinSession(&transco.ParticipantJoinBody{
	// 	ClientId:  "shippingservice",
	// 	RequestId: in.SessionId,
	// })
	// span1.End()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to join session: %v", err)
	}

	// 1. Create a Tracking ID
	baseAddress := fmt.Sprintf("%s, %s, %s", in.Address.StreetAddress, in.Address.City, in.Address.State)
	id := CreateTrackingId(baseAddress)

	// _, span1 = tr.Start(ctx, "partial commit session")
	// span1.SetAttributes(attribute.String("session_id", session.Id))
	uri := "http://shippingservice:8181/tracking/" + id + "/compensate"
	// span.AddEvent("partial commit session")
	sendTrace("partial commit session", func() error {
		err = participant.PartialCommit(&transco.ParticipantAction{
			Uri:  &uri,
			Data: map[string]string{"x-traceparent": traceCarrier.Get("traceparent")},
		}, nil)

		return err
	})
	// err = participant.PartialCommit(&transco.ParticipantAction{
	// 	Uri:  &uri,
	// 	Data: map[string]string{"x-traceparent": traceCarrier.Get("traceparent")},
	// }, nil)
	// span1.End()
	if err != nil {
		// span.AddEvent(fmt.Sprintf("partial commit session failed: %v", err))
		return nil, status.Errorf(codes.Internal, "failed to partial commit")
	}

	// 2. Generate a response.
	return &pb.ShipOrderResponse{
		TrackingId: id,
	}, nil
}

func (s *server) compensateShipHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	log.Info(fmt.Sprintf("[CompensateShip] receved compensate at tracking_id: %v", id))

	// tracing
	ctx := traceCtxFromHttpReq(context.Background(), r)
	tr := otel.GetTracerProvider().Tracer("compensate-ship")
	ctx, span := tr.Start(ctx, "compensating ship -> cancel ship")
	defer span.End()

	w.WriteHeader(200)
	w.Write(nil)
}

func initJaegerTracing() {
	svcAddr := os.Getenv("JAEGER_SERVICE_ADDR")
	if svcAddr == "" {
		log.Info("jaeger initialization disabled.")
		return
	}

	// Register the Jaeger exporter to be able to retrieve
	// the collected spans.
	exporter, err := jaeger.NewExporter(jaeger.Options{
		Endpoint: fmt.Sprintf("http://%s", svcAddr),
		Process: jaeger.Process{
			ServiceName: "shippingservice",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	trace.RegisterExporter(exporter)
	log.Info("jaeger initialization completed.")
}

func initStats(exporter *stackdriver.Exporter) {
	view.SetReportingPeriod(60 * time.Second)
	view.RegisterExporter(exporter)
	if err := view.Register(ocgrpc.DefaultServerViews...); err != nil {
		log.Warn("Error registering default server views")
	} else {
		log.Info("Registered default server views")
	}
}

func initStackdriverTracing() {
	// TODO(ahmetb) this method is duplicated in other microservices using Go
	// since they are not sharing packages.
	for i := 1; i <= 3; i++ {
		exporter, err := stackdriver.NewExporter(stackdriver.Options{})
		if err != nil {
			log.Warnf("failed to initialize Stackdriver exporter: %+v", err)
		} else {
			trace.RegisterExporter(exporter)
			trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
			log.Info("registered Stackdriver tracing")

			// Register the views to collect server stats.
			initStats(exporter)
			return
		}
		d := time.Second * 10 * time.Duration(i)
		log.Infof("sleeping %v to retry initializing Stackdriver exporter", d)
		time.Sleep(d)
	}
	log.Warn("could not initialize Stackdriver exporter after retrying, giving up")
}

func initTracing() {
	initJaegerTracing()
	initStackdriverTracing()
}

func initProfiling(service, version string) {
	// TODO(ahmetb) this method is duplicated in other microservices using Go
	// since they are not sharing packages.
	for i := 1; i <= 3; i++ {
		if err := profiler.Start(profiler.Config{
			Service:        service,
			ServiceVersion: version,
			// ProjectID must be set if not running on GCP.
			// ProjectID: "my-project",
		}); err != nil {
			log.Warnf("failed to start profiler: %+v", err)
		} else {
			log.Info("started Stackdriver profiler")
			return
		}
		d := time.Second * 10 * time.Duration(i)
		log.Infof("sleeping %v to retry initializing Stackdriver profiler", d)
		time.Sleep(d)
	}
	log.Warn("could not initialize Stackdriver profiler after retrying, giving up")
}

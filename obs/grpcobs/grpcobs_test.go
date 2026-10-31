package grpcobs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/popododo0720/vws-lib/errs"
	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/grpcobs"
	"github.com/popododo0720/vws-lib/obs/report"
)

// svc is a health server whose Check does what the request's Service field says.
type svc struct {
	grpc_health_v1.UnimplementedHealthServer
	sawRequestID string
}

func (s *svc) Check(ctx context.Context, r *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(grpcobs.RequestIDHeader); len(v) > 0 {
			s.sawRequestID = v[0]
		}
	}
	switch r.GetService() {
	case "panic":
		panic("secret password=hunter2")
	case "notfound":
		return nil, errs.New(errs.CodeNotFound, "volume vol-1 not found")
	case "internal":
		// The handler reports; the interceptor must not report the same error again.
		return nil, report.Error( //nolint:wrapcheck // the handler returns the reported error as is
			ctx, errs.Internal(errors.New("dial 10.0.0.7: password=hunter2")))
	case "exists", "exists-wrapped", "exists-reported":
		st, err := status.New(codes.AlreadyExists, "subnet sub-1 already exists").
			WithDetails(&errdetails.ErrorInfo{Reason: "ALREADY_EXISTS", Domain: "network.vws"})
		if err != nil {
			panic(err)
		}
		switch r.GetService() {
		case "exists-wrapped":
			return nil, fmt.Errorf("create subnet: %w", st.Err())
		case "exists-reported":
			return nil, errs.Reported(fmt.Errorf("create subnet: %w", st.Err()))
		}
		return nil, st.Err() //nolint:wrapcheck // the handler returns a status as is
	case "plain":
		return nil, errors.New("raw failure password=hunter2")
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

type harness struct {
	client grpc_health_v1.HealthClient
	svc    *svc
	reader *sdkmetric.ManualReader
	logs   *bytes.Buffer
}

func start(t *testing.T) *harness {
	t.Helper()
	h := &harness{svc: &svc{}, reader: sdkmetric.NewManualReader(), logs: &bytes.Buffer{}}
	shutdown, err := obs.Setup(t.Context(), obs.ForService("compute"), obs.WithOverride(func(c *obs.Config) {
		c.Getenv = func(string) string { return "" }
		c.LogOutput = h.logs
		c.MetricReaders = []sdkmetric.Reader{h.reader}
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) }) //nolint:errcheck // cleanup

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpcobs.ServerOptions("compute")...)
	grpc_health_v1.RegisterHealthServer(srv, h.svc)
	go func() { _ = srv.Serve(lis) }() //nolint:errcheck // Serve returns when the test stops it
	t.Cleanup(srv.Stop)

	opts := append(grpcobs.ClientOptions("console-api", "compute"),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() }) //nolint:errcheck // cleanup
	h.client = grpc_health_v1.NewHealthClient(conn)
	return h
}

func (h *harness) check(t *testing.T, service string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(obs.WithRequestID(t.Context(), "req-42"), 5*time.Second)
	defer cancel()
	_, err := h.client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: service})
	return err //nolint:wrapcheck // the test reads the status code
}

func (h *harness) sum(t *testing.T, name string, match map[string]string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if s, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range s.DataPoints {
					ok := true
					for k, v := range match {
						if got, _ := dp.Attributes.Value(attribute.Key(k)); got.AsString() != v {
							ok = false
						}
					}
					if ok {
						total += dp.Value
					}
				}
			}
		}
	}
	return total
}

func TestPanicReturnsInternalWithoutTheValue(t *testing.T) {
	h := start(t)
	err := h.check(t, "panic")
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal || st.Message() != "internal error" {
		t.Fatalf("got %v %q", st.Code(), st.Message())
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the panic value reached the client")
	}
	if h.sum(t, obs.MetricPanics, nil) != 1 || h.sum(t, obs.MetricErrors, map[string]string{"code": "Internal"}) != 1 {
		t.Errorf("one panic and one Internal error must be counted")
	}
	if !strings.Contains(h.logs.String(), `"request_id":"req-42"`) {
		t.Errorf("the request id from the client must be in the server log: %s", h.logs.String())
	}
	// The server survives the panic.
	if err := h.check(t, "ok"); err != nil {
		t.Errorf("server unusable after a panic: %v", err)
	}
}

func TestRequestIDTravelsAndMetricsCountCodes(t *testing.T) {
	h := start(t)
	if err := h.check(t, "ok"); err != nil {
		t.Fatal(err)
	}
	if h.svc.sawRequestID != "req-42" {
		t.Errorf("request id on the server: %q", h.svc.sawRequestID)
	}
	err := h.check(t, "notfound")
	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "volume vol-1 not found") {
		t.Errorf("a caller error keeps its message: %v", err)
	}
	// vws-lib#62: the reason reaches the client through the wire, and CodeOf keeps the code.
	var info *errdetails.ErrorInfo
	for _, d := range status.Convert(err).Details() {
		if i, ok := d.(*errdetails.ErrorInfo); ok {
			info = i
		}
	}
	if info.GetReason() != "RESOURCE_NOT_FOUND" || info.GetDomain() != "vws.dev" {
		t.Errorf("ErrorInfo lost on the wire: %v", status.Convert(err).Details())
	}
	if got := errs.CodeOf(err); got != errs.CodeNotFound {
		t.Errorf("CodeOf on the wire error = %v, want NotFound", got)
	}
	if n := h.sum(t, obs.MetricRPCServerRequests, map[string]string{"grpc_code": "OK"}); n != 1 {
		t.Errorf("OK count %d", n)
	}
	if n := h.sum(t, obs.MetricRPCServerRequests, map[string]string{"grpc_code": "NotFound"}); n != 1 {
		t.Errorf("NotFound count %d", n)
	}
	if n := h.sum(t, obs.MetricErrors, nil); n != 0 {
		t.Errorf("a caller error is not a server fault and must not be reported: %d", n)
	}
}

func TestOneReportPerFailureAndNoInternalDetail(t *testing.T) {
	h := start(t)
	for _, name := range []string{"internal", "plain"} {
		err := h.check(t, name)
		st, _ := status.FromError(err)
		if st.Code() != codes.Internal || st.Message() != "internal error" || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: client saw %v %q", name, st.Code(), st.Message())
		}
	}
	// The handler reported "internal" itself; the interceptor reported "plain". Two failures, two reports.
	if n := h.sum(t, obs.MetricErrors, map[string]string{"code": "Internal"}); n != 1 {
		t.Errorf("the handler's report must not be repeated: Internal counted %d times for the reported error", n)
	}
	if n := h.sum(t, obs.MetricErrors, map[string]string{"code": "Unspecified"}); n != 1 {
		t.Errorf("a plain error is reported once by the interceptor: %d", n)
	}
	if strings.Count(h.logs.String(), "error reported") != 2 {
		t.Errorf("two failures must write two log lines: %s", h.logs.String())
	}
}

// vws-lib#13: a handler's gRPC status reaches the client unchanged and is not reported.
func TestHandlerStatusPassesThroughUnreported(t *testing.T) {
	h := start(t)
	for _, name := range []string{"exists", "exists-wrapped", "exists-reported"} {
		err := h.check(t, name)
		st, _ := status.FromError(err)
		if st.Code() != codes.AlreadyExists || st.Message() != "subnet sub-1 already exists" {
			t.Errorf("%s: client saw %v %q", name, st.Code(), st.Message())
		}
		if strings.Count(err.Error(), "rpc error") != 1 {
			t.Errorf("%s: message double-wrapped: %q", name, err.Error())
		}
		var info *errdetails.ErrorInfo
		for _, d := range st.Details() {
			if i, ok := d.(*errdetails.ErrorInfo); ok {
				info = i
			}
		}
		if info == nil || info.GetReason() != "ALREADY_EXISTS" {
			t.Errorf("%s: ErrorInfo lost: %v", name, st.Details())
		}
		if got := errs.CodeOf(errs.FromGRPC(err)); got != errs.CodeAlreadyExists {
			t.Errorf("%s: CodeOf on the client = %v", name, got)
		}
	}
	if n := h.sum(t, obs.MetricErrors, nil); n != 0 {
		t.Errorf("a caller error carried as a status must not be reported: %d", n)
	}
	if strings.Contains(h.logs.String(), "error reported") {
		t.Errorf("no error report expected: %s", h.logs.String())
	}
	if n := h.sum(t, obs.MetricRPCServerRequests, map[string]string{"grpc_code": "AlreadyExists"}); n != 3 {
		t.Errorf("AlreadyExists count %d", n)
	}
}

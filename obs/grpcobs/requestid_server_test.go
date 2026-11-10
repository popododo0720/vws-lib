package grpcobs_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/grpcobs"
)

// headerStream records the response header a unary handler sets.
type headerStream struct{ header metadata.MD }

func (s *headerStream) Method() string { return "/svc.V1/Do" }
func (s *headerStream) SetHeader(md metadata.MD) error {
	s.header = metadata.Join(s.header, md)
	return nil
}
func (s *headerStream) SendHeader(md metadata.MD) error { return s.SetHeader(md) }
func (s *headerStream) SetTrailer(metadata.MD) error    { return nil }

func reason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if i, ok := d.(*errdetails.ErrorInfo); ok {
			return i.GetReason()
		}
	}
	return ""
}

var generatedID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestServerRequestIDRules(t *testing.T) {
	const field = "3f2b8c1e-9a4d-4e6f-8b7a-1c2d3e4f5a6b"
	long := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		header  []string // nil: no header
		field   string
		want    string // "" with wantErr false: a generated id
		wantErr bool
	}{
		{"absent header, empty field gets a new id", nil, "", "", false},
		{"empty header counts as absent", []string{""}, "", "", false},
		{"valid header is used", []string{"req-42"}, "", "req-42", false},
		{"all allowed characters", []string{"Az09._-"}, "", "Az09._-", false},
		{"64 characters", []string{long}, "", long, false},
		{"first of repeated headers", []string{"req-1", "req-2"}, "", "req-1", false},
		{"65 characters", []string{long + "a"}, "", "", true},
		{"space", []string{"req 42"}, "", "", true},
		{"newline", []string{"req\n42"}, "", "", true},
		{"slash", []string{"req/42"}, "", "", true},
		{"non-ascii", []string{"req-é"}, "", "", true},
		{"field without header is used", nil, field, field, false},
		{"field with empty header is used", []string{""}, field, field, false},
		{"field equal to header", []string{field}, field, field, false},
		{"header differs from field", []string{"req-42"}, field, "", true},
		{"header differs only in case", []string{strings.ToUpper(field)}, field, "", true},
		{"malformed field", nil, "bad id", "", true},
		{"malformed header with field", []string{"bad id"}, field, "", true},
	}
	ic := grpcobs.UnaryServerInterceptor("compute")
	info := &grpc.UnaryServerInfo{FullMethod: "/svc.V1/Do"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Twice: a retry of the same request resolves the same way.
			for range 2 {
				hs := &headerStream{}
				ctx := grpc.NewContextWithServerTransportStream(t.Context(), hs)
				if tc.header != nil {
					ctx = metadata.NewIncomingContext(ctx, metadata.MD{grpcobs.RequestIDHeader: tc.header})
				}
				var seen string
				called := false
				handler := func(ctx context.Context, _ any) (any, error) {
					called, seen = true, obs.RequestID(ctx)
					return "ok", nil
				}
				_, err := ic(ctx, &idRequest{id: tc.field}, info, handler)
				echo := hs.header.Get(grpcobs.RequestIDHeader)
				if tc.wantErr {
					if status.Code(err) != codes.InvalidArgument || reason(err) != "INVALID_SPEC" {
						t.Fatalf("err = %v (reason %q), want INVALID_ARGUMENT / INVALID_SPEC", err, reason(err))
					}
					if called || len(echo) != 0 {
						t.Fatalf("rejected call reached the handler (%v) or echoed %q", called, echo)
					}
					continue
				}
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if len(echo) != 1 || echo[0] != seen {
					t.Fatalf("echoed %q, handler saw %q", echo, seen)
				}
				if tc.want == "" && !generatedID.MatchString(seen) || tc.want != "" && seen != tc.want {
					t.Fatalf("id = %q, want %q", seen, tc.want)
				}
			}
		})
	}
}

func TestServerRequestIDOnTheWire(t *testing.T) {
	h := start(t)
	call := func(id, service string) (metadata.MD, error) {
		ctx, cancel := context.WithTimeout(obs.WithRequestID(t.Context(), id), 5*time.Second)
		defer cancel()
		var hdr metadata.MD
		_, err := h.client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: service}, grpc.Header(&hdr))
		return hdr, err //nolint:wrapcheck // the test reads the status code
	}
	// A slice, not a map: the handler assertion below needs "panic" last.
	for _, tc := range []struct {
		service string
		code    codes.Code
	}{{"ok", codes.OK}, {"notfound", codes.NotFound}, {"panic", codes.Internal}} {
		service, code := tc.service, tc.code
		hdr, err := call("req-"+service, service)
		got := strings.Join(hdr.Get(grpcobs.RequestIDHeader), ",")
		if status.Code(err) != code || got != "req-"+service {
			t.Errorf("%s: x-request-id response header = %q, err %v", service, got, err)
		}
	}
	if h.svc.sawRequestID != "req-panic" {
		t.Fatalf("handler saw %q", h.svc.sawRequestID)
	}
	_, err := call("req 42", "ok")
	if status.Code(err) != codes.InvalidArgument || reason(err) != "INVALID_SPEC" {
		t.Fatalf("malformed header: err = %v (reason %q)", err, reason(err))
	}
	if h.svc.sawRequestID != "req-panic" {
		t.Errorf("the rejected call reached the handler: %q", h.svc.sawRequestID)
	}
	if n := h.sum(t, obs.MetricRPCServerRequests, map[string]string{"grpc_code": "InvalidArgument"}); n != 1 {
		t.Errorf("InvalidArgument count %d", n)
	}
}

func TestStreamRequestIDOnTheWire(t *testing.T) {
	h := start(t)
	// ClientOptions has no stream interceptor, so the test sends the header itself.
	watch := func(id, service string) (metadata.MD, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		ctx = metadata.AppendToOutgoingContext(ctx, grpcobs.RequestIDHeader, id)
		ws, err := h.client.Watch(ctx, &grpc_health_v1.HealthCheckRequest{Service: service})
		if err != nil {
			return nil, err //nolint:wrapcheck // the test reads the status code
		}
		_, err = ws.Recv()
		hdr, herr := ws.Header()
		if herr != nil {
			return nil, herr //nolint:wrapcheck // the test reads the status code
		}
		return hdr, err //nolint:wrapcheck // the test reads the status code
	}
	for _, tc := range []struct {
		service string
		code    codes.Code
	}{{"ok", codes.OK}, {"notfound", codes.NotFound}} {
		service, code := tc.service, tc.code
		hdr, err := watch("req-"+service, service)
		got := strings.Join(hdr.Get(grpcobs.RequestIDHeader), ",")
		if status.Code(err) != code || got != "req-"+service {
			t.Errorf("%s: x-request-id response header = %q, err %v", service, got, err)
		}
	}
	_, err := watch("req/42", "ok")
	if status.Code(err) != codes.InvalidArgument || reason(err) != "INVALID_SPEC" {
		t.Fatalf("malformed header: err = %v (reason %q)", err, reason(err))
	}
}

func TestStreamServerRequestID(t *testing.T) {
	ic := grpcobs.StreamServerInterceptor("compute")
	info := &grpc.StreamServerInfo{FullMethod: "/svc.V1/Watch", IsServerStream: true}
	run := func(header string) (seen string, echo []string, err error) {
		md := metadata.Pairs(grpcobs.RequestIDHeader, header)
		ss := &fakeServerStream{ctx: metadata.NewIncomingContext(t.Context(), md)}
		err = ic(nil, ss, info, func(_ any, s grpc.ServerStream) error {
			seen = obs.RequestID(s.Context())
			return nil
		})
		return seen, ss.header.Get(grpcobs.RequestIDHeader), err
	}
	seen, echo, err := run("req-42")
	if err != nil || seen != "req-42" || len(echo) != 1 || echo[0] != "req-42" {
		t.Fatalf("valid header: seen %q echo %q err %v", seen, echo, err)
	}
	seen, echo, err = run("req/42")
	if status.Code(err) != codes.InvalidArgument || reason(err) != "INVALID_SPEC" || seen != "" || len(echo) != 0 {
		t.Fatalf("malformed header: seen %q echo %q err %v", seen, echo, err)
	}
	seen, echo, err = run("")
	if err != nil || !generatedID.MatchString(seen) || len(echo) != 1 || echo[0] != seen {
		t.Fatalf("empty header: seen %q echo %q err %v", seen, echo, err)
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx    context.Context //nolint:containedctx // test stream
	header metadata.MD
}

func (s *fakeServerStream) Context() context.Context { return s.ctx }
func (s *fakeServerStream) SetHeader(md metadata.MD) error {
	s.header = metadata.Join(s.header, md)
	return nil
}

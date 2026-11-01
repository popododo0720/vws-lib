package grpcobs_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/grpcobs"
)

// idRequest stands in for a generated message with a request_id field.
type idRequest struct{ id string }

func (r *idRequest) GetRequestId() string { return r.id }

// sentRequestIDs runs the client interceptor and returns the x-request-id values it sent.
func sentRequestIDs(ctx context.Context, t *testing.T, req any) []string {
	t.Helper()
	var got []string
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		got = md.Get(grpcobs.RequestIDHeader)
		return nil
	}
	ic := grpcobs.UnaryClientInterceptor("caller", "callee")
	if err := ic(ctx, "/svc.V1/Do", req, nil, nil, invoker); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	return got
}

func TestClientSendsRequestIDField(t *testing.T) {
	const field = "3f2b8c1e-9a4d-4e6f-8b7a-1c2d3e4f5a6b"
	tests := []struct {
		name  string
		ctxID string
		req   any
		want  []string
	}{
		{"field set wins over context id", "ctx-1", &idRequest{id: field}, []string{field}},
		{"field set without context id", "", &idRequest{id: field}, []string{field}},
		{"empty field falls back to context id", "ctx-1", &idRequest{}, []string{"ctx-1"}},
		{"message without the field sends context id", "ctx-1", struct{}{}, []string{"ctx-1"}},
		{"nothing to send", "", &idRequest{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.ctxID != "" {
				ctx = obs.WithRequestID(ctx, tc.ctxID)
			}
			got := sentRequestIDs(ctx, t, tc.req)
			if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
				t.Errorf("x-request-id = %q, want %q", got, tc.want)
			}
		})
	}
}

// Package grpcobs instruments gRPC servers and clients: traces through otelgrpc, the vws.rpc.*
// metrics from vws-lib's own instruments (so their names do not change with the instrumentation
// library), panic recovery, one error report per failure, and request_id propagation.
//
// A server returns only what errs.GRPCStatus allows: an Internal error reaches the client as
// "internal error" and never carries the cause.
package grpcobs

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/popododo0720/vws-lib/errs"
	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/report"
)

// RequestIDHeader is the gRPC metadata key that carries the request id between services.
const RequestIDHeader = "x-request-id"

// ServerOptions returns the options for grpc.NewServer: tracing, then the unary and stream
// interceptors.
func ServerOptions(service string) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))),
		grpc.ChainUnaryInterceptor(UnaryServerInterceptor(service)),
		grpc.ChainStreamInterceptor(StreamServerInterceptor(service)),
	}
}

// ClientOptions returns the dial options for a call to the peer service.
func ClientOptions(service, peer string) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))),
		grpc.WithChainUnaryInterceptor(UnaryClientInterceptor(service, peer)),
	}
}

func requestContext(ctx context.Context) context.Context {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(RequestIDHeader); len(v) > 0 && v[0] != "" {
			return obs.WithRequestID(ctx, v[0])
		}
	}
	return obs.WithRequestID(ctx, obs.NewRequestID())
}

// serverFault reports whether an error is the server's fault: only those go to Sentry.
// A NotFound or InvalidArgument is the caller's mistake and shows in the RPC metrics.
func serverFault(err error) bool {
	switch errs.CodeOf(err) {
	case errs.CodeInternal, errs.CodeUnavailable, errs.CodeUnspecified:
		return true
	default:
		return false
	}
}

// finish converts the handler outcome to the error the client sees and records it.
func finish(ctx context.Context, service, method string, start time.Time, err error) error {
	if err != nil && serverFault(err) {
		err = report.Error(ctx, err, slog.String(obs.FieldOp, method))
	}
	var out error
	if err != nil {
		out = errs.GRPCStatus(err).Err() //nolint:wrapcheck // the status error is the wire answer
	}
	obs.M().RPCServer(ctx, service, method, status.Code(out).String(), time.Since(start))
	return out //nolint:wrapcheck // the status error is the wire answer
}

// UnaryServerInterceptor records, reports, and recovers for unary RPCs.
func UnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		start := time.Now()
		ctx = requestContext(ctx)
		defer func() {
			if r := recover(); r != nil {
				resp, err = nil, finish(ctx, service, info.FullMethod, start, report.Panic(ctx, r))
			}
		}()
		resp, err = handler(ctx, req)
		return resp, finish(ctx, service, info.FullMethod, start, err)
	}
}

// StreamServerInterceptor does the same for streaming RPCs.
func StreamServerInterceptor(service string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		start := time.Now()
		ctx := requestContext(ss.Context())
		defer func() {
			if r := recover(); r != nil {
				err = finish(ctx, service, info.FullMethod, start, report.Panic(ctx, r))
			}
		}()
		err = handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
		return finish(ctx, service, info.FullMethod, start, err)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // the stream carries its request context by design
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// UnaryClientInterceptor sends the request id and records vws.rpc.client.duration. The error is
// returned as it came; callers convert it with errs.FromGRPC.
func UnaryClientInterceptor(service, peer string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		if id := obs.RequestID(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, RequestIDHeader, id)
		}
		err := invoker(ctx, method, req, reply, cc, opts...)
		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}
		obs.M().RPCClient(ctx, service, peer, strings.TrimPrefix(method, "/"), code.String(), time.Since(start))
		return err //nolint:wrapcheck // callers convert with errs.FromGRPC
	}
}

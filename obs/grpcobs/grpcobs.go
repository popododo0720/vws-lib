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

// incomingRequestID returns the first x-request-id value, or "" when absent or empty.
func incomingRequestID(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(RequestIDHeader); len(v) > 0 {
		return v[0]
	}
	return ""
}

// validRequestID reports whether id is 1-64 characters of [A-Za-z0-9._-] (vws-proto docs/errors.md).
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func invalidRequestID(field, message string) error {
	return errs.New(errs.CodeInvalidArgument, message, errs.WithField(field))
}

// resolveRequestID picks the id a server uses for a call per vws-proto docs/errors.md: a set
// request_id field wins and a present header must equal it; otherwise the header, otherwise a
// new id. A malformed or mismatched id is INVALID_ARGUMENT / INVALID_SPEC.
func resolveRequestID(header, field string) (string, error) {
	if header != "" && !validRequestID(header) {
		return "", invalidRequestID(RequestIDHeader, "x-request-id must be 1-64 characters of [A-Za-z0-9._-]")
	}
	if field != "" {
		if !validRequestID(field) {
			return "", invalidRequestID("request_id", "request_id must be 1-64 characters of [A-Za-z0-9._-]")
		}
		if header != "" && header != field {
			return "", invalidRequestID(RequestIDHeader, "x-request-id does not match request_id")
		}
		return field, nil
	}
	if header != "" {
		return header, nil
	}
	return obs.NewRequestID(), nil
}

// requestContext resolves the request id, puts it in ctx and returns it in the x-request-id
// response header through setHeader. A rejected id is not logged or echoed: ctx gets a new one.
func requestContext(
	ctx context.Context, field string, setHeader func(context.Context, metadata.MD) error,
) (context.Context, error) {
	id, err := resolveRequestID(incomingRequestID(ctx), field)
	if err != nil {
		return obs.WithRequestID(ctx, obs.NewRequestID()), err
	}
	ctx = obs.WithRequestID(ctx, id)
	if herr := setHeader(ctx, metadata.Pairs(RequestIDHeader, id)); herr != nil {
		slog.WarnContext(ctx, "set x-request-id response header", slog.String("error", herr.Error()))
	}
	return ctx, nil
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
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (resp any, err error) {
		start := time.Now()
		field := ""
		if r, ok := req.(requestIDField); ok {
			field = r.GetRequestId()
		}
		ctx, err = requestContext(ctx, field, grpc.SetHeader)
		if err != nil {
			return nil, finish(ctx, service, info.FullMethod, start, err)
		}
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
		ctx, err := requestContext(ss.Context(), "",
			func(_ context.Context, md metadata.MD) error { return ss.SetHeader(md) })
		if err != nil {
			return finish(ctx, service, info.FullMethod, start, err)
		}
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

// requestIDField is implemented by every generated message with a request_id field.
type requestIDField interface{ GetRequestId() string }

// outgoingRequestID picks the x-request-id for a call. vws-proto docs/errors.md requires the
// header to equal a set request_id field, so a non-empty field wins over the context id.
func outgoingRequestID(ctx context.Context, req any) string {
	if r, ok := req.(requestIDField); ok {
		if id := r.GetRequestId(); id != "" {
			return id
		}
	}
	return obs.RequestID(ctx)
}

// UnaryClientInterceptor sends the request id and records vws.rpc.client.duration. The error is
// returned as it came; callers convert it with errs.FromGRPC.
func UnaryClientInterceptor(service, peer string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		start := time.Now()
		if id := outgoingRequestID(ctx, req); id != "" {
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

// Package report is the one place an error is reported. Error does four things once: it records
// the error on the current span, writes one structured log line, counts vws.errors, and sends
// the error to Sentry when SENTRY_DSN is set. Services never import sentry-go.
//
// An error reported here is marked with errs.Reported, so the gRPC interceptor and the HTTP
// middleware that wrap the handler do not report it again.
package report

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/popododo0720/vws-lib/errs"
)

// Sink receives what Error and Panic produce besides the span and the log line. obs.Setup
// installs the metric counters and the Sentry sender. The zero Sink does nothing.
type Sink struct {
	Logger     *slog.Logger
	CountError func(ctx context.Context, code string)
	CountPanic func(ctx context.Context)
	// Capture sends an error to Sentry. nil when SENTRY_DSN is not set.
	Capture func(ctx context.Context, err error, tags map[string]string)
}

var sink atomic.Pointer[Sink]

// Configure installs the sink for the process.
func Configure(s Sink) { sink.Store(&s) }

func current() Sink {
	if s := sink.Load(); s != nil {
		return *s
	}
	return Sink{}
}

// Error reports err once and returns it marked as reported. A nil error returns nil. An error
// already marked is returned as is and not recorded again. attrs are added to the log line;
// request_id and resource_id among them become Sentry tags.
func Error(ctx context.Context, err error, attrs ...slog.Attr) error {
	if err == nil || errs.IsReported(err) {
		return err
	}
	s := current()
	code := errs.CodeOf(err)
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, code.String())
	if s.Logger != nil {
		s.Logger.LogAttrs(ctx, slog.LevelError, "error reported",
			append([]slog.Attr{slog.String("err", err.Error()), slog.String("err_code", code.String())}, attrs...)...)
	}
	if s.CountError != nil {
		s.CountError(ctx, code.String())
	}
	if s.Capture != nil {
		tags := map[string]string{"err_code": code.String()}
		if sc := span.SpanContext(); sc.IsValid() {
			tags["trace_id"] = sc.TraceID().String()
			tags["span_id"] = sc.SpanID().String()
		}
		for _, a := range attrs {
			if a.Key == "request_id" || a.Key == "resource_id" {
				tags[a.Key] = a.Value.String()
			}
		}
		s.Capture(ctx, err, tags)
	}
	return errs.Reported(err)
}

// Panic reports a recovered panic through the same path as Error and returns the Internal error
// to hand to the client: its message is "internal error", the panic value stays in the cause.
// The stack goes to the log only.
func Panic(ctx context.Context, recovered any) error {
	s := current()
	if s.CountPanic != nil {
		s.CountPanic(ctx)
	}
	cause := fmt.Errorf("panic: %v", recovered)
	return Error(ctx, errs.Internal(cause), slog.String("stack", string(debug.Stack())))
}

// FlushTimeout is how long Shutdown waits for Sentry to send queued events.
const FlushTimeout = 2 * time.Second

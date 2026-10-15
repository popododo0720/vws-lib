package obs

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// ctxHandler adds trace_id, span_id, and request_id from the context to each record. The OTLP
// bridge adds the trace ids to the OTLP record itself, but the stdout JSON gets nothing from it.
type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String(FieldTraceID, sc.TraceID().String()), slog.String(FieldSpanID, sc.SpanID().String()))
	}
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String(FieldRequestID, id))
	}
	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // slog.Handler contract: errors pass through
}

func (h ctxHandler) WithAttrs(a []slog.Attr) slog.Handler { return ctxHandler{h.Handler.WithAttrs(a)} }

func (h ctxHandler) WithGroup(n string) slog.Handler { return ctxHandler{h.Handler.WithGroup(n)} }

// fanout sends each record to every handler. Stdout is first: an OTLP failure never hides a log line.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(a []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(a)
	}
	return out
}

func (f fanout) WithGroup(n string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(n)
	}
	return out
}

// newLogger builds the service logger: stdout (JSON or text), plus extra handlers such as the
// OTLP bridge. Every record carries service and version.
func newLogger(c *Config, extra ...slog.Handler) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	var stdout slog.Handler
	if c.LogFormat == "text" {
		stdout = slog.NewTextHandler(c.LogOutput, opts)
	} else {
		stdout = slog.NewJSONHandler(c.LogOutput, opts)
	}
	handlers := fanout{ctxHandler{stdout}}
	for _, h := range extra {
		handlers = append(handlers, levelGate{h, c.LogLevel})
	}
	return slog.New(handlers).With(slog.String(FieldService, c.Service), slog.String(FieldVersion, c.Version))
}

// levelGate applies the configured level to a handler that has none of its own (the OTLP bridge).
type levelGate struct {
	slog.Handler
	min slog.Level
}

func (g levelGate) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= g.min && g.Handler.Enabled(ctx, l)
}

func (g levelGate) WithAttrs(a []slog.Attr) slog.Handler {
	return levelGate{g.Handler.WithAttrs(a), g.min}
}

func (g levelGate) WithGroup(n string) slog.Handler { return levelGate{g.Handler.WithGroup(n), g.min} }

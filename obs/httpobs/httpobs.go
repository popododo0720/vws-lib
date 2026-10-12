// Package httpobs wraps an http.Handler: traces through otelhttp, the vws.http.server.* metrics,
// panic recovery that answers 500 without the panic value, and request_id.
package httpobs

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/report"
)

// RequestIDHeader carries the request id on HTTP.
const RequestIDHeader = "X-Request-Id"

// Middleware instruments next. route returns the route template for a request ("/v1/{id}"),
// never the raw path, because the template is a metric label. A nil route labels everything
// "unknown".
func Middleware(service string, route func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			id := r.Header.Get(RequestIDHeader)
			if id == "" {
				id = obs.NewRequestID()
			}
			ctx := obs.WithRequestID(r.Context(), id)
			r = r.WithContext(ctx)
			w.Header().Set(RequestIDHeader, id)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if p := recover(); p != nil {
					report.Panic(ctx, p) //nolint:errcheck // the client gets a fixed body, not the error
					if !rec.wrote {
						http.Error(rec, "internal error", http.StatusInternalServerError)
					}
					rec.status = http.StatusInternalServerError
				}
				name := "unknown"
				if route != nil {
					name = route(r)
				}
				obs.M().HTTPServer(ctx, service, name, strconv.Itoa(rec.status/100)+"xx", time.Since(start))
				if rec.status >= http.StatusInternalServerError {
					slog.LogAttrs(ctx, slog.LevelWarn, "http server error", slog.String(obs.FieldOp, r.Method+" "+name))
				}
			}()
			next.ServeHTTP(rec, r)
		})
		return otelhttp.NewHandler(inner, service, otelhttp.WithMeterProvider(noop.NewMeterProvider()))
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	n, err := s.ResponseWriter.Write(b)
	if err != nil {
		return n, err //nolint:wrapcheck // http.ResponseWriter contract
	}
	return n, nil
}

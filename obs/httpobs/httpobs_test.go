package httpobs_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/popododo0720/vws-lib/obs"
	"github.com/popododo0720/vws-lib/obs/httpobs"
)

func TestMiddleware(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	var logs bytes.Buffer
	shutdown, err := obs.Setup(t.Context(), obs.ForService("console-api"), obs.WithOverride(func(c *obs.Config) {
		c.Getenv = func(string) string { return "" }
		c.LogOutput = &logs
		c.MetricReaders = []sdkmetric.Reader{reader}
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) }) //nolint:errcheck // cleanup

	mux := http.NewServeMux()
	mux.HandleFunc("/ok/", func(w http.ResponseWriter, r *http.Request) {
		if obs.RequestID(r.Context()) == "" {
			http.Error(w, "no request id", http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("secret password=hunter2") })
	h := httpobs.Middleware("console-api", func(r *http.Request) string {
		if strings.HasPrefix(r.URL.Path, "/ok/") {
			return "/ok/{id}"
		}
		return r.URL.Path
	})(mux)

	do := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do("/ok/vol-12345", map[string]string{httpobs.RequestIDHeader: "req-7"})
	if rec.Code != http.StatusOK || rec.Header().Get(httpobs.RequestIDHeader) != "req-7" {
		t.Errorf("ok: %d %v", rec.Code, rec.Header())
	}
	if gen := do("/ok/x", nil).Header().Get(httpobs.RequestIDHeader); len(gen) != 16 {
		t.Errorf("a missing request id must be generated: %q", gen)
	}

	rec = do("/boom", nil)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") || !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("panic: %d %q", rec.Code, rec.Body.String())
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if s, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == obs.MetricHTTPServerRequests {
				for _, dp := range s.DataPoints {
					route, _ := dp.Attributes.Value(attribute.Key("route"))
					class, _ := dp.Attributes.Value(attribute.Key("status_class"))
					counts[route.AsString()+" "+class.AsString()] += dp.Value
				}
			}
		}
	}
	if counts["/ok/{id} 2xx"] != 2 || counts["/boom 5xx"] != 1 {
		t.Errorf("route templates, not raw paths, must be the label: %v", counts)
	}
	if strings.Contains(logs.String(), "vol-12345") {
		t.Errorf("the raw path must not leak into the metrics or logs")
	}
}

func TestMiddlewareRequestIDPolicy(t *testing.T) {
	var seen string
	h := httpobs.Middleware("console-api", nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = obs.RequestID(r.Context())
	}))
	for _, tc := range []struct {
		name, in string
		keep     bool
	}{
		{"uuid", "3f2b8c1e-9a4d-4e2b-8f1a-0c5d6e7f8a9b", true},
		{"charset", "Az09._:-", true},
		{"64 bytes", strings.Repeat("a", 64), true},
		{"65 bytes", strings.Repeat("a", 65), false},
		{"1 MiB", strings.Repeat("a", 1<<20), false},
		{"space", "req 7", false},
		{"crlf", "req\r\nSet-Cookie: x=1", false},
		{"quote", `req"7`, false},
		{"slash", "req/7", false},
		{"utf8", "req-é", false},
		{"nul", "req\x007", false},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header[httpobs.RequestIDHeader] = []string{tc.in}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get(httpobs.RequestIDHeader)
		if got != seen {
			t.Errorf("%s: the response id %q must be the context id %q", tc.name, got, seen)
		}
		if tc.keep && got != tc.in {
			t.Errorf("%s: a valid id must be kept, got %q", tc.name, got)
		}
		if !tc.keep && (got == tc.in || len(got) != 16) {
			t.Errorf("%s: an invalid id must be replaced by a generated one, got %q", tc.name, got)
		}
	}
}

func TestMiddlewareInboundTraceContext(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator()) })
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	const inboundTrace = "0af7651916cd43dd8448eb211c80319c"
	serve := func(opts ...httpobs.Option) (trace.SpanContext, string) {
		var sc trace.SpanContext
		var bag string
		h := httpobs.Middleware("console-api", nil, opts...)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			sc = trace.SpanContextFromContext(r.Context())
			bag = baggage.FromContext(r.Context()).Member("tenant").Value()
		}))
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("traceparent", "00-"+inboundTrace+"-b7ad6b7169203331-01")
		req.Header.Set("baggage", "tenant=evil")
		h.ServeHTTP(httptest.NewRecorder(), req)
		return sc, bag
	}

	sc, bag := serve()
	if sc.TraceID().String() != inboundTrace || bag != "evil" {
		t.Fatalf("by default the inbound trace context must be honored: %s %q", sc.TraceID(), bag)
	}
	for range 2 {
		sc, bag = serve(httpobs.IgnoreInboundTraceContext())
		if !sc.IsValid() || sc.TraceID().String() == inboundTrace || bag != "" {
			t.Errorf("a public listener must start a new trace and drop baggage: %s %q", sc.TraceID(), bag)
		}
	}
}

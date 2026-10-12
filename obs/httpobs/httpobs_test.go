package httpobs_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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

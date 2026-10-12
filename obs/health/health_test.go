package health_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/popododo0720/vws-lib/obs/health"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestHealthzChecksNothing(t *testing.T) {
	h := health.New()
	h.Register("db", func(context.Context) error { return errors.New("down") })
	h.NotReady()
	if code, _ := get(t, h, "/healthz"); code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 whatever the dependencies do", code)
	}
}

func TestReadyzRunsChecksAndHidesErrorText(t *testing.T) {
	h := health.New()
	if code, _ := get(t, h, "/readyz"); code != http.StatusOK {
		t.Errorf("no checks: /readyz = %d", code)
	}
	h.Register("kube-api", func(context.Context) error { return errors.New("token=SECRET") })
	h.Register("store", func(context.Context) error { return nil })
	code, body := get(t, h, "/readyz")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "kube-api") || strings.Contains(body, "SECRET") || strings.Contains(body, "store") {
		t.Errorf("failing check: %d %s", code, body)
	}
}

func TestNotReadyFailsReadyz(t *testing.T) {
	h := health.New()
	h.NotReady()
	if code, _ := get(t, h, "/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("after NotReady /readyz = %d, want 503", code)
	}
}

func TestTelemetryBackendsCannotGateReadiness(t *testing.T) {
	for _, name := range []string{"otel-collector", "Telemetry", "sentry", "ClickHouse", "loki-push", "prometheus"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: want a panic", name)
				}
			}()
			health.New().Register(name, func(context.Context) error { return nil })
		}()
	}
}

func TestUnknownPathIs404(t *testing.T) {
	if code, _ := get(t, health.New(), "/other"); code != http.StatusNotFound {
		t.Errorf("got %d", code)
	}
}

func TestServe(t *testing.T) {
	addr, stop, err := health.Serve(t.Context(), "127.0.0.1:0", health.New())
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
	if err := stop(context.Background()); err != nil {
		t.Errorf("stop: %v", err)
	}
	if _, noop, err := health.Serve(t.Context(), "", health.New()); err != nil || noop(context.Background()) != nil {
		t.Errorf("an empty address must do nothing")
	}
}

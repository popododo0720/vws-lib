package obs_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/popododo0720/vws-lib/obs"
)

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test read
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func startPrometheus(t *testing.T, addr string) func(context.Context) error {
	t.Helper()
	var buf bytes.Buffer
	shutdown, err := obs.Setup(t.Context(), obs.ForService("iam"), obs.WithOverride(func(c *obs.Config) {
		c.Getenv = env(map[string]string{obs.EnvMetricsExporter: "prometheus", obs.EnvHealthAddr: addr})
		c.LogOutput = &buf
		c.Version = "v1.2.3"
	}))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return shutdown
}

func TestPrometheusPullServesEveryVWSInstrument(t *testing.T) {
	addr := freeAddr(t)
	shutdown := startPrometheus(t, addr)
	t.Cleanup(func() { _ = shutdown(context.Background()) }) //nolint:errcheck // not the subject

	m := obs.M()
	ctx := t.Context()
	m.RPCServer(ctx, "iam", "/vws.IAM/Get", "OK", 20*time.Millisecond)
	m.RPCClient(ctx, "iam", "storage", "vws.Storage/Get", "OK", time.Millisecond)
	m.HTTPServer(ctx, "iam", "/v1/{id}", "2xx", time.Millisecond)
	m.AWSError(ctx, "iam", "CreateUser", "EntityAlreadyExists")
	m.Error(ctx, "iam", "NotFound")
	m.Panic(ctx, "iam")
	m.Reconcile(ctx, "user", "ok", time.Millisecond)
	m.ReconcileError(ctx, "user", "Conflict")
	m.StoreConflict(ctx, "configmap")
	m.Audit(ctx, "iam", "delete", "ok")
	m.RegisterQueue("retry", func() int64 { return 3 }, func() time.Duration { return 4 * time.Second })

	code, body := get(t, "http://"+addr+"/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics status %d", code)
	}
	for _, want := range []string{
		"vws_rpc_server_requests_total{", "vws_rpc_server_duration_seconds_bucket{",
		"vws_rpc_client_duration_seconds_bucket{", "vws_http_server_requests_total{",
		"vws_http_server_duration_seconds_bucket{", "vws_aws_errors_total{", "vws_errors_total{",
		"vws_panics_total{", "vws_reconcile_total{", "vws_reconcile_duration_seconds_bucket{",
		"vws_reconcile_errors_total{", "vws_queue_depth{", "vws_queue_oldest_age_seconds{",
		"vws_store_conflicts_total{", "vws_audit_events_total{", "vws_build_info{",
	} {
		if !strings.Contains(body, "\n"+want) {
			t.Errorf("/metrics has no %s", want)
		}
	}
	// Every sample line is a vws_ series; the exporter's target_info and scope labels are off.
	for _, l := range strings.Split(body, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.HasPrefix(l, "vws_") {
			t.Errorf("non-vws series on /metrics: %q", l)
		}
	}
	if code, _ := get(t, "http://"+addr+"/healthz"); code != http.StatusOK {
		t.Errorf("/healthz status %d next to /metrics", code)
	}
	// The process-wide health handler may already be NotReady from an earlier test's shutdown,
	// so only check that /readyz still reaches it.
	if code, body := get(t, "http://"+addr+"/readyz"); !strings.Contains(body, "status") {
		t.Errorf("/readyz status %d body %q next to /metrics", code, body)
	}
}

// A restart in the same process (tests, a re-run Setup) must not hit a duplicate registration.
func TestPrometheusPullSurvivesRestartOnSameAddr(t *testing.T) {
	addr := freeAddr(t)
	for range 2 {
		shutdown := startPrometheus(t, addr)
		obs.M().Panic(t.Context(), "iam")
		code, body := get(t, "http://"+addr+"/metrics")
		if code != http.StatusOK || !strings.Contains(body, "vws_panics_total") {
			t.Fatalf("/metrics status %d, panics series present %v", code, strings.Contains(body, "vws_panics_total"))
		}
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	}
}

// OTEL_SDK_DISABLED=true must not serve /metrics, even when prometheus is named.
func TestPrometheusPullOffWhenSDKDisabled(t *testing.T) {
	addr := freeAddr(t)
	setup(t, map[string]string{
		obs.EnvMetricsExporter: "prometheus",
		obs.EnvHealthAddr:      addr,
		obs.EnvSDKDisabled:     "true",
	})
	if code, _ := get(t, "http://"+addr+"/metrics"); code != http.StatusNotFound {
		t.Errorf("/metrics status %d with OTEL_SDK_DISABLED=true, want 404", code)
	}
	if code, _ := get(t, "http://"+addr+"/healthz"); code != http.StatusOK {
		t.Errorf("/healthz status %d with OTEL_SDK_DISABLED=true, want 200", code)
	}
}

func TestPrometheusOffByDefaultAndValidated(t *testing.T) {
	addr := freeAddr(t)
	setup(t, map[string]string{obs.EnvHealthAddr: addr})
	if code, _ := get(t, "http://"+addr+"/metrics"); code != http.StatusNotFound {
		t.Errorf("/metrics status %d without opt-in, want 404", code)
	}
	for name, vars := range map[string]map[string]string{
		"prometheus without a health address": {obs.EnvMetricsExporter: "prometheus"},
		"unknown metrics exporter":            {obs.EnvMetricsExporter: "statsd", obs.EnvHealthAddr: "127.0.0.1:0"},
		"prometheus for traces":               {obs.EnvTracesExporter: "prometheus", obs.EnvHealthAddr: "127.0.0.1:0"},
	} {
		var buf bytes.Buffer
		shutdown, err := obs.Setup(t.Context(), obs.ForService("iam"), obs.WithOverride(func(c *obs.Config) {
			c.Getenv = env(vars)
			c.LogOutput = &buf
		}))
		if err == nil {
			_ = shutdown(context.Background()) //nolint:errcheck // not the subject
			t.Errorf("%s: Setup accepted it", name)
		}
	}
}

// Package health serves /healthz and /readyz.
//
// /healthz says only that the process runs. It checks no dependency, because restarting a
// service for a dead dependency makes an outage bigger. /readyz runs the registered checks and
// fails once NotReady is called (SIGTERM). A telemetry backend is never a readiness check: an
// observability outage must not become a service outage.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const checkBudget = 2 * time.Second

// forbidden are name prefixes of telemetry backends. Register panics on them.
var forbidden = []string{"otel", "telemetry", "collector", "sentry", "clickhouse", "loki", "tempo", "prometheus"}

// Handler holds the readiness checks.
type Handler struct {
	mu       sync.Mutex
	checks   map[string]func(context.Context) error
	notReady atomic.Bool
}

// New returns an empty Handler: always healthy and ready.
func New() *Handler { return &Handler{checks: map[string]func(context.Context) error{}} }

var def = New()

// Default is the process-wide Handler that obs.Setup serves.
func Default() *Handler { return def }

// Register adds a readiness check. The check must be cheap: read a cached result, do not call
// the dependency on each probe. Registering a telemetry backend panics.
func (h *Handler) Register(name string, check func(context.Context) error) {
	lower := strings.ToLower(name)
	for _, p := range forbidden {
		if strings.HasPrefix(lower, p) {
			panic(fmt.Sprintf("health: %q looks like a telemetry backend; it must not gate readiness", name))
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = check
}

// NotReady makes /readyz fail from now on. Call it first on SIGTERM, then stop serving.
func (h *Handler) NotReady() { h.notReady.Store(true) }

// ServeHTTP serves /healthz and /readyz.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n")) //nolint:errcheck // a failed write to the probe has no remedy
	case "/readyz":
		h.ready(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	var failed []string
	if h.notReady.Load() {
		failed = append(failed, "shutting-down")
	}
	h.mu.Lock()
	checks := make(map[string]func(context.Context) error, len(h.checks))
	for k, v := range h.checks {
		checks[k] = v
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), checkBudget)
	defer cancel()
	for name, check := range checks {
		if err := check(ctx); err != nil {
			failed = append(failed, name)
		}
	}
	sort.Strings(failed)
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	body := map[string]any{"status": "ok"}
	if len(failed) > 0 {
		status = http.StatusServiceUnavailable
		body = map[string]any{"status": "not ready", "failed": failed} // names only: error text may leak
	}
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return // the probe went away
	}
}

// Serve listens on addr and serves h until the returned stop function is called. bound is the
// address actually listened on (useful for ":0"). addr "" does nothing and returns a no-op stop.
func Serve(ctx context.Context, addr string, h http.Handler) (bound string, stop func(context.Context) error, err error) {
	if addr == "" {
		return "", func(context.Context) error { return nil }, nil
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("health listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		// Serve returns http.ErrServerClosed after stop; any other error ends the listener
		// and the next probe fails, which is the signal.
		_ = srv.Serve(ln) //nolint:errcheck // see above
	}()
	return ln.Addr().String(), srv.Shutdown, nil
}

package obs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// rateLimitedHandler logs OTel SDK errors as warnings, at most one per interval. The rest are
// counted and reported in the next line. A dead Collector must not flood the log.
type rateLimitedHandler struct {
	logger     *slog.Logger
	interval   time.Duration
	mu         sync.Mutex
	last       time.Time
	suppressed atomic.Int64
	now        func() time.Time
}

func (h *rateLimitedHandler) Handle(err error) {
	h.mu.Lock()
	now := h.now()
	if !h.last.IsZero() && now.Sub(h.last) < h.interval {
		h.mu.Unlock()
		h.suppressed.Add(1)
		return
	}
	h.last = now
	h.mu.Unlock()
	h.logger.LogAttrs(context.Background(), slog.LevelWarn, "otel sdk error",
		slog.String(FieldErr, err.Error()), slog.Int64("suppressed", h.suppressed.Swap(0)))
}

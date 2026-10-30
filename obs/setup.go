// Package obs sets up OpenTelemetry logs, traces, and metrics for VWS services.
//
//	shutdown, err := obs.Setup(ctx, obs.ForService("compute"))
//
// Setup reads only the standard OTEL_* and SENTRY_* variables and VWS_LOG_LEVEL, VWS_LOG_FORMAT,
// VWS_HEALTH_ADDR (names.go). OTEL_METRICS_EXPORTER=prometheus serves the metrics as vws_* on
// /metrics at VWS_HEALTH_ADDR instead of pushing them. With OTEL_EXPORTER_OTLP_ENDPOINT empty,
// or OTEL_SDK_DISABLED=true, no OTLP exporter is created and the instrumentation calls do
// nothing visible; the JSON log on stdout stays. Defaults are in defaults.go, names in names.go.
//
// Design: https://github.com/popododo0720/vws-deploy/issues/42.
package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/popododo0720/vws-lib/obs/health"
	"github.com/popododo0720/vws-lib/obs/report"
)

// Setup starts logging, tracing, metrics, the error report path, and the health server, and
// returns the function that stops them. Call shutdown at exit with a context that is still alive;
// it flushes within Config.ShutdownTimeout. A second call does nothing.
func Setup(ctx context.Context, opts ...Option) (shutdown func(context.Context) error, err error) {
	cfg, err := resolve(opts...)
	if err != nil {
		return nil, fmt.Errorf("obs config: %w", err)
	}
	if cfg.Version == "" {
		cfg.Version = "devel"
	}
	res, err := newResource(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	var stops []func(context.Context) error
	fail := func(err error) (func(context.Context) error, error) {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		for i := len(stops) - 1; i >= 0; i-- {
			if stopErr := stops[i](shutdownCtx); stopErr != nil {
				err = errors.Join(err, stopErr)
			}
		}
		return nil, err
	}

	// Traces.
	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res), sdktrace.WithSampler(cfg.sampler())}
	if cfg.Exporting() && cfg.TracesExporter == "otlp" {
		exp, err := cfg.newTraceExporter(ctx)
		if err != nil {
			return fail(err)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp,
			sdktrace.WithMaxQueueSize(cfg.QueueSize), sdktrace.WithMaxExportBatchSize(cfg.BatchSize),
			sdktrace.WithBatchTimeout(cfg.BatchInterval), sdktrace.WithExportTimeout(cfg.ExportTimeout)))
	}
	for _, sp := range cfg.SpanProcessors {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(sp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	stops = append(stops, tp.Shutdown)

	// Metrics.
	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if cfg.Exporting() && cfg.MetricsExporter == "otlp" {
		exp, err := cfg.newMetricExporter(ctx)
		if err != nil {
			return fail(err)
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(cfg.MetricInterval), sdkmetric.WithTimeout(cfg.ExportTimeout))))
	}
	var metricsHandler http.Handler
	if cfg.MetricsExporter == "prometheus" {
		// A registry per Setup, not the client_golang global: a second Setup in the same process
		// would otherwise fail with a duplicate registration.
		reg := prometheus.NewRegistry()
		exp, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutTargetInfo(), otelprom.WithoutScopeInfo())
		if err != nil {
			return fail(fmt.Errorf("prometheus exporter: %w", err))
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(exp))
		metricsHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError, Timeout: prometheusScrapeTimeout,
		})
	}
	for _, r := range cfg.MetricReaders {
		mpOpts = append(mpOpts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	stops = append(stops, mp.Shutdown)
	metrics, err := newMetrics(mp.Meter(instrumentationScopeName), cfg.Service, cfg.Version, buildCommit())
	if err != nil {
		return fail(err)
	}

	// Logs: stdout always, OTLP too when it is on.
	var extra []slog.Handler
	if cfg.Exporting() && cfg.LogsExporter == "otlp" {
		exp, err := cfg.newLogExporter(ctx)
		if err != nil {
			return fail(err)
		}
		lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exp,
			sdklog.WithMaxQueueSize(cfg.QueueSize), sdklog.WithExportMaxBatchSize(cfg.BatchSize),
			sdklog.WithExportInterval(cfg.BatchInterval), sdklog.WithExportTimeout(cfg.ExportTimeout))))
		stops = append(stops, lp.Shutdown)
		extra = append(extra, otelslog.NewHandler(cfg.Service, otelslog.WithLoggerProvider(lp)))
	}
	logger := newLogger(&cfg, extra...)

	// Error reports and Sentry.
	sink := report.Sink{
		Logger:     logger,
		CountError: func(ctx context.Context, code string) { metrics.Error(ctx, cfg.Service, code) },
		CountPanic: func(ctx context.Context) { metrics.Panic(ctx, cfg.Service) },
	}
	capture, flush, err := report.InitSentry(report.SentryOptions{
		DSN: cfg.Sentry.DSN, Environment: cfg.Sentry.Environment, Release: cfg.Sentry.Release,
		SampleRate: cfg.Sentry.SampleRate, Service: cfg.Service,
	})
	if err != nil {
		return fail(fmt.Errorf("obs sentry: %w", err))
	}
	sink.Capture = capture
	if flush != nil {
		stops = append(stops, func(context.Context) error { flush(report.FlushTimeout); return nil })
	}

	// Health server and SIGTERM.
	h := health.Default()
	var served http.Handler = h
	if metricsHandler != nil {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metricsHandler)
		mux.Handle("/", h)
		served = mux
	}
	_, stopHealth, err := health.Serve(ctx, cfg.HealthAddr, served)
	if err != nil {
		return fail(fmt.Errorf("obs health: %w", err))
	}
	stops = append(stops, stopHealth)

	// Install the process globals only after every step succeeded. A failed Setup must not leave
	// a shut-down provider in otel, slog or obs.M for the next Setup or a still-running service.
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetMeterProvider(mp)
	current.Store(metrics)
	slog.SetDefault(logger)
	otel.SetErrorHandler(&rateLimitedHandler{logger: logger, interval: cfg.ErrorLogEvery, now: time.Now})
	report.Configure(sink)

	term := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		select {
		case <-term:
			h.NotReady()
		case <-done:
		}
	}()

	logger.LogAttrs(ctx, slog.LevelInfo, "observability started",
		slog.Bool("otlp", cfg.Exporting()), slog.Bool("sentry", cfg.Sentry.DSN != ""))

	var once sync.Once
	return func(shutdownCtx context.Context) error {
		var result error
		once.Do(func() {
			h.NotReady()
			signal.Stop(term)
			close(done)
			ctx, cancel := context.WithTimeout(shutdownCtx, cfg.ShutdownTimeout)
			defer cancel()
			var errs []error
			for i := len(stops) - 1; i >= 0; i-- {
				if err := stops[i](ctx); err != nil {
					errs = append(errs, err)
				}
			}
			result = errors.Join(errs...)
		})
		return result
	}, nil
}

func newResource(ctx context.Context, c *Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", c.Service),
		attribute.String("service.version", c.Version),
	}
	instance := c.InstanceID
	if instance == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = NewRequestID()
		}
		instance = host
	}
	attrs = append(attrs, attribute.String("service.instance.id", instance))
	for k, v := range c.ResourceAttrs {
		if k == "service.name" || k == "service.version" || k == "service.instance.id" {
			continue // the explicit settings above win
		}
		attrs = append(attrs, attribute.String(k, v))
	}
	res, err := resource.New(ctx, resource.WithAttributes(attrs...))
	if err != nil {
		return nil, fmt.Errorf("obs resource: %w", err)
	}
	return res, nil
}

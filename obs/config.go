package obs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config is everything Setup reads. A service does not build one: it passes ForService and,
// rarely, WithOverride to Setup. Values come from, in order: the defaults in defaults.go,
// ForService, the environment (names.go), WithOverride.
type Config struct {
	Service    string
	Version    string
	InstanceID string

	// Endpoint is OTEL_EXPORTER_OTLP_ENDPOINT. Empty means nothing is exported.
	Endpoint string
	// Protocol is "grpc" or "http/protobuf".
	Protocol string
	Headers  map[string]string
	// Disabled is OTEL_SDK_DISABLED=true. It creates no OpenTelemetry exporter and no Prometheus
	// reader, so /metrics is not served even when prometheus is named.
	Disabled bool

	Sampler    string
	SamplerArg float64

	// The exporter per signal: "otlp" or "none". MetricsExporter may also be "prometheus": an
	// opt-in pull endpoint, /metrics on HealthAddr, instead of OTLP push.
	TracesExporter  string
	MetricsExporter string
	LogsExporter    string

	LogLevel  slog.Level
	LogFormat string
	// LogOutput is where the JSON log goes. Default os.Stdout.
	LogOutput io.Writer

	HealthAddr    string
	ResourceAttrs map[string]string

	Sentry SentryConfig

	QueueSize       int
	BatchSize       int
	BatchInterval   time.Duration
	ExportTimeout   time.Duration
	MetricInterval  time.Duration
	RetryInitial    time.Duration
	RetryMax        time.Duration
	RetryElapsed    time.Duration
	ShutdownTimeout time.Duration
	ErrorLogEvery   time.Duration

	// MetricReaders and SpanProcessors are added to the providers next to the OTLP exporters.
	// They are the hook for tests (ManualReader, span recorder) and extra readers.
	MetricReaders  []sdkmetric.Reader
	SpanProcessors []sdktrace.SpanProcessor

	// Getenv is os.Getenv unless a test replaces it.
	Getenv func(string) string
}

// SentryConfig turns on the Sentry report path when DSN is not empty.
type SentryConfig struct {
	DSN         string
	Environment string
	Release     string
	SampleRate  float64
}

// Exporting reports whether any OTLP exporter is created.
func (c *Config) Exporting() bool { return c.Endpoint != "" && !c.Disabled }

type phase int

const (
	phaseBase phase = iota
	phaseOverride
)

// Option changes a Config. ForService runs before the environment is read, WithOverride after.
type Option struct {
	phase phase
	fn    func(*Config)
}

// ForService names the service. OTEL_SERVICE_NAME still wins over it.
func ForService(name string) Option {
	return Option{phase: phaseBase, fn: func(c *Config) { c.Service = name }}
}

// WithOverride changes a Config value after the environment is read.
func WithOverride(fn func(*Config)) Option {
	return Option{phase: phaseOverride, fn: fn}
}

func defaultConfig() Config {
	return Config{
		Protocol:        defaultProtocol,
		Sampler:         defaultSampler,
		SamplerArg:      defaultSamplerArg,
		LogLevel:        slog.LevelInfo,
		LogFormat:       defaultLogFormat,
		QueueSize:       defaultQueueSize,
		BatchSize:       defaultBatchSize,
		BatchInterval:   defaultBatchInterval,
		ExportTimeout:   defaultExportTimeout,
		MetricInterval:  defaultMetricInterval,
		RetryInitial:    defaultRetryInitial,
		RetryMax:        defaultRetryMax,
		RetryElapsed:    defaultRetryElapsed,
		ShutdownTimeout: defaultShutdownTimeout,
		ErrorLogEvery:   defaultErrorLogInterval,
		Sentry:          SentryConfig{SampleRate: 1.0},
		Getenv:          os.Getenv,
		LogOutput:       os.Stdout,
	}
}

// resolve builds the Config from defaults, options, and the environment.
func resolve(opts ...Option) (Config, error) {
	c := defaultConfig()
	for _, o := range opts {
		if o.phase == phaseBase && o.fn != nil {
			o.fn(&c)
		}
	}
	// A WithOverride may replace Getenv, so apply it first only for that field.
	for _, o := range opts {
		if o.phase == phaseOverride && o.fn != nil {
			probe := c
			o.fn(&probe)
			if probe.Getenv != nil {
				c.Getenv = probe.Getenv
			}
		}
	}
	if err := c.readEnv(); err != nil {
		return Config{}, err
	}
	for _, o := range opts {
		if o.phase == phaseOverride && o.fn != nil {
			o.fn(&c)
		}
	}
	return c, c.validate()
}

func (c *Config) readEnv() error {
	env := c.Getenv
	if v := env(EnvServiceName); v != "" {
		c.Service = v
	}
	if c.Service == "" {
		c.Service = filepath.Base(os.Args[0])
	}
	c.Endpoint = env(EnvEndpoint)
	if v := env(EnvProtocol); v != "" {
		c.Protocol = v
	}
	c.Headers = parseKV(env(EnvHeaders))
	c.ResourceAttrs = parseKV(env(EnvResourceAttrs))
	if v := env(EnvSDKDisabled); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", EnvSDKDisabled, v, err)
		}
		c.Disabled = b
	}
	if v := env(EnvSampler); v != "" {
		c.Sampler = v
	}
	if v := env(EnvSamplerArg); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", EnvSamplerArg, v, err)
		}
		c.SamplerArg = f
	}
	exporterDefault := "none"
	if c.Exporting() {
		exporterDefault = "otlp"
	}
	c.TracesExporter = firstNonEmpty(env(EnvTracesExporter), exporterDefault)
	c.MetricsExporter = firstNonEmpty(env(EnvMetricsExporter), exporterDefault)
	c.LogsExporter = firstNonEmpty(env(EnvLogsExporter), exporterDefault)
	if v := env(EnvMetricInterval); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return fmt.Errorf("%s=%q: want a positive number of milliseconds", EnvMetricInterval, v)
		}
		c.MetricInterval = time.Duration(ms) * time.Millisecond
	}
	level := firstNonEmpty(env(EnvLogLevel), defaultLogLevel)
	if err := c.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return fmt.Errorf("%s=%q: %w", EnvLogLevel, level, err)
	}
	c.LogFormat = firstNonEmpty(env(EnvLogFormat), c.LogFormat)
	c.HealthAddr = env(EnvHealthAddr)
	c.Sentry.DSN = env(EnvSentryDSN)
	c.Sentry.Environment = firstNonEmpty(env(EnvSentryEnv), c.ResourceAttrs["deployment.environment"])
	c.Sentry.Release = firstNonEmpty(env(EnvSentryRelease), c.Version)
	if v := env(EnvSentrySample); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return fmt.Errorf("%s=%q: want a number from 0 to 1", EnvSentrySample, v)
		}
		c.Sentry.SampleRate = f
	}
	return nil
}

func (c *Config) validate() error {
	switch c.Protocol {
	case "grpc", "http/protobuf":
	default:
		return fmt.Errorf("%s=%q: want grpc or http/protobuf", EnvProtocol, c.Protocol)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("%s=%q: want json or text", EnvLogFormat, c.LogFormat)
	}
	for name, v := range map[string]string{EnvTracesExporter: c.TracesExporter, EnvLogsExporter: c.LogsExporter} {
		if v != "otlp" && v != "none" {
			return fmt.Errorf("%s=%q: this version supports otlp and none", name, v)
		}
	}
	switch c.MetricsExporter {
	case "otlp", "none":
	case "prometheus":
		if c.HealthAddr == "" {
			return fmt.Errorf("%s=prometheus serves /metrics on %s, which is empty", EnvMetricsExporter, EnvHealthAddr)
		}
	default:
		return fmt.Errorf("%s=%q: this version supports otlp, prometheus and none", EnvMetricsExporter, c.MetricsExporter)
	}
	switch c.Sampler {
	case "always_on", "always_off", "traceidratio", "parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio":
	default:
		return fmt.Errorf("%s=%q is not a supported sampler", EnvSampler, c.Sampler)
	}
	if c.SamplerArg < 0 || c.SamplerArg > 1 {
		return fmt.Errorf("%s=%v: want a number from 0 to 1", EnvSamplerArg, c.SamplerArg)
	}
	if c.QueueSize <= 0 || c.BatchSize <= 0 || c.BatchSize > c.QueueSize {
		return fmt.Errorf("batch size %d must be positive and not above queue size %d", c.BatchSize, c.QueueSize)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseKV reads "k1=v1,k2=v2". A malformed pair is skipped.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && k != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

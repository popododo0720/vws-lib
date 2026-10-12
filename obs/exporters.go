package obs

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The exporters take the endpoint and headers from Config, not from the process environment,
// so one Config decides everything. gRPC takes the URL as given. http/protobuf takes a full URL,
// so the signal path is appended to the base endpoint, as the OTLP specification says.

func httpURL(base, path string) string { return strings.TrimRight(base, "/") + path }

func (c *Config) newTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	if c.Protocol == "http/protobuf" {
		e, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(httpURL(c.Endpoint, "/v1/traces")),
			otlptracehttp.WithHeaders(c.Headers),
			otlptracehttp.WithTimeout(c.ExportTimeout),
			otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
				Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
			}))
		if err != nil {
			return nil, fmt.Errorf("trace exporter: %w", err)
		}
		return e, nil
	}
	e, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(c.Endpoint),
		otlptracegrpc.WithHeaders(c.Headers),
		otlptracegrpc.WithTimeout(c.ExportTimeout),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
		}))
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	return e, nil
}

func (c *Config) newMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	if c.Protocol == "http/protobuf" {
		e, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(httpURL(c.Endpoint, "/v1/metrics")),
			otlpmetrichttp.WithHeaders(c.Headers),
			otlpmetrichttp.WithTimeout(c.ExportTimeout),
			otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
				Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
			}))
		if err != nil {
			return nil, fmt.Errorf("metric exporter: %w", err)
		}
		return e, nil
	}
	e, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(c.Endpoint),
		otlpmetricgrpc.WithHeaders(c.Headers),
		otlpmetricgrpc.WithTimeout(c.ExportTimeout),
		otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{
			Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
		}))
	if err != nil {
		return nil, fmt.Errorf("metric exporter: %w", err)
	}
	return e, nil
}

func (c *Config) newLogExporter(ctx context.Context) (sdklog.Exporter, error) {
	if c.Protocol == "http/protobuf" {
		e, err := otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(httpURL(c.Endpoint, "/v1/logs")),
			otlploghttp.WithHeaders(c.Headers),
			otlploghttp.WithTimeout(c.ExportTimeout),
			otlploghttp.WithRetry(otlploghttp.RetryConfig{
				Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
			}))
		if err != nil {
			return nil, fmt.Errorf("log exporter: %w", err)
		}
		return e, nil
	}
	e, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(c.Endpoint),
		otlploggrpc.WithHeaders(c.Headers),
		otlploggrpc.WithTimeout(c.ExportTimeout),
		otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
			Enabled: true, InitialInterval: c.RetryInitial, MaxInterval: c.RetryMax, MaxElapsedTime: c.RetryElapsed,
		}))
	if err != nil {
		return nil, fmt.Errorf("log exporter: %w", err)
	}
	return e, nil
}

func (c *Config) sampler() sdktrace.Sampler {
	if (!c.Exporting() || c.TracesExporter != "otlp") && len(c.SpanProcessors) == 0 {
		// Nothing is exported, so nothing is recorded. Span contexts and trace ids still exist,
		// so logs carry trace_id and propagation keeps working.
		return sdktrace.NeverSample()
	}
	switch c.Sampler {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(c.SamplerArg)
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.SamplerArg))
	}
}

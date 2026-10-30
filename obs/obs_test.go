package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/popododo0720/vws-lib/obs"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// setup starts obs with a fake environment and returns the stdout log buffer.
func setup(t *testing.T, vars map[string]string, extra ...obs.Option) (*bytes.Buffer, func(context.Context) error) {
	t.Helper()
	var buf bytes.Buffer
	opts := append([]obs.Option{
		obs.ForService("compute"),
		obs.WithOverride(func(c *obs.Config) { c.Getenv = env(vars); c.LogOutput = &buf; c.Version = "v1.2.3" }),
	}, extra...)
	shutdown, err := obs.Setup(t.Context(), opts...)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) }) //nolint:errcheck // second call after the test body, error is not the subject
	return &buf, shutdown
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestConfigPrecedenceAndValidation(t *testing.T) {
	var got obs.Config
	capture := func(c *obs.Config) { got = *c }
	run := func(vars map[string]string, opts ...obs.Option) error {
		var buf bytes.Buffer
		all := append([]obs.Option{
			obs.ForService("storage"),
			obs.WithOverride(func(c *obs.Config) {
				c.Getenv = env(vars)
				c.LogOutput = &buf
				c.ShutdownTimeout = 100 * time.Millisecond
			}),
		}, opts...)
		all = append(all, obs.WithOverride(capture))
		shutdown, err := obs.Setup(t.Context(), all...)
		if err == nil {
			defer shutdown(context.Background()) //nolint:errcheck // not the subject
		}
		return err //nolint:wrapcheck // the test inspects the error
	}

	if err := run(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if got.Service != "storage" || got.Exporting() || got.TracesExporter != "none" || got.Protocol != "grpc" || got.LogFormat != "json" {
		t.Errorf("defaults: %+v", got)
	}
	if got.SamplerArg != 0.1 || got.Sampler != "parentbased_traceidratio" {
		t.Errorf("sampler defaults: %v %v", got.Sampler, got.SamplerArg)
	}

	if err := run(map[string]string{"OTEL_SERVICE_NAME": "from-env", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317", "VWS_LOG_LEVEL": "debug"}); err != nil {
		t.Fatal(err)
	}
	if got.Service != "from-env" || !got.Exporting() || got.TracesExporter != "otlp" || got.LogLevel != slog.LevelDebug {
		t.Errorf("env must beat ForService and turn exporters on: %+v", got)
	}

	if err := run(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317", "OTEL_SDK_DISABLED": "true"}); err != nil {
		t.Fatal(err)
	}
	if got.Exporting() {
		t.Errorf("OTEL_SDK_DISABLED must turn exporting off")
	}

	if err := run(map[string]string{"VWS_LOG_LEVEL": "debug"}, obs.WithOverride(func(c *obs.Config) { c.LogLevel = slog.LevelError })); err != nil {
		t.Fatal(err)
	}
	if got.LogLevel != slog.LevelError {
		t.Errorf("WithOverride must beat the environment")
	}

	for name, vars := range map[string]map[string]string{
		"bad protocol": {"OTEL_EXPORTER_OTLP_PROTOCOL": "carrier-pigeon"},
		"bad level":    {"VWS_LOG_LEVEL": "loud"},
		"bad format":   {"VWS_LOG_FORMAT": "xml"},
		"bad sampler":  {"OTEL_TRACES_SAMPLER": "lucky"},
		"bad ratio":    {"OTEL_TRACES_SAMPLER_ARG": "7"},
		"prometheus":   {"OTEL_METRICS_EXPORTER": "prometheus"},
		"bad disabled": {"OTEL_SDK_DISABLED": "maybe"},
		"bad sentry":   {"SENTRY_SAMPLE_RATE": "2"},
		"bad interval": {"OTEL_METRIC_EXPORT_INTERVAL": "0"},
	} {
		if err := run(vars); err == nil {
			t.Errorf("%s: Setup must fail", name)
		}
	}
}

// A late failure (the health port is taken) must leave the process globals alone, not point
// them at providers that fail() already shut down.
func TestFailedSetupKeepsProcessGlobals(t *testing.T) {
	tp, mp, logger, metrics := otel.GetTracerProvider(), otel.GetMeterProvider(), slog.Default(), obs.M()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck // test listener

	_, err = obs.Setup(t.Context(), obs.ForService("compute"), obs.WithOverride(func(c *obs.Config) {
		c.Getenv = env(map[string]string{obs.EnvHealthAddr: ln.Addr().String()})
		c.LogOutput = io.Discard
		c.ShutdownTimeout = 100 * time.Millisecond
	}))
	if err == nil {
		t.Fatal("Setup must fail when the health address is taken")
	}

	if got := otel.GetTracerProvider(); got != tp {
		t.Error("failed Setup replaced the global tracer provider")
	}
	if got := otel.GetMeterProvider(); got != mp {
		t.Error("failed Setup replaced the global meter provider")
	}
	if got := slog.Default(); got != logger {
		t.Error("failed Setup replaced the default logger")
	}
	if got := obs.M(); got != metrics {
		t.Error("failed Setup replaced obs.M")
	}
}

func TestLogCarriesTraceRequestAndServiceFields(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	buf, _ := setup(t, map[string]string{"OTEL_TRACES_SAMPLER": "always_on"},
		obs.WithOverride(func(c *obs.Config) { c.SpanProcessors = []sdktrace.SpanProcessor{rec} }))

	ctx, span := otel.Tracer("t").Start(t.Context(), "op")
	ctx = obs.WithRequestID(ctx, "req-1")
	slog.InfoContext(ctx, "hello", slog.String(obs.FieldResourceID, "vol-1"))
	span.End()

	var hello map[string]any
	for _, l := range lines(t, buf) {
		if l["msg"] == "hello" {
			hello = l
		}
	}
	if hello == nil {
		t.Fatalf("no hello line in %s", buf.String())
	}
	sc := span.SpanContext()
	want := map[string]string{
		"service": "compute", "version": "v1.2.3", "request_id": "req-1", "resource_id": "vol-1",
		"trace_id": sc.TraceID().String(), "span_id": sc.SpanID().String(), "level": "INFO",
	}
	for k, v := range want {
		if hello[k] != v {
			t.Errorf("%s = %v, want %v", k, hello[k], v)
		}
	}
	if len(rec.Ended()) != 1 {
		t.Errorf("span recorder saw %d spans", len(rec.Ended()))
	}
}

func TestTraceIDsExistWhenNothingIsExported(t *testing.T) {
	buf, _ := setup(t, map[string]string{})
	ctx, span := otel.Tracer("t").Start(t.Context(), "op")
	slog.InfoContext(ctx, "x")
	span.End()
	if span.IsRecording() {
		t.Errorf("nothing exports, so nothing must be recorded")
	}
	for _, l := range lines(t, buf) {
		if l["msg"] == "x" && (l["trace_id"] == nil || l["trace_id"] == "00000000000000000000000000000000") {
			t.Errorf("a log line needs a trace id even when export is off: %v", l)
		}
	}
}

func TestLogLevelAppliesAndTextFormat(t *testing.T) {
	buf, _ := setup(t, map[string]string{"VWS_LOG_LEVEL": "warn", "VWS_LOG_FORMAT": "text"})
	slog.InfoContext(t.Context(), "quiet")
	slog.WarnContext(t.Context(), "loud")
	out := buf.String()
	if strings.Contains(out, "quiet") || !strings.Contains(out, "loud") || strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("level or format ignored: %q", out)
	}
}

func TestMetricsNamesAndLabels(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, map[string]string{}, obs.WithOverride(func(c *obs.Config) { c.MetricReaders = []sdkmetric.Reader{reader} }))
	m := obs.M()
	ctx := t.Context()
	m.RPCServer(ctx, "compute", "/vws.Compute/Get", "OK", 20*time.Millisecond)
	m.RPCClient(ctx, "compute", "storage", "vws.Storage/Get", "OK", time.Millisecond)
	m.HTTPServer(ctx, "console-api", "/v1/{id}", "2xx", time.Millisecond)
	m.AWSError(ctx, "console-api", "RunInstances", "InvalidParameterValue")
	m.Error(ctx, "compute", "NotFound")
	m.Panic(ctx, "compute")
	m.Reconcile(ctx, "vm", "ok", time.Millisecond)
	m.ReconcileError(ctx, "vm", "Conflict")
	m.StoreConflict(ctx, "configmap")
	m.Audit(ctx, "compute", "delete", "ok")
	m.RegisterQueue("retry", func() int64 { return 3 }, func() time.Duration { return 4 * time.Second })
	custom, err := m.NewCounter("vws.custom.things", "things")
	if err != nil {
		t.Fatal(err)
	}
	custom.Add(ctx, 2, obs.LabelOutcome, "ok")
	hist, err := m.NewHistogram("vws.custom.latency", "latency", "s")
	if err != nil {
		t.Fatal(err)
	}
	hist.RecordDuration(ctx, 30*time.Millisecond, obs.LabelOutcome, "ok")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			keys := map[string]bool{}
			collect := func(sets ...[]string) {
				for _, s := range sets {
					for _, k := range s {
						keys[k] = true
					}
				}
			}
			switch d := mm.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					collect(attrKeys(dp.Attributes.ToSlice()))
				}
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					collect(attrKeys(dp.Attributes.ToSlice()))
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					collect(attrKeys(dp.Attributes.ToSlice()))
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					collect(attrKeys(dp.Attributes.ToSlice()))
				}
			}
			for k := range keys {
				got[mm.Name] = append(got[mm.Name], k)
			}
		}
	}
	for _, name := range []string{
		obs.MetricRPCServerRequests, obs.MetricRPCServerDuration, obs.MetricRPCClientDuration,
		obs.MetricHTTPServerRequests, obs.MetricHTTPServerDuration, obs.MetricAWSErrors, obs.MetricErrors,
		obs.MetricPanics, obs.MetricReconcileTotal, obs.MetricReconcileDuration, obs.MetricReconcileErrors,
		obs.MetricQueueDepth, obs.MetricQueueOldestAge, obs.MetricStoreConflicts, obs.MetricAuditEvents,
		obs.MetricBuildInfo, "vws.custom.things", "vws.custom.latency",
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("metric %s was not exported", name)
		}
		for _, k := range got[name] {
			for _, banned := range []string{"resource_id", "request_id", "trace_id", "ip", "user", "account", "error", "message"} {
				if k == banned {
					t.Errorf("metric %s has banned label %s", name, k)
				}
			}
		}
	}
}

func attrKeys(kvs []attribute.KeyValue) []string {
	out := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, string(kv.Key))
	}
	return out
}

func TestForbiddenLabelPanics(t *testing.T) {
	for name, fn := range map[string]func(){
		"resource_id": func() { obs.Attrs("resource_id", "vol-1") },
		"odd":         func() { obs.Attrs(oddArgs()...) },
		"counter":     func() { c, _ := obs.M().NewCounter("vws.x", ""); c.Add(context.Background(), 1, "request_id", "r") }, //nolint:errcheck // name is valid
		"histogram": func() {
			h, _ := obs.M().NewHistogram("vws.y", "", "s") //nolint:errcheck // name is valid
			h.Record(context.Background(), 1, "request_id", "r")
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: want a panic", name)
				}
			}()
			fn()
		}()
	}
	if _, err := obs.M().NewCounter("custom.no_prefix", ""); err == nil {
		t.Errorf("a metric name without the vws. prefix must be refused")
	}
	if _, err := obs.M().NewHistogram("custom.no_prefix", "", "s"); err == nil {
		t.Errorf("a histogram name without the vws. prefix must be refused")
	}
}

func TestNewHistogramRecordsValuesAndUnit(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, map[string]string{}, obs.WithOverride(func(c *obs.Config) { c.MetricReaders = []sdkmetric.Reader{reader} }))
	ctx := t.Context()
	secs, err := obs.M().NewHistogram("vws.test.wait", "wait time", "s")
	if err != nil {
		t.Fatal(err)
	}
	size, err := obs.M().NewHistogram("vws.test.size", "object size", "")
	if err != nil {
		t.Fatal(err)
	}
	secs.RecordDuration(ctx, 250*time.Millisecond, obs.LabelOutcome, "ok")
	secs.Record(ctx, 0.75, obs.LabelOutcome, "ok")
	size.Record(ctx, 4096)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	type got struct {
		unit       string
		count      uint64
		sum        float64
		labelValue string
	}
	seen := map[string]got{}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			d, ok := mm.Data.(metricdata.Histogram[float64])
			if !ok || len(d.DataPoints) != 1 {
				continue
			}
			dp := d.DataPoints[0]
			v, _ := dp.Attributes.Value(attribute.Key(obs.LabelOutcome))
			seen[mm.Name] = got{mm.Unit, dp.Count, dp.Sum, v.AsString()}
		}
	}
	if g := seen["vws.test.wait"]; g.unit != "s" || g.count != 2 || g.sum != 1.0 || g.labelValue != "ok" {
		t.Errorf("vws.test.wait = %+v, want unit s, count 2, sum 1, outcome ok", g)
	}
	if g := seen["vws.test.size"]; g.unit != "" || g.count != 1 || g.sum != 4096 {
		t.Errorf("vws.test.size = %+v, want no unit, count 1, sum 4096", g)
	}
}

func TestSDKErrorsAreRateLimited(t *testing.T) {
	buf, _ := setup(t, map[string]string{}, obs.WithOverride(func(c *obs.Config) { c.ErrorLogEvery = time.Hour }))
	for range 100 {
		otel.Handle(io.ErrUnexpectedEOF)
	}
	n := 0
	for _, l := range lines(t, buf) {
		if l["msg"] == "otel sdk error" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want 1 warning for 100 SDK errors, got %d", n)
	}
}

// otlpReceiver counts what a Collector would receive over OTLP/HTTP.
type otlpReceiver struct {
	mu    sync.Mutex
	paths map[string]int
	srv   *httptest.Server
}

func newReceiver(t *testing.T) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{paths: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return
		}
		r.mu.Lock()
		r.paths[req.URL.Path]++
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *otlpReceiver) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[path]
}

func TestOTLPHTTPExportsAllThreeSignals(t *testing.T) {
	recv := newReceiver(t)
	_, shutdown := setup(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": recv.srv.URL, "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_TRACES_SAMPLER": "always_on",
	})
	ctx, span := otel.Tracer("t").Start(t.Context(), "op")
	slog.InfoContext(ctx, "to otlp")
	obs.M().Panic(ctx, "compute")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	for _, p := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if recv.count(p) == 0 {
			t.Errorf("the receiver got nothing on %s: %v", p, recv.paths)
		}
	}
}

func TestNothingIsSentWithoutEndpointOrWhenDisabled(t *testing.T) {
	recv := newReceiver(t)
	for name, vars := range map[string]map[string]string{
		"disabled":    {"OTEL_EXPORTER_OTLP_ENDPOINT": recv.srv.URL, "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf", "OTEL_SDK_DISABLED": "true"},
		"no endpoint": {},
	} {
		_, shutdown := setup(t, vars)
		ctx, span := otel.Tracer("t").Start(t.Context(), "op")
		slog.InfoContext(ctx, "exported")
		span.End()
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("%s: shutdown: %v", name, err)
		}
	}
	if len(recv.paths) != 0 {
		t.Errorf("an exporter was created: %v", recv.paths)
	}
}

// A Collector that accepts connections and never answers must not slow the request path.
func TestBlockedCollectorDoesNotSlowTheRequestPath(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close() //nolint:errcheck,gosec // test cleanup
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec // test cleanup
		}
	})
	_, shutdown := setup(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://" + ln.Addr().String(), "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_TRACES_SAMPLER": "always_on",
	}, obs.WithOverride(func(c *obs.Config) {
		c.QueueSize, c.BatchSize = 64, 16 // small queue: it must drop, not block
		c.BatchInterval = 10 * time.Millisecond
		c.ExportTimeout = 200 * time.Millisecond
		c.ShutdownTimeout = 500 * time.Millisecond
		c.RetryMax = 50 * time.Millisecond
		c.RetryElapsed = 100 * time.Millisecond
	}))
	start := time.Now()
	for range 5000 {
		ctx, span := otel.Tracer("t").Start(t.Context(), "op")
		slog.InfoContext(ctx, "work")
		obs.M().Reconcile(ctx, "vm", "ok", time.Microsecond)
		span.End()
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("5000 spans took %v with a blocked Collector", d)
	}
	start = time.Now()
	_ = shutdown(context.Background()) //nolint:errcheck // a flush timeout is expected here
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("shutdown took %v, want it bounded by ShutdownTimeout", d)
	}
}

// oddArgs hides the length from the static check that the test provokes on purpose.
func oddArgs() []string { return []string{"service"} }

// A 30 ms observation lands in (0.025, 0.05], not in the SDK default first bucket le="5".
func TestSecondsHistogramsUseSecondsBuckets(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, map[string]string{}, obs.WithOverride(func(c *obs.Config) { c.MetricReaders = []sdkmetric.Reader{reader} }))
	ctx := t.Context()
	custom, err := obs.M().NewHistogram("vws.test.wait", "wait time", "s")
	if err != nil {
		t.Fatal(err)
	}
	custom.RecordDuration(ctx, 30*time.Millisecond)
	obs.M().RPCServer(ctx, "compute", "/vws.compute.v1.InstanceService/GetInstance", "OK", 30*time.Millisecond)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if mm.Name != "vws.test.wait" && mm.Name != obs.MetricRPCServerDuration {
				continue
			}
			d, ok := mm.Data.(metricdata.Histogram[float64])
			if !ok || len(d.DataPoints) != 1 {
				t.Fatalf("%s is %T with %d points, want one float64 histogram point", mm.Name, mm.Data, len(d.DataPoints))
			}
			dp := d.DataPoints[0]
			if !slices.Equal(dp.Bounds, obs.SecondsBuckets) {
				t.Errorf("%s bounds = %v, want %v", mm.Name, dp.Bounds, obs.SecondsBuckets)
				continue
			}
			want := make([]uint64, len(dp.Bounds)+1)
			want[slices.Index(dp.Bounds, 0.05)] = 1
			if !slices.Equal(dp.BucketCounts, want) {
				t.Errorf("%s buckets = %v, want the 30ms in le=0.05 only: %v", mm.Name, dp.BucketCounts, want)
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d histograms, want 2", checked)
	}
}

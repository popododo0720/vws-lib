package obs

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Attrs builds metric attributes from key, value pairs. A key outside the label allowlist in
// names.go panics: a label that can grow without bound is a bug found by the first test, not
// by the metrics bill. An odd number of arguments panics too.
func Attrs(kv ...string) metric.MeasurementOption {
	if len(kv)%2 != 0 {
		panic("obs.Attrs: odd number of arguments")
	}
	set := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		if !allowedLabels[kv[i]] {
			panic(fmt.Sprintf("obs.Attrs: %q is not an allowed metric label", kv[i]))
		}
		set = append(set, attribute.String(kv[i], kv[i+1]))
	}
	return metric.WithAttributes(set...)
}

// Metrics holds the instruments of the metric table in the design. One value exists per
// process; before Setup it records nothing.
type Metrics struct {
	meter metric.Meter

	rpcServerRequests  metric.Int64Counter
	rpcServerDuration  metric.Float64Histogram
	rpcClientDuration  metric.Float64Histogram
	httpServerRequests metric.Int64Counter
	httpServerDuration metric.Float64Histogram
	awsErrors          metric.Int64Counter
	errors             metric.Int64Counter
	panics             metric.Int64Counter
	reconcileTotal     metric.Int64Counter
	reconcileDuration  metric.Float64Histogram
	reconcileErrors    metric.Int64Counter
	storeConflicts     metric.Int64Counter
	auditEvents        metric.Int64Counter
	queueDepth         metric.Int64ObservableGauge
	queueOldestAge     metric.Float64ObservableGauge
	buildInfo          metric.Int64ObservableGauge

	queues atomic.Pointer[[]queue]
}

type queue struct {
	name   string
	depth  func() int64
	oldest func() time.Duration
}

var current atomic.Pointer[Metrics]

func init() {
	m, err := newMetrics(noop.NewMeterProvider().Meter(instrumentationScopeName), "", "", "")
	if err != nil {
		panic(err) // the noop meter does not fail
	}
	current.Store(m)
}

// M returns the process metrics. It is never nil.
func M() *Metrics { return current.Load() }

func newMetrics(meter metric.Meter, service, version, commit string) (*Metrics, error) {
	m := &Metrics{meter: meter}
	var errs []string
	counter := func(name, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			errs = append(errs, err.Error())
		}
		return c
	}
	hist := func(name, desc string) metric.Float64Histogram {
		h, err := meter.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit("s"))
		if err != nil {
			errs = append(errs, err.Error())
		}
		return h
	}
	m.rpcServerRequests = counter(MetricRPCServerRequests, "RPCs served, by gRPC code")
	m.rpcServerDuration = hist(MetricRPCServerDuration, "RPC server latency")
	m.rpcClientDuration = hist(MetricRPCClientDuration, "RPC client latency")
	m.httpServerRequests = counter(MetricHTTPServerRequests, "HTTP requests served, by route and status class")
	m.httpServerDuration = hist(MetricHTTPServerDuration, "HTTP server latency")
	m.awsErrors = counter(MetricAWSErrors, "AWS error codes returned to callers")
	m.errors = counter(MetricErrors, "errors passed to report.Error, by code")
	m.panics = counter(MetricPanics, "recovered panics")
	m.reconcileTotal = counter(MetricReconcileTotal, "reconcile passes")
	m.reconcileDuration = hist(MetricReconcileDuration, "reconcile latency")
	m.reconcileErrors = counter(MetricReconcileErrors, "reconcile errors, by code")
	m.storeConflicts = counter(MetricStoreConflicts, "store compare-and-swap conflicts")
	m.auditEvents = counter(MetricAuditEvents, "audit events")

	var err error
	m.queueDepth, err = meter.Int64ObservableGauge(MetricQueueDepth, metric.WithDescription("queue length"))
	if err != nil {
		errs = append(errs, err.Error())
	}
	m.queueOldestAge, err = meter.Float64ObservableGauge(MetricQueueOldestAge, metric.WithDescription("age of the oldest queue item"), metric.WithUnit("s"))
	if err != nil {
		errs = append(errs, err.Error())
	}
	m.buildInfo, err = meter.Int64ObservableGauge(MetricBuildInfo, metric.WithDescription("build information, always 1"))
	if err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("create instruments: %s", strings.Join(errs, "; "))
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if qs := m.queues.Load(); qs != nil {
			for _, q := range *qs {
				o.ObserveInt64(m.queueDepth, q.depth(), Attrs(LabelQueue, q.name))
				if q.oldest != nil {
					o.ObserveFloat64(m.queueOldestAge, q.oldest().Seconds(), Attrs(LabelQueue, q.name))
				}
			}
		}
		o.ObserveInt64(m.buildInfo, 1, Attrs(LabelService, service, LabelVersion, version, LabelCommit, commit))
		return nil
	}, m.queueDepth, m.queueOldestAge, m.buildInfo)
	if err != nil {
		return nil, fmt.Errorf("register callback: %w", err)
	}
	return m, nil
}

func buildCommit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "unknown"
}

// RPCServer records one served RPC. rpcMethod is the static full method name.
func (m *Metrics) RPCServer(ctx context.Context, service, rpcMethod, grpcCode string, d time.Duration) {
	a := Attrs(LabelService, service, LabelRPCMethod, rpcMethod, LabelGRPCCode, grpcCode)
	m.rpcServerRequests.Add(ctx, 1, a)
	m.rpcServerDuration.Record(ctx, d.Seconds(), a)
}

// RPCClient records one RPC to a peer service.
func (m *Metrics) RPCClient(ctx context.Context, service, peer, rpcMethod, grpcCode string, d time.Duration) {
	m.rpcClientDuration.Record(ctx, d.Seconds(),
		Attrs(LabelService, service, LabelPeer, peer, LabelRPCMethod, rpcMethod, LabelGRPCCode, grpcCode))
}

// HTTPServer records one HTTP request. route is the template ("/v1/{id}"), never the raw path.
func (m *Metrics) HTTPServer(ctx context.Context, service, route, statusClass string, d time.Duration) {
	a := Attrs(LabelService, service, LabelRoute, route, LabelStatusClass, statusClass)
	m.httpServerRequests.Add(ctx, 1, a)
	m.httpServerDuration.Record(ctx, d.Seconds(), a)
}

// AWSError counts an AWS error code returned to a caller.
func (m *Metrics) AWSError(ctx context.Context, service, action, awsCode string) {
	m.awsErrors.Add(ctx, 1, Attrs(LabelService, service, LabelAction, action, LabelAWSCode, awsCode))
}

// Error counts an error that report.Error handled.
func (m *Metrics) Error(ctx context.Context, service, code string) {
	m.errors.Add(ctx, 1, Attrs(LabelService, service, LabelCode, code))
}

// Panic counts a recovered panic.
func (m *Metrics) Panic(ctx context.Context, service string) {
	m.panics.Add(ctx, 1, Attrs(LabelService, service))
}

// Reconcile records one reconcile pass. result is ok, error, or requeue.
func (m *Metrics) Reconcile(ctx context.Context, controller, result string, d time.Duration) {
	a := Attrs(LabelController, controller, LabelResult, result)
	m.reconcileTotal.Add(ctx, 1, a)
	m.reconcileDuration.Record(ctx, d.Seconds(), a)
}

// ReconcileError counts a reconcile error by errs.Code name.
func (m *Metrics) ReconcileError(ctx context.Context, controller, code string) {
	m.reconcileErrors.Add(ctx, 1, Attrs(LabelController, controller, LabelCode, code))
}

// StoreConflict counts a compare-and-swap conflict in a store.
func (m *Metrics) StoreConflict(ctx context.Context, store string) {
	m.storeConflicts.Add(ctx, 1, Attrs(LabelStore, store))
}

// Audit counts an audit event.
func (m *Metrics) Audit(ctx context.Context, service, action, outcome string) {
	m.auditEvents.Add(ctx, 1, Attrs(LabelService, service, LabelAction, action, LabelOutcome, outcome))
}

// RegisterQueue exposes a queue's length and, when oldest is not nil, the age of its oldest
// item. The functions run on each collection and must be cheap and safe to call concurrently.
func (m *Metrics) RegisterQueue(name string, depth func() int64, oldest func() time.Duration) {
	for {
		old := m.queues.Load()
		var next []queue
		if old != nil {
			next = append(next, *old...)
		}
		next = append(next, queue{name: name, depth: depth, oldest: oldest})
		if m.queues.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Counter is a service-defined counter. Its name must start with "vws." and its labels must be
// in the allowlist (Attrs panics otherwise).
type Counter struct{ c metric.Int64Counter }

// NewCounter creates a counter on the process meter.
func (m *Metrics) NewCounter(name, description string) (Counter, error) {
	if !strings.HasPrefix(name, MetricPrefix) {
		return Counter{}, fmt.Errorf("metric %q must start with %q", name, MetricPrefix)
	}
	c, err := m.meter.Int64Counter(name, metric.WithDescription(description))
	if err != nil {
		return Counter{}, fmt.Errorf("create counter %s: %w", name, err)
	}
	return Counter{c}, nil
}

// Add adds n with labels given as key, value pairs.
func (c Counter) Add(ctx context.Context, n int64, labels ...string) {
	c.c.Add(ctx, n, Attrs(labels...))
}

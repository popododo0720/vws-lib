package obs

// Names live here and nowhere else. A new metric, label, or environment variable is a constant
// in this file first.

// Environment variables. OTEL_* and SENTRY_* are the standard names. Only the three VWS_*
// variables are ours, because the standard has no name for them.
const (
	EnvServiceName     = "OTEL_SERVICE_NAME"
	EnvSDKDisabled     = "OTEL_SDK_DISABLED"
	EnvEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvProtocol        = "OTEL_EXPORTER_OTLP_PROTOCOL"
	EnvHeaders         = "OTEL_EXPORTER_OTLP_HEADERS"
	EnvSampler         = "OTEL_TRACES_SAMPLER"
	EnvSamplerArg      = "OTEL_TRACES_SAMPLER_ARG"
	EnvResourceAttrs   = "OTEL_RESOURCE_ATTRIBUTES"
	EnvTracesExporter  = "OTEL_TRACES_EXPORTER"
	EnvMetricsExporter = "OTEL_METRICS_EXPORTER"
	EnvLogsExporter    = "OTEL_LOGS_EXPORTER"
	EnvMetricInterval  = "OTEL_METRIC_EXPORT_INTERVAL"
	EnvSentryDSN       = "SENTRY_DSN"
	EnvSentryEnv       = "SENTRY_ENVIRONMENT"
	EnvSentryRelease   = "SENTRY_RELEASE"
	EnvSentrySample    = "SENTRY_SAMPLE_RATE"
	EnvLogLevel        = "VWS_LOG_LEVEL"
	EnvLogFormat       = "VWS_LOG_FORMAT"
	EnvHealthAddr      = "VWS_HEALTH_ADDR"
)

// Log field names, snake_case.
const (
	FieldService      = "service"
	FieldVersion      = "version"
	FieldRequestID    = "request_id"
	FieldTraceID      = "trace_id"
	FieldSpanID       = "span_id"
	FieldResourceID   = "resource_id"
	FieldResourceType = "resource_type"
	FieldOp           = "op"
	FieldErr          = "err"
	FieldErrCode      = "err_code"
	FieldAudit        = "audit"
)

// Metric names (OTel form). The Prometheus form, if a Collector converts them, is its business.
const (
	MetricRPCServerRequests    = "vws.rpc.server.requests"
	MetricRPCServerDuration    = "vws.rpc.server.duration"
	MetricRPCClientDuration    = "vws.rpc.client.duration"
	MetricHTTPServerRequests   = "vws.http.server.requests"
	MetricHTTPServerDuration   = "vws.http.server.duration"
	MetricAWSErrors            = "vws.aws.errors"
	MetricErrors               = "vws.errors"
	MetricPanics               = "vws.panics"
	MetricReconcileTotal       = "vws.reconcile.total"
	MetricReconcileDuration    = "vws.reconcile.duration"
	MetricReconcileErrors      = "vws.reconcile.errors"
	MetricQueueDepth           = "vws.queue.depth"
	MetricQueueOldestAge       = "vws.queue.oldest_age"
	MetricStoreConflicts       = "vws.store.conflicts"
	MetricAuditEvents          = "vws.audit.events"
	MetricBuildInfo            = "vws.build.info"
	MetricPrefix               = "vws."
	instrumentationScopeName   = "github.com/popododo0720/vws-lib/obs"
	defaultServiceNameFallback = "unknown_service"
)

// Metric label keys. Only these may be a label. A label's value set must be small and fixed.
// Never a resource id, request id, trace id, account, user, IP, error message, free text,
// bucket or key name.
const (
	LabelService     = "service"
	LabelRPCMethod   = "rpc_method"
	LabelGRPCCode    = "grpc_code"
	LabelCode        = "code"
	LabelController  = "controller"
	LabelResult      = "result"
	LabelQueue       = "queue"
	LabelOutcome     = "outcome"
	LabelPeer        = "peer"
	LabelRoute       = "route"
	LabelStatusClass = "status_class"
	LabelAction      = "action"
	LabelAWSCode     = "aws_code"
	LabelStore       = "store"
	LabelVersion     = "version"
	LabelCommit      = "commit"
)

// allowedLabels is the allowlist Attrs checks.
var allowedLabels = map[string]bool{
	LabelService: true, LabelRPCMethod: true, LabelGRPCCode: true, LabelCode: true,
	LabelController: true, LabelResult: true, LabelQueue: true, LabelOutcome: true,
	LabelPeer: true, LabelRoute: true, LabelStatusClass: true, LabelAction: true,
	LabelAWSCode: true, LabelStore: true, LabelVersion: true, LabelCommit: true,
}

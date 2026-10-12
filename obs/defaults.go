package obs

import "time"

// Every default is in this file. A service changes one with WithOverride, never by copying a
// number. The chart's services.<name>.observability uses the same keys.
const (
	defaultLogLevel         = "info"
	defaultLogFormat        = "json"
	defaultProtocol         = "grpc"
	defaultSampler          = "parentbased_traceidratio"
	defaultSamplerArg       = 0.1
	defaultQueueSize        = 2048
	defaultBatchSize        = 512
	defaultBatchInterval    = 5 * time.Second
	defaultExportTimeout    = 10 * time.Second
	defaultMetricInterval   = 60 * time.Second
	defaultRetryInitial     = 1 * time.Second
	defaultRetryMax         = 30 * time.Second
	defaultRetryElapsed     = 1 * time.Minute
	defaultShutdownTimeout  = 5 * time.Second
	defaultErrorLogInterval = 10 * time.Second
)

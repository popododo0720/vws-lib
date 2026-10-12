# vws-lib

Shared Go library for VWS services: observability (OpenTelemetry) and errors. VWS is not affiliated with Amazon or AWS.

Status: `obs` and `errs` are implemented (vws-deploy#44 item a). Not yet in: the Prometheus pull reader, the `go list -deps` check, the sample consumer build, `scripts/vendor-lib.sh`, and the `v0.1.0` tag. The design is [vws-deploy#42](https://github.com/popododo0720/vws-deploy/issues/42) and the implementation work is [vws-deploy#44](https://github.com/popododo0720/vws-deploy/issues/44).

## Packages

| Package | Purpose |
| --- | --- |
| `obs` | `Setup`, `Config` and its defaults (`defaults.go`), names (`names.go`), the JSON logger with `trace_id`, `span_id`, `request_id`, and the `vws.*` metrics (`obs.M()`). Logs, traces, and metrics go over OTLP only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. |
| `obs/report` | `report.Error` and `report.Panic`: span error, log line, `vws.errors`, and Sentry when `SENTRY_DSN` is set. An error is reported once (`errs.Reported`). The only package that imports sentry-go. |
| `obs/grpcobs`, `obs/httpobs` | Server and client instrumentation, panic recovery, request id, `vws.rpc.*` and `vws.http.*` metrics. |
| `obs/health` | `/healthz` (checks nothing), `/readyz` (registered checks, fails after `NotReady` or SIGTERM). Telemetry backends cannot be registered. |
| `errs` | Error codes and kinds shared by services, with the gRPC and AWS code mappings. It reuses the `resource.Code` and `resource.Kind` set of vws-console-api rather than adding a new taxonomy. |

This module never imports vws-proto or any service repository. The dependency points one way: service to lib.

## Use in a service

```go
shutdown, err := obs.Setup(ctx, obs.ForService("compute"))
if err != nil { return err }
defer shutdown(context.WithoutCancel(ctx)) // flushes within Config.ShutdownTimeout

srv := grpc.NewServer(grpcobs.ServerOptions("compute")...)
health.Default().Register("store", func(ctx context.Context) error { return storeReady() })
return report.Error(ctx, err) // once; outer layers skip an error already reported
```

Services never import sentry-go. A `depguard` rule keeps it that way in the consumer; copy this from `.golangci.yml`:

```yaml
linters:
  settings:
    depguard:
      rules:
        no-sentry-direct:
          files: [$all]
          deny:
            - pkg: github.com/getsentry/sentry-go
              desc: use vws-lib/obs/report
```

Differences from the design text: `obs/log`, `obs/trace`, and `obs/metric` are files of package `obs`, not packages. The `vws.rpc.*` and `vws.http.*` metrics come from vws-lib's own instruments, not from the instrumentation libraries' default metrics, so their names do not move with those libraries. `OTEL_*_EXPORTER` accepts `otlp` and `none` only.

## Environment variables

Services read only these. Standard OpenTelemetry and Sentry names come first. `VWS_*` names exist only where no standard name does.

| Variable | Meaning |
| --- | --- |
| `OTEL_*` (`OTEL_SERVICE_NAME`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER`, `OTEL_RESOURCE_ATTRIBUTES`, ...) | Standard OpenTelemetry SDK settings. An empty `OTEL_EXPORTER_OTLP_ENDPOINT` means nothing is exported. |
| `SENTRY_DSN`, `SENTRY_ENVIRONMENT`, `SENTRY_RELEASE`, `SENTRY_SAMPLE_RATE` | Sentry is on only when `SENTRY_DSN` is set. |
| `VWS_LOG_LEVEL` | `debug`, `info`, `warn`, or `error`. Default `info`. |
| `VWS_LOG_FORMAT` | `json` (default) or `text` for local development. |
| `VWS_HEALTH_ADDR` | Address for `/healthz` and `/readyz`. Empty means off. |

The full table and defaults are in section 4 of the design issue. Secrets (headers, DSN) are injected from Secrets, never written in values files.

## Use it from a service repo

Same way as vws-proto: vendor a copy of a tagged version and point `replace` at it.

1. Copy the tree of the tag into `third_party/vws-lib` and record the tag and commit in the consumer README.
2. In `go.mod`: `require github.com/popododo0720/vws-lib v0.0.0` and `replace github.com/popododo0720/vws-lib => ./third_party/vws-lib`.
3. Do not edit the vendored copy. Change it here, tag, and copy again.

When this repository is public, drop the `replace` and require the tag. There is no tag yet. A vendoring script (`scripts/vendor-lib.sh`) and its `check` are part of vws-deploy#44. They do not exist yet.

## Develop

```sh
go vet ./...
go test -race ./...
golangci-lint run ./...        # v2.14.0, config in .golangci.yml
scripts/guard-agents.sh selftest
```

Contributor rules, branch and PR rules, and the insider-risk guardrails are in `AGENTS.md`. License: Apache-2.0.

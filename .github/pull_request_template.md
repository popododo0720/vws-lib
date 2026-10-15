Part of #

## What changed

## How it was verified (only what was actually run)

## Not verified (needs checking)

## Conflicts with other PRs

## Quality checklist (required)

AWS-grade production quality, not demo (see QUALITY.md). Every checked box needs evidence: a file:line, test name, command output, or link. Write "N/A: reason" where an item truly does not apply. An unchecked box without a reason means the PR is not done.

- [ ] **Error handling**: every error is wrapped with context and returned; no panic, crash or unchecked error on any input. Evidence:
- [ ] **Retries and timeouts**: every external call (API, gRPC, Kubernetes, storage, network) has a timeout or context deadline and retries transient failures with bounded backoff. Evidence:
- [ ] **Security**: all input validated; least-privilege RBAC/permissions; secrets only via Kubernetes Secret, never logged and never in the repo. Evidence:
- [ ] **Unit tests**: new behavior and failure paths covered; a new test fails on main. Evidence:
- [ ] **Integration tests**: covered against real dependencies or envtest/fakes at the boundary. Evidence:
- [ ] **vws-test live evidence (required)**: link or paths to commands and output from the vws-test cluster. No live evidence = not done. Evidence:
- [ ] **Observability**: OpenTelemetry traces/metrics for the new paths and structured logs (no secrets). Evidence:
- [ ] **Idempotency**: safe on retry, duplicate request and process restart. Evidence:
- [ ] **AWS compatibility (where applicable)**: API behavior and error codes match AWS. Evidence:

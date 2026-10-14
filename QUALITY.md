# Quality standard

Adopted 2026-10-14. Every change in this repo must be AWS-grade production quality, not demo quality. A PR is done only when the "Quality checklist (required)" in the pull request template is filled with evidence for each item.

1. **Error handling**: every error is wrapped with context and returned. No panic or crash on any input.
2. **Retries and timeouts**: every external call has a timeout or context deadline and retries transient failures with bounded backoff.
3. **Security**: validate all input, grant least privilege (RBAC and permissions), and keep secrets only in Kubernetes Secrets. Secrets are never logged and never committed.
4. **Unit tests** for new behavior and failure paths.
5. **Integration tests** at real or faithfully faked boundaries.
6. **Live evidence on vws-test**: commands and output from the vws-test cluster, linked or pathed in the PR. Without live evidence the work is not done.
7. **Observability**: OpenTelemetry traces and metrics plus structured logs.
8. **Idempotency**: safe on retry, duplicate request and restart.
9. **AWS compatibility** where applicable: API behavior and error codes match AWS.

Reviewers do not merge a PR whose checklist lacks evidence or a stated reason for N/A.

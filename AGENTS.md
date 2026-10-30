# Agent rules

This repository holds only the Go library shared by the VWS services (`obs`, `errs`). The design is https://github.com/popododo0720/vws-deploy/issues/42 and the implementation work is tracked in https://github.com/popododo0720/vws-deploy/issues/44. The module is `github.com/popododo0720/vws-lib`.

## Rules

| Rule | Enforcer |
| --- | --- |
| L1. A PR targets main and its body has `Part of #N` and no closing keyword; CI reports commits that reached main without a PR. AGENTS.md has Insider-risk guardrails 1-7. Structure: `.github/workflows/repo-guard.yml`, `.github/workflows/direct-push-watch.yml`, `scripts/guard-agents.sh`. | `scripts/guard-agents.sh agents`; CI `pr-hygiene` (`pr-base`, `pr-body`, `pr-commits`); `push-watch` (CI `direct-push-watch`) |
| L2. Do not use Cursor cloud agents. Structure: `AGENTS.md`. | Judgment |
| L3. This module does not import vws-proto or the service repos (compute, storage, network, console-api). Dependencies point one way: service → lib. Structure: `go.mod`, `scripts/check-imports.sh`. | `scripts/check-imports.sh`, CI `imports` job |
| L4. Code follows gofmt and goimports and passes the linters in `.golangci.yml`. The `std-error-handling` preset is not used. Structure: `.golangci.yml`. | CI `golangci-lint` v2.14.0, `lint` job |
| L5. `go vet ./...` and `go test -race ./...` pass. Structure: `.github/workflows/ci.yml`. | CI `test` job |
| L6. The three guard files (`repo-guard.yml`, `direct-push-watch.yml`, `scripts/guard-agents.sh`) are byte-identical to vws-deploy main. Do not edit them in this repository. | Judgment (compare with `sha256sum`) |
| L7. The repo is English only (see "English only"). Hangul is allowed only in files `.github/english-allowlist` lists with a reason. Structure: `.github/english-allowlist`, `scripts/check-english.sh`. | `scripts/check-english.sh files`, CI `english` job |
| L8. Layer rules: `errs` imports no module, telemetry or logging package; only `obs/report` imports sentry-go; only `obs` imports the OpenTelemetry SDK and exporters; no Kubernetes client package; no package imports std `log`; production code does not import `testing`. Structure: `internal/archtest`. | `TestArchitectureRules`, CI `test` job |

The author and reviewer uphold the rules whose Enforcer cell is Judgment.

## English only

Write everything in this repo in English: README, code comments, docs, AGENTS.md, QUALITY.md, PR and issue templates, commit messages, PR and issue bodies, review comments and `Verified-by` sections. The `english` job in CI runs `scripts/check-english.sh`: `files` fails on a tracked file with Hangul unless `.github/english-allowlist` lists it as `<glob> <reason>` (only test data and user-facing UI strings belong there), and `text` and `commits` reject Hangul in the PR body and the PR's commit messages with no exception.

## Branches and atomic commits

- Work on an independent branch cut from main and send it as one PR. No stacked PRs. Do not push to main directly (only the first structural commit that created the repo was an exception).
- One commit is one change. Keep the subject short, like `scope: summary`. `go build ./...` and `go test ./...` must pass on every commit.
- Write `Part of #N` in the PR body and never a closing keyword (`Closes`, `Fixes`, `Resolves`); the issue is closed by hand after verification. The issue may live in another repo, like vws-deploy#44; for an issue in another repository, also give the full URL.
- An author bot never merges its own PR.
- Tags (`v0.x.y`) are annotated tags on a merged main commit with green CI, published by the orchestrator or a person, not the author bot. Consumer repos copy the tag contents into `third_party/vws-lib` and connect it with `replace`.

## Way of working (poteto-mode)

- Write concisely and solve with the smallest code change. Do not add abstraction layers; delete them when you can.
- Decide the shape of the data before writing code. When the same mistake shows up twice, block it with structure or a check instead of a sentence.
- Say something is verified only if you actually ran it. Mark what you could not run as "unverified".
- Write commit messages and PR descriptions as short plain statements. No unsupported claims and no made-up links.
- Do not use Cursor cloud agents (CloudAgent). Work with `gh` and `git`.
- Never write secrets or account IDs in code, tests or logs.

## /correct loop

1. When a bot is corrected (by the user Su Kim, tmp or eggbot, or by a failed `vws verify`), it opens an issue the same day with the `incident` label using the `.github/ISSUE_TEMPLATE/incident.yml` form. It must fill in the correction and "the check that would have caught this".
2. Every few days vws-verify runs a gardener pass. It groups open incidents by cause and promotes repeated ones to a check (test, lint, guard rule, archtest), not a sentence. The check must actually fail on an input that contains the mistake.
3. When the promoting PR is merged, close the incident with its link. An incident that will not be promoted is closed with the reason.
4. The milestone report (to tmp) lists the number of incidents in the period and the checks promoted.
5. The `incident` label is defined in `.github/labels.yml`, copied from vws-deploy. That file is only a definition and does not create the label on GitHub. `scripts/guard.sh correct` runs `scripts/guard-agents.sh correct`, which checks this section and its body, the required fields of the form and the label definition; the `correct` job in `.github/workflows/ci.yml` runs it on every pull request and `scripts/guard_test.sh` shows it fails on an empty section.

## Insider-risk guardrails

Bots are insider risks too. The rules below have no exceptions.

1. **Independent verification**: an important result (merge, security, data) is never verified by the bot/model that wrote it. Another bot (or a person) verifies it. (Process rule; CI cannot enforce it.)
2. **Records**: every change leaves a trail of issue → PR (`Part of #N`) → commits. Work logs go in the PR body or issue comments in a form people can read. No force-push or history rewriting (except with the user's approval).
3. **Failure tests**: a new feature is not tested on the happy path only. Include at least one case each for power loss/restart, idempotent re-run and malicious or boundary input, or state in the PR why that is not possible.
4. **Least privilege**: no direct pushes to main. Every change is a single PR from its own branch cut from main. No stacked PRs. No local merges. Tokens are scoped to the repo. RBAC covers only the resources the service code actually calls, as a Role (namespace scope) where possible.
5. **Separated audit**: author bot ≠ verifier bot ≠ merge decision. An author bot never merges its own PR. Merge only when base==main and every CI check on the head commit is success.
6. **Isolation and emergency stop**: a bot/agent is stopped immediately at the user's request or by the orchestrator stopping that bot's session and revoking its token (`gh auth`/PAT revoke). Risky work happens only in a sandbox (the box or a throwaway cluster). No access to production clusters.
7. **Incident disclosure**: rule violations and incidents are never hidden; record them in an issue as `Cause / Impact / Action / Prevention` (label `incident`).

Main is protected by the active `vws-main` ruleset: a pull request is required, only a squash merge is allowed, force push and deletion are off, and the required checks include `pr-hygiene`, `verify-gate` and `vws-verify`. The ruleset has no bypass actors. vws-deploy's `.github/drift/drift-check.sh` fails when this repo loses any of these. Guardrails 1, 5 and 6 stay process rules. For parts of 2 and 4, `.github/workflows/repo-guard.yml` checks the PR's base, `Part of #N` and that no PR text or commit closes an issue, and `.github/workflows/direct-push-watch.yml` reports after the fact any commit that reached main without a PR.

### Script push, PR and merge

A deterministic script (the factory orchestrator in vws-deploy `docs/factory-orchestrator.md`, the handoff submit) may push a branch, open a pull request and merge one. A script merge happens only when all of these hold:

- every CI check on the head commit is green;
- `verify-gate` passes;
- the `vws-verify` App check run passes;
- the pull request has no conflicts;
- the merge is a squash, made with `--match-head-commit` set to the verified head;
- the pull request is not labeled `blocked`.

`ORCHESTRATE_MERGE=apply` is approved only under these conditions. The lead (dr eggbot) is woken only for a decision or a failure (issue scope, real conflicts, a failed check), not for a routine push, pull request or merge. Guardrails 1, 4 and 2 still hold: the author bot is never the verifier, nothing pushes to main, and every pull request body has `Part of #N` with no closing keyword.

# Agent rules

This repository holds only the Go library shared by the VWS services (`obs`, `errs`). The design is https://github.com/popododo0720/vws-deploy/issues/42 and the implementation work is tracked in https://github.com/popododo0720/vws-deploy/issues/44. The module is `github.com/popododo0720/vws-lib`.

## Rules

| ID | Rule | Structure | Check |
| --- | --- | --- | --- |
| L1 | A PR targets main and its body has `Part of #N` and no closing keyword; CI reports commits that reached main without a PR. AGENTS.md has Insider-risk guardrails 1-7 | `.github/workflows/repo-guard.yml` `.github/workflows/direct-push-watch.yml` `scripts/guard-agents.sh` | `scripts/guard-agents.sh agents` |
| L2 | Do not use Cursor cloud agents | `AGENTS.md` | Judgment |
| L3 | This module does not import vws-proto or the service repos (compute, storage, network, console-api). Dependencies point one way: service → lib | `go.mod` `scripts/check-imports.sh` | `scripts/check-imports.sh` (the CI `imports` job) |
| L4 | Code follows gofmt and goimports and passes the linters in `.golangci.yml`. The `std-error-handling` preset is not used | `.golangci.yml` | CI `golangci-lint` v2.14.0 |
| L5 | `go vet ./...` and `go test -race ./...` pass | `.github/workflows/ci.yml` | CI |
| L6 | The three guard files (`repo-guard.yml`, `direct-push-watch.yml`, `scripts/guard-agents.sh`) are byte-identical to vws-deploy main. Do not edit them in this repository | - | Judgment (compare with `sha256sum`) |
| L7 | The repo is English only (see "English only"). Hangul is allowed only in files `.github/english-allowlist` lists with a reason | `.github/english-allowlist` `scripts/check-english.sh` | `scripts/check-english.sh files` (the CI `english` job) |

The author and reviewer uphold the rules whose check cell is Judgment.

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

## Insider-risk guardrails

Bots are insider risks too. The rules below have no exceptions.

1. **Independent verification**: an important result (merge, security, data) is never verified by the bot/model that wrote it. Another bot (or a person) verifies it. (Process rule; CI cannot enforce it.)
2. **Records**: every change leaves a trail of issue → PR (`Part of #N`) → commits. Work logs go in the PR body or issue comments in a form people can read. No force-push or history rewriting (except with the user's approval).
3. **Failure tests**: a new feature is not tested on the happy path only. Include at least one case each for power loss/restart, idempotent re-run and malicious or boundary input, or state in the PR why that is not possible.
4. **Least privilege**: no direct pushes to main. Every change is a single PR from its own branch cut from main. No stacked PRs. No local merges. Tokens are scoped to the repo. RBAC covers only the resources the service code actually calls, as a Role (namespace scope) where possible.
5. **Separated audit**: author bot ≠ verifier bot ≠ merge decision. An author bot never merges its own PR. Merge only when base==main and every CI check on the head commit is success.
6. **Isolation and emergency stop**: a bot/agent is stopped immediately at the user's request or by the orchestrator stopping that bot's session and revoking its token (`gh auth`/PAT revoke). Risky work happens only in a sandbox (the box or a throwaway cluster). No access to production clusters.
7. **Incident disclosure**: rule violations and incidents are never hidden; record them in an issue as `Cause / Impact / Action / Prevention` (label `incident`).

This repo has no server-side protection (private, free plan). 1, 5 and 6 are process rules. For parts of 2 and 4, `.github/workflows/repo-guard.yml` checks the PR's base, `Part of #N` and that no PR text or commit closes an issue, and `.github/workflows/direct-push-watch.yml` reports after the fact any commit that reached main without a PR. This is detection, not blocking.

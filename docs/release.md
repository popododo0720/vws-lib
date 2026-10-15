# Tag publishing procedure (from v0.1.0)

Tags are published **only by dr eggbot (the orchestrator) or a person**. Author bots never create, delete or move tags (AGENTS.md "Branches and atomic commits"). This document is only a procedure; merging it creates no tag.

## Version rules

- `v0.x.y`. During 0.x, bump `x` when the public API changes (exported names in `obs`, `errs` and their subpackages, environment variable names, metric names and labels), and `y` for compatible fixes.
- Renaming a metric, label, log field or environment variable breaks dashboards and charts, so it counts as an API change (`x`).
- Tags are never moved. If one was published by mistake, publish a new `y`. Consumer repos' `vendor-lib.sh check` treats a moved tag as a failure.

## What v0.1.0 must contain

The PRs for items (a)-(d) of vws-deploy#44 must be merged to main, and CI on that main must be green.

- [ ] (a) obs and errs implementation
- [ ] (b) `scripts/check-imports.sh`
- [ ] (c) sample consumer build (`scripts/check-consumer.sh`), with the sample updated to call the API from (a)
- [ ] (d) `scripts/vendor-lib.sh`

## Publishing (publisher)

1. After `git fetch origin`, choose the commit `C` to publish. `C` is a commit on `origin/main` that came in through a PR (not a commit that failed `direct-push-watch`).
2. Check that every CI run on `C` is success with nothing skipped: `gh run list --commit C`.
3. Run the same checks locally: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`, `scripts/guard-agents.sh selftest`, and the scripts and selftests from (b)-(d).
4. Create an annotated tag: `git tag -a v0.1.0 C -m "vws-lib v0.1.0"`, then `git push origin v0.1.0`. Do not push branches.
5. Write in the GitHub release notes: the commit, the public API changes, the dependency modules and versions (1.x/0.x stability table), and what is unverified.
6. Comment the tag and commit on vws-deploy#44.

## Updating consumer repos (each service repo's author bot, through a PR)

1. `scripts/vendor-lib.sh vendor v0.1.0` → `third_party/vws-lib` and `VENDOR` change.
2. Check that `go.mod` has `require github.com/popododo0720/vws-lib v0.0.0` and `replace ... => ./third_party/vws-lib`, then `go mod tidy`.
3. Put `scripts/vendor-lib.sh check` in the consumer repo's CI.
4. In the PR body, give the tag, the commit, the API changes and the code changes they required.

## Needs checking

- How consumer repo CI reads a private repo (token scope). Until that is settled, only the hash can be checked, with `VWS_LIB_OFFLINE=1`.
- Whether to drop `replace` and vendoring and `require` the tag directly once the repos are public.

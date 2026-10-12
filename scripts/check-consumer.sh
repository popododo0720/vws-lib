#!/usr/bin/env bash
# check-consumer.sh [selftest]
# Builds examples/consumer, a separate module that requires vws-lib through replace, the way a
# service repo does. go.mod and go.sum must already be tidy. selftest breaks a copy of the lib and
# expects the build to fail.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# build runs under "!" and "||" in selftest, where set -e is off, so every step checks itself.
build() {
  local repo=$1
  cd "${repo}/examples/consumer" || return 1
  go mod tidy || return 1
  if [[ -d ${repo}/.git ]] && ! git -C "$repo" diff --exit-code -- examples/consumer/go.mod examples/consumer/go.sum >/dev/null 2>&1; then
    echo "check-consumer: examples/consumer/go.mod or go.sum is not tidy; run go mod tidy" >&2
    return 1
  fi
  go vet ./... || return 1
  go build -o /dev/null ./... || return 1
}

selftest() {
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  cp -r "$root"/. "$tmp"/
  rm -rf "$tmp/.git"
  (build "$tmp") || { echo "selftest: the unbroken copy failed to build" >&2; return 1; }
  rm -rf "$tmp/obs"
  ! (build "$tmp") >/dev/null 2>&1 || { echo "selftest: the build passed with package obs removed" >&2; return 1; }
  cp -r "$root"/obs "$tmp"/obs
  rm -rf "$tmp/errs"
  ! (build "$tmp") >/dev/null 2>&1 || { echo "selftest: the build passed with package errs removed" >&2; return 1; }
  cp -r "$root"/errs "$tmp"/errs
  sed -i 's/^func New(/func NewRenamed(/' "$tmp/errs/error.go"
  ! (build "$tmp") >/dev/null 2>&1 || { echo "selftest: the build passed with errs.New renamed" >&2; return 1; }
  cp "$root"/errs/error.go "$tmp"/errs/error.go
  printf '\n// stray\nfunc broken( {\n' >> "$tmp/errs/doc.go"
  ! (build "$tmp") >/dev/null 2>&1 || { echo "selftest: the build passed with a syntax error in the lib" >&2; return 1; }
  echo "check-consumer selftest ok"
}

case ${1:-} in
  selftest) selftest ;;
  "") (build "$root") && echo "check-consumer ok" ;;
  *) echo "usage: check-consumer.sh [selftest]" >&2; exit 2 ;;
esac

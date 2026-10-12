#!/usr/bin/env bash
# check-imports.sh [DIR] | selftest
# Fails when the module in DIR (default .) depends, directly or not, on vws-proto or a VWS
# service repository. The dependency points one way: service -> lib (AGENTS.md L3).
set -euo pipefail

FORBIDDEN='^github\.com/popododo0720/(vws-proto|vws-compute|vws-storage|vws-network|vws-console|vws-console-api|vws-hypervisor|vws-deploy)(/|$)'

check() {
  local dir=$1 deps bad
  deps=$(cd "$dir" && go list -deps ./... 2>&1) || { echo "check-imports: go list failed in ${dir}:" >&2; echo "$deps" >&2; return 2; }
  bad=$(grep -E "$FORBIDDEN" <<<"$deps" || true)
  if [[ -n $bad ]]; then
    echo "check-imports: forbidden dependencies in ${dir}:" >&2
    echo "$bad" >&2
    return 1
  fi
}

selftest() {
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/mod" "$tmp/fake"
  printf 'module github.com/popododo0720/vws-proto\n\ngo 1.26.0\n' > "$tmp/fake/go.mod"
  printf 'package fake\n\nconst X = 1\n' > "$tmp/fake/fake.go"
  printf 'module example.com/mod\n\ngo 1.26.0\n\nrequire github.com/popododo0720/vws-proto v0.0.0\n\nreplace github.com/popododo0720/vws-proto => ../fake\n' > "$tmp/mod/go.mod"
  printf 'package mod\n\nimport _ "github.com/popododo0720/vws-proto"\n' > "$tmp/mod/mod.go"
  ! check "$tmp/mod" 2>/dev/null || { echo "selftest: a direct vws-proto import was accepted" >&2; return 1; }
  # Transitive: mid imports vws-proto, mod imports mid.
  mkdir -p "$tmp/mid"
  printf 'module example.com/mid\n\ngo 1.26.0\n\nrequire github.com/popododo0720/vws-proto v0.0.0\n\nreplace github.com/popododo0720/vws-proto => ../fake\n' > "$tmp/mid/go.mod"
  printf 'package mid\n\nimport _ "github.com/popododo0720/vws-proto"\n' > "$tmp/mid/mid.go"
  printf 'module example.com/mod\n\ngo 1.26.0\n\nrequire example.com/mid v0.0.0\n\nreplace example.com/mid => ../mid\n\nreplace github.com/popododo0720/vws-proto => ../fake\n' > "$tmp/mod/go.mod"
  printf 'package mod\n\nimport _ "example.com/mid"\n' > "$tmp/mod/mod.go"
  ! check "$tmp/mod" 2>/dev/null || { echo "selftest: a transitive vws-proto import was accepted" >&2; return 1; }
  # Clean module passes.
  printf 'module example.com/mod\n\ngo 1.26.0\n' > "$tmp/mod/go.mod"
  printf 'package mod\n' > "$tmp/mod/mod.go"
  check "$tmp/mod" || { echo "selftest: a clean module was rejected" >&2; return 1; }
  # A broken module is an error, not a pass.
  rm "$tmp/mod/go.mod"
  ! check "$tmp/mod" 2>/dev/null || { echo "selftest: a module that cannot be listed was accepted" >&2; return 1; }
  echo "check-imports selftest ok"
}

case ${1:-.} in
  selftest) selftest ;;
  *) check "${1:-.}" && echo "check-imports ok" ;;
esac

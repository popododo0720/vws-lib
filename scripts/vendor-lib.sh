#!/usr/bin/env bash
# vendor-lib.sh: copy a tagged vws-lib into a consumer repo and check the copy.
#
#   vendor-lib.sh vendor REF [DEST]   copy REF (a tag or a full 40-character commit id) into DEST (default third_party/vws-lib)
#   vendor-lib.sh check [DEST]        fail if DEST differs from what VENDOR records, or REF moved
#   vendor-lib.sh selftest            negative tests against a throwaway source repo
#
# Run it from the consumer repo root. The source is VWS_LIB_REPO (a URL or a local path, default
# the GitHub repo). check needs the source to confirm REF; with VWS_LIB_OFFLINE=1 it checks only
# that the files match the hash recorded in VENDOR. Never edit the copy: change vws-lib, tag, vendor again.
set -euo pipefail

SOURCE=${VWS_LIB_REPO:-https://github.com/popododo0720/vws-lib.git}
DEFAULT_DEST=third_party/vws-lib

die() { echo "vendor-lib: $*" >&2; exit 1; }

# tree_hash DIR: one sha256 over every file path and content under DIR, except VENDOR.
tree_hash() {
  (cd "$1" && find . -type f ! -path ./VENDOR -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)
}

# fetch REF into a temp clone; prints "<clone dir> <commit>".
fetch() {
  local ref=$1 tmp commit
  tmp=$(mktemp -d)
  git -C "$tmp" init -q
  git -C "$tmp" fetch -q --tags "$SOURCE" "$ref" 2>/dev/null || { rm -rf "$tmp"; die "cannot fetch ${ref} from ${SOURCE}"; }
  commit=$(git -C "$tmp" rev-parse --verify -q "FETCH_HEAD^{commit}") || { rm -rf "$tmp"; die "${ref} is not a commit"; }
  echo "$tmp $commit"
}

export_tree() { # clone commit dest
  rm -rf "$3"
  mkdir -p "$3"
  git -C "$1" archive "$2" | tar -x -C "$3"
}

cmd_vendor() {
  local ref=$1 dest=${2:-$DEFAULT_DEST} out tmp commit
  out=$(fetch "$ref")
  read -r tmp commit <<<"$out"
  export_tree "$tmp" "$commit" "$dest"
  rm -rf "$tmp"
  printf 'ref %s\ncommit %s\nsha256 %s\n' "$ref" "$commit" "$(tree_hash "$dest")" > "$dest/VENDOR"
  echo "vendored vws-lib ${ref} (${commit}) into ${dest}"
}

field() { sed -n "s/^$1 //p" "$2"; }

cmd_check() {
  local dest=${1:-$DEFAULT_DEST} ref commit want out tmp got fresh
  [[ -f $dest/VENDOR ]] || die "${dest}/VENDOR is missing; run vendor-lib.sh vendor REF"
  ref=$(field ref "$dest/VENDOR")
  commit=$(field commit "$dest/VENDOR")
  want=$(field sha256 "$dest/VENDOR")
  [[ -n $ref && -n $commit && -n $want ]] || die "${dest}/VENDOR is incomplete"
  got=$(tree_hash "$dest")
  [[ $got == "$want" ]] || die "${dest} was edited: its files do not match VENDOR; vendor again instead"
  if [[ ${VWS_LIB_OFFLINE:-0} == 1 ]]; then
    echo "vendor-lib: ${dest} matches VENDOR (offline: ${ref} not confirmed)"
    return 0
  fi
  out=$(fetch "$ref")
  read -r tmp got <<<"$out"
  [[ $got == "$commit" ]] || { rm -rf "$tmp"; die "${ref} now points to ${got}, VENDOR records ${commit}; a tag moved"; }
  fresh=$(mktemp -d)
  export_tree "$tmp" "$commit" "$fresh"
  rm -rf "$tmp"
  got=$(tree_hash "$fresh")
  rm -rf "$fresh"
  [[ $got == "$want" ]] || die "${dest} does not match ${ref} (${commit})"
  echo "vendor-lib: ${dest} matches ${ref} (${commit})"
}

cmd_selftest() {
  local self tmp src
  self=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  src=$tmp/src
  git init -q "$src"
  printf 'module github.com/popododo0720/vws-lib\n\ngo 1.26.0\n' > "$src/go.mod"
  mkdir -p "$src/obs" && printf 'package obs\n' > "$src/obs/doc.go"
  git -C "$src" -c user.name=t -c user.email=t@t add -A
  git -C "$src" -c user.name=t -c user.email=t@t commit -qm one
  git -C "$src" tag v0.1.0
  mkdir -p "$tmp/consumer"
  run() { (cd "$tmp/consumer" && VWS_LIB_REPO=$src "$self" "$@"); }
  expect_fail() { local why=$1; shift; if run "$@" >/dev/null 2>&1; then echo "selftest: ${why} was accepted" >&2; return 1; fi; }

  run vendor v0.1.0 >/dev/null || { echo "selftest: vendor failed" >&2; return 1; }
  run check >/dev/null || { echo "selftest: a fresh copy failed check" >&2; return 1; }
  (cd "$tmp/consumer" && VWS_LIB_OFFLINE=1 "$self" check >/dev/null) || { echo "selftest: offline check of a fresh copy failed" >&2; return 1; }

  printf '// edit\n' >> "$tmp/consumer/third_party/vws-lib/obs/doc.go"
  expect_fail "an edited file" check || return 1
  (cd "$tmp/consumer" && ! VWS_LIB_OFFLINE=1 "$self" check >/dev/null 2>&1) || { echo "selftest: offline check accepted an edit" >&2; return 1; }
  run vendor v0.1.0 >/dev/null
  printf 'package obs\n' > "$tmp/consumer/third_party/vws-lib/obs/extra.go"
  expect_fail "an added file" check || return 1
  run vendor v0.1.0 >/dev/null
  rm "$tmp/consumer/third_party/vws-lib/obs/doc.go"
  expect_fail "a deleted file" check || return 1

  # The tag moves to a new commit: the copy no longer matches the tag.
  run vendor v0.1.0 >/dev/null
  printf 'package obs // moved\n' > "$src/obs/doc.go"
  git -C "$src" -c user.name=t -c user.email=t@t commit -qam two
  git -C "$src" tag -f v0.1.0 >/dev/null
  expect_fail "a moved tag" check || return 1

  # Forged VENDOR: files and hash edited together still differ from the tag.
  run vendor v0.1.0 >/dev/null
  printf '// forged\n' >> "$tmp/consumer/third_party/vws-lib/obs/doc.go"
  sed -i "s/^sha256 .*/sha256 $(tree_hash "$tmp/consumer/third_party/vws-lib")/" "$tmp/consumer/third_party/vws-lib/VENDOR"
  expect_fail "an edit with a rewritten VENDOR hash" check || return 1

  rm "$tmp/consumer/third_party/vws-lib/VENDOR"
  expect_fail "a missing VENDOR" check || return 1
  expect_fail "an unknown ref" vendor v9.9.9 || return 1

  # A commit id works as a ref too.
  if ! { run vendor "$(git -C "$src" rev-parse HEAD)" >/dev/null && run check >/dev/null; }; then
    echo "selftest: vendoring a commit id failed" >&2
    return 1
  fi
  echo "vendor-lib selftest ok"
}

case ${1:-} in
  vendor) [[ $# -ge 2 ]] || die "usage: vendor-lib.sh vendor REF [DEST]"; cmd_vendor "$2" "${3:-$DEFAULT_DEST}" ;;
  check) cmd_check "${2:-$DEFAULT_DEST}" ;;
  selftest) cmd_selftest ;;
  *) echo "usage: vendor-lib.sh vendor REF [DEST] | check [DEST] | selftest" >&2; exit 2 ;;
esac

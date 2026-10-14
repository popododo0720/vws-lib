#!/usr/bin/env bash
# vendor-proto.sh: copy a released vws-proto tag into a consumer repo and check the copy.
#
#   vendor-proto.sh vendor TAG [DEST]   copy TAG (a release tag vX.Y.Z, v0.2.0 or later) into DEST (default third_party/vws-proto)
#   vendor-proto.sh check [DEST]        fail if DEST differs from what VENDOR records, the ref is not a release tag, or the tag moved
#   vendor-proto.sh selftest            negative tests against a throwaway source repo
#
# Modelled on vendor-lib.sh. Unlike vendor-lib.sh, a commit id or a branch is never a ref: only a tag under
# refs/tags that matches vX.Y.Z and is at least MIN (v0.2.0). Run it from the consumer repo root. The source
# is VWS_PROTO_REPO (a URL or a local path, default the GitHub repo). With VWS_PROTO_OFFLINE=1 check confirms
# only the ref grammar and the recorded hash. Never edit the copy: change vws-proto, tag, vendor again.
set -euo pipefail

SOURCE=${VWS_PROTO_REPO:-https://github.com/popododo0720/vws-proto.git}
DEFAULT_DEST=third_party/vws-proto
MIN=v0.2.0

die() { echo "vendor-proto: $*" >&2; exit 1; }

# release_ref REF: REF is vX.Y.Z (no suffix) and not older than MIN.
release_ref() {
  local ref=$1 a b c x y z
  [[ $ref =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] ||
    die "${ref} is not a release tag (vX.Y.Z); commit ids, branches and pre-releases are not allowed"
  a=${BASH_REMATCH[1]} b=${BASH_REMATCH[2]} c=${BASH_REMATCH[3]}
  [[ $MIN =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]
  x=${BASH_REMATCH[1]} y=${BASH_REMATCH[2]} z=${BASH_REMATCH[3]}
  if ((10#$a != 10#$x)); then ((10#$a > 10#$x)); elif ((10#$b != 10#$y)); then ((10#$b > 10#$y)); else ((10#$c >= 10#$z)); fi ||
    die "${ref} is older than ${MIN}"
}

tree_hash() {
  (cd "$1" && find . -type f ! -path ./VENDOR -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)
}

# fetch TAG: fetches refs/tags/TAG only, so a branch of the same name never matches. Prints "<clone> <commit>".
fetch() {
  local ref=$1 tmp commit
  tmp=$(mktemp -d)
  git -C "$tmp" init -q
  git -C "$tmp" fetch -q --no-tags "$SOURCE" "refs/tags/${ref}:refs/tags/${ref}" 2>/dev/null ||
    { rm -rf "$tmp"; die "${ref} is not a tag in ${SOURCE}"; }
  commit=$(git -C "$tmp" rev-parse --verify -q "refs/tags/${ref}^{commit}") || { rm -rf "$tmp"; die "${ref} is not a commit"; }
  echo "$tmp $commit"
}

export_tree() { # clone commit dest
  rm -rf "$3"
  mkdir -p "$3"
  git -C "$1" archive "$2" | tar -x -C "$3"
}

cmd_vendor() {
  local ref=$1 dest=${2:-$DEFAULT_DEST} out tmp commit
  release_ref "$ref"
  out=$(fetch "$ref")
  read -r tmp commit <<<"$out"
  export_tree "$tmp" "$commit" "$dest"
  rm -rf "$tmp"
  printf 'ref %s\ncommit %s\nsha256 %s\n' "$ref" "$commit" "$(tree_hash "$dest")" >"$dest/VENDOR"
  echo "vendored vws-proto ${ref} (${commit}) into ${dest}"
}

field() { sed -n "s/^$1 //p" "$2"; }

cmd_check() {
  local dest=${1:-$DEFAULT_DEST} ref commit want out tmp got fresh
  [[ -f $dest/VENDOR ]] || die "${dest}/VENDOR is missing; run vendor-proto.sh vendor TAG"
  ref=$(field ref "$dest/VENDOR")
  commit=$(field commit "$dest/VENDOR")
  want=$(field sha256 "$dest/VENDOR")
  [[ -n $ref && -n $commit && -n $want ]] || die "${dest}/VENDOR is incomplete"
  release_ref "$ref"
  got=$(tree_hash "$dest")
  [[ $got == "$want" ]] || die "${dest} was edited: its files do not match VENDOR; vendor again instead"
  if [[ ${VWS_PROTO_OFFLINE:-0} == 1 ]]; then
    echo "vendor-proto: ${dest} matches VENDOR (offline: ${ref} not confirmed)"
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
  echo "vendor-proto: ${dest} matches ${ref} (${commit})"
}

cmd_selftest() {
  local self tmp src v vendor_dir
  self=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  src=$tmp/src
  git init -q -b main "$src"
  g() { git -C "$src" -c user.name=t -c user.email=t@t "$@"; }
  mkdir -p "$src/gen/iam/v1" && printf 'package iamv1\n' >"$src/gen/iam/v1/iam.pb.go"
  g add -A && g commit -qm one
  g tag v0.1.9
  g tag v0.2.0
  g tag v0.2.0-rc1
  g tag v0.10.0
  g branch feat/iam-v1
  g branch v0.3.0 # a branch that looks like a release tag
  mkdir -p "$tmp/consumer"
  run() { (cd "$tmp/consumer" && VWS_PROTO_REPO=$src "$self" "$@"); }
  fail() { echo "selftest: $*" >&2; return 1; }
  expect_fail() { local why=$1; shift; if run "$@" >/dev/null 2>&1; then fail "${why} was accepted"; fi; }
  vendor_dir=$tmp/consumer/third_party/vws-proto

  run vendor v0.2.0 >/dev/null || fail "vendoring v0.2.0 failed"
  run check >/dev/null || fail "a fresh copy failed check"
  (cd "$tmp/consumer" && VWS_PROTO_OFFLINE=1 "$self" check >/dev/null) || fail "offline check of a fresh copy failed"
  { run vendor v0.10.0 >/dev/null && run check >/dev/null; } || fail "v0.10.0 (numeric, not string, order) was rejected"

  expect_fail "a tag older than v0.2.0" vendor v0.1.9 || return 1
  expect_fail "a pre-release tag" vendor v0.2.0-rc1 || return 1
  expect_fail "a commit id" vendor "$(g rev-parse HEAD)" || return 1
  expect_fail "a short commit id" vendor "$(g rev-parse --short HEAD)" || return 1
  expect_fail "a branch" vendor feat/iam-v1 || return 1
  expect_fail "a branch named like a release" vendor v0.3.0 || return 1
  expect_fail "main" vendor main || return 1
  expect_fail "an unknown tag" vendor v9.9.9 || return 1

  run vendor v0.2.0 >/dev/null
  printf '// edit\n' >>"$vendor_dir/gen/iam/v1/iam.pb.go"
  expect_fail "a hand-edited file" check || return 1
  (cd "$tmp/consumer" && ! VWS_PROTO_OFFLINE=1 "$self" check >/dev/null 2>&1) || fail "offline check accepted an edit"
  run vendor v0.2.0 >/dev/null
  printf 'package iamv1\n' >"$vendor_dir/gen/iam/v1/extra.go"
  expect_fail "an added file" check || return 1
  run vendor v0.2.0 >/dev/null
  rm "$vendor_dir/gen/iam/v1/iam.pb.go"
  expect_fail "a deleted file" check || return 1

  # VENDOR rewritten to an untagged ref: rejected even offline.
  run vendor v0.2.0 >/dev/null
  sed -i 's/^ref .*/ref feat\/iam-v1/' "$vendor_dir/VENDOR"
  expect_fail "VENDOR naming a branch" check || return 1
  (cd "$tmp/consumer" && ! VWS_PROTO_OFFLINE=1 "$self" check >/dev/null 2>&1) || fail "offline check accepted a branch ref"

  # Forged VENDOR: files and hash edited together still differ from the tag.
  run vendor v0.2.0 >/dev/null
  printf '// forged\n' >>"$vendor_dir/gen/iam/v1/iam.pb.go"
  v=$(tree_hash "$vendor_dir")
  sed -i "s/^sha256 .*/sha256 ${v}/" "$vendor_dir/VENDOR"
  expect_fail "an edit with a rewritten VENDOR hash" check || return 1

  # The tag moves to a new commit.
  run vendor v0.2.0 >/dev/null
  printf 'package iamv1 // moved\n' >"$src/gen/iam/v1/iam.pb.go"
  g commit -qam two
  g tag -f v0.2.0 >/dev/null
  expect_fail "a moved tag" check || return 1

  rm "$vendor_dir/VENDOR"
  expect_fail "a missing VENDOR" check || return 1
  echo "vendor-proto selftest ok"
}

case ${1:-} in
  vendor) [[ $# -ge 2 ]] || die "usage: vendor-proto.sh vendor TAG [DEST]"; cmd_vendor "$2" "${3:-$DEFAULT_DEST}" ;;
  check) cmd_check "${2:-$DEFAULT_DEST}" ;;
  selftest) cmd_selftest ;;
  *) echo "usage: vendor-proto.sh vendor TAG [DEST] | check [DEST] | selftest" >&2; exit 2 ;;
esac

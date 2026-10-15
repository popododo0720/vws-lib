#!/usr/bin/env bash
# English-only check. The same semantics as `guard-agents.sh english` in vws-deploy, kept as a separate
# file so this repo's guard-agents.sh copy (and its PR-body rules) stays as it is.
#
#   check-english.sh files [ALLOWLIST]   no tracked file has Hangul unless a glob in ALLOWLIST (default
#                                        .github/english-allowlist, lines "<glob> <reason>") covers it
#   check-english.sh text [FILE]         FILE (or $PR_BODY when no FILE) has no Hangul
#   check-english.sh commits BASE HEAD   no commit message in BASE..HEAD has Hangul (git comment lines ignored)
#   check-english.sh selftest            bad inputs fail, good inputs pass
set -euo pipefail

die() { echo "$*" >&2; exit 1; }

# exit 0 when stdin has a Hangul character. perl, because grep -P needs a UTF-8 locale.
has_hangul() { perl -CSD -ne '$f = 1 if /\p{Hangul}/; END { exit($f ? 0 : 1) }' 2>/dev/null; }

cmd_files() {
  local list=${1:-.github/english-allowlist} glob reason f ok
  local -a globs=() bad=()
  if [[ -f $list ]]; then
    while read -r glob reason; do
      [[ -z $glob || $glob == \#* ]] && continue
      [[ -n $reason ]] || die "english: ${list}: '${glob}' has no reason"
      globs+=("$glob")
    done <"$list"
  fi
  while IFS= read -r -d '' f; do
    [[ -f $f ]] || continue
    ok=0
    for glob in "${globs[@]}"; do
      # shellcheck disable=SC2053
      [[ $f == $glob ]] && ok=1 && break
    done
    [[ $ok -eq 1 ]] && continue
    has_hangul <"$f" && bad+=("$f")
  done < <(git ls-files -z)
  [[ ${#bad[@]} -eq 0 ]] || die "english: Hangul in ${bad[*]}; write English or add '<glob> <reason>' to ${list}"
}

cmd_text() {
  local body=${PR_BODY:-}
  if [[ -n ${1:-} ]]; then
    [[ -f $1 ]] || die "english: ${1} does not exist"
    body=$(cat "$1")
  fi
  ! has_hangul <<<"$body" || die "english: write the pull request text in English (found Hangul)"
}

cmd_commits() {
  local base=${1:-} head=${2:-} sha
  [[ -n $base && -n $head ]] || die "english: commits needs BASE and HEAD"
  for sha in $(git rev-list --no-merges "${base}..${head}"); do
    ! git log -1 --format=%B "$sha" | grep -v '^#' | has_hangul ||
      die "english: commit ${sha:0:12} has Hangul; write the message in English"
  done
}

expect() {
  local want=$1 label=$2 rc=0
  shift 2
  ("$@") >/dev/null 2>&1 || rc=$?
  if [[ $want == pass && $rc -ne 0 ]] || [[ $want == fail && $rc -eq 0 ]]; then die "selftest: ${label} should ${want}"; fi
  echo "ok: ${label} ${want}s"
}

# in_dir DIR ARGS: run this script with ARGS inside DIR.
in_dir() { local d=$1; shift; (cd "$d" && bash "$SELF" "$@"); }

cmd_selftest() {
  local tmp ko
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  ko=$(printf '\352\267\234')
  expect fail "text with Hangul" env PR_BODY="Part of #1 ${ko}" bash "$SELF" text
  expect fail "text with Hangul in an HTML comment" env PR_BODY="Part of #1 <!-- ${ko} -->" bash "$SELF" text
  expect pass "text with other non-ASCII" env PR_BODY="Part of #1 caf$(printf '\303\251') $(printf '\342\200\224')" bash "$SELF" text
  git init -q "$tmp/r"
  printf 'plain\n' >"$tmp/r/a.md"; printf 'x %s\n' "$ko" >"$tmp/r/ko.md"; mkdir "$tmp/r/t"; printf '%s\n' "$ko" >"$tmp/r/t/d.txt"
  git -C "$tmp/r" add -A
  expect fail "files with Hangul and no allowlist" in_dir "$tmp/r" files
  printf '# c\nko.md test input\nt/* locale data\n' >"$tmp/r/allow"
  expect pass "files with every Hangul file allowlisted" in_dir "$tmp/r" files allow
  printf 't/* locale data\n' >"$tmp/r/allow"
  expect fail "files with one Hangul file not allowlisted" in_dir "$tmp/r" files allow
  printf 'ko.md\nt/* locale data\n' >"$tmp/r/allow"
  expect fail "allowlist entry without a reason" in_dir "$tmp/r" files allow
  git -C "$tmp/r" -c user.name=t -c user.email=t@t commit -q -m "base"
  git -C "$tmp/r" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "ci: x" -m "Part of #1"
  expect pass "English commits" in_dir "$tmp/r" commits HEAD~1 HEAD
  git -C "$tmp/r" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "ci: ${ko}"
  expect fail "a commit with Hangul" in_dir "$tmp/r" commits HEAD~2 HEAD
  echo "selftest passed"
}

SELF=$(readlink -f "$0")
c=${1:-}
shift || true
case "$c" in
  files) cmd_files "$@" ;;
  text) cmd_text "$@" ;;
  commits) cmd_commits "$@" ;;
  selftest) cmd_selftest ;;
  *) die "usage: check-english.sh <files [ALLOWLIST]|text [FILE]|commits BASE HEAD|selftest>" ;;
esac

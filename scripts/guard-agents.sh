#!/usr/bin/env bash
# Insider-risk guards that need no repo-specific paths. Copy this file as-is into another repo.
#
#   guard-agents.sh agents [FILE]           FILE (default AGENTS.md) has the guardrail section and items 1-7
#   guard-agents.sh pr-base BASE_REF        a pull request must target the main branch
#   guard-agents.sh pr-body [FILE]          the pull request text (FILE or $PR_BODY) has "Part of #N" or "Closes #N" (N >= 1)
#   guard-agents.sh push-watch BEFORE AFTER every first-parent commit pushed to main came from a pull request
#   guard-agents.sh selftest                run the guards on bad inputs; every one must fail
#
# Environment: GUARD_MAIN_BRANCH (default main), GUARD_HEADING (default "## Insider-risk guardrails"),
# GUARD_ITEMS (default 7), GITHUB_REPOSITORY and an authenticated gh for push-watch.
set -euo pipefail
export LC_ALL=C

MAIN=${GUARD_MAIN_BRANCH:-main}
HEADING=${GUARD_HEADING:-## Insider-risk guardrails}
ITEMS=${GUARD_ITEMS:-7}

die() {
  echo "$*" >&2
  exit 1
}

cmd_agents() {
  local file=${1:-AGENTS.md} section n
  [[ -f $file ]] || die "agents: ${file} does not exist"
  grep -q -x -F -- "$HEADING" "$file" || die "agents: ${file} has no '${HEADING}' heading"
  section=$(awk -v h="$HEADING" '
    $0 == h { on = 1; next }
    on && /^## / { exit }
    on { print }' "$file")
  for ((n = 1; n <= ITEMS; n++)); do
    [[ $(grep -c -E "^${n}\. +[^ ]" <<<"$section") -eq 1 ]] ||
      die "agents: '${HEADING}' needs exactly one item numbered ${n}"
  done
  if grep -q -E "^$((ITEMS + 1))\. " <<<"$section"; then
    die "agents: '${HEADING}' has more than ${ITEMS} numbered items"
  fi
}

cmd_pr_base() {
  local base=${1:-}
  [[ -n $base ]] || die "pr-base: needs the base ref"
  [[ $base == "$MAIN" ]] || die "pr-base: this pull request targets '${base}', not '${MAIN}'. Do not stack pull requests."
}

# "Part of #N" or "Part of owner/repo#N", the same pattern as vws-deploy's guard-agents.sh.
# Issue numbers start at 1: #0, #00 and #012 name no issue and do not count.
PART_OF_RE='(^|[^[:alnum:]])part of[[:space:]]+([A-Za-z0-9._/-]+)?#[1-9][0-9]*'
CLOSES_RE='(^|[^[:alnum:]])(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]+([A-Za-z0-9._/-]+)?#[1-9][0-9]*'

cmd_pr_body() {
  local body=${PR_BODY:-}
  if [[ -n ${1:-} ]]; then
    [[ -f $1 ]] || die "pr-body: ${1} does not exist"
    body=$(cat "$1")
  fi
  grep -q -i -E "$PART_OF_RE|$CLOSES_RE" <<<"$body" ||
    die "pr-body: the pull request text needs 'Part of #N' or 'Closes #N'"
}

pr_count() {
  local sha=$1 i n
  for i in 1 2 3; do
    n=$(gh api "repos/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/commits/${sha}/pulls" --jq 'length') || n=0
    [[ $n -gt 0 ]] && break
    # The association appears a few seconds after a merge.
    [[ $i -lt 3 ]] && sleep "${GUARD_RETRY_SECONDS:-5}"
  done
  echo "$n"
}

cmd_push_watch() {
  local before=${1:-} after=${2:-} zero sha
  local -a commits bad=()
  [[ -n $before && -n $after ]] || die "push-watch: needs BEFORE and AFTER"
  zero=$(printf '0%.0s' {1..40})
  if [[ $before == "$zero" ]] || ! git cat-file -e "${before}^{commit}" 2>/dev/null; then
    commits=("$after")
  else
    git merge-base --is-ancestor "$before" "$after" ||
      die "push-watch: history of '${MAIN}' was rewritten (${before} is not an ancestor of ${after})"
    mapfile -t commits < <(git rev-list --first-parent "${before}..${after}")
  fi
  for sha in "${commits[@]}"; do
    [[ $(pr_count "$sha") -gt 0 ]] || bad+=("$sha")
  done
  [[ ${#bad[@]} -eq 0 ]] || die "push-watch: commits on '${MAIN}' without a pull request: ${bad[*]}"
}

expect_fail() {
  local label=$1
  shift
  if ("$@") >/dev/null 2>&1; then
    die "selftest: ${label} was accepted"
  fi
  echo "ok: ${label} fails"
}

expect_pass() {
  local label=$1
  shift
  "$@" >/dev/null 2>&1 || die "selftest: ${label} was rejected"
  echo "ok: ${label} passes"
}

cmd_selftest() {
  local tmp good i
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  good="$tmp/good.md"
  { echo "# t"; echo "$HEADING"; echo; for ((i = 1; i <= ITEMS; i++)); do echo "${i}. **rule ${i}** text"; done; echo; echo "## next"; } >"$good"
  expect_pass "complete section" cmd_agents "$good"
  expect_fail "missing file" cmd_agents "$tmp/none.md"
  printf '# t\n## other\n1. a\n' >"$tmp/nohead.md"
  expect_fail "missing heading" cmd_agents "$tmp/nohead.md"
  grep -v -E "^3\. " "$good" >"$tmp/noitem.md"
  expect_fail "missing item 3" cmd_agents "$tmp/noitem.md"
  { cat "$good"; echo "3. again"; } >"$tmp/dup.md"
  sed -i "/^## next/d" "$tmp/dup.md"
  expect_fail "duplicate item 3" cmd_agents "$tmp/dup.md"
  awk -v h="$HEADING" '$0 == h { print "## Other"; next } { print }' "$good" >"$tmp/renamed.md"
  expect_fail "renamed heading" cmd_agents "$tmp/renamed.md"

  expect_pass "base is main" cmd_pr_base "$MAIN"
  expect_fail "base is a stack branch" cmd_pr_base "feature-stack-base"
  expect_fail "empty base" cmd_pr_base ""

  expect_pass "Closes #N" env PR_BODY="Closes #12" bash "$0" pr-body
  expect_pass "Part of #N" env PR_BODY="Part of #12" bash "$0" pr-body
  expect_pass "Part of owner/repo#N" env PR_BODY="intro
Part of acme/repo#7" bash "$0" pr-body
  expect_fail "Part of #0" env PR_BODY="Part of #0" bash "$0" pr-body
  expect_fail "Part of #012" env PR_BODY="Part of #012" bash "$0" pr-body
  expect_fail "Closes #0" env PR_BODY="Closes #0" bash "$0" pr-body
  expect_fail "Part of without number" env PR_BODY="Part of the plan" bash "$0" pr-body
  expect_pass "fixes owner/repo#N" env PR_BODY="intro
fixes acme/repo#7" bash "$0" pr-body
  expect_fail "no issue reference" env PR_BODY="mentions #12 only" bash "$0" pr-body
  expect_fail "empty body" env PR_BODY="" bash "$0" pr-body
  expect_fail "Closes without number" env PR_BODY="Closes the gap" bash "$0" pr-body

  # push-watch against a throwaway repository and a gh that answers from a file.
  local repo="$tmp/repo" bin="$tmp/bin" c1 c2 c3
  mkdir -p "$repo" "$bin"
  git -C "$repo" init -q -b "$MAIN"
  git -C "$repo" config user.email t@example.invalid
  git -C "$repo" config user.name t
  for i in 1 2 3; do
    echo "$i" >"$repo/f"
    git -C "$repo" add f
    git -C "$repo" commit -q -m "c$i"
  done
  c1=$(git -C "$repo" rev-parse HEAD~2)
  c2=$(git -C "$repo" rev-parse HEAD~1)
  c3=$(git -C "$repo" rev-parse HEAD)
  cat >"$bin/gh" <<'GH'
#!/usr/bin/env bash
# fake gh api repos/R/commits/SHA/pulls --jq length: prints the number of lines for SHA in $FAKE_PULLS
sha=$(sed -n 's#.*/commits/\([0-9a-f]*\)/pulls.*#\1#p' <<<"$*")
grep -c "^${sha}$" "$FAKE_PULLS" || true
GH
  chmod +x "$bin/gh"
  : >"$tmp/pulls"
  printf '%s\n%s\n' "$c2" "$c3" >"$tmp/pulls"
  push() { (cd "$repo" && PATH="$bin:$PATH" FAKE_PULLS="$tmp/pulls" GITHUB_REPOSITORY=o/r GUARD_RETRY_SECONDS=0 bash "$OLDPWD_SCRIPT" push-watch "$@"); }
  export OLDPWD_SCRIPT
  OLDPWD_SCRIPT=$(readlink -f "$0")
  expect_pass "every pushed commit has a pull request" push "$c1" "$c3"
  printf '%s\n' "$c3" >"$tmp/pulls"
  expect_fail "a pushed commit without a pull request" push "$c1" "$c3"
  git -C "$repo" checkout -q -b rewritten "$c1"
  echo x >"$repo/g"
  git -C "$repo" add g
  git -C "$repo" commit -q -m rewrite
  expect_fail "rewritten history" push "$c3" "$(git -C "$repo" rev-parse HEAD)"
  expect_fail "new branch whose head has no pull request" push "$(printf '0%.0s' {1..40})" "$c1"
  echo "selftest passed"
}

command_name=${1:-}
shift || true
case "$command_name" in
  agents) cmd_agents "$@" ;;
  pr-base) cmd_pr_base "$@" ;;
  pr-body) cmd_pr_body "$@" ;;
  push-watch) cmd_push_watch "$@" ;;
  selftest) cmd_selftest ;;
  *) die "usage: guard-agents.sh <agents [FILE]|pr-base BASE|pr-body [FILE]|push-watch BEFORE AFTER|selftest>" ;;
esac

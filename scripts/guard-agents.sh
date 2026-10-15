#!/usr/bin/env bash
# Insider-risk guards that need no repo-specific paths. Copy this file as-is into another repo.
#
#   guard-agents.sh agents [FILE]           FILE (default AGENTS.md) has the guardrail section and items 1-7
#   guard-agents.sh rules [FILE] [HEADING]  every row of the rule table under HEADING names an enforcer; prints the Judgment count
#   guard-agents.sh judgment [FILE] [HEADING] alias of rules
#   guard-agents.sh archtest [FILE] [HEADING] prints the rules line, then runs go test -v on GUARD_ARCHTEST_PKG
#   guard-agents.sh identity [SUMFILE]      this file's sha256 equals SUMFILE (default scripts/guard-agents.sh.sha256)
#   guard-agents.sh pr-base BASE_REF        a pull request must target the main branch
#   guard-agents.sh pr-body [FILE]          the pull request text (FILE or $PR_BODY) has "Part of #N", no closing keyword and no Hangul
#   guard-agents.sh commit-msg FILE         the commit message has no closing keyword (Closes/Fixes/Resolves #N, owner/repo#N or an issue URL) and no Hangul
#   guard-agents.sh pr-commits BASE HEAD    every commit in BASE..HEAD passes commit-msg
#   guard-agents.sh push-watch BEFORE AFTER every first-parent commit pushed to main came from a pull request
#   guard-agents.sh verify-gate HEAD LABELS [FILE]
#                                           the pull request is verify-approved for HEAD: LABELS (comma list) has
#                                           verify-approved and a comment in the text (FILE or $PR_TEXT) has a
#                                           "Verified-by: vws-verify" section naming HEAD with evidence for every
#                                           QUALITY.md item, plus a "Codex review: <full path or URL> blocking=0"
#                                           line. Each comment starts with a "@@comment-by: LOGIN" line;
#                                           text outside a comment (such as the PR body) never counts
#   guard-agents.sh english [ALLOWLIST]     no tracked file has Hangul unless a glob in ALLOWLIST (default
#                                           .github/english-allowlist, lines "<glob> <reason>") covers it
#   guard-agents.sh selftest                run the guards on bad inputs; every one must fail
#
# Environment: GUARD_MAIN_BRANCH (default main), GUARD_HEADING (default "## Insider-risk guardrails"),
# GUARD_ITEMS (default 7), GUARD_RULES_HEADING (default: the first of "## Rules" and "## 규칙표" in FILE),
# GUARD_VERIFY_LABEL (default verify-approved), GUARD_QUALITY_ITEMS (default 9, the QUALITY.md checklist),
# GUARD_VERIFY_ACTORS (comma list of logins allowed to approve; empty = any author), GUARD_VERIFY_LABELER
# (login that last added the label), GUARD_VERIFY_EXCLUDE (PR author and head pusher: never approve),
# GUARD_VERIFY_IDENTITY (the vws-verify login, exempt from EXCLUDE), GUARD_ARCHTEST_PKG (default ./internal/archtest/...), GITHUB_REPOSITORY and an authenticated gh for push-watch.
set -euo pipefail
export LC_ALL=C

MAIN=${GUARD_MAIN_BRANCH:-main}
HEADING=${GUARD_HEADING:-## Insider-risk guardrails}
ITEMS=${GUARD_ITEMS:-7}
VERIFY_LABEL=${GUARD_VERIFY_LABEL:-verify-approved}
QUALITY_ITEMS=${GUARD_QUALITY_ITEMS:-9}
VERIFY_ACTORS=${GUARD_VERIFY_ACTORS:-}

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

# The rule table under HEADING has two columns, rule | enforcer. An enforcer cell is never empty;
# a rule without a test, lint or CI step says so with a cell that starts with "Judgment".
# HEADING: the argument, else GUARD_RULES_HEADING, else "## Rules" or "## 규칙표", whichever FILE has.
cmd_rules() {
  local file=${1:-AGENTS.md} heading=${2:-${GUARD_RULES_HEADING:-}} h rows
  [[ -f $file ]] || die "rules: ${file} does not exist"
  if [[ -z $heading ]]; then
    for h in "## Rules" "## 규칙표"; do
      if grep -q -x -F -- "$h" "$file"; then heading=$h; break; fi
    done
    [[ -n $heading ]] || die "rules: ${file} has no '## Rules' or '## 규칙표' heading"
  fi
  grep -q -x -F -- "$heading" "$file" || die "rules: ${file} has no '${heading}' heading"
  rows=$(awk -v h="$heading" '
    $0 == h { on = 1; next }
    on && /^## / { exit }
    on && /^\|/ { print }' "$file")
  [[ -n $rows ]] || die "rules: '${heading}' has no table"
  awk -F'|' 'NR == 1 && NF != 4 { exit 1 } NR == 2 && $2 !~ /^[[:space:]]*:?-+:?[[:space:]]*$/ { exit 1 }' <<<"$rows" ||
    die "rules: '${heading}' needs a two-column '| Rule | Enforcer |' table"
  rows=$(tail -n +3 <<<"$rows")
  [[ -n $rows ]] || die "rules: '${heading}' has no rule rows"
  awk -F'|' 'NF != 4 || $3 ~ /^[[:space:]]*$/ { print "rules: row without an enforcer: " $0; bad = 1 } END { exit bad }' <<<"$rows" >&2 ||
    die "rules: every row needs an enforcer (test, lint, CI step) or 'Judgment'"
  echo "rules: $(wc -l <<<"$rows") rows, $(awk -F'|' '$3 ~ /^[[:space:]]*Judgment/' <<<"$rows" | wc -l) Judgment"
}

# verify-gate: the approval is bound to one head commit. A push changes the head, so the old section no
# longer matches and the gate is red until vws-verify judges the new head (the label is also removed on
# synchronize by the workflow). Evidence per checklist item: a URL, a path, or "N/A: <reason>".
cmd_verify_gate() {
  local head=${1:-} labels=${2:-} text=${PR_TEXT:-} section n line
  [[ $head =~ ^[0-9a-f]{40}$ ]] || die "verify-gate: needs the full head commit SHA"
  if [[ -n ${3:-} ]]; then
    [[ -f $3 ]] || die "verify-gate: ${3} does not exist"
    text=$(cat "$3")
  fi
  [[ ",${labels}," == *",${VERIFY_LABEL},"* ]] || die "verify-gate: the pull request has no '${VERIFY_LABEL}' label"
  local labeler=${GUARD_VERIFY_LABELER:-} excluded=${GUARD_VERIFY_EXCLUDE:-} identity=${GUARD_VERIFY_IDENTITY:-}
  if [[ -n $VERIFY_ACTORS ]]; then
    [[ -n $labeler && ",${VERIFY_ACTORS}," == *",${labeler},"* ]] ||
      die "verify-gate: '${VERIFY_LABEL}' was added by '${labeler:-unknown}', not by one of: ${VERIFY_ACTORS}"
  fi
  # No self-approval: the PR author and the head pusher (GUARD_VERIFY_EXCLUDE) can neither label nor
  # approve, unless the login is the vws-verify identity (GUARD_VERIFY_IDENTITY). Same-account gap: when
  # vws-verify and the author share one login, this check cannot tell them apart.
  if [[ -n $labeler && $labeler != "$identity" && ",${excluded}," == *",${labeler},"* ]]; then
    die "verify-gate: '${VERIFY_LABEL}' was added by '${labeler}', the PR author or pusher"
  fi
  # Keep only comments ("@@comment-by: LOGIN" blocks) by allowed authors. Each kept block starts with a
  # "#" line so a section never runs from one comment into the next. The PR body is not a comment.
  # Control characters: a CRLF line ending is dropped; any other control character (a lone \r, \v, ESC...)
  # becomes "?", so "\r@@comment-by:" (which the workflow's "^@@comment-by:" defang misses) never turns into
  # a marker line. Tab and newline stay.
  text=$(sed 's/\r$//' <<<"$text" | LC_ALL=C tr '\000-\010\013-\037\177' '?' | awk -v actors=",${VERIFY_ACTORS}," -v excl=",${excluded}," -v id="$identity" '
    /^@@comment-by: / { who = substr($0, 15); keep = (actors == ",," || index(actors, "," who ",") > 0)
      if (who != id && index(excl, "," who ",") > 0) keep = 0
      if (keep) print "#"; next }
    keep { print }')
  # The section that names this head: from its "Verified-by: vws-verify" line to the next such line or heading.
  section=$(tr -d '`' <<<"$text" | awk -v h="$head" '
    /Verified-by:[[:space:]]*vws-verify/ { if (keep) exit; on = 1; buf = ""; next }
    on && /^#/ { if (keep) exit; on = 0; next }
    on { buf = buf $0 "\n"; if ($0 ~ "^[[:space:]]*Head:[[:space:]]*" h "[[:space:]]*$") keep = 1 }
    END { if (keep) printf "%s", buf }')
  [[ -n $section ]] || die "verify-gate: no 'Verified-by: vws-verify' section names the head ${head}; a push after approval needs a new verification"
  for ((n = 1; n <= QUALITY_ITEMS; n++)); do
    line=$(grep -m1 -E "^[[:space:]]*(- )?${n}\.[[:space:]]" <<<"$section" || true)
    line=${line#*"${n}."}
    # "N/A:" needs a reason; otherwise a URL or a file path (dir/file.ext) is the evidence.
    if grep -q -E '^[[:space:]]*N/A' <<<"$line"; then
      grep -q -E '^[[:space:]]*N/A:[[:space:]]*[^[:space:]]' <<<"$line"
    else
      grep -q -E '(https?://[^[:space:]]+|[A-Za-z0-9._-]+/[A-Za-z0-9._/-]*\.[A-Za-z0-9]+)' <<<"$line"
    fi ||
      die "verify-gate: the Verified-by section for ${head:0:12} has no evidence (URL, path or 'N/A: reason') for checklist item ${n}"
  done
  # The same section carries the Codex review: "Codex review: <absolute path or http(s) URL> blocking=<N>".
  # It passes only with blocking=0. Path rule: /workspace/handoff/codex/<repo>-<pr>-<sha7>.md.
  local codex where blocking
  codex=$(grep -m1 -E '^[[:space:]]*(- )?Codex review:' <<<"$section" || true)
  [[ -n $codex ]] || die "verify-gate: no Codex review in the Verified-by section for ${head:0:12}; add 'Codex review: <full path or URL> blocking=<N>'"
  codex=${codex#*Codex review:}
  # blocking=<N> is matched as a string, never as bash arithmetic: $((N)) wraps 2^64 to 0, so only
  # all-zero digits pass; empty, signed, hex, spaced or other non-digit values are rejected.
  [[ $codex =~ blocking=([^[:space:]]*)[[:space:]]*$ ]] || die "verify-gate: the Codex review line needs 'blocking=<N>' at the end: ${codex}"
  blocking=${BASH_REMATCH[1]}
  where=${codex%blocking=*}
  where=$(sed -E 's/[[:space:]]+$//; s/^[[:space:]]+//' <<<"$where")
  [[ $where =~ ^(/[^[:space:]]+|https?://[^[:space:]/]+[^[:space:]]*)$ ]] ||
    die "verify-gate: Codex review needs a full path or URL (an absolute /... path or an http(s) URL), got '${where}'"
  [[ $blocking =~ ^[0-9]+$ ]] || die "verify-gate: Codex review blocking=<N> must be decimal digits, got '${blocking}' (${where})"
  [[ $blocking =~ ^0+$ ]] || die "verify-gate: Codex review has ${blocking} blocking findings (${where})"
}

# The one guard command for the architecture test: the Judgment count line comes first so the CI log
# always shows it, even when the test fails; go test runs with -v so every rule test is listed.
# A repo without the archtest directory says so and passes; the rules check still runs.
cmd_archtest() {
  local pkg=${GUARD_ARCHTEST_PKG:-./internal/archtest/...}
  cmd_rules "$@"
  if [[ ! -d ${pkg%/...} ]]; then
    echo "archtest: ${pkg%/...} does not exist; no architecture test to run"
    return 0
  fi
  go test -v -count=1 "$pkg" || die "archtest: go test -v ${pkg} failed"
}

# Every repo carries a byte-identical copy of the vws-deploy file. SUMFILE pins its sha256 (sha256sum format).
# vws-deploy CI checks SUMFILE against the file, so a change to the canonical file updates SUMFILE in the
# same pull request; other repos copy both files unchanged.
cmd_identity() {
  local sumfile=${1:-scripts/guard-agents.sh.sha256} self want got
  self=${GUARD_SELF:-${BASH_SOURCE[0]}}
  [[ -f $sumfile ]] || die "identity: ${sumfile} does not exist"
  want=$(awk 'NR == 1 { print $1 }' "$sumfile")
  [[ $want =~ ^[0-9a-f]{64}$ ]] || die "identity: ${sumfile} has no sha256"
  got=$(sha256sum "$self" | cut -d' ' -f1)
  [[ $got == "$want" ]] ||
    die "identity: ${self} (sha256 ${got}) is not the canonical vws-deploy copy (${want}); copy scripts/guard-agents.sh and its .sha256 from vws-deploy main, do not edit them here"
  echo "identity: ${self} is the canonical copy (${want:0:12})"
}

cmd_pr_base() {
  local base=${1:-}
  [[ -n $base ]] || die "pr-base: needs the base ref"
  [[ $base == "$MAIN" ]] || die "pr-base: this pull request targets '${base}', not '${MAIN}'. Do not stack pull requests."
}

# A closing keyword followed by an issue reference: GitHub would close that issue on merge.
# The reference is #N, owner/repo#N or a full issue or pull request URL (GitHub closes on all three).
CLOSING_RE='(^|[^[:alnum:]])(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]*:?[[:space:]]+(([A-Za-z0-9._/-]+)?#[0-9]+|(https?://)?[^[:space:]]*/(issues|pull)/[0-9]+)'
# "Part of #N" or "Part of owner/repo#N". #TBD is allowed in commits only, until the issue exists.
# Issue numbers start at 1: #0, #00 and #012 name no issue and are rejected wherever they appear.
PART_OF_RE='(^|[^[:alnum:]])part of[[:space:]]+([A-Za-z0-9._/-]+)?#[1-9][0-9]*'
PART_OF_ZERO_RE='(^|[^[:alnum:]])part of[[:space:]]+([A-Za-z0-9._/-]+)?#0'

# Work items are tracked on parent issues that a human closes. No PR or commit may close an
# issue automatically, so closing keywords are banned outright. Telling a parent issue from a
# leaf would need the GitHub API (sub-issues or a tracking label) in every hook and run; a
# plain ban needs no token, works offline and cannot be fooled by a missing label.
# The text a reader sees: HTML comments (<!-- -->, also across lines) and fenced code blocks
# (``` or ~~~) are removed, so a "Part of #N" hidden there does not count.
visible_text() {
  awk '
    {
      line = $0; out = ""
      if (!incom && line ~ /^[[:space:]]*(```|~~~)/) { fence = !fence; next }
      if (fence) next
      while (1) {
        if (incom) {
          i = index(line, "-->"); if (!i) { line = ""; break }
          line = substr(line, i + 3); incom = 0
        } else {
          i = index(line, "<!--"); if (!i) { out = out line; break }
          out = out substr(line, 1, i - 1); line = substr(line, i + 4); incom = 1
        }
      }
      print out
    }'
}

# English only: exit 0 when stdin has a Hangul character. perl, because grep -P needs a UTF-8 locale.
has_hangul() {
  perl -CSD -ne '$f = 1 if /\p{Hangul}/; END { exit($f ? 0 : 1) }' 2>/dev/null
}

cmd_english() {
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

cmd_pr_body() {
  local body=${PR_BODY:-} seen
  if [[ -n ${1:-} ]]; then
    [[ -f $1 ]] || die "pr-body: ${1} does not exist"
    body=$(cat "$1")
  fi
  seen=$(visible_text <<<"$body")
  # Closing keywords are checked in the raw text and in the visible text, so hiding one in a
  # comment or fence never makes a body pass that would otherwise fail.
  if grep -q -i -E "$CLOSING_RE" <<<"$body" || grep -q -i -E "$CLOSING_RE" <<<"$seen"; then
    die "pr-body: do not close issues from a pull request (found Closes/Fixes/Resolves #N); write 'Part of #N'"
  fi
  ! grep -q -i -E "$PART_OF_ZERO_RE" <<<"$body" ||
    die "pr-body: 'Part of #0' names no issue; issue numbers start at 1"
  grep -q -i -E "$PART_OF_RE" <<<"$seen" ||
    die "pr-body: the pull request text needs 'Part of #N' outside HTML comments and code fences"
  ! has_hangul <<<"$body" || die "pr-body: write the pull request text in English (found Hangul)"
}

cmd_commit_msg() {
  local file=${1:-} msg
  [[ -n $file && -f $file ]] || die "commit-msg: needs the message file"
  # Lines starting with # are git's comments and never reach the commit.
  msg=$(grep -v '^#' "$file" || true)
  if grep -q -i -E "$CLOSING_RE" <<<"$msg" || grep -q -i -E "$CLOSING_RE" <<<"$(visible_text <<<"$msg")"; then
    die "commit-msg: do not close issues from a commit (found Closes/Fixes/Resolves #N); write 'Part of #N'"
  fi
  ! grep -q -i -E "$PART_OF_ZERO_RE" <<<"$msg" ||
    die "commit-msg: 'Part of #0' names no issue; issue numbers start at 1"
  ! has_hangul <<<"$msg" || die "commit-msg: write the commit message in English (found Hangul)"
}

cmd_pr_commits() {
  local base=${1:-} head=${2:-} sha tmp
  [[ -n $base && -n $head ]] || die "pr-commits: needs BASE and HEAD"
  tmp=$(mktemp)
  trap 'rm -f "$tmp"' RETURN
  for sha in $(git rev-list --no-merges "${base}..${head}"); do
    git log -1 --format=%B "$sha" >"$tmp"
    (cmd_commit_msg "$tmp") 2>/dev/null || die "pr-commits: ${sha:0:12} closes an issue, names #0 or has Hangul; write English with 'Part of #N'"
  done
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

CORRECT_HEADING="## /correct loop"
# The Korean heading is still accepted until every repo's AGENTS.md is English.
CORRECT_HEADING_LEGACY="## /correct 교정 루프"
CORRECT_FIELDS="what-happened impact detection root-cause catching-check follow-ups"

cmd_correct() {
  local dir=${1:-.} tpl labels id h=$CORRECT_HEADING
  tpl="${dir}/.github/ISSUE_TEMPLATE/incident.yml"
  labels="${dir}/.github/labels.yml"
  [[ -f ${dir}/AGENTS.md ]] || die "correct: ${dir}/AGENTS.md does not exist"
  if ! tr -d '\r' <"${dir}/AGENTS.md" | grep -q -x -F -- "$h"; then
    tr -d '\r' <"${dir}/AGENTS.md" | grep -q -x -F -- "$CORRECT_HEADING_LEGACY" && h=$CORRECT_HEADING_LEGACY
  fi
  # CRLF line endings are read as LF, so a heading saved on Windows still matches.
  tr -d '\r' <"${dir}/AGENTS.md" | grep -q -x -F -- "$h" ||
    die "correct: AGENTS.md has no line that is exactly '${h}' (trailing spaces or a different heading do not match)"
  # Empty: no content before the next # or ## heading. A ### subheading and a line inside a code fence are
  # content; the fence lines themselves and blank lines are not, so an empty fence is an empty section.
  tr -d '\r' <"${dir}/AGENTS.md" | awk -v h="$h" '
    $0 == h { s = 1; next }
    !s { next }
    /^[[:space:]]*(```|~~~)/ { fence = !fence; next }
    !fence && /^##?([[:space:]]|$)/ { exit }
    /[^[:space:]]/ { f = 1; exit }
    END { exit !f }' || die "correct: AGENTS.md '${h}' section is empty (blank lines and empty code fences do not count)"
  [[ -f $tpl ]] || die "correct: ${tpl} does not exist"
  grep -q -E '^labels:.*\bincident\b' "$tpl" || die "correct: ${tpl} does not apply the incident label"
  for id in $CORRECT_FIELDS; do
    grep -q -E "^[[:space:]]+id:[[:space:]]+${id}[[:space:]]*$" "$tpl" || die "correct: ${tpl} has no field '${id}'"
  done
  [[ -f $labels ]] || die "correct: ${labels} does not exist"
  grep -q -E '^- name:[[:space:]]+incident[[:space:]]*$' "$labels" || die "correct: ${labels} does not define the incident label"
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

  local rules="$tmp/rules.md"
  printf '# t\n## Rules\n\n| Rule | Enforcer |\n| --- | --- |\n| a | `TestA` |\n| b | Judgment. why |\n\n## next\n| x | |\n' >"$rules"
  expect_pass "every rule has an enforcer" cmd_rules "$rules"
  [[ $(cmd_rules "$rules") == "rules: 2 rows, 1 Judgment" ]] || die "selftest: Judgment count is wrong"
  echo "ok: Judgment count"
  [[ $(bash "$0" judgment "$rules") == "rules: 2 rows, 1 Judgment" ]] || die "selftest: judgment is not an alias of rules"
  echo "ok: judgment is an alias of rules"
  mkdir -p "$tmp/bin" "$tmp/arch/internal/archtest"
  printf '#!/bin/sh\necho "go $*" >>"$GOLOG"\n[ -z "$GOFAIL" ]\n' >"$tmp/bin/go"
  chmod +x "$tmp/bin/go"
  local out self
  self=$(readlink -f "$0")
  out=$(cd "$tmp/arch" && PATH="$tmp/bin:$PATH" GOLOG="$tmp/go.log" bash "$self" archtest "$rules")
  [[ $(head -1 <<<"$out") == "rules: 2 rows, 1 Judgment" ]] || die "selftest: archtest does not print the Judgment count first"
  grep -q -x -F 'go test -v -count=1 ./internal/archtest/...' "$tmp/go.log" || die "selftest: archtest does not run go test -v"
  echo "ok: archtest prints the Judgment count and runs go test -v"
  expect_fail "a failing archtest" bash -c 'cd "$1" && PATH="$2:$PATH" GOLOG=/dev/null GOFAIL=1 bash "$3" archtest "$4"' _ "$tmp/arch" "$tmp/bin" "$self" "$rules"
  out=$(cd "$tmp" && PATH="$tmp/bin:$PATH" GOLOG=/dev/null GOFAIL=1 bash "$self" archtest "$rules") || die "selftest: archtest failed without an archtest directory"
  grep -q -x -F "rules: 2 rows, 1 Judgment" <<<"$out" || die "selftest: archtest without a directory dropped the Judgment count"
  echo "ok: archtest without a directory still prints the Judgment count"
  sed 's/^## Rules$/## 규칙표/; s/| Rule | Enforcer |/| 규칙 | 강제 |/' "$rules" >"$tmp/rules-ko.md"
  expect_pass "the Korean heading is found" cmd_rules "$tmp/rules-ko.md"
  sed 's/^## Rules$/## Policy/' "$rules" >"$tmp/rules-arg.md"
  expect_pass "heading from the argument" cmd_rules "$tmp/rules-arg.md" "## Policy"
  expect_pass "heading from GUARD_RULES_HEADING" env GUARD_RULES_HEADING="## Policy" bash "$0" rules "$tmp/rules-arg.md"
  expect_fail "a heading the file does not have" cmd_rules "$rules" "## Policy"
  sed 's/^| a | `TestA` |$/| a |  |/' "$rules" >"$tmp/norule.md"
  expect_fail "a rule without an enforcer" cmd_rules "$tmp/norule.md"
  sed 's/^| a | `TestA` |$/| a | `TestA` | extra |/' "$rules" >"$tmp/threecol.md"
  expect_fail "a row with three columns" cmd_rules "$tmp/threecol.md"
  sed 's/| Rule | Enforcer |/| Rule | Enforcer | Note |/' "$rules" >"$tmp/header.md"
  expect_fail "a three-column header" cmd_rules "$tmp/header.md"
  sed 's/^## Rules$/## other/' "$rules" >"$tmp/norules.md"
  expect_fail "no rule table heading" cmd_rules "$tmp/norules.md"
  printf '# t\n## Rules\n\n| Rule | Enforcer |\n| --- | --- |\n' >"$tmp/emptyrules.md"
  expect_fail "an empty rule table" cmd_rules "$tmp/emptyrules.md"

  cp "$0" "$tmp/copy.sh"
  sha256sum "$tmp/copy.sh" >"$tmp/copy.sha256"
  expect_pass "an identical copy" env GUARD_SELF="$tmp/copy.sh" bash "$0" identity "$tmp/copy.sha256"
  echo "# local edit" >>"$tmp/copy.sh"
  expect_fail "an edited copy" env GUARD_SELF="$tmp/copy.sh" bash "$0" identity "$tmp/copy.sha256"
  echo "not-a-hash  x" >"$tmp/bad.sha256"
  expect_fail "a sum file without a hash" bash "$0" identity "$tmp/bad.sha256"
  expect_fail "a missing sum file" bash "$0" identity "$tmp/none.sha256"

  expect_pass "base is main" cmd_pr_base "$MAIN"
  expect_fail "base is a stack branch" cmd_pr_base "feature-stack-base"
  expect_fail "empty base" cmd_pr_base ""

  expect_pass "Part of #N" env PR_BODY="Part of #12" bash "$0" pr-body
  expect_pass "part of owner/repo#N" env PR_BODY="intro
part of acme/repo#7" bash "$0" pr-body
  expect_fail "Closes #N" env PR_BODY="Part of #3
Closes #12" bash "$0" pr-body
  expect_fail "fixes owner/repo#N" env PR_BODY="Part of #3
fixes acme/repo#7" bash "$0" pr-body
  expect_fail "Resolves: #N" env PR_BODY="Part of #3 Resolves: #7" bash "$0" pr-body
  expect_fail "no Part of" env PR_BODY="mentions #12 only" bash "$0" pr-body
  expect_fail "empty body" env PR_BODY="" bash "$0" pr-body
  expect_fail "Part of without number" env PR_BODY="Part of the gap" bash "$0" pr-body
  expect_fail "Part of #TBD in a pull request" env PR_BODY="Part of #TBD" bash "$0" pr-body

  expect_fail "Part of only in an HTML comment" env PR_BODY="<!-- Part of #12 -->" bash "$0" pr-body
  expect_fail "Part of only in a multi-line HTML comment" env PR_BODY="intro
<!--
Part of #12
-->
outro" bash "$0" pr-body
  expect_fail "Part of only in a backtick fence" env PR_BODY="intro
\`\`\`
Part of #12
\`\`\`" bash "$0" pr-body
  expect_fail "Part of only in a tilde fence" env PR_BODY="~~~text
Part of #12
~~~" bash "$0" pr-body
  expect_fail "Part of only in an unclosed fence" env PR_BODY="\`\`\`
Part of #12" bash "$0" pr-body
  expect_pass "Part of after a closed comment on the same line" env PR_BODY="<!-- note --> Part of #12" bash "$0" pr-body
  expect_pass "Part of after a fence" env PR_BODY="\`\`\`
code
\`\`\`
Part of #12" bash "$0" pr-body
  expect_fail "Closes hidden in an HTML comment" env PR_BODY="Part of #3 <!-- Closes #12 -->" bash "$0" pr-body
  expect_fail "Closes hidden in a fence" env PR_BODY="Part of #3
\`\`\`
Closes #12
\`\`\`" bash "$0" pr-body
  expect_fail "Closes split around a comment" env PR_BODY="Part of #3 Closes<!-- x --> #12" bash "$0" pr-body
  printf 'ci: x\n\nPart of #3 Fixes<!-- --> #12\n' >"$tmp/msg-hidden"
  expect_fail "commit-msg Fixes split around a comment" cmd_commit_msg "$tmp/msg-hidden"
  expect_fail "Part of #0" env PR_BODY="Part of #0" bash "$0" pr-body
  expect_fail "Part of owner/repo#0" env PR_BODY="Part of acme/repo#0" bash "$0" pr-body
  expect_fail "Part of #00" env PR_BODY="Part of #00" bash "$0" pr-body
  expect_fail "Part of #012" env PR_BODY="Part of #012" bash "$0" pr-body
  expect_fail "Part of #0 next to a valid Part of" env PR_BODY="Part of #3
Part of #0" bash "$0" pr-body
  expect_pass "Part of #10" env PR_BODY="Part of #10" bash "$0" pr-body
  printf 'ci: x\n\nPart of #0\n' >"$tmp/msg-zero"
  expect_fail "commit with Part of #0" cmd_commit_msg "$tmp/msg-zero"
  printf 'ci: x\n\nPart of acme/repo#0\n' >"$tmp/msg-zero2"
  expect_fail "commit with Part of owner/repo#0" cmd_commit_msg "$tmp/msg-zero2"
  printf 'ci: x\n\nPart of #12\n' >"$tmp/msg-ok"
  expect_pass "commit with Part of #N" cmd_commit_msg "$tmp/msg-ok"
  # English only: Hangul anywhere in a commit message or pull request text fails.
  printf 'ci: \352\267\234\354\271\231\n\nPart of #12\n' >"$tmp/msg-ko"
  expect_fail "commit with Hangul in the subject" cmd_commit_msg "$tmp/msg-ko"
  printf 'ci: x\n\nVerified-by: \352\262\200\354\246\235\n\nPart of #12\n' >"$tmp/msg-ko2"
  expect_fail "commit with Hangul in the body" cmd_commit_msg "$tmp/msg-ko2"
  printf '# \352\267\234\354\271\231\nci: x\n\nPart of #12\n' >"$tmp/msg-ko3"
  expect_pass "Hangul only in a git comment line" cmd_commit_msg "$tmp/msg-ko3"
  expect_fail "pr-body with Hangul" env PR_BODY="Part of #12 $(printf '\352\267\234')" bash "$0" pr-body
  expect_fail "pr-body with Hangul in an HTML comment" env PR_BODY="Part of #12 <!-- $(printf '\352\267\234') -->" bash "$0" pr-body
  expect_pass "pr-body with other non-ASCII text" env PR_BODY="Part of #12 caf$(printf '\303\251') $(printf '\342\200\224') ok" bash "$0" pr-body
  # english: tracked files with Hangul fail unless an allowlist glob with a reason covers them.
  local en="$tmp/en" self
  self=$(readlink -f "$0")
  git init -q "$en"
  printf 'plain\n' >"$en/a.md"; printf 'x \352\267\234\n' >"$en/ko.md"; mkdir "$en/t"; printf '\352\260\222\n' >"$en/t/data.txt"
  git -C "$en" add -A
  expect_fail "english with a Hangul file and no allowlist" bash -c 'cd "$1" && bash "$2" english' _ "$en" "$self"
  printf '# comment\nko.md test input\nt/* locale data\n' >"$en/allow"
  expect_pass "english with every Hangul file allowlisted" bash -c 'cd "$1" && bash "$2" english allow' _ "$en" "$self"
  printf 't/* locale data\n' >"$en/allow"
  expect_fail "english with one Hangul file not allowlisted" bash -c 'cd "$1" && bash "$2" english allow' _ "$en" "$self"
  printf 'ko.md\nt/* locale data\n' >"$en/allow"
  expect_fail "english allowlist entry without a reason" bash -c 'cd "$1" && bash "$2" english allow' _ "$en" "$self"
  printf 'ci: x\n\nPart of #TBD\n' >"$tmp/msg-tbd"
  expect_pass "commit with Part of #TBD" cmd_commit_msg "$tmp/msg-tbd"
  printf 'ci: x\n\nCloses #12\n' >"$tmp/msg-closes"
  expect_fail "commit with Closes #N" cmd_commit_msg "$tmp/msg-closes"
  printf 'docs: y (fixes acme/repo#7)\n' >"$tmp/msg-fixes"
  expect_fail "subject with fixes owner/repo#N" cmd_commit_msg "$tmp/msg-fixes"
  local kw
  for kw in Closes closes CLOSED close Fix fixes FIXED fixed Resolve resolves RESOLVED resolved; do
    expect_fail "pr-body ${kw} with a full issue URL" env PR_BODY="Part of #3
${kw} https://github.com/acme/repo/issues/7" bash "$0" pr-body
    printf 'ci: x\n\n%s https://github.com/acme/repo/issues/7\n\nPart of #3\n' "$kw" >"$tmp/msg-url"
    expect_fail "commit-msg ${kw} with a full issue URL" cmd_commit_msg "$tmp/msg-url"
  done
  expect_fail "Closes: with a pull request URL" env PR_BODY="Part of #3 Closes: https://github.com/acme/repo/pull/7" bash "$0" pr-body
  expect_fail "fixes with a URL without the scheme" env PR_BODY="Part of #3 fixes github.com/acme/repo/issues/7" bash "$0" pr-body
  expect_pass "an issue URL without a closing keyword" env PR_BODY="Part of #3, see https://github.com/acme/repo/issues/7" bash "$0" pr-body
  printf 'ci: x\n# Closes #12 in a git comment line\n' >"$tmp/msg-comment"
  expect_pass "Closes in a git comment line" cmd_commit_msg "$tmp/msg-comment"

  # correct: a complete repo passes; each missing piece fails.
  local cr="$tmp/correct"
  mkdir -p "$cr/.github/ISSUE_TEMPLATE"
  printf '# t\n%s\n1. x\n' "$CORRECT_HEADING" >"$cr/AGENTS.md"
  { echo "name: incident"; echo "labels: [incident]"; echo "body:"; for id in $CORRECT_FIELDS; do echo "  - type: textarea"; echo "    id: ${id}"; done; } >"$cr/.github/ISSUE_TEMPLATE/incident.yml"
  printf -- '- name: incident\n  color: "b60205"\n' >"$cr/.github/labels.yml"
  expect_pass "complete /correct loop" cmd_correct "$cr"
  rm -rf "$tmp/legacy"; cp -r "$cr" "$tmp/legacy"; printf '# t\n%s\n1. x\n' "$CORRECT_HEADING_LEGACY" >"$tmp/legacy/AGENTS.md"
  expect_pass "the Korean /correct heading until every repo is English" cmd_correct "$tmp/legacy"
  local cb="$tmp/correct-bad"
  rm -rf "$cb"; cp -r "$cr" "$cb"; sed -i '/correct/d' "$cb/AGENTS.md"
  expect_fail "no /correct section" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n\n## next\n1. x\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_fail "/correct heading with an empty body" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n\n### 절차\n\n## next\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_pass "/correct body with only a ### subheading" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n```sh\n# gh issue create --template incident.yml\n```\n## next\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_pass "/correct body with a fenced '#' line" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n\n```\n```\n\n## next\n1. x\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_fail "/correct body with only an empty code fence" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n  \t\n```sh\n   \n```\n## next\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_fail "/correct body with whitespace and a blank fence" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\r\n%s\r\n1. x\r\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_pass "/correct heading with CRLF line endings" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\r\n%s\r\n\r\n## next\r\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_fail "/correct CRLF heading with an empty body" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; printf '# t\n%s\n\n\n# top\n1. x\n' "$CORRECT_HEADING" >"$cb/AGENTS.md"
  expect_fail "/correct body empty before a # heading" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; sed -i '/^labels:/d' "$cb/.github/ISSUE_TEMPLATE/incident.yml"
  expect_fail "template missing only the labels: line" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; sed -i '/id: catching-check/d' "$cb/.github/ISSUE_TEMPLATE/incident.yml"
  expect_fail "template without the catching-check field" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; sed -i 's/^labels:.*/labels: [bug]/' "$cb/.github/ISSUE_TEMPLATE/incident.yml"
  expect_fail "template without the incident label" cmd_correct "$cb"
  rm -rf "$cb"; cp -r "$cr" "$cb"; rm "$cb/.github/labels.yml"
  expect_fail "no labels file" cmd_correct "$cb"

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
  git -C "$repo" checkout -q -b commits "$c3"
  echo y >"$repo/h"
  git -C "$repo" add h
  git -C "$repo" commit -q -m "ci: y" -m "Part of #4"
  expect_pass "commits with Part of" bash -c 'cd "$1" && bash "$2" pr-commits "$3" HEAD' _ "$repo" "$OLDPWD_SCRIPT" "$c3"
  git -C "$repo" commit -q --allow-empty -m "ci: z" -m "Closes #4"
  expect_fail "a commit that closes an issue" bash -c 'cd "$1" && bash "$2" pr-commits "$3" HEAD' _ "$repo" "$OLDPWD_SCRIPT" "$c3"
  local c4
  c4=$(git -C "$repo" rev-parse HEAD)
  git -C "$repo" commit -q --allow-empty -m "ci: u" -m "Resolved https://github.com/acme/repo/issues/4" -m "Part of #4"
  expect_fail "a commit that closes an issue by URL" bash -c 'cd "$1" && bash "$2" pr-commits "$3" HEAD' _ "$repo" "$OLDPWD_SCRIPT" "$c4"
  # verify-gate: label and a section bound to the head with evidence for every item.
  local h1 h2 ev
  h1=$(printf 'a%.0s' {1..40}); h2=$(printf 'b%.0s' {1..40})
  ev="$tmp/ev.md"
  { echo "@@comment-by: verifier"; echo "## Verified-by: vws-verify"; echo "Head: ${h1}"
    for ((i = 1; i <= QUALITY_ITEMS; i++)); do echo "${i}. https://example.invalid/log/${i}"; done
    echo "Codex review: /workspace/handoff/codex/vws-deploy-1-aaaaaaa.md blocking=0"; } >"$ev"
  expect_pass "label and evidence for the head" cmd_verify_gate "$h1" "bug,${VERIFY_LABEL}" "$ev"
  expect_fail "no label" cmd_verify_gate "$h1" "bug" "$ev"
  expect_fail "a push after approval (new head)" cmd_verify_gate "$h2" "$VERIFY_LABEL" "$ev"
  expect_fail "short SHA" cmd_verify_gate "${h1:0:12}" "$VERIFY_LABEL" "$ev"
  grep -v -E "^4\. " "$ev" >"$tmp/ev4.md"
  expect_fail "missing evidence for item 4" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/ev4.md"
  sed 's#^6\. .*#6. done#' "$ev" >"$tmp/ev6.md"
  expect_fail "item 6 without a link, path or N/A reason" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/ev6.md"
  sed 's#^7\. .*#7. N/A: no telemetry in a shell script#; s#^2\. .*#2. tests/vws/run.sh output in evidence/run.log#' "$ev" >"$tmp/evna.md"
  expect_pass "N/A with a reason and a path count as evidence" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/evna.md"
  sed 's#^7\. .*#7. N/A:#' "$ev" >"$tmp/evna2.md"
  expect_fail "N/A without a reason" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/evna2.md"
  expect_fail "no section" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/nohead.md"
  # Codex review line: required, a full path or URL, and blocking=0.
  local msg
  grep -v 'Codex review:' "$ev" >"$tmp/nocodex.md"
  expect_fail "no Codex review line" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/nocodex.md"
  msg=$( (cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/nocodex.md") 2>&1 >/dev/null || true)
  [[ $msg == *"no Codex review"* ]] || die "selftest: no Codex review line gave '${msg}'"
  sed 's#blocking=0#blocking=2#' "$ev" >"$tmp/codex2.md"
  expect_fail "a Codex review with blocking=2" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codex2.md"
  msg=$( (cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codex2.md") 2>&1 >/dev/null || true)
  [[ $msg == *"Codex review has 2 blocking findings"* ]] || die "selftest: blocking=2 gave '${msg}'"
  echo "ok: blocking=2 names the count"
  sed 's#^Codex review: .*#Codex review: https://example.invalid/codex/vws-deploy-1-aaaaaaa.md blocking=0#' "$ev" >"$tmp/codexurl.md"
  expect_pass "a Codex review with blocking=0 (path)" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$ev"
  expect_pass "a Codex review with blocking=0 (URL)" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexurl.md"
  sed 's#^Codex review: .*#Codex review: blocking=0#' "$ev" >"$tmp/codexnopath.md"
  expect_fail "a Codex review without a path or URL" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexnopath.md"
  msg=$( (cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexnopath.md") 2>&1 >/dev/null || true)
  [[ $msg == *"Codex review needs a full path or URL"* ]] || die "selftest: no path gave '${msg}'"
  sed 's#^Codex review: .*#Codex review: codex/vws-deploy-1-aaaaaaa.md blocking=0#' "$ev" >"$tmp/codexrel.md"
  expect_fail "a Codex review with a relative path" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexrel.md"
  # blocking=<N> is a string match: only all-zero digits pass, no bash arithmetic (2^64 wrapped to 0).
  local bv i=0
  for bv in 18446744073709551616 -0 -1 '' abc 0x0 ' 0' 1; do
    i=$((i + 1))
    sed "s#blocking=0#blocking=${bv}#" "$ev" >"$tmp/codexbad$i.md"
    expect_fail "a Codex review with blocking='${bv}'" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexbad$i.md"
  done
  sed 's#blocking=0#blocking=00#' "$ev" >"$tmp/codex00.md"
  expect_pass "a Codex review with blocking=00" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codex00.md"
  sed 's#^Codex review: .*#Codex review: /workspace/handoff/codex/vws-deploy-1-aaaaaaa.md#' "$ev" >"$tmp/codexnon.md"
  expect_fail "a Codex review without blocking=<N>" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/codexnon.md"
  { echo "@@comment-by: verifier"; echo "## Verified-by: vws-verify"; echo "Head: ${h2}"; echo "1. stale"; cat "$ev"; } >"$tmp/two.md"
  expect_pass "a stale section and a current one: the current one counts" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/two.md"
  # Only comments count: the same section in the PR body (no @@comment-by block) is ignored.
  grep -v '^@@comment-by:' "$ev" >"$tmp/body.md"
  expect_fail "a section in the PR body, not a comment" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/body.md"
  # A section split across comments does not count: items 5-9 come from another comment.
  { head -n 7 "$ev"; echo "@@comment-by: verifier"; tail -n +8 "$ev"; } >"$tmp/split.md"
  expect_fail "a section split across two comments" cmd_verify_gate "$h1" "$VERIFY_LABEL" "$tmp/split.md"
  # Allowed actors: the comment author and the labeler must both be in GUARD_VERIFY_ACTORS.
  expect_pass "allowed author and labeler" env GUARD_VERIFY_ACTORS=bot,verifier GUARD_VERIFY_LABELER=verifier \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  sed 's#^@@comment-by: .*#@@comment-by: intruder#' "$ev" >"$tmp/other.md"
  expect_fail "a comment by an author who may not approve" env GUARD_VERIFY_ACTORS=verifier GUARD_VERIFY_LABELER=verifier \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$tmp/other.md"
  # A comment line "\r@@comment-by: LOGIN" (or another control character before the marker) escapes the
  # workflow's "^@@comment-by:" defang; it must not become a marker once control characters are handled.
  local cc
  for cc in '\r' '\v' '\f' '\033' '\177'; do
    { echo "@@comment-by: intruder"; printf "${cc}@@comment-by: verifier\n"; tail -n +2 "$ev"; } >"$tmp/forge.md"
    expect_fail "a forged '${cc}@@comment-by:' line in a comment" env GUARD_VERIFY_ACTORS=verifier GUARD_VERIFY_LABELER=verifier \
      "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$tmp/forge.md"
  done
  { printf '@@comment-by: verifier\r\n'; sed 's/$/\r/' <(tail -n +2 "$ev"); } >"$tmp/crlf.md"
  expect_pass "a CRLF verdict comment by an allowed author" env GUARD_VERIFY_ACTORS=verifier GUARD_VERIFY_LABELER=verifier \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$tmp/crlf.md"
  expect_fail "a label added by someone who may not approve" env GUARD_VERIFY_ACTORS=verifier GUARD_VERIFY_LABELER=intruder \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  expect_fail "an unknown labeler when actors are set" env GUARD_VERIFY_ACTORS=verifier \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  # No self-approval: a comment or label by the PR author/pusher never counts, unless it is the vws-verify identity.
  expect_fail "a verdict comment by the PR author" env GUARD_VERIFY_EXCLUDE=verifier "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  expect_fail "a label added by the pusher" env GUARD_VERIFY_EXCLUDE=author,pusher GUARD_VERIFY_LABELER=pusher \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  expect_pass "a verdict by someone other than the author" env GUARD_VERIFY_EXCLUDE=author,pusher GUARD_VERIFY_LABELER=verifier \
    "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  expect_pass "the vws-verify identity on the same account (known gap)" env GUARD_VERIFY_EXCLUDE=verifier GUARD_VERIFY_IDENTITY=verifier \
    GUARD_VERIFY_LABELER=verifier "$OLDPWD_SCRIPT" verify-gate "$h1" "$VERIFY_LABEL" "$ev"
  echo "selftest passed"
}

command_name=${1:-}
shift || true
case "$command_name" in
  agents) cmd_agents "$@" ;;
  pr-base) cmd_pr_base "$@" ;;
  pr-body) cmd_pr_body "$@" ;;
  rules | judgment) cmd_rules "$@" ;;
  archtest) cmd_archtest "$@" ;;
  verify-gate) cmd_verify_gate "$@" ;;
  identity) cmd_identity "$@" ;;
  commit-msg) cmd_commit_msg "$@" ;;
  pr-commits) cmd_pr_commits "$@" ;;
  push-watch) cmd_push_watch "$@" ;;
  correct) cmd_correct "$@" ;;
  english) cmd_english "$@" ;;
  selftest) cmd_selftest ;;
  *) die "usage: guard-agents.sh <agents [FILE]|pr-base BASE|rules [FILE] [HEADING]|judgment [FILE] [HEADING]|archtest [FILE] [HEADING]|identity [SUMFILE]|pr-body [FILE]|commit-msg FILE|pr-commits BASE HEAD|push-watch BEFORE AFTER|verify-gate HEAD LABELS [FILE]|correct [DIR]|english [ALLOWLIST]|selftest>" ;;
esac

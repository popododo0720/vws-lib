#!/usr/bin/env bash
# Repo guard (popododo0720/vws-deploy#448). Runs the checks of the shared scripts/guard-agents.sh that
# read this repo's own files. repo-guard.yml runs it on every pull request; scripts/guard_test.sh shows
# that it fails on a broken input.
# Usage: scripts/guard.sh [all|correct]
set -euo pipefail
cd "$(dirname "$0")/.."

die() {
  echo "guard: $*" >&2
  exit 1
}

# R21: the /correct loop. AGENTS.md has a non-empty '## /correct loop' section, the incident form applies
# the incident label with every required field, and .github/labels.yml defines the label.
cmd_correct() {
  scripts/guard-agents.sh correct . ||
    die "R21: the /correct loop, incident form or incident label is missing (AGENTS.md '## /correct loop', .github/ISSUE_TEMPLATE/incident.yml, .github/labels.yml)"
}

case ${1:-all} in
  all | correct) cmd_correct ;;
  -h | --help)
    sed -n "2,5p" "$0"
    exit 0
    ;;
  *) die "usage: scripts/guard.sh [all|correct]" ;;
esac
echo "guard ok"

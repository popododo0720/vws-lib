#!/usr/bin/env bash
# check-test-rand.sh [DIR]
# assumes gofmt-formatted source; CI enforces it
# Fails when a tracked *_test.go file draws from an unseeded source, so a rerun gives the same verdict.
# Rules (each failure prints file:line: rule N: reason):
#   1. imports "math/rand" (its globals are unseeded)
#   2. imports "math/rand/v2" and calls rand.F( for an F other than New, NewPCG, NewChaCha8, NewZipf
#   3. imports "crypto/rand" and has no cryptotest.SetGlobalRandom( call
#   4. a line has time.Now().UnixNano() or time.Now().Unix()
#   5. math/rand, math/rand/v2 or crypto/rand is imported under an alias
# `//vws:unseeded <reason>` in a file's first 20 lines skips rule 3; at the end of a line it skips rules 2 and 4.
# The reason must not be empty.
set -euo pipefail
export LC_ALL=C

dir=${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}

files=$(git -C "$dir" ls-files '*_test.go' | grep -v -e '^vendor/' -e '/vendor/' || true)
out=
while IFS= read -r f; do
  [[ -n $f ]] || continue
  out+=$(awk -v F="$f" '
    function flag(n, why) { printf "%s:%d: rule %d: %s\n", F, FNR, n, why; bad = 1 }
    function token(t) {
      if (t == "import") { imports = 1; alias = ""; return }
      if (!imports) return
      if (t == "(") { block = 1; return }
      if (t == ")") { imports = block = 0; return }
      if (t == ";") { alias = ""; return }
      alias = t
    }
    function literal(path) {
      if (!imports) return
      if (path == "math/rand" || path == "math/rand/v2" || path == "crypto/rand") {
        if (alias != "" && alias != "rand") flag(5, "\"" path "\" imported as " alias)
        if (path == "math/rand") flag(1, "math/rand globals are unseeded; use math/rand/v2 New with a fixed seed")
        if (path == "math/rand/v2") v2 = 1
        if (path == "crypto/rand" && !cr) { cr = 1; crline = FNR }
      }
      alias = ""
      if (!block) imports = 0
    }
    BEGIN { ok["New"]; ok["NewPCG"]; ok["NewChaCha8"]; ok["NewZipf"] }
    {
      line = ""; word = ""; reason = 0
      for (i = 1; i <= length($0); i++) {
        c = substr($0, i, 1); pair = substr($0, i, 2)
        if (comment) {
          if (pair == "*/") { comment = 0; i++ }
          line = line " "
          continue
        }
        if (quote != "") {
          line = line " "
          if (escaped) { escaped = 0; value = value c; continue }
          if (c == "\\" && quote != "`") { escaped = 1; value = value c; continue }
          if (c == quote) { literal(value); quote = ""; value = "" }
          else value = value c
          continue
        }
        if (c ~ /[A-Za-z0-9_]/) { word = word c; line = line c; continue }
        if (word != "") { token(word); word = "" }
        if (pair == "//") {
          reason = (substr($0, i) ~ /^\/\/vws:unseeded[[:space:]]+[^[:space:]]/)
          if (FNR <= 20 && reason) filemark = 1
          break
        }
        if (pair == "/*") { comment = 1; i++; line = line " "; continue }
        if (c == "\"" || c == "`" || c == sprintf("%c", 39)) {
          quote = c; value = ""; line = line " "; continue
        }
        if (c !~ /[[:space:]]/) token(c)
        line = line c
      }
      if (word != "") token(word)
      if (line ~ /(^|[^A-Za-z0-9_.])cryptotest\.SetGlobalRandom\(/) seeded = 1
      if (!reason && (index(line, "time.Now().UnixNano()") || index(line, "time.Now().Unix()")))
        flag(4, "time-derived seed or id; use a fixed value")
      if (v2 && !reason) {
        s = line
        while (match(s, /(^|[^A-Za-z0-9_.])rand\.[A-Za-z0-9_]+\(/)) {
          tok = substr(s, RSTART, RLENGTH)
          s = substr(s, RSTART + RLENGTH)
          sub(/^[^r]*rand\./, "", tok); sub(/\($/, "", tok)
          if (!(tok in ok)) flag(2, "rand." tok " uses the unseeded global source")
        }
      }
    }
    END {
      if (cr && !seeded && !filemark) { printf "%s:%d: rule 3: crypto/rand without cryptotest.SetGlobalRandom\n", F, crline; bad = 1 }
      exit bad
    }' "$dir/$f" || true)
  out+=$'\n'
done <<<"$files"

out=$(grep -v '^$' <<<"$out" || true)
if [[ -n $out ]]; then
  echo "$out" >&2
  echo "check-test-rand: unseeded randomness in tests; see AGENTS.md rule L9" >&2
  exit 1
fi
echo "check-test-rand ok"

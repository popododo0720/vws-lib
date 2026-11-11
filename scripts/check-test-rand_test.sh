#!/usr/bin/env bash
# Tests for scripts/check-test-rand.sh: it passes on clean and properly marked test files and fails,
# naming the rule, on one planted input per rule. Each case runs on a throwaway git repo in a temp dir.
# Run: scripts/check-test-rand_test.sh
set -euo pipefail
export LC_ALL=C
root=$(cd "$(dirname "$0")/.." && pwd)
script=$root/scripts/check-test-rand.sh
[[ -f $script ]] || { echo "check-test-rand_test: $script is missing" >&2; exit 1; }
tmp=$(mktemp -d "${TMPDIR:-$root}/check-test-rand.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
fails=0

# run NAME WANT RULE: the fixture is read from stdin. WANT is pass or fail; RULE is the rule a failure names.
run() {
  local name=$1 want=$2 rule=${3:-} d="$tmp/$1" rc=0
  mkdir -p "$d"
  git -C "$d" init -q
  cat >"$d/x_test.go"
  git -C "$d" add x_test.go
  bash "$script" "$d" >"$tmp/out" 2>&1 || rc=$?
  local ok=0
  if [[ $want == pass ]]; then
    [[ $rc -eq 0 ]] && grep -q '^check-test-rand ok$' "$tmp/out" && ok=1
  else
    [[ $rc -eq 1 ]] && grep -q "^x_test.go:[0-9]*: rule ${rule}: " "$tmp/out" && ok=1
  fi
  if [[ $ok -eq 1 ]]; then
    echo "ok: ${name} (${want})"
  else
    echo "FAIL: ${name}: want ${want} ${rule}, exit ${rc}"
    sed 's/^/    /' "$tmp/out"
    fails=$((fails + 1))
  fi
}

run clean pass <<'EOF'
package x

import (
	"math/rand/v2"
	"testing"
)

func TestX(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	_ = r.IntN(5)
}
EOF

run seeded-crypto pass <<'EOF'
package x

import (
	"crypto/rand"
	"testing"
	"testing/cryptotest"
)

func TestX(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 1)
	_, _ = rand.Read(nil)
}
EOF

run file-marker pass <<'EOF'
package x

//vws:unseeded proves production key material is unpredictable

import "crypto/rand"

var _ = rand.Reader
EOF

run line-marker pass <<'EOF'
package x

import (
	"math/rand/v2"
	"time"
)

var _ = rand.IntN(5) //vws:unseeded jitter under test, value not asserted
var _ = time.Now().UnixNano() //vws:unseeded measures elapsed time
EOF

run rule1 fail 1 <<'EOF'
package x

import "math/rand"

var _ = rand.Intn(5)
EOF

run rule2 fail 2 <<'EOF'
package x

import "math/rand/v2"

var _ = rand.IntN(5)
EOF

run rule3 fail 3 <<'EOF'
package x

import "crypto/rand"

var _ = rand.Reader
EOF

run rule4 fail 4 <<'EOF'
package x

import "time"

var _ = time.Now().UnixNano()
EOF

run rule4-unix fail 4 <<'EOF'
package x

import "time"

var _ = time.Now().Unix()
EOF

run rule5 fail 5 <<'EOF'
package x

import mrand "math/rand/v2"

var _ = mrand.NewPCG(1, 2)
EOF

run rule5-crypto fail 5 <<'EOF'
package x

import (
	cr "crypto/rand"
	"testing/cryptotest"
)

var _ = cryptotest.SetGlobalRandom
var _ = cr.Reader
EOF

run file-marker-no-reason fail 3 <<'EOF'
package x

//vws:unseeded

import "crypto/rand"

var _ = rand.Reader
EOF

run line-marker-no-reason fail 4 <<'EOF'
package x

import "time"

var _ = time.Now().UnixNano() //vws:unseeded
EOF

run file-marker-spaces fail 3 < <(printf 'package x\n//vws:unseeded   \nimport "crypto/rand"\n')
run line-marker-spaces fail 4 < <(printf 'package x\nvar _ = time.Now().Unix() //vws:unseeded   \n')

run constructors pass <<'EOF'
package x
import rand "math/rand/v2"
var _, _, _, _ = rand.New(nil), rand.NewPCG(1, 2), rand.NewChaCha8([32]byte{}), rand.NewZipf(nil, 1, 2, 3)
EOF

run commented-call pass <<'EOF'
package x
import "math/rand/v2"
var _ = rand.NewPCG(1, 2)
// rand.IntN(5)
/* rand.IntN(5)
time.Now().UnixNano()
*/
EOF

run string-call pass <<'EOF'
package x
import "math/rand/v2"
var _ = rand.NewPCG(1, 2)
var _ = "rand.IntN(5) time.Now().Unix() \" //"
var _ = `rand.IntN(5)
time.Now().UnixNano()`
EOF

run commented-import pass <<'EOF'
package x
/*
import "math/rand"
*/
// import r "crypto/rand"
EOF

run raw-string-import pass <<'EOF'
package x
var _ = `
import "math/rand"
import r "crypto/rand"
`
EOF

run unrelated-seed-identifier fail 3 <<'EOF'
package x
import "crypto/rand"
var notcryptotest = struct { SetGlobalRandom func() }{func(){}}
func init() { notcryptotest.SetGlobalRandom() }
var _ = rand.Reader
EOF

run qualified-seed-identifier fail 3 <<'EOF'
package x
import "crypto/rand"
var other = struct { cryptotest struct { SetGlobalRandom func() } }{}
func example() { other.cryptotest.SetGlobalRandom() }
var _ = rand.Reader
EOF

run trailing-file-marker pass <<'EOF'
package x //vws:unseeded production entropy required
import "crypto/rand"
var _ = rand.Reader
EOF

run fake-crypto-call-comment fail 3 <<'EOF'
package x
import "crypto/rand"
// cryptotest.SetGlobalRandom(t, 1)
/* cryptotest.SetGlobalRandom(t, 1) */
var _ = rand.Reader
EOF

run fake-crypto-call-string fail 3 <<'EOF'
package x
import "crypto/rand"
var _ = "cryptotest.SetGlobalRandom(t, 1)"
var _ = `cryptotest.SetGlobalRandom(t, 1)`
var _ = rand.Reader
EOF

run fake-file-marker-string fail 3 <<'EOF'
package x
import "crypto/rand"
var _ = "//vws:unseeded example text"
var _ = `//vws:unseeded example text`
var _ = rand.Reader
EOF

run fake-line-marker-string fail 2 <<'EOF'
package x
import "math/rand/v2"
var example, n = "//vws:unseeded example text", rand.IntN(5)
EOF

run fake-marker-block-comment fail 4 <<'EOF'
package x
/* //vws:unseeded example text */
var _ = time.Now().Unix() /* //vws:unseeded example text */
EOF

run late-file-marker fail 3 <<'EOF'
package x



















//vws:unseeded too late
import "crypto/rand"
var _ = rand.Reader
EOF

run compact-import fail 2 <<'EOF'
package x
import ("math/rand/v2")
var _ = rand.IntN(5)
EOF

run compact-crypto-import fail 3 <<'EOF'
package x
import ("testing"; "crypto/rand")
var _ = rand.Reader
EOF

run compact-multiple-imports fail 2 <<'EOF'
package x
import ("testing"; "math/rand/v2"; "time")
var _ = rand.IntN(5)
EOF

run compact-alias fail 5 <<'EOF'
package x
import ("testing"; r "math/rand/v2")
var _ = r.NewPCG(1, 2)
EOF

run compact-own-name pass <<'EOF'
package x
import ("testing"; rand "math/rand/v2")
var _ = rand.NewPCG(1, 2)
EOF

mkdir -p "$tmp/vendor/vendor/p"
git -C "$tmp/vendor" init -q
printf 'package x\nimport "math/rand"\n' >"$tmp/vendor/vendor/p/x_test.go"
git -C "$tmp/vendor" add vendor/p/x_test.go
if ! bash "$script" "$tmp/vendor"; then
  echo "FAIL: tracked vendor file"
  fails=$((fails + 1))
fi

mkdir -p "$tmp/missing/scripts"
cp "$0" "$tmp/missing/scripts/check-test-rand_test.sh"
if bash "$tmp/missing/scripts/check-test-rand_test.sh" >"$tmp/out" 2>&1; then
  echo "FAIL: missing script was accepted"
  fails=$((fails + 1))
elif ! grep -q 'is missing' "$tmp/out"; then
  echo "FAIL: missing script diagnostic"
  fails=$((fails + 1))
fi

if [[ $fails -ne 0 ]]; then
  echo "check-test-rand_test: ${fails} case(s) failed" >&2
  exit 1
fi
echo "check-test-rand_test: all cases passed"

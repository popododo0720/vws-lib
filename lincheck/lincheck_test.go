package lincheck_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/popododo0720/vws-lib/lincheck"
)

type regIn struct {
	key   string
	write bool
	value int
}

type regOut struct {
	value int
	err   error
}

var errLost = errors.New("connection lost")

// register is a set of integer registers, one per key, starting at 0. A write
// that fails may or may not have applied.
var register = lincheck.Model[int, regIn, regOut]{
	Init: func() int { return 0 },
	Step: func(s int, in regIn, out regOut) []int {
		switch {
		case in.write && out.err != nil:
			return []int{s, in.value}
		case in.write:
			return []int{in.value}
		case out.err != nil:
			return []int{s}
		case out.value == s:
			return []int{s}
		default:
			return nil
		}
	},
	Key: func(in regIn) string { return in.key },
}

const timeout = 10 * time.Second

func op(client int, in regIn, out regOut, call, ret int64) lincheck.Op[regIn, regOut] {
	return lincheck.Op[regIn, regOut]{Client: client, In: in, Out: out, Call: call, Return: ret}
}

func w(v int) regIn            { return regIn{key: "x", write: true, value: v} }
func r() regIn                 { return regIn{key: "x"} }
func got(v int) regOut         { return regOut{value: v} }
func failed() regOut           { return regOut{err: errLost} }
func wk(k string, v int) regIn { return regIn{key: k, write: true, value: v} }

func TestHistories(t *testing.T) {
	cases := []struct {
		name    string
		history []lincheck.Op[regIn, regOut]
		want    error
	}{
		{"Empty", nil, nil},
		{"ReadDuringWriteSeesEitherValue", []lincheck.Op[regIn, regOut]{
			op(0, w(1), got(0), 0, 10),
			op(1, r(), got(0), 1, 2),
			op(1, r(), got(1), 3, 4),
		}, nil},
		{"StaleReadAfterNewerRead", []lincheck.Op[regIn, regOut]{
			op(0, w(1), got(0), 0, 10),
			op(1, r(), got(1), 1, 2),
			op(1, r(), got(0), 3, 4),
		}, lincheck.ErrNotLinearizable},
		{"StaleReadAfterWriteReturned", []lincheck.Op[regIn, regOut]{
			op(0, w(1), got(0), 0, 1),
			op(1, r(), got(0), 2, 3),
		}, lincheck.ErrNotLinearizable},
		{"TouchingIntervalsAreConcurrent", []lincheck.Op[regIn, regOut]{
			op(0, w(1), got(0), 0, 2),
			op(1, r(), got(0), 2, 3),
		}, nil},
		{"KeysAreIndependent", []lincheck.Op[regIn, regOut]{
			op(0, wk("a", 1), got(0), 0, 1),
			op(1, regIn{key: "b"}, got(0), 2, 3),
		}, nil},
		{"StaleReadOnOneKeyFails", []lincheck.Op[regIn, regOut]{
			op(0, wk("a", 1), got(0), 0, 1),
			op(1, regIn{key: "b"}, got(0), 2, 3),
			op(1, regIn{key: "a"}, got(0), 4, 5),
		}, lincheck.ErrNotLinearizable},
		{"WriteCutByRestartMayHaveApplied", []lincheck.Op[regIn, regOut]{
			op(0, w(1), failed(), 0, 1),
			op(1, r(), got(1), 2, 3),
		}, nil},
		{"WriteCutByRestartMayNotHaveApplied", []lincheck.Op[regIn, regOut]{
			op(0, w(1), failed(), 0, 1),
			op(1, r(), got(0), 2, 3),
		}, nil},
		{"WriteCutByRestartCannotInventAValue", []lincheck.Op[regIn, regOut]{
			op(0, w(1), failed(), 0, 1),
			op(1, r(), got(2), 2, 3),
		}, lincheck.ErrNotLinearizable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for range 2 {
				if err := lincheck.Check(register, c.history, timeout); !errors.Is(err, c.want) {
					t.Fatalf("Check = %v, want %v", err, c.want)
				}
			}
		})
	}
}

// lockedRegister is a correct register.
type lockedRegister struct {
	mu sync.Mutex
	v  map[string]int
}

func (l *lockedRegister) apply(in regIn) regOut {
	l.mu.Lock()
	defer l.mu.Unlock()
	if in.write {
		l.v[in.key] = in.value
		return regOut{}
	}
	return regOut{value: l.v[in.key]}
}

func TestRecordedConcurrentHistoryOfACorrectRegisterPasses(t *testing.T) {
	reg := &lockedRegister{v: map[string]int{}}
	var rec lincheck.Recorder[regIn, regOut]
	var wg sync.WaitGroup
	for c := range 8 {
		wg.Go(func() {
			for i := range 50 {
				in := regIn{key: []string{"a", "b"}[i%2]}
				if i%3 == 0 {
					in = regIn{key: in.key, write: true, value: c*100 + i}
				}
				rec.Do(c, in, reg.apply)
			}
		})
	}
	wg.Wait()
	h := rec.History()
	if len(h) != 400 {
		t.Fatalf("recorded %d operations, want 400", len(h))
	}
	if err := lincheck.Check(register, h, timeout); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

func TestRecordedHistoryOfAStaleReplicaFails(t *testing.T) {
	primary := &lockedRegister{v: map[string]int{}}
	replica := &lockedRegister{v: map[string]int{}}
	var rec lincheck.Recorder[regIn, regOut]
	rec.Do(0, w(1), primary.apply)
	if out := rec.Do(1, r(), replica.apply); out.value != 0 {
		t.Fatalf("replica read %d, want the stale 0", out.value)
	}
	if err := lincheck.Check(register, rec.History(), timeout); !errors.Is(err, lincheck.ErrNotLinearizable) {
		t.Fatalf("Check = %v, want ErrNotLinearizable", err)
	}
}

func TestHistoryIsACopy(t *testing.T) {
	var rec lincheck.Recorder[regIn, regOut]
	rec.Do(0, w(1), func(regIn) regOut { return regOut{} })
	h := rec.History()
	h[0].In.value = 9
	if got := rec.History()[0].In.value; got != 1 {
		t.Fatalf("recorded value changed to %d through a returned history", got)
	}
}

func TestTimeout(t *testing.T) {
	slow := register
	slow.Step = func(s int, in regIn, out regOut) []int {
		time.Sleep(50 * time.Millisecond)
		return register.Step(s, in, out)
	}
	h := []lincheck.Op[regIn, regOut]{op(0, w(1), got(0), 0, 1), op(1, r(), got(1), 2, 3)}
	if err := lincheck.Check(slow, h, time.Millisecond); !errors.Is(err, lincheck.ErrTimeout) {
		t.Fatalf("Check = %v, want ErrTimeout", err)
	}
}

func TestInvalidInputIsAnError(t *testing.T) {
	ok := []lincheck.Op[regIn, regOut]{op(0, r(), got(0), 0, 1)}
	noStep := register
	noStep.Step = nil
	noInit := register
	noInit.Init = nil
	cases := []struct {
		name    string
		m       lincheck.Model[int, regIn, regOut]
		history []lincheck.Op[regIn, regOut]
		timeout time.Duration
	}{
		{"NoStep", noStep, ok, timeout},
		{"NoInit", noInit, ok, timeout},
		{"ZeroTimeout", register, ok, 0},
		{"NegativeTimeout", register, ok, -time.Second},
		{"ReturnBeforeCall", register, []lincheck.Op[regIn, regOut]{op(0, r(), got(0), 5, 4)}, timeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := lincheck.Check(c.m, c.history, c.timeout)
			if err == nil || errors.Is(err, lincheck.ErrNotLinearizable) || errors.Is(err, lincheck.ErrTimeout) {
				t.Fatalf("Check = %v, want an input error", err)
			}
		})
	}
}

func TestNilInterfaceOutput(t *testing.T) {
	m := lincheck.Model[bool, int, error]{
		Init: func() bool { return false },
		Step: func(_ bool, _ int, err error) []bool {
			if err != nil {
				return nil
			}
			return []bool{true}
		},
	}
	var rec lincheck.Recorder[int, error]
	if err := rec.Do(0, 1, func(int) error { return nil }); err != nil {
		t.Fatalf("Do = %v, want nil", err)
	}
	if err := lincheck.Check(m, rec.History(), timeout); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if err := rec.Do(0, 2, func(int) error { return errLost }); !errors.Is(err, errLost) {
		t.Fatalf("Do = %v, want errLost", err)
	}
	if err := lincheck.Check(m, rec.History(), timeout); !errors.Is(err, lincheck.ErrNotLinearizable) {
		t.Fatalf("Check = %v, want ErrNotLinearizable", err)
	}
}

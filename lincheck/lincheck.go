// Package lincheck checks that a concurrent history of operations is
// linearizable against a sequential model. It records each operation's call
// and return on a logical clock and hands the history to porcupine.
package lincheck

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anishathalye/porcupine"
)

// Model is the sequential specification a history is checked against.
// S is comparable so states can be deduplicated with ==.
type Model[S comparable, I, O any] struct {
	// Init returns the state before any operation.
	Init func() S
	// Step returns every state the model can be in after in returned out
	// from state s. An empty result means out is not a legal answer. An
	// operation whose effect is unknown, such as a write cut off by a
	// restart, returns both the unchanged and the applied state.
	Step func(s S, in I, out O) []S
	// Key, when set, partitions the history: operations on different keys
	// are checked independently, each from Init.
	Key func(in I) string
}

// Op is one completed operation. Call and Return are logical timestamps:
// an operation that returned before another was called has a smaller Return
// than the other's Call.
type Op[I, O any] struct {
	Client       int
	In           I
	Out          O
	Call, Return int64
}

// Recorder collects a history from concurrent clients. The zero value is
// ready to use.
type Recorder[I, O any] struct {
	clock atomic.Int64
	mu    sync.Mutex
	ops   []Op[I, O]
}

// Do runs f(in) for client and records it. It returns f's output.
func (r *Recorder[I, O]) Do(client int, in I, f func(I) O) O {
	call := r.clock.Add(1)
	out := f(in)
	ret := r.clock.Add(1)
	r.mu.Lock()
	r.ops = append(r.ops, Op[I, O]{Client: client, In: in, Out: out, Call: call, Return: ret})
	r.mu.Unlock()
	return out
}

// History returns a copy of the operations recorded so far.
func (r *Recorder[I, O]) History() []Op[I, O] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ops)
}

var (
	// ErrNotLinearizable means no sequential order of the history is legal
	// under the model.
	ErrNotLinearizable = errors.New("lincheck: history is not linearizable")
	// ErrTimeout means the check ran out of time before reaching a verdict.
	ErrTimeout = errors.New("lincheck: check timed out")
)

// Check reports whether history is linearizable under m. It returns nil,
// ErrNotLinearizable, ErrTimeout, or an error for an invalid model, timeout
// or history.
func Check[S comparable, I, O any](m Model[S, I, O], history []Op[I, O], timeout time.Duration) error {
	if m.Init == nil || m.Step == nil {
		return errors.New("lincheck: model needs Init and Step")
	}
	if timeout <= 0 {
		return fmt.Errorf("lincheck: timeout %v must be positive", timeout)
	}
	ops := make([]porcupine.Operation, len(history))
	for i, op := range history {
		if op.Call > op.Return {
			return fmt.Errorf("lincheck: operation %d returns at %d before its call at %d", i, op.Return, op.Call)
		}
		ops[i] = porcupine.Operation{ClientId: op.Client, Input: op.In, Output: op.Out, Call: op.Call, Return: op.Return}
	}
	nm := porcupine.NondeterministicModel{
		Init: func() []any { return []any{m.Init()} },
		Step: func(s, in, out any) []any {
			var next []any
			for _, n := range m.Step(as[S](s), as[I](in), as[O](out)) {
				next = append(next, n)
			}
			return next
		},
	}
	if m.Key != nil {
		nm.Partition = func(h []porcupine.Operation) [][]porcupine.Operation {
			var keys []string
			byKey := map[string][]porcupine.Operation{}
			for _, op := range h {
				k := m.Key(as[I](op.Input))
				if _, ok := byKey[k]; !ok {
					keys = append(keys, k)
				}
				byKey[k] = append(byKey[k], op)
			}
			parts := make([][]porcupine.Operation, len(keys))
			for i, k := range keys {
				parts[i] = byKey[k]
			}
			return parts
		}
	}
	switch res := porcupine.CheckOperationsTimeout(nm.ToModel(), ops, timeout); res {
	case porcupine.Ok:
		return nil
	case porcupine.Illegal:
		return fmt.Errorf("%w: %d operations", ErrNotLinearizable, len(ops))
	case porcupine.Unknown:
		return fmt.Errorf("%w after %v: %d operations", ErrTimeout, timeout, len(ops))
	default:
		return fmt.Errorf("lincheck: unexpected porcupine result %q", res)
	}
}

// as converts v back to T. A nil interface value, such as a nil error output,
// fails the assertion and is T's zero value.
func as[T any](v any) T {
	if t, ok := v.(T); ok {
		return t
	}
	var zero T
	return zero
}

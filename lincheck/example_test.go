package lincheck_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/popododo0720/vws-lib/lincheck"
)

var errConflict = errors.New("revision conflict")

// memStore stands in for the store under test.
type memStore struct {
	mu   sync.Mutex
	objs map[string]object
}

func (m *memStore) Get(_ context.Context, name string) (string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.objs[name]
	return o.value, o.rev, nil
}

func (m *memStore) Put(_ context.Context, name, value string, expect int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.objs[name]
	if o.rev != expect {
		return 0, errConflict
	}
	m.objs[name] = object{value: value, rev: o.rev + 1}
	return o.rev + 1, nil
}

type storeIn struct {
	name   string
	put    bool
	value  string
	expect int64
}

type storeOut struct {
	value string
	rev   int64
	err   error
}

type object struct {
	value string
	rev   int64
}

// objects is the sequential model: one object per name, written with an
// expected revision.
var objects = lincheck.Model[object, storeIn, storeOut]{
	Init: func() object { return object{} },
	Step: func(s object, in storeIn, out storeOut) []object {
		next := object{value: in.value, rev: s.rev + 1}
		switch {
		case !in.put && out.err == nil:
			if out.value == s.value && out.rev == s.rev {
				return []object{s}
			}
			return nil
		case !in.put:
			return []object{s}
		case errors.Is(out.err, errConflict):
			if in.expect != s.rev {
				return []object{s}
			}
			return nil
		case out.err != nil:
			if in.expect == s.rev {
				return []object{s, next}
			}
			return []object{s}
		case in.expect == s.rev && out.rev == next.rev:
			return []object{next}
		default:
			return nil
		}
	},
	Key: func(in storeIn) string { return in.name },
}

func ExampleCheck() {
	s := &memStore{objs: map[string]object{}}
	var rec lincheck.Recorder[storeIn, storeOut]
	do := func(in storeIn) storeOut {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !in.put {
			v, rev, err := s.Get(ctx, in.name)
			return storeOut{value: v, rev: rev, err: err}
		}
		rev, err := s.Put(ctx, in.name, in.value, in.expect)
		return storeOut{rev: rev, err: err}
	}
	var wg sync.WaitGroup
	for c := range 8 {
		wg.Go(func() {
			for n := range 50 {
				name := fmt.Sprintf("obj-%d", n%4)
				got := rec.Do(c, storeIn{name: name}, do)
				rec.Do(c, storeIn{name: name, put: true, value: fmt.Sprintf("%d-%d", c, n), expect: got.rev}, do)
			}
		})
	}
	wg.Wait()
	fmt.Println(lincheck.Check(objects, rec.History(), time.Minute))
	// Output: <nil>
}

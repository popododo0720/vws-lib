# Linearizability checks with lincheck

`lincheck` checks that a store behaves like its sequential reference under concurrency. Run many goroutines against the real store, record every call and its result, then ask porcupine (v1.3.1) whether some sequential order of those operations is legal under a model. The model is usually a few lines that mirror the memstore.

Use it from a test only. Linearizability checking is NP-complete. Partition by key and keep each key's history to a few hundred operations.

## The pieces

| Name | What it is |
| --- | --- |
| `Model[S, I, O]` | `Init` gives the empty state. `Step(s, in, out)` returns every state the store can be in after `in` returned `out` from `s`. It returns none when `out` is wrong. `Key`, when set, checks each key on its own. `S` must be comparable. Use a struct of values, not a map or a pointer. |
| `Recorder[I, O]` | `Do(client, in, f)` runs `f(in)` and records it on a logical clock. It is safe for concurrent use. `History()` returns a copy. |
| `Check(m, history, timeout)` | Returns nil, `ErrNotLinearizable`, `ErrTimeout`, or an error for a bad model, timeout or history. |

## Wire a service store

`lincheck/example_test.go` is the template. It checks a versioned object store with `Get(name)` and `Put(name, value, expectRevision)`, the shape the compute, storage, network and iam stores share, and it runs with `go test ./lincheck`. To use it in a service:

1. Copy the model (`objects`) and replace its input, output and state types with the service's own. The model is the memstore's behavior for one object.
2. Open the kube store with the same open function the `storetest` suite gets, and replace `memStore` in `do` with it. Give each call its own context deadline.
3. Start several goroutines that call `rec.Do` with the operations the `storetest` cases use, then call `lincheck.Check` on `rec.History()` and fail the test on any error.

Rules that keep the check honest:

- Record the error in `O` and let `Step` decide. An error that proves nothing happened (a conflict, not found) returns the unchanged state. An error with an unknown outcome (a restart, a deadline) returns both the unchanged and the applied state.
- Record every operation a client started. Dropping a failed one hides the case the check exists for.
- To inject a restart, stop and reopen the store between `Do` calls from another goroutine. Operations in flight then fail with an unknown outcome, and the model above already allows both results.
- Pass a timeout. `ErrTimeout` is not a pass. Shrink the history or partition it more finely.

`lincheck/lincheck_test.go` has a register model with each of these cases, including a write cut off by a restart.

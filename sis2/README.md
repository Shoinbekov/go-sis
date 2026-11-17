# SIS Project — Text Analyzer

**Checkpoint 2: Concurrent Processing** · Golang Application Development

An in-memory Go application that processes **text-analysis tasks**.
Each task takes a piece of text, validates it, analyzes it and stores the
statistics as the task result.

Checkpoint 1 processed tasks one by one in the calling goroutine.
**Checkpoint 2** adds a **worker pool**: tasks are submitted to a **bounded queue**
and processed **concurrently** by a fixed number of worker goroutines.
All Checkpoint 1 behavior (creation, validation, IDs, storage, retrieval,
synchronous `Process`, lifecycle, errors, tests) still works.

```
Input:   "Hello Go world"
Process: analyze the text
Result:  characters 14, words 3, unique words 3, sentences 1, lines 1,
         average word length 4.00, longest word "Hello", top words …
```

## Domain

| Part | Description |
|---|---|
| **Input** | a text (`string`), any language — counting works on Unicode runes |
| **Validation** | text must be valid UTF-8, must not be empty / whitespace-only, must contain at least one word |
| **Processing** | count characters (runes) and bytes, words, unique words (case-insensitive), sentences, lines, average word length, longest word, top-N most frequent words |
| **Result** | `textanalysis.Result`, stored inside the task once it is completed |

## Concurrency architecture

```
 svc.Create(text) ─► storage: Task{Pending}
                                         
 pool.Submit(id) ─┬─ pool stopped?  → ErrPoolStopped
                  ├─ queue full?    → ErrQueueFull   (task stays Pending)
                  ├─ Pending → Queued (atomic, in storage) else ErrInvalidTransition
                  └─ send id ─► ┌──────────────────────────┐
                                │ queue: chan task.ID       │  buffered, capacity N
                                └──────────────────────────┘
                                   │         │         │
                                Worker 1  Worker 2  Worker 3   (fixed number of goroutines)
                                   │         │         │
                                   ▼         ▼         ▼
                         Service.ProcessQueued(id):
                           1. Queued → Processing   (atomic, under lock)
                           2. Analyze(text)         (NO lock held)
                           3. → Completed + Result  (atomic, under lock)
                              or Failed + Error
                                   │
                                   ▼
                      storage.Memory  (map + sync.RWMutex)
```

| Setting | Value |
|---|---|
| Workers | **3** by default (`-workers N`), minimum 1, the pool is `worker.NewPool(svc, workers, capacity)` |
| Queue capacity | **10** by default (`-queue N`) — a buffered channel `make(chan task.ID, capacity)`, it never grows |
| Full queue | **Rejected**: `Submit` returns `worker.ErrQueueFull` immediately. The task is not changed and stays `Pending`, so it can be submitted again later. |
| Submission | `Submit` does not wait for processing: it puts the ID into the queue and returns. |

### How tasks move

1. `svc.Create(text)` validates the text and stores a `Pending` task.
2. `pool.Submit(id)` moves it to `Queued` and puts its **ID** into the queue.
   Only IDs travel through the channel; the task data stays in the storage.
3. A free worker receives the ID from the channel (`for id := range queue`),
   moves the task to `Processing`, analyzes the text and saves the result
   (`Completed`) or the error (`Failed`).
4. The worker then takes the next ID — workers are reused, not one goroutine per task.

Processing order is **not guaranteed**: a task submitted later can finish first.
Nothing in the application or the tests depends on the order.

## Task lifecycle

```
           Submit             worker takes it           success
 Pending ─────────► Queued ─────────────────► Processing ─────────► Completed
    │                                          ▲     │
    └──── Process (synchronous, Checkpoint 1) ─┘     │ error
                                                     └────────────► Failed
```

| State | Meaning |
|---|---|
| `Pending` | created and validated, not submitted (initial state, zero value of `Status`) |
| `Queued` | accepted by the worker pool, waiting in the queue |
| `Processing` | a worker is analyzing it |
| `Completed` | processed successfully, `Result` is set — **final** |
| `Failed` | the processor returned an error, `Error` holds the reason — **final** |

**Allowed transitions:** `Pending → Queued`, `Queued → Processing`,
`Pending → Processing` (synchronous `Process`), `Processing → Completed`,
`Processing → Failed`. Everything else returns `task.ErrInvalidTransition`
and leaves the task unchanged.

So submitting a task that is already `Queued`, `Processing`, `Completed` or
`Failed` returns an error — **a task cannot be submitted or processed twice**.
The synchronous `Process` only accepts `Pending`, and workers only accept
`Queued`, so they never take the same task.
Retries are not supported: `Completed` and `Failed` are final.

## Synchronization — what is shared and how it is protected

| Shared data | Used by | Protection |
|---|---|---|
| `map[task.ID]task.Task` and `lastID` in `storage.Memory` | workers, main goroutine, any caller of `Get`/`List` | `sync.RWMutex`: `Lock` for `Add`/`Update`/`Modify`, `RLock` for `Get`/`List` (many readers at once) |
| Fields of each task (status, result) | workers and readers | the same mutex: tasks are stored as **values** and copied in and out with `Clone`, nobody outside holds a pointer into the map, so a task changes only under the lock |
| `closed` flag and the queue channel | `Submit`, `Shutdown` | `sync.Mutex` in `Pool` |
| running workers | `Shutdown` | `sync.WaitGroup` |

**Atomic state changes.** A state change is a *read → check → write* sequence.
Done as separate `Get` and `Update`, two workers could both read `Queued` and
both start the same task (check-then-act race). So every state change goes
through `Memory.Modify(id, fn)`, which does read, check and write under **one
lock**. Of two concurrent `Submit` calls for the same task, exactly one sees
`Pending`.

**No lock during processing.** `Analyze` runs between two short `Modify`
calls without holding any lock, so other tasks can be read and updated while a
task is being analyzed. `textanalysis.Analyzer` has no shared state, so many
workers can call it at the same time.

**No deadlocks.** There are only two locks and they are never held in a cycle:
`Submit` holds the pool mutex and calls storage (pool → storage); workers
never take the pool mutex. `Shutdown` releases the pool mutex **before**
`wg.Wait()`. The send in `Submit` cannot block, because the free space is
checked under the same mutex and only `Submit` adds to the queue.

## Stopping the workers

```
pool.Shutdown()
  1. under the mutex: closed = true; close(queue)   → new Submit gets ErrPoolStopped
  2. workers keep reading: `range` over a closed channel still returns
     every buffered ID, so all accepted tasks are processed
  3. when the queue is empty, `range` ends, each worker calls wg.Done()
  4. wg.Wait() returns when all workers have exited
```

- **Channel ownership:** the `Pool` owns the queue. Only `Submit` sends and
  only `Shutdown` closes, both under the same mutex, so there is never a send
  on a closed channel (no panic), even when `Submit` and `Shutdown` run at the
  same time.
- **Accepted tasks are not lost:** every task that got `nil` from `Submit` ends
  as `Completed` or `Failed`. Rejected tasks stay `Pending`.
- **Calling `Shutdown` again** (also concurrently) is a safe no-op: the channel
  is closed once, and every call returns after the workers have finished.
- Context cancellation and interrupting running tasks are Checkpoint 3.

## Errors

| Error | When |
|---|---|
| `task.ErrInvalidInput` | text failed validation (empty, no words, invalid UTF-8) |
| `task.ErrNotFound` | no task with this ID |
| `task.ErrInvalidTransition` | operation not allowed in the current state (e.g. duplicate submit) |
| `task.ErrProcessingFailed` | the analyzer returned an error (task becomes `Failed`) |
| `worker.ErrQueueFull` | the queue has no free place |
| `worker.ErrPoolStopped` | `Submit` after `Shutdown` |
| `worker.ErrInvalidConfig` | `NewPool` with workers < 1 or capacity < 1 |

All errors are returned values checked with `errors.Is`. No `panic` for application errors.

## Project structure

```
sis1/
├── go.mod
├── cmd/sis/main.go                  demo: worker pool, submit, shutdown, results
└── internal/
    ├── textanalysis/                domain logic: validation + statistics (pure, no state)
    │   ├── analyzer.go
    │   └── analyzer_test.go
    ├── task/                        Task model, lifecycle, interfaces, service
    │   ├── task.go                  Task, ID, errors, Enqueue/Start/Complete/Fail, Clone
    │   ├── status.go                Status (Pending, Queued, Processing, Completed, Failed), transitions
    │   ├── service.go               Processor & Repository interfaces; Create, Get, List,
    │   │                            Process, MarkQueued, ProcessQueued
    │   ├── task_test.go
    │   └── service_test.go
    ├── storage/                     thread-safe in-memory Repository
    │   ├── memory.go                map + sync.RWMutex, atomic Modify
    │   ├── memory_test.go
    │   └── memory_concurrent_test.go
    └── worker/                      worker pool (Checkpoint 2)
        ├── pool.go                  NewPool, Submit, Shutdown, bounded queue
        ├── pool_test.go             startup, async submit, results, overlap, limits
        └── pool_submit_test.go      duplicates, full queue, concurrent access, shutdown
```

## Running

Requires **Go 1.27**.

```bash
go run ./cmd/sis                                    # built-in demo
go run ./cmd/sis "Your text here." "Another one!"   # analyze your own texts
go run ./cmd/sis -workers 4 -queue 20 "some text"   # change pool settings
```

## Testing

```bash
gofmt -l .           # must print nothing
go vet ./...
go test ./...
go test -race ./...  # race detector
go test -v ./internal/worker
```

> On Windows `-race` needs cgo and a C compiler (gcc, e.g. MSYS2/MinGW-w64)
> and `CGO_ENABLED=1`.

**Checkpoint 1 tests** (all still pass): creation, invalid input (table-driven),
retrieval, nonexistent task, processing, result correctness (incl. Cyrillic),
state after processing, invalid transitions (table-driven), processing failure,
multiple independent tasks, protection of stored data through copies.

**Checkpoint 2 tests:**

| Requirement | Test |
|---|---|
| pool startup, worker termination | `TestNewPoolStartsAndStopsWorkers` (3 tasks run at once; goroutines return to baseline after `Shutdown`), `TestNewPoolRejectsInvalidConfig` |
| asynchronous submission | `TestSubmitDoesNotWaitForProcessing` — `Submit` returns while the processor is still blocked |
| many tasks, correct results and states | `TestProcessManyTasksWithCorrectResults` — 100 tasks, each with its own expected word count |
| **real overlap** | `TestTwoTasksProcessedConcurrently` — both tasks are inside `Analyze` at the same time before either is allowed to finish |
| concurrency limit | `TestPoolRespectsWorkerLimit` — with 3 busy workers exactly 7 of 10 tasks stay in the queue; max concurrent = 3 |
| failed processing | `TestFailedProcessingMarksTaskFailed` |
| duplicate / invalid submissions | `TestSubmitDuplicateWhileQueuedOrProcessing`, `TestSubmitFinishedOrUnknownTask`, `TestConcurrentSubmitOfSameTask` (50 goroutines, exactly 1 accepted, analyzed once) |
| full queue | `TestSubmitRejectsWhenQueueIsFull` — `ErrQueueFull`, task stays `Pending`, can be resubmitted |
| safe concurrent access | `TestConcurrentReadsDuringProcessing`, `TestMemoryConcurrentAddUniqueIDs`, `TestMemoryModifyIsAtomic` |
| shutdown | `TestShutdownFinishesAcceptedTasks`, `TestSubmitAfterShutdownIsRejected`, `TestShutdownTwiceIsSafe`, `TestConcurrentSubmitAndShutdown` (no panic, no lost tasks, 20 rounds) |

Tests control timing with channels (a fake processor that reports "started"
and waits for "release"), not with `time.Sleep`. Timeouts are used only as a
guard that turns a deadlock into a test failure.

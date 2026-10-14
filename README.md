# SIS Project — Text Analyzer

**Checkpoint 1: Core Go** · Golang Application Development

A small synchronous, in-memory Go application that processes **text-analysis tasks**.
Each task takes a piece of text as input, validates it, analyzes it and stores the
statistics as the task result, while tracking the task's lifecycle state.

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

A *word* is a maximal sequence of Unicode letters and digits.
A *sentence* ends with `.`, `!` or `?` (repeated marks like `...` or `?!` end one sentence; a trailing fragment without a mark also counts).

## Task lifecycle

```
            Start()               Complete(result)
 Pending ─────────────► Processing ─────────────────► Completed
                            │
                            │ Fail(reason)
                            └─────────────────────────► Failed
```

| State | Meaning |
|---|---|
| `Pending` | created and validated, not processed yet (initial state, zero value of `Status`) |
| `Processing` | the analyzer is working on the task |
| `Completed` | processed successfully, `Result` is set — **final** |
| `Failed` | the processor returned an error, `Error` holds the reason — **final** |

**Allowed transitions:** `Pending → Processing`, `Processing → Completed`, `Processing → Failed`.
Everything else is rejected with `task.ErrInvalidTransition` and the task is left unchanged —
e.g. processing an already completed task again, or completing a task that was never started.

Invalid input never creates a task: `Create` returns `task.ErrInvalidInput` and nothing is stored.

## Project structure

```
sis/
├── go.mod
├── cmd/sis/main.go                  demo application
└── internal/
    ├── textanalysis/                domain logic: validation + statistics (pure, no state)
    │   ├── analyzer.go
    │   └── analyzer_test.go
    ├── task/                        Task model, lifecycle, interfaces, service
    │   ├── task.go                  Task struct, ID type, errors, Start/Complete/Fail
    │   ├── status.go                Status type and allowed transitions
    │   ├── service.go               Processor & Repository interfaces, Service use cases
    │   ├── task_test.go
    │   └── service_test.go
    └── storage/                     in-memory Repository (map)
        ├── memory.go
        └── memory_test.go
```

## Design decisions

- **Two interfaces** in package `task`:
  - `Processor` (`Validate`, `Analyze`) — the service does not depend on a concrete analyzer.
    `textanalysis.Analyzer` implements it implicitly; tests use a fake failing processor
    to check the `Failed` path.
  - `Repository` (`Add`, `Get`, `Update`, `List`) — `storage.Memory` implements it with a
    `map[task.ID]task.Task`. A different storage can be added later without changing the service.
- **Value vs pointer semantics**
  - Methods that change state (`Task.Start/Complete/Fail`, `Memory.Add/Update`) use pointer
    receivers; all methods of these types use pointers for consistency.
  - Small, read-only types use value receivers: `Status`, `textanalysis.Analyzer`, `Result.Clone`.
  - The repository stores **copies** and returns **deep copies** (`Clone`), because `Result`
    contains a slice and `Task` a pointer. A caller cannot change a stored task without `Update`.
- **Errors**: sentinel errors (`ErrNotFound`, `ErrInvalidInput`, `ErrInvalidTransition`,
  `ErrProcessingFailed`, `textanalysis.ErrEmptyText`, …) wrapped with `%w`, checked with
  `errors.Is`. No `panic` for application errors.
- **IDs**: sequential integers (`1, 2, 3 …`) of a dedicated type `task.ID`, assigned by the repository.
- **Determinism**: top words are sorted by count, then alphabetically (map order in Go is random), so results are testable.
- Concurrency and HTTP are later checkpoints and are intentionally not implemented;
  `storage.Memory` is not safe for concurrent use yet.

## Running

Requires **Go 1.27**.

```bash
go run ./cmd/sis                                   # built-in demo
go run ./cmd/sis "Your text here." "Another one!"  # analyze your own texts
```

## Testing

```bash
gofmt -l .        # must print nothing
go vet ./...
go test ./...
go test -v ./...  # see every test case
go test -cover ./...
```

Tests cover: task creation, invalid input (table-driven), retrieval, nonexistent task,
processing, correctness of results (table-driven, incl. Cyrillic text), state after processing,
invalid transitions (table-driven), processing failure, multiple independent tasks, and
protection of stored data from modification through returned copies.

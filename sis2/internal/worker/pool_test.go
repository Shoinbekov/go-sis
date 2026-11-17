package worker_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sis/internal/storage"
	"sis/internal/task"
	"sis/internal/textanalysis"
	"sis/internal/worker"
)

// guard is the maximum time a test waits for something that must happen.
// It never makes a test pass: it only turns a hang (deadlock) into a failure.
const guard = 5 * time.Second

// gateProcessor is a fake Processor that lets a test control timing with
// channels instead of time.Sleep:
//   - every Analyze call reports its input on `started`;
//   - then it blocks until the test closes `release`;
//   - it records how many Analyze calls run at the same moment.
type gateProcessor struct {
	started   chan string
	release   chan struct{}
	calls     atomic.Int32
	active    atomic.Int32
	maxActive atomic.Int32
}

func newGate() *gateProcessor {
	return &gateProcessor{started: make(chan string, 1000), release: make(chan struct{})}
}

func (g *gateProcessor) Validate(text string) error { return textanalysis.Analyzer{}.Validate(text) }

func (g *gateProcessor) Analyze(text string) (textanalysis.Result, error) {
	g.calls.Add(1)
	n := g.active.Add(1)
	for { // maxActive = max(maxActive, n), without a data race
		m := g.maxActive.Load()
		if n <= m || g.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	g.started <- text
	<-g.release
	g.active.Add(-1)
	return textanalysis.Analyzer{}.Analyze(text) // the REAL domain processing
}

// waitStarted waits until n Analyze calls have started.
func waitStarted(t *testing.T, g *gateProcessor, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-g.started:
		case <-time.After(guard):
			t.Fatalf("only %d of %d tasks started processing", i, n)
		}
	}
}

func newPool(t *testing.T, p task.Processor, workers, capacity int) (*task.Service, *worker.Pool) {
	t.Helper()
	svc := task.NewService(storage.NewMemory(), p)
	pool, err := worker.NewPool(svc, workers, capacity)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return svc, pool
}

func create(t *testing.T, svc *task.Service, text string) task.ID {
	t.Helper()
	tk, err := svc.Create(text)
	if err != nil {
		t.Fatalf("Create(%q): %v", text, err)
	}
	return tk.ID
}

func status(t *testing.T, svc *task.Service, id task.ID) task.Status {
	t.Helper()
	tk, err := svc.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return tk.Status
}

// shutdownWithin fails the test if Shutdown does not return in time (deadlock).
func shutdownWithin(t *testing.T, pool *worker.Pool) {
	t.Helper()
	done := make(chan struct{})
	go func() { pool.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(guard):
		t.Fatal("Shutdown did not return: workers did not terminate")
	}
}

// waitGoroutines waits until the number of goroutines drops to at most want.
func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(guard)
	for runtime.NumGoroutine() > want {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want <= %d (worker leak)", runtime.NumGoroutine(), want)
		}
		runtime.Gosched()
	}
}

// ---------- startup and termination ----------

func TestNewPoolStartsAndStopsWorkers(t *testing.T) {
	before := runtime.NumGoroutine()

	g := newGate()
	svc, pool := newPool(t, g, 3, 5)
	if pool.Workers() != 3 || pool.Capacity() != 5 || pool.QueueLen() != 0 {
		t.Fatalf("workers=%d capacity=%d queue=%d, want 3, 5, 0",
			pool.Workers(), pool.Capacity(), pool.QueueLen())
	}
	// Proof that 3 workers are running: 3 tasks are inside Analyze at once.
	for i := 0; i < 3; i++ {
		if err := pool.Submit(create(t, svc, fmt.Sprintf("task %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	waitStarted(t, g, 3)

	close(g.release)
	shutdownWithin(t, pool)
	waitGoroutines(t, before) // all workers have exited — no goroutine leak
}

func TestNewPoolRejectsInvalidConfig(t *testing.T) {
	svc := task.NewService(storage.NewMemory(), textanalysis.Analyzer{})
	tests := []struct {
		name              string
		svc               *task.Service
		workers, capacity int
	}{
		{"no service", nil, 2, 2},
		{"zero workers", svc, 0, 2},
		{"zero capacity", svc, 2, 0},
		{"negative", svc, -1, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := worker.NewPool(tc.svc, tc.workers, tc.capacity); !errors.Is(err, worker.ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

// ---------- submission and processing ----------

func TestSubmitDoesNotWaitForProcessing(t *testing.T) {
	g := newGate()
	svc, pool := newPool(t, g, 2, 4)
	id := create(t, svc, "Hello Go world")

	// The processor blocks until release is closed. If Submit waited for the
	// processing to finish, this call would never return.
	if err := pool.Submit(id); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if s := status(t, svc, id); s != task.StatusQueued && s != task.StatusProcessing {
		t.Fatalf("status right after Submit = %s, want Queued or Processing", s)
	}

	waitStarted(t, g, 1)
	if s := status(t, svc, id); s != task.StatusProcessing {
		t.Fatalf("status while a worker analyzes it = %s, want Processing", s)
	}

	close(g.release)
	shutdownWithin(t, pool)

	tk, _ := svc.Get(id)
	if tk.Status != task.StatusCompleted || tk.Result == nil || tk.Result.Words != 3 || tk.Result.Characters != 14 {
		t.Fatalf("after processing: status=%s result=%+v", tk.Status, tk.Result)
	}
}

func TestProcessManyTasksWithCorrectResults(t *testing.T) {
	svc, pool := newPool(t, textanalysis.Analyzer{}, 4, 100)

	// Task i has i%7+1 words, so every task has its own expected result.
	want := map[task.ID]int{}
	for i := 0; i < 100; i++ {
		words := i%7 + 1
		text := strings.TrimSpace(strings.Repeat(fmt.Sprintf("w%d ", i), words))
		id := create(t, svc, text)
		want[id] = words
		if err := pool.Submit(id); err != nil {
			t.Fatalf("Submit(%d): %v", id, err)
		}
	}
	shutdownWithin(t, pool)

	// Order of completion is not checked — only that every task has its OWN result.
	for _, tk := range svc.List() {
		if tk.Status != task.StatusCompleted {
			t.Errorf("task %d status = %s, want Completed", tk.ID, tk.Status)
			continue
		}
		if tk.Result.Words != want[tk.ID] {
			t.Errorf("task %d words = %d, want %d (result of another task?)", tk.ID, tk.Result.Words, want[tk.ID])
		}
	}
	if n := len(svc.List()); n != 100 {
		t.Fatalf("stored %d tasks, want 100", n)
	}
}

// Two tasks are inside Analyze at the SAME time: both workers report "started"
// while neither is allowed to finish. With sequential processing the second
// task could not start before the first one finishes, and this test would fail.
func TestTwoTasksProcessedConcurrently(t *testing.T) {
	g := newGate()
	svc, pool := newPool(t, g, 2, 2)
	a := create(t, svc, "first task")
	b := create(t, svc, "second task")
	for _, id := range []task.ID{a, b} {
		if err := pool.Submit(id); err != nil {
			t.Fatal(err)
		}
	}

	waitStarted(t, g, 2) // both started, nothing released yet → overlapping
	if s1, s2 := status(t, svc, a), status(t, svc, b); s1 != task.StatusProcessing || s2 != task.StatusProcessing {
		t.Fatalf("statuses = %s, %s; want both Processing", s1, s2)
	}
	if m := g.maxActive.Load(); m != 2 {
		t.Fatalf("max tasks in progress = %d, want 2", m)
	}

	close(g.release)
	shutdownWithin(t, pool)
	if status(t, svc, a) != task.StatusCompleted || status(t, svc, b) != task.StatusCompleted {
		t.Fatal("both tasks must be Completed")
	}
}

func TestPoolRespectsWorkerLimit(t *testing.T) {
	g := newGate()
	svc, pool := newPool(t, g, 3, 10)
	for i := 0; i < 10; i++ {
		if err := pool.Submit(create(t, svc, fmt.Sprintf("task %d", i))); err != nil {
			t.Fatal(err)
		}
	}

	waitStarted(t, g, 3)
	// 3 workers are blocked inside Analyze, so nobody takes more tasks:
	// exactly 7 tasks must still be waiting in the queue.
	if n := pool.QueueLen(); n != 7 {
		t.Fatalf("queue length = %d, want 7 (only 3 workers may be busy)", n)
	}

	close(g.release)
	shutdownWithin(t, pool)
	if m := g.maxActive.Load(); m != 3 {
		t.Fatalf("max concurrent tasks = %d, want exactly 3 (= workers)", m)
	}
	for _, tk := range svc.List() {
		if tk.Status != task.StatusCompleted {
			t.Errorf("task %d = %s, want Completed", tk.ID, tk.Status)
		}
	}
}

func TestFailedProcessingMarksTaskFailed(t *testing.T) {
	svc, pool := newPool(t, failingProcessor{}, 2, 2)
	id := create(t, svc, "anything")
	if err := pool.Submit(id); err != nil {
		t.Fatal(err)
	}
	shutdownWithin(t, pool)

	tk, _ := svc.Get(id)
	if tk.Status != task.StatusFailed || tk.Result != nil || tk.Error != "boom" {
		t.Fatalf("got status=%s result=%v error=%q, want Failed, nil, boom", tk.Status, tk.Result, tk.Error)
	}
}

type failingProcessor struct{}

func (failingProcessor) Validate(string) error { return nil }
func (failingProcessor) Analyze(string) (textanalysis.Result, error) {
	return textanalysis.Result{}, errors.New("boom")
}

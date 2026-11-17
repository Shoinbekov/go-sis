package worker_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"sis/internal/storage"
	"sis/internal/task"
	"sis/internal/textanalysis"
	"sis/internal/worker"
)

// ---------- invalid and duplicate submissions ----------

func TestSubmitDuplicateWhileQueuedOrProcessing(t *testing.T) {
	g := newGate()
	svc, pool := newPool(t, g, 1, 5)

	processing := create(t, svc, "being processed")
	queued := create(t, svc, "waiting in queue")
	if err := pool.Submit(processing); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, g, 1) // the only worker is busy with `processing`
	if err := pool.Submit(queued); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		id         task.ID
		wantStatus task.Status
	}{
		{"again while Processing", processing, task.StatusProcessing},
		{"again while Queued", queued, task.StatusQueued},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := pool.Submit(tc.id); !errors.Is(err, task.ErrInvalidTransition) {
				t.Fatalf("Submit error = %v, want ErrInvalidTransition", err)
			}
			if s := status(t, svc, tc.id); s != tc.wantStatus {
				t.Fatalf("rejected Submit changed status to %s, want %s", s, tc.wantStatus)
			}
		})
	}
	if n := pool.QueueLen(); n != 1 {
		t.Fatalf("queue length = %d, want 1 (duplicates must not enter the queue)", n)
	}

	close(g.release)
	shutdownWithin(t, pool)
	if c := g.calls.Load(); c != 2 {
		t.Fatalf("Analyze called %d times, want 2 (each task exactly once)", c)
	}
}

func TestSubmitFinishedOrUnknownTask(t *testing.T) {
	okSvc := task.NewService(storage.NewMemory(), textanalysis.Analyzer{})
	completed := create(t, okSvc, "Hello Go world")
	if _, err := okSvc.Process(completed); err != nil { // synchronous, Checkpoint 1
		t.Fatal(err)
	}

	failSvc := task.NewService(storage.NewMemory(), failingProcessor{})
	failed := create(t, failSvc, "anything")
	_, _ = failSvc.Process(failed)

	tests := []struct {
		name string
		svc  *task.Service
		id   task.ID
		want error
	}{
		{"Completed task", okSvc, completed, task.ErrInvalidTransition},
		{"Failed task", failSvc, failed, task.ErrInvalidTransition},
		{"nonexistent task", okSvc, 999, task.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := worker.NewPool(tc.svc, 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Shutdown()
			if err := pool.Submit(tc.id); !errors.Is(err, tc.want) {
				t.Fatalf("Submit error = %v, want %v", err, tc.want)
			}
		})
	}

	// The completed task keeps its result.
	tk, _ := okSvc.Get(completed)
	if tk.Status != task.StatusCompleted || tk.Result.Words != 3 {
		t.Fatalf("completed task changed: %+v", tk)
	}
}

// Many goroutines submit the SAME task at the same moment.
// Exactly one Submit may succeed, and the task is analyzed exactly once.
func TestConcurrentSubmitOfSameTask(t *testing.T) {
	g := newGate()
	close(g.release) // do not block in this test
	svc, pool := newPool(t, g, 4, 10)
	id := create(t, svc, "only once")

	const n = 50
	var wg sync.WaitGroup
	var ok, dup int32
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all goroutines start at the same time
			err := pool.Submit(id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, task.ErrInvalidTransition):
				dup++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	shutdownWithin(t, pool)

	if ok != 1 || dup != n-1 {
		t.Fatalf("accepted=%d rejected=%d, want 1 and %d", ok, dup, n-1)
	}
	if c := g.calls.Load(); c != 1 {
		t.Fatalf("task analyzed %d times, want exactly 1", c)
	}
	if s := status(t, svc, id); s != task.StatusCompleted {
		t.Fatalf("status = %s, want Completed", s)
	}
}

// ---------- full queue ----------

func TestSubmitRejectsWhenQueueIsFull(t *testing.T) {
	g := newGate()
	svc, pool := newPool(t, g, 1, 1) // 1 worker, queue for 1 task
	a := create(t, svc, "task A")
	b := create(t, svc, "task B")
	c := create(t, svc, "task C")

	if err := pool.Submit(a); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, g, 1) // the worker holds A, the queue is empty again
	if err := pool.Submit(b); err != nil {
		t.Fatalf("Submit B: %v (queue has one free place)", err)
	}
	// Queue: [B] — full.
	if err := pool.Submit(c); !errors.Is(err, worker.ErrQueueFull) {
		t.Fatalf("Submit C error = %v, want ErrQueueFull", err)
	}
	// A rejected task stays Pending: not Queued, not Processing.
	if s := status(t, svc, c); s != task.StatusPending {
		t.Fatalf("rejected task status = %s, want Pending", s)
	}
	if n := pool.QueueLen(); n != 1 {
		t.Fatalf("queue length = %d, want 1 (capacity must not grow)", n)
	}

	close(g.release)
	shutdownWithin(t, pool)
	if status(t, svc, a) != task.StatusCompleted || status(t, svc, b) != task.StatusCompleted {
		t.Fatal("accepted tasks A and B must be Completed")
	}

	// Because C stayed Pending, it can be submitted again later.
	pool2, _ := worker.NewPool(svc, 1, 1)
	if err := pool2.Submit(c); err != nil {
		t.Fatalf("resubmit C: %v", err)
	}
	shutdownWithin(t, pool2)
	if s := status(t, svc, c); s != task.StatusCompleted {
		t.Fatalf("C after resubmit = %s, want Completed", s)
	}
}

// ---------- shared data ----------

// Readers call Get/List while workers update tasks. Run with -race:
// without the RWMutex in storage this test reports a DATA RACE or crashes
// with "concurrent map read and map write".
func TestConcurrentReadsDuringProcessing(t *testing.T) {
	svc, pool := newPool(t, textanalysis.Analyzer{}, 4, 200)

	var ids []task.ID
	for i := 0; i < 200; i++ {
		ids = append(ids, create(t, svc, fmt.Sprintf("text number %d.", i)))
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, tk := range svc.List() {
					if got, err := svc.Get(tk.ID); err != nil || got.Input != tk.Input {
						t.Errorf("Get(%d) = %+v, %v", tk.ID, got, err)
						return
					}
				}
			}
		}()
	}

	for _, id := range ids {
		if err := pool.Submit(id); err != nil {
			t.Fatal(err)
		}
	}
	shutdownWithin(t, pool)
	close(stop)
	readers.Wait()

	for _, tk := range svc.List() {
		if tk.Status != task.StatusCompleted || tk.Result.Words != 3 {
			t.Errorf("task %d: status=%s result=%+v", tk.ID, tk.Status, tk.Result)
		}
	}
}

// ---------- shutdown ----------

func TestShutdownFinishesAcceptedTasks(t *testing.T) {
	svc, pool := newPool(t, textanalysis.Analyzer{}, 2, 50)
	for i := 0; i < 50; i++ {
		if err := pool.Submit(create(t, svc, fmt.Sprintf("accepted %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	// Shutdown right away: most tasks are still waiting in the queue.
	shutdownWithin(t, pool)

	for _, tk := range svc.List() {
		if tk.Status != task.StatusCompleted {
			t.Errorf("task %d = %s after Shutdown, want Completed (accepted tasks must not be lost)", tk.ID, tk.Status)
		}
	}
	if pool.QueueLen() != 0 {
		t.Fatalf("queue still has %d tasks", pool.QueueLen())
	}
}

func TestSubmitAfterShutdownIsRejected(t *testing.T) {
	svc, pool := newPool(t, textanalysis.Analyzer{}, 2, 5)
	id := create(t, svc, "too late")
	shutdownWithin(t, pool)

	if err := pool.Submit(id); !errors.Is(err, worker.ErrPoolStopped) {
		t.Fatalf("Submit after Shutdown error = %v, want ErrPoolStopped", err)
	}
	if s := status(t, svc, id); s != task.StatusPending {
		t.Fatalf("rejected task status = %s, want Pending", s)
	}
}

func TestShutdownTwiceIsSafe(t *testing.T) {
	_, pool := newPool(t, textanalysis.Analyzer{}, 3, 5)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ { // concurrent Shutdown calls: no "close of closed channel" panic
		wg.Add(1)
		go func() { defer wg.Done(); pool.Shutdown() }()
	}
	wg.Wait()
	shutdownWithin(t, pool) // and one more time after it already stopped
}

// Submitters and Shutdown run at the same time. Afterwards every task must be
// in a valid final place: accepted → Completed, rejected → still Pending.
// No task may be stuck in Queued/Processing (lost), and nothing may panic
// with "send on closed channel".
func TestConcurrentSubmitAndShutdown(t *testing.T) {
	for round := 0; round < 20; round++ {
		svc, pool := newPool(t, textanalysis.Analyzer{}, 3, 5)

		const submitters, perSubmitter = 8, 20
		var accepted, rejected sync.Map
		var wg sync.WaitGroup
		for s := 0; s < submitters; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perSubmitter; i++ {
					tk, err := svc.Create(fmt.Sprintf("s%d task %d", s, i))
					if err != nil {
						t.Error(err)
						return
					}
					err = pool.Submit(tk.ID)
					switch {
					case err == nil:
						accepted.Store(tk.ID, true)
					case errors.Is(err, worker.ErrPoolStopped), errors.Is(err, worker.ErrQueueFull):
						rejected.Store(tk.ID, true)
					default:
						t.Errorf("unexpected Submit error: %v", err)
					}
				}
			}()
		}
		go pool.Shutdown() // concurrently with the submitters
		wg.Wait()
		shutdownWithin(t, pool)

		total := 0
		for _, tk := range svc.List() {
			total++
			_, wasAccepted := accepted.Load(tk.ID)
			_, wasRejected := rejected.Load(tk.ID)
			switch {
			case wasAccepted && tk.Status != task.StatusCompleted:
				t.Fatalf("round %d: accepted task %d is %s, want Completed", round, tk.ID, tk.Status)
			case wasRejected && tk.Status != task.StatusPending:
				t.Fatalf("round %d: rejected task %d is %s, want Pending", round, tk.ID, tk.Status)
			case !wasAccepted && !wasRejected:
				t.Fatalf("round %d: task %d has no Submit result", round, tk.ID)
			}
		}
		if total != submitters*perSubmitter {
			t.Fatalf("round %d: %d tasks stored, want %d", round, total, submitters*perSubmitter)
		}
	}
}

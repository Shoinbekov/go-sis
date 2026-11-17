// Package worker runs task processing concurrently with a fixed-size worker pool.
//
//	Submit(id) ──► queue (buffered channel, capacity N) ──► worker 1 ─┐
//	                                                    ──► worker 2 ─┼─► Service.ProcessQueued ─► storage
//	                                                    ──► worker 3 ─┘
package worker

import (
	"errors"
	"fmt"
	"sync"

	"sis/internal/task"
)

// Pool errors. Callers check them with errors.Is.
var (
	// ErrQueueFull: the queue has no free place. The task is NOT changed and
	// stays Pending, so it can be submitted again later.
	ErrQueueFull = errors.New("task queue is full")
	// ErrPoolStopped: Shutdown was called, new tasks are not accepted.
	ErrPoolStopped = errors.New("worker pool is stopped")
	// ErrInvalidConfig: wrong number of workers or queue capacity.
	ErrInvalidConfig = errors.New("invalid worker pool config")
)

// Pool is a worker pool: a fixed number of worker goroutines that take task
// IDs from one bounded queue.
//
// Who owns what:
//   - queue: the Pool owns the channel. Only Submit sends to it and only
//     Shutdown closes it, both under mu, so a send never happens after close
//     (no "send on closed channel" panic). Workers only receive.
//   - closed: protected by mu.
//   - tasks: live in the Service's storage, which has its own lock.
//     Only task IDs travel through the channel.
type Pool struct {
	svc     *task.Service
	queue   chan task.ID
	workers int

	mu     sync.Mutex // protects closed and sending/closing queue
	closed bool

	wg sync.WaitGroup // counts running workers
}

// NewPool creates the pool and starts `workers` worker goroutines.
// capacity is the maximum number of tasks waiting in the queue.
func NewPool(svc *task.Service, workers, capacity int) (*Pool, error) {
	if svc == nil || workers < 1 || capacity < 1 {
		return nil, fmt.Errorf("%w: workers=%d capacity=%d", ErrInvalidConfig, workers, capacity)
	}
	p := &Pool{
		svc:     svc,
		queue:   make(chan task.ID, capacity), // bounded: capacity never grows
		workers: workers,
	}
	for i := 1; i <= workers; i++ {
		p.wg.Add(1) // before go: Shutdown's Wait must already see this worker
		go p.work()
	}
	return p, nil
}

// work is one worker. It processes tasks until the queue is closed AND empty:
// `range` over a channel keeps receiving buffered values after close, so tasks
// accepted before Shutdown are still processed — nothing is silently lost.
func (p *Pool) work() {
	defer p.wg.Done()
	for id := range p.queue {
		// The error is not lost: on failure the task itself becomes Failed
		// with the reason in task.Error, and callers read it with Get.
		_, _ = p.svc.ProcessQueued(id)
	}
}

// Submit accepts a Pending task for asynchronous processing and returns
// immediately — it does not wait until the task is processed.
//
// Full queue policy: REJECT. If the queue is full, Submit returns ErrQueueFull
// and the task stays Pending (it is not marked Queued or Processing).
//
// Errors:
//   - ErrPoolStopped — Shutdown was already called;
//   - ErrQueueFull — no free place in the queue;
//   - task.ErrNotFound — no task with this ID;
//   - task.ErrInvalidTransition — the task is not Pending (already Queued,
//     Processing, Completed or Failed): duplicate submissions are rejected.
func (p *Pool) Submit(id task.ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrPoolStopped
	}
	// Only Submit adds to the queue, and only while holding mu. Workers only
	// remove. So if there is a free place now, it is still free a moment later
	// when we send — the send below can never block.
	if len(p.queue) == cap(p.queue) {
		return fmt.Errorf("%w: task %d", ErrQueueFull, id)
	}
	// Pending -> Queued atomically in storage. Two concurrent Submits of the
	// same task: only one sees Pending, the other gets ErrInvalidTransition.
	if _, err := p.svc.MarkQueued(id); err != nil {
		return err
	}
	p.queue <- id
	return nil
}

// Shutdown stops the pool:
//  1. stops accepting new tasks (Submit returns ErrPoolStopped);
//  2. closes the queue — workers finish all tasks already accepted;
//  3. waits until every worker goroutine has exited.
//
// Calling Shutdown again (also concurrently) is a safe no-op: the channel is
// closed only once, and every call returns after the workers have finished.
func (p *Pool) Shutdown() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.queue)
	}
	p.mu.Unlock() // unlock BEFORE Wait: workers never need mu, but holding it
	//               while waiting would block every Submit caller for nothing.
	p.wg.Wait()
}

// Workers returns the number of worker goroutines.
func (p *Pool) Workers() int { return p.workers }

// Capacity returns the queue capacity.
func (p *Pool) Capacity() int { return cap(p.queue) }

// QueueLen returns how many tasks are waiting in the queue right now.
func (p *Pool) QueueLen() int { return len(p.queue) }

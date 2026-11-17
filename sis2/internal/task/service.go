package task

import (
	"fmt"

	"sis/internal/textanalysis"
)

// Processor validates and processes a task input.
//
// TaskService depends on this interface, not on a concrete analyzer. That
// allows tests to plug in a fake processor (for example, one that always
// fails) and lets the domain logic be replaced without touching the service.
// textanalysis.Analyzer satisfies it implicitly — there is no "implements".
type Processor interface {
	Validate(input string) error
	Analyze(input string) (textanalysis.Result, error)
}

// Repository stores tasks. The in-memory implementation lives in
// internal/storage; a database could be added later behind the same interface.
//
// The repository works with Task values (copies), so code outside it can
// never modify a stored task without calling Update.
type Repository interface {
	// Add assigns a new unique ID, stores the task and returns the stored copy.
	Add(t Task) (Task, error)
	// Get returns a copy of the task or an error wrapping ErrNotFound.
	Get(id ID) (Task, error)
	// Update replaces an existing task or returns an error wrapping ErrNotFound.
	Update(t Task) error
	// List returns copies of all tasks ordered by ID.
	List() []Task
	// Modify atomically applies fn to the stored task: read, check, change
	// and save happen as one step. If fn returns an error, nothing is saved.
	// Implementations must be safe for concurrent use.
	Modify(id ID, fn func(t *Task) error) (Task, error)
}

// Compile-time check that the real analyzer implements Processor.
var _ Processor = textanalysis.Analyzer{}

// Service implements the application use cases: create, get, list, process.
type Service struct {
	repo      Repository
	processor Processor
}

// NewService builds a Service. It is a regular function: Go has no constructors.
func NewService(repo Repository, processor Processor) *Service {
	return &Service{repo: repo, processor: processor}
}

// Create validates the input and stores a new Pending task.
func (s *Service) Create(input string) (Task, error) {
	if err := s.processor.Validate(input); err != nil {
		// Two %w verbs: the caller can check both ErrInvalidInput and the
		// concrete reason (e.g. textanalysis.ErrEmptyText) with errors.Is.
		return Task{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	return s.repo.Add(New(input))
}

// Get returns the task with the given ID.
func (s *Service) Get(id ID) (Task, error) {
	return s.repo.Get(id)
}

// List returns all tasks.
func (s *Service) List() []Task {
	return s.repo.List()
}

// Process synchronously runs a Pending task through its lifecycle (Checkpoint 1):
// Pending -> Processing -> Completed (or Failed if the processor returns an error).
// Processing a task that is not Pending returns ErrInvalidTransition.
func (s *Service) Process(id ID) (Task, error) {
	return s.run(id, StatusPending)
}

// MarkQueued atomically moves a Pending task to Queued. The worker pool calls
// it when it accepts a task. Any other state (Queued, Processing, Completed,
// Failed) returns ErrInvalidTransition — this is what rejects duplicate
// submissions.
func (s *Service) MarkQueued(id ID) (Task, error) {
	return s.repo.Modify(id, func(t *Task) error { return t.Enqueue() })
}

// ProcessQueued runs a Queued task: Queued -> Processing -> Completed/Failed.
// Workers of the pool call it for every task they take from the queue.
func (s *Service) ProcessQueued(id ID) (Task, error) {
	return s.run(id, StatusQueued)
}

// run is the shared processing logic.
//
//  1. Atomically check that the task is in state `from` and move it to
//     Processing (one Modify call = one lock). If two goroutines try to start
//     the same task, exactly one succeeds; the other gets ErrInvalidTransition.
//     So a task is never processed twice at the same time.
//  2. Analyze the text WITHOUT holding any lock. Analysis may take time, and
//     other goroutines must be able to read and update other tasks meanwhile.
//     Analyzer has no shared state, so it is safe to call concurrently.
//  3. Atomically store the result: Completed, or Failed with the reason.
func (s *Service) run(id ID, from Status) (Task, error) {
	t, err := s.repo.Modify(id, func(t *Task) error {
		if t.Status != from {
			return fmt.Errorf("%w: task %d: %s -> %s", ErrInvalidTransition, t.ID, t.Status, StatusProcessing)
		}
		return t.Start()
	})
	if err != nil {
		return t, err
	}

	result, procErr := s.processor.Analyze(t.Input)

	if procErr != nil {
		failed, err := s.repo.Modify(id, func(t *Task) error { return t.Fail(procErr) })
		if err != nil {
			return Task{}, err
		}
		return failed, fmt.Errorf("%w: task %d: %w", ErrProcessingFailed, id, procErr)
	}
	return s.repo.Modify(id, func(t *Task) error { return t.Complete(result) })
}

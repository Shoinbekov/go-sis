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

// Process runs the task through its lifecycle:
// Pending -> Processing -> Completed (or Failed if the processor returns an error).
// Processing a task that is not Pending returns ErrInvalidTransition.
func (s *Service) Process(id ID) (Task, error) {
	t, err := s.repo.Get(id)
	if err != nil {
		return Task{}, err
	}

	if err := t.Start(); err != nil {
		return t, err
	}
	// Persist the intermediate state, so the task is visibly "Processing".
	if err := s.repo.Update(t); err != nil {
		return Task{}, err
	}

	result, procErr := s.processor.Analyze(t.Input)
	if procErr != nil {
		if err := t.Fail(procErr); err != nil {
			return Task{}, err
		}
		if err := s.repo.Update(t); err != nil {
			return Task{}, err
		}
		return t, fmt.Errorf("%w: task %d: %w", ErrProcessingFailed, t.ID, procErr)
	}

	if err := t.Complete(result); err != nil {
		return Task{}, err
	}
	if err := s.repo.Update(t); err != nil {
		return Task{}, err
	}
	return t, nil
}

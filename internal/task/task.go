// Package task contains the Task domain model, its lifecycle and the
// TaskService that ties validation, processing and storage together.
package task

import (
	"errors"
	"fmt"

	"sis/internal/textanalysis"
)

// ID uniquely identifies a task. A separate named type prevents mixing task
// IDs with ordinary integers by accident.
type ID int

// Application-level errors. Callers check them with errors.Is.
var (
	ErrNotFound          = errors.New("task not found")
	ErrInvalidInput      = errors.New("invalid task input")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrProcessingFailed  = errors.New("task processing failed")
)

// Task is a unit of work: a text that has to be analyzed.
type Task struct {
	ID     ID
	Input  string               // domain-specific payload: the text to analyze
	Status Status               // current lifecycle state
	Result *textanalysis.Result // nil until the task is Completed
	Error  string               // reason of failure, set only when Failed
}

// New creates a Pending task with the given input. The ID is assigned later
// by the repository when the task is stored.
func New(input string) Task {
	return Task{Input: input, Status: StatusPending}
}

// transition moves the task to the next state or returns ErrInvalidTransition.
//
// Pointer receiver: the method modifies the task. All methods that change a
// Task use pointer receivers; with a value receiver they would only change a copy.
func (t *Task) transition(next Status) error {
	if !t.Status.CanTransitionTo(next) {
		return fmt.Errorf("%w: task %d: %s -> %s", ErrInvalidTransition, t.ID, t.Status, next)
	}
	t.Status = next
	return nil
}

// Start marks the task as Processing. Only a Pending task can be started.
func (t *Task) Start() error {
	return t.transition(StatusProcessing)
}

// Complete stores the result and marks the task as Completed.
func (t *Task) Complete(r textanalysis.Result) error {
	if err := t.transition(StatusCompleted); err != nil {
		return err
	}
	res := r.Clone()
	t.Result = &res
	t.Error = ""
	return nil
}

// Fail records the reason and marks the task as Failed.
func (t *Task) Fail(reason error) error {
	if err := t.transition(StatusFailed); err != nil {
		return err
	}
	t.Error = reason.Error()
	return nil
}

// Clone returns a deep copy of the task.
//
// A plain copy (`c := *t`) would share the Result pointer, so a caller could
// change a result stored inside the repository. Clone copies the result too.
//
// Pointer receiver for consistency: the other Task methods need *Task, and
// the Go FAQ recommends not mixing receiver kinds on one type.
func (t *Task) Clone() Task {
	c := *t
	if t.Result != nil {
		res := t.Result.Clone()
		c.Result = &res
	}
	return c
}

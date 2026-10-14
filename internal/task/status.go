package task

import "fmt"

// Status is the lifecycle state of a task.
//
//	Pending ──► Processing ──► Completed
//	                 │
//	                 └───────► Failed
//
// Completed and Failed are final states.
type Status int

// The zero value of Status is StatusPending, so a freshly declared Task is
// automatically "created but not processed yet".
const (
	StatusPending Status = iota
	StatusProcessing
	StatusCompleted
	StatusFailed
)

// allowedTransitions lists, for every state, the states it may move to.
var allowedTransitions = map[Status][]Status{
	StatusPending:    {StatusProcessing},
	StatusProcessing: {StatusCompleted, StatusFailed},
	StatusCompleted:  {},
	StatusFailed:     {},
}

// String implements fmt.Stringer, so statuses print as words, not numbers.
// Value receiver: Status is just an int.
func (s Status) String() string {
	switch s {
	case StatusPending:
		return "Pending"
	case StatusProcessing:
		return "Processing"
	case StatusCompleted:
		return "Completed"
	case StatusFailed:
		return "Failed"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// CanTransitionTo reports whether moving from s to next is allowed.
func (s Status) CanTransitionTo(next Status) bool {
	for _, allowed := range allowedTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// IsFinal reports whether no further transitions are possible.
func (s Status) IsFinal() bool {
	return len(allowedTransitions[s]) == 0
}

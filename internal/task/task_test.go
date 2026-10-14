package task

import (
	"errors"
	"testing"

	"sis/internal/textanalysis"
)

func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		from, to Status
		allowed  bool
	}{
		{StatusPending, StatusProcessing, true},
		{StatusProcessing, StatusCompleted, true},
		{StatusProcessing, StatusFailed, true},

		{StatusPending, StatusCompleted, false}, // cannot skip Processing
		{StatusPending, StatusFailed, false},
		{StatusProcessing, StatusPending, false},
		{StatusCompleted, StatusProcessing, false}, // final state
		{StatusCompleted, StatusPending, false},
		{StatusFailed, StatusProcessing, false}, // final state
	}

	for _, tc := range tests {
		t.Run(tc.from.String()+"->"+tc.to.String(), func(t *testing.T) {
			if got := tc.from.CanTransitionTo(tc.to); got != tc.allowed {
				t.Fatalf("CanTransitionTo = %v, want %v", got, tc.allowed)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	if got := StatusCompleted.String(); got != "Completed" {
		t.Fatalf("String() = %q", got)
	}
	if got := Status(42).String(); got != "Status(42)" {
		t.Fatalf("String() of unknown status = %q", got)
	}
}

func TestTaskLifecycle(t *testing.T) {
	tk := New("hello")
	if tk.Status != StatusPending {
		t.Fatalf("new task status = %s, want Pending", tk.Status)
	}

	if err := tk.Complete(textanalysis.Result{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Complete on Pending task: err = %v, want ErrInvalidTransition", err)
	}
	if tk.Status != StatusPending || tk.Result != nil {
		t.Fatal("failed transition must not change the task")
	}

	if err := tk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tk.Complete(textanalysis.Result{Words: 1}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if tk.Status != StatusCompleted || tk.Result == nil || tk.Result.Words != 1 {
		t.Fatalf("unexpected task after Complete: %+v", tk)
	}

	if err := tk.Start(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Start on Completed task: err = %v, want ErrInvalidTransition", err)
	}
}

func TestTaskCloneIsDeep(t *testing.T) {
	tk := New("x")
	_ = tk.Start()
	_ = tk.Complete(textanalysis.Result{Words: 5, TopWords: []textanalysis.WordCount{{Word: "x", Count: 1}}})

	c := tk.Clone()
	c.Result.Words = 100
	c.Result.TopWords[0].Count = 100

	if tk.Result.Words != 5 || tk.Result.TopWords[0].Count != 1 {
		t.Fatalf("changing the clone changed the original: %+v", tk.Result)
	}
}

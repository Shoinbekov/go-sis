// Service tests live in the external package task_test: they use the real
// in-memory storage, which itself imports package task.
package task_test

import (
	"errors"
	"fmt"
	"testing"

	"sis/internal/storage"
	"sis/internal/task"
	"sis/internal/textanalysis"
)

func newService() *task.Service {
	return task.NewService(storage.NewMemory(), textanalysis.Analyzer{})
}

func TestCreateTask(t *testing.T) {
	svc := newService()

	tk, err := svc.Create("Hello Go world")
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if tk.ID != 1 {
		t.Errorf("ID = %d, want 1", tk.ID)
	}
	if tk.Input != "Hello Go world" {
		t.Errorf("Input = %q", tk.Input)
	}
	if tk.Status != task.StatusPending {
		t.Errorf("Status = %s, want Pending", tk.Status)
	}
	if tk.Result != nil {
		t.Errorf("Result must be nil before processing, got %+v", tk.Result)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCause error
	}{
		{"empty", "", textanalysis.ErrEmptyText},
		{"whitespace only", " \t\n ", textanalysis.ErrEmptyText},
		{"no words", "!!! ???", textanalysis.ErrNoWords},
		{"invalid utf8", "\xff\xfe", textanalysis.ErrInvalidUTF8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService()

			_, err := svc.Create(tc.input)
			if !errors.Is(err, task.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want cause %v", err, tc.wantCause)
			}
			if n := len(svc.List()); n != 0 {
				t.Fatalf("invalid task must not be stored, store has %d tasks", n)
			}
		})
	}
}

func TestGetTask(t *testing.T) {
	svc := newService()
	created, err := svc.Create("Hello Go world")
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if got.ID != created.ID || got.Input != created.Input || got.Status != task.StatusPending {
		t.Fatalf("Get returned %+v, want %+v", got, created)
	}
}

func TestGetNonexistentTask(t *testing.T) {
	svc := newService()
	if _, err := svc.Create("something"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []task.ID{0, 2, -1, 999} {
		if _, err := svc.Get(id); !errors.Is(err, task.ErrNotFound) {
			t.Errorf("Get(%d) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestProcessTask(t *testing.T) {
	svc := newService()
	created, err := svc.Create("Hello Go world")
	if err != nil {
		t.Fatal(err)
	}

	processed, err := svc.Process(created.ID)
	if err != nil {
		t.Fatalf("Process: unexpected error: %v", err)
	}

	// State after processing.
	if processed.Status != task.StatusCompleted {
		t.Fatalf("Status = %s, want Completed", processed.Status)
	}
	// Correctness of the result (the example from the specification).
	r := processed.Result
	if r == nil {
		t.Fatal("Result is nil after processing")
	}
	if r.Words != 3 || r.Characters != 14 || r.LongestWord != "Hello" {
		t.Fatalf("unexpected result: %+v", *r)
	}

	// The result is stored and associated with the task.
	stored, err := svc.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != task.StatusCompleted || stored.Result == nil || stored.Result.Words != 3 {
		t.Fatalf("stored task was not updated: %+v", stored)
	}
}

func TestProcessTwiceIsInvalid(t *testing.T) {
	svc := newService()
	created, _ := svc.Create("Hello Go world")
	if _, err := svc.Process(created.ID); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Process(created.ID)
	if !errors.Is(err, task.ErrInvalidTransition) {
		t.Fatalf("second Process error = %v, want ErrInvalidTransition", err)
	}

	stored, _ := svc.Get(created.ID)
	if stored.Status != task.StatusCompleted || stored.Result.Words != 3 {
		t.Fatalf("invalid operation must not change the task: %+v", stored)
	}
}

func TestProcessNonexistentTask(t *testing.T) {
	_, err := newService().Process(42)
	if !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// failingProcessor is a fake Processor: valid input, but processing fails.
// It shows why Service depends on an interface — tests can substitute behavior.
type failingProcessor struct{}

var errBoom = errors.New("boom")

func (failingProcessor) Validate(string) error { return nil }
func (failingProcessor) Analyze(string) (textanalysis.Result, error) {
	return textanalysis.Result{}, errBoom
}

func TestProcessFailureMarksTaskFailed(t *testing.T) {
	svc := task.NewService(storage.NewMemory(), failingProcessor{})
	created, err := svc.Create("anything")
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Process(created.ID)
	if !errors.Is(err, task.ErrProcessingFailed) || !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want ErrProcessingFailed wrapping errBoom", err)
	}

	stored, _ := svc.Get(created.ID)
	if stored.Status != task.StatusFailed {
		t.Fatalf("Status = %s, want Failed", stored.Status)
	}
	if stored.Result != nil || stored.Error != "boom" {
		t.Fatalf("failed task: Result=%v Error=%q", stored.Result, stored.Error)
	}

	// Failed is final: the task cannot be processed again.
	if _, err := svc.Process(created.ID); !errors.Is(err, task.ErrInvalidTransition) {
		t.Fatalf("Process on Failed task error = %v, want ErrInvalidTransition", err)
	}
}

func TestMultipleIndependentTasks(t *testing.T) {
	svc := newService()
	inputs := []struct {
		text      string
		wantWords int
	}{
		{"one", 1},
		{"one two", 2},
		{"one two three. Four!", 4},
	}

	ids := make([]task.ID, len(inputs))
	for i, in := range inputs {
		tk, err := svc.Create(in.text)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = tk.ID
	}

	// IDs are unique.
	seen := map[task.ID]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %d in %v", id, ids)
		}
		seen[id] = true
	}

	// Process only the middle task: the others must stay Pending.
	if _, err := svc.Process(ids[1]); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		tk, _ := svc.Get(id)
		want := task.StatusPending
		if i == 1 {
			want = task.StatusCompleted
		}
		if tk.Status != want {
			t.Errorf("task %d status = %s, want %s", id, tk.Status, want)
		}
	}

	// Process the rest and check that every task got its OWN result.
	for _, id := range []task.ID{ids[0], ids[2]} {
		if _, err := svc.Process(id); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range ids {
		tk, _ := svc.Get(id)
		if tk.Input != inputs[i].text {
			t.Errorf("task %d input = %q, want %q", id, tk.Input, inputs[i].text)
		}
		if tk.Result == nil || tk.Result.Words != inputs[i].wantWords {
			t.Errorf("task %d result = %+v, want %d words", id, tk.Result, inputs[i].wantWords)
		}
	}

	if got := len(svc.List()); got != len(inputs) {
		t.Fatalf("List() has %d tasks, want %d", got, len(inputs))
	}
}

func TestReturnedTaskCannotModifyStorage(t *testing.T) {
	svc := newService()
	created, _ := svc.Create("Hello Go world")
	processed, _ := svc.Process(created.ID)

	// Try to tamper with the returned copy.
	processed.Status = task.StatusPending
	processed.Result.Words = 999

	stored, _ := svc.Get(created.ID)
	if stored.Status != task.StatusCompleted || stored.Result.Words != 3 {
		t.Fatalf("stored task changed through a returned copy: %+v", stored)
	}
}

func ExampleService() {
	svc := task.NewService(storage.NewMemory(), textanalysis.Analyzer{})

	tk, _ := svc.Create("Hello Go world")
	fmt.Println(tk.ID, tk.Status)

	tk, _ = svc.Process(tk.ID)
	fmt.Println(tk.Status, tk.Result.Words, tk.Result.Characters)

	_, err := svc.Process(tk.ID)
	fmt.Println(err)
	// Output:
	// 1 Pending
	// Completed 3 14
	// invalid state transition: task 1: Completed -> Processing
}

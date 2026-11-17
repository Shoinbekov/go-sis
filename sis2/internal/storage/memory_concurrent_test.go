package storage

import (
	"errors"
	"sync"
	"testing"

	"sis/internal/task"
)

// Concurrent Add: every task must get its own unique ID (no lost increment
// of lastID, no "concurrent map writes" crash). Run with -race.
func TestMemoryConcurrentAddUniqueIDs(t *testing.T) {
	m := NewMemory()
	const n = 500

	var wg sync.WaitGroup
	ids := make([]task.ID, n) // each goroutine writes its own element
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk, err := m.Add(task.New("text"))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = tk.ID
		}()
	}
	wg.Wait()

	seen := make(map[task.ID]bool, n)
	for _, id := range ids {
		if id < 1 || id > n || seen[id] {
			t.Fatalf("bad or duplicate id %d", id)
		}
		seen[id] = true
	}
	if got := len(m.List()); got != n {
		t.Fatalf("List has %d tasks, want %d", got, n)
	}
}

// Modify is atomic: many goroutines try Pending -> Queued on the same task,
// exactly one succeeds. With a separate Get + Update several could succeed.
func TestMemoryModifyIsAtomic(t *testing.T) {
	m := NewMemory()
	tk, _ := m.Add(task.New("text"))

	const n = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Modify(tk.ID, func(t *task.Task) error { return t.Enqueue() })
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, task.ErrInvalidTransition) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok != 1 {
		t.Fatalf("%d goroutines moved the task to Queued, want exactly 1", ok)
	}
	got, _ := m.Get(tk.ID)
	if got.Status != task.StatusQueued {
		t.Fatalf("status = %s, want Queued", got.Status)
	}
}

func TestMemoryModifyErrorSavesNothing(t *testing.T) {
	m := NewMemory()
	tk, _ := m.Add(task.New("text"))

	_, err := m.Modify(tk.ID, func(t *task.Task) error {
		t.Input = "changed"
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("want the error from fn")
	}
	got, _ := m.Get(tk.ID)
	if got.Input != "text" {
		t.Fatalf("Input = %q: a failed Modify must not save changes", got.Input)
	}

	if _, err := m.Modify(999, func(*task.Task) error { return nil }); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Modify(999) error = %v, want ErrNotFound", err)
	}
}

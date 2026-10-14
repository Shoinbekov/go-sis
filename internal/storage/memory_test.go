package storage

import (
	"errors"
	"testing"

	"sis/internal/task"
)

func TestMemoryAddAssignsSequentialIDs(t *testing.T) {
	m := NewMemory()
	for want := task.ID(1); want <= 3; want++ {
		tk, err := m.Add(task.New("text"))
		if err != nil {
			t.Fatal(err)
		}
		if tk.ID != want {
			t.Fatalf("ID = %d, want %d", tk.ID, want)
		}
	}
}

func TestMemoryGetAndUpdate(t *testing.T) {
	m := NewMemory()
	tk, _ := m.Add(task.New("text"))

	tk.Input = "changed"
	if err := m.Update(tk); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := m.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Input != "changed" {
		t.Fatalf("Input = %q, want %q", got.Input, "changed")
	}
}

func TestMemoryNotFound(t *testing.T) {
	m := NewMemory()

	if _, err := m.Get(1); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Get error = %v, want ErrNotFound", err)
	}
	ghost := task.New("ghost")
	ghost.ID = 7
	if err := m.Update(ghost); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Update error = %v, want ErrNotFound", err)
	}
	if n := len(m.List()); n != 0 {
		t.Fatalf("Update of a missing task must not create it; List has %d", n)
	}
}

func TestMemoryListIsOrderedByID(t *testing.T) {
	m := NewMemory()
	for i := 0; i < 10; i++ {
		_, _ = m.Add(task.New("text"))
	}
	list := m.List()
	for i, tk := range list {
		if tk.ID != task.ID(i+1) {
			t.Fatalf("List()[%d].ID = %d, want %d", i, tk.ID, i+1)
		}
	}
}

// Package storage contains task repositories.
package storage

import (
	"fmt"
	"sort"
	"sync"

	"sis/internal/task"
)

// Memory is an in-memory task.Repository backed by a map.
//
// Concurrency: Memory is safe for concurrent use. The map and lastID are
// shared between workers, the main goroutine and test goroutines, so every
// method takes mu:
//   - Lock (write) for Add, Update and Modify — they change the map;
//   - RLock (read) for Get and List — many readers may read at the same time.
//
// A Go map is NOT safe for concurrent use, even for different keys:
// concurrent writes crash the program with "fatal error: concurrent map writes".
//
// Tasks are stored as values and every method copies them in and out
// (Task.Clone). Because nobody outside holds a pointer into the map, the
// mutex protects the map AND the fields of the tasks inside it: a task can
// only change through Add, Update or Modify, all under the lock.
type Memory struct {
	mu     sync.RWMutex
	tasks  map[task.ID]task.Task
	lastID task.ID
}

// Compile-time check that *Memory implements task.Repository.
var _ task.Repository = (*Memory)(nil)

// NewMemory returns an empty repository. The map must be created with make:
// writing to a nil map panics.
func NewMemory() *Memory {
	return &Memory{tasks: make(map[task.ID]task.Task)}
}

// Add assigns the next ID (1, 2, 3, ...) and stores a copy of the task.
// lastID++ and the map write happen under one lock, so two concurrent Adds
// can never get the same ID.
func (m *Memory) Add(t task.Task) (task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastID++
	t.ID = m.lastID
	m.tasks[t.ID] = t.Clone()
	return t.Clone(), nil
}

// Get returns a copy of the task with the given ID.
func (m *Memory) Get(id task.ID) (task.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tasks[id]
	if !ok {
		return task.Task{}, fmt.Errorf("%w: id %d", task.ErrNotFound, id)
	}
	// t is a local variable (addressable), so the pointer method Clone can be
	// called on it. m.tasks[id].Clone() would not compile: map elements are not addressable.
	return t.Clone(), nil
}

// Update replaces an existing task.
//
// Note: Get + change + Update is NOT atomic — another goroutine can change the
// task between Get and Update. Code that changes a task's state must use Modify.
func (m *Memory) Update(t task.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tasks[t.ID]; !ok {
		return fmt.Errorf("%w: id %d", task.ErrNotFound, t.ID)
	}
	m.tasks[t.ID] = t.Clone()
	return nil
}

// Modify atomically reads, changes and stores one task.
//
// fn receives a copy of the stored task. If fn returns nil, the changed copy
// is saved; if fn returns an error, nothing is saved. The whole
// read-check-write sequence happens under ONE lock, so two goroutines cannot
// both see "Pending" and both move the task forward (check-then-act race).
//
// fn must be short and must not call back into Memory (that would deadlock):
// it only checks and changes the task's fields.
func (m *Memory) Modify(id task.ID, fn func(t *task.Task) error) (task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.tasks[id]
	if !ok {
		return task.Task{}, fmt.Errorf("%w: id %d", task.ErrNotFound, id)
	}
	c := stored.Clone()
	if err := fn(&c); err != nil {
		return stored.Clone(), err
	}
	m.tasks[id] = c.Clone()
	return c, nil
}

// List returns copies of all tasks ordered by ID (map order is random).
func (m *Memory) List() []task.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]task.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		list = append(list, t.Clone())
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

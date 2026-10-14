// Package storage contains task repositories.
package storage

import (
	"fmt"
	"sort"

	"sis/internal/task"
)

// Memory is an in-memory task.Repository backed by a map.
//
// Tasks are stored as values and every method copies them in and out
// (Task.Clone), so callers can never change stored data by accident.
//
// Memory is not safe for concurrent use yet: concurrency is a topic of a
// later checkpoint, where a sync.Mutex will be added here.
type Memory struct {
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
//
// Pointer receivers on all methods: Add and Update modify the repository,
// and the rest use *Memory for consistency.
func (m *Memory) Add(t task.Task) (task.Task, error) {
	m.lastID++
	t.ID = m.lastID
	m.tasks[t.ID] = t.Clone()
	return t.Clone(), nil
}

// Get returns a copy of the task with the given ID.
func (m *Memory) Get(id task.ID) (task.Task, error) {
	t, ok := m.tasks[id]
	if !ok {
		return task.Task{}, fmt.Errorf("%w: id %d", task.ErrNotFound, id)
	}
	// t is a local variable (addressable), so the pointer method Clone can be
	// called on it. m.tasks[id].Clone() would not compile: map elements are not addressable.
	return t.Clone(), nil
}

// Update replaces an existing task.
func (m *Memory) Update(t task.Task) error {
	if _, ok := m.tasks[t.ID]; !ok {
		return fmt.Errorf("%w: id %d", task.ErrNotFound, t.ID)
	}
	m.tasks[t.ID] = t.Clone()
	return nil
}

// List returns copies of all tasks ordered by ID (map order is random).
func (m *Memory) List() []task.Task {
	list := make([]task.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		list = append(list, t.Clone())
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// Command sis is a demo of the Text Analyzer with concurrent processing.
//
//	go run ./cmd/sis                                  # built-in demo
//	go run ./cmd/sis "Your text here" "Another one"   # analyze your own texts
//	go run ./cmd/sis -workers 4 -queue 20 "text"      # change pool settings
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"sis/internal/storage"
	"sis/internal/task"
	"sis/internal/textanalysis"
	"sis/internal/worker"
)

func main() {
	workers := flag.Int("workers", 3, "number of worker goroutines")
	queueCap := flag.Int("queue", 10, "task queue capacity")
	flag.Parse()

	svc := task.NewService(storage.NewMemory(), textanalysis.Analyzer{TopN: 3})
	pool, err := worker.NewPool(svc, *workers, *queueCap)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("worker pool started: %d workers, queue capacity %d\n\n", pool.Workers(), pool.Capacity())

	inputs := flag.Args()
	if len(inputs) == 0 {
		inputs = []string{
			"Hello Go world",
			"Go is fun. Go is fast! Is Go simple?",
			"Привет, мир! Go работает с Unicode.",
			"Workers share one queue. Each task is processed once.",
			"   ", // invalid: whitespace only
		}
	}

	fmt.Println("== Create and submit tasks ==")
	var ids []task.ID
	for _, in := range inputs {
		t, err := svc.Create(in)
		if err != nil {
			fmt.Printf("rejected %q: %v\n", in, err)
			continue
		}
		// Submit returns immediately; a worker processes the task later.
		if err := pool.Submit(t.ID); err != nil {
			fmt.Printf("task %d not submitted: %v\n", t.ID, err)
			continue
		}
		fmt.Printf("submitted task %d %q\n", t.ID, t.Input)
		ids = append(ids, t.ID)
	}

	fmt.Println("\n== Invalid operations ==")
	if len(ids) > 0 {
		if err := pool.Submit(ids[0]); errors.Is(err, task.ErrInvalidTransition) {
			fmt.Println("submit the same task again:", err)
		}
	}
	if err := pool.Submit(999); errors.Is(err, task.ErrNotFound) {
		fmt.Println("submit missing task:", err)
	}

	// Stop accepting tasks, finish the accepted ones, wait for the workers.
	pool.Shutdown()
	fmt.Println("\n== Shutdown: all workers stopped ==")

	if t, err := svc.Create("too late"); err == nil {
		if err := pool.Submit(t.ID); errors.Is(err, worker.ErrPoolStopped) {
			fmt.Println("submit after shutdown:", err)
		}
	}

	fmt.Println("\n== Results ==")
	for _, id := range ids {
		t, err := svc.Get(id)
		if err != nil {
			fmt.Printf("task %d: %v\n", id, err)
			continue
		}
		printTask(t)
	}

	fmt.Println("== All tasks ==")
	for _, t := range svc.List() {
		words := "-"
		if t.Result != nil {
			words = fmt.Sprint(t.Result.Words)
		}
		fmt.Printf("task %d: %-9s words=%s\n", t.ID, t.Status, words)
	}
}

func printTask(t task.Task) {
	if t.Result == nil {
		fmt.Printf("task %d [%s] %q %s\n\n", t.ID, t.Status, t.Input, t.Error)
		return
	}
	r := t.Result
	top := make([]string, len(r.TopWords))
	for i, wc := range r.TopWords {
		top[i] = fmt.Sprintf("%s(%d)", wc.Word, wc.Count)
	}
	fmt.Printf("task %d [%s] %q\n", t.ID, t.Status, t.Input)
	fmt.Printf("  characters: %d, bytes: %d\n", r.Characters, r.Bytes)
	fmt.Printf("  words: %d (unique %d), sentences: %d, lines: %d\n", r.Words, r.UniqueWords, r.Sentences, r.Lines)
	fmt.Printf("  avg word length: %.2f, longest word: %q\n", r.AvgWordLength, r.LongestWord)
	fmt.Printf("  top words: %s\n\n", strings.Join(top, ", "))
}

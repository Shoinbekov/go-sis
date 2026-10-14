// Command sis is a small demo of the Text Analyzer task service.
//
//	go run ./cmd/sis                      # built-in demo
//	go run ./cmd/sis "Your text here" ... # analyze your own texts
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"sis/internal/storage"
	"sis/internal/task"
	"sis/internal/textanalysis"
)

func main() {
	svc := task.NewService(storage.NewMemory(), textanalysis.Analyzer{TopN: 3})

	inputs := os.Args[1:]
	if len(inputs) == 0 {
		inputs = []string{
			"Hello Go world",
			"Go is fun. Go is fast! Is Go simple?",
			"Привет, мир! Go работает с Unicode.",
			"   ", // invalid: whitespace only
		}
	}

	fmt.Println("== Create tasks ==")
	var ids []task.ID
	for _, in := range inputs {
		t, err := svc.Create(in)
		if err != nil {
			fmt.Printf("rejected %q: %v\n", in, err)
			continue
		}
		fmt.Printf("created task %d [%s] %q\n", t.ID, t.Status, t.Input)
		ids = append(ids, t.ID)
	}

	fmt.Println("\n== Process tasks ==")
	for _, id := range ids {
		t, err := svc.Process(id)
		if err != nil {
			fmt.Printf("task %d: %v\n", id, err)
			continue
		}
		printTask(t)
	}

	fmt.Println("== Invalid operations ==")
	if len(ids) > 0 {
		if _, err := svc.Process(ids[0]); errors.Is(err, task.ErrInvalidTransition) {
			fmt.Println("process again:", err)
		}
	}
	if _, err := svc.Get(999); errors.Is(err, task.ErrNotFound) {
		fmt.Println("get missing task:", err)
	}

	fmt.Println("\n== All tasks ==")
	for _, t := range svc.List() {
		words := "-"
		if t.Result != nil {
			words = fmt.Sprint(t.Result.Words)
		}
		fmt.Printf("task %d: %-9s words=%s\n", t.ID, t.Status, words)
	}
}

func printTask(t task.Task) {
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

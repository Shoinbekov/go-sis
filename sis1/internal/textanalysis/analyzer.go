// Package textanalysis contains the domain logic of the application:
// validating a piece of text and calculating statistics about it.
//
// The package knows nothing about tasks, IDs or states — it is pure,
// deterministic business logic, which makes it easy to unit-test.
package textanalysis

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validation errors. They are exported variables so callers can check them
// with errors.Is.
var (
	ErrEmptyText   = errors.New("text is empty or contains only whitespace")
	ErrInvalidUTF8 = errors.New("text is not valid UTF-8")
	ErrNoWords     = errors.New("text contains no words")
)

// DefaultTopN is the number of most frequent words reported when the
// analyzer is created with a non-positive TopN.
const DefaultTopN = 3

// WordCount is one entry of the "most frequent words" list.
type WordCount struct {
	Word  string
	Count int
}

// Result holds the statistics calculated for a text.
type Result struct {
	Characters    int         // number of runes (Unicode characters), not bytes
	Bytes         int         // size of the text in bytes (UTF-8)
	Words         int         // total number of words
	UniqueWords   int         // number of distinct words (case-insensitive)
	Sentences     int         // sentences, separated by '.', '!' or '?'
	Lines         int         // number of lines
	AvgWordLength float64     // average word length in runes
	LongestWord   string      // first longest word (in runes)
	TopWords      []WordCount // most frequent words, most frequent first
}

// Clone returns a deep copy of the result.
//
// Result contains a slice. Copying the struct copies only the slice header,
// so both copies would share the same underlying array. Clone allocates a new
// array so that the caller cannot modify data stored inside the application.
func (r Result) Clone() Result {
	c := r
	if r.TopWords != nil {
		c.TopWords = make([]WordCount, len(r.TopWords))
		copy(c.TopWords, r.TopWords)
	}
	return c
}

// Analyzer calculates text statistics. The zero value is ready to use.
type Analyzer struct {
	// TopN is how many of the most frequent words to report.
	// Values <= 0 mean DefaultTopN.
	TopN int
}

// Validate checks that the text can be analyzed.
//
// Value receiver: Analyzer is a small configuration struct and the method
// does not modify it.
func (a Analyzer) Validate(text string) error {
	if !utf8.ValidString(text) {
		return ErrInvalidUTF8
	}
	if strings.TrimSpace(text) == "" {
		return ErrEmptyText
	}
	if len(splitWords(text)) == 0 {
		return ErrNoWords
	}
	return nil
}

// Analyze validates the text and calculates its statistics.
func (a Analyzer) Analyze(text string) (Result, error) {
	if err := a.Validate(text); err != nil {
		return Result{}, err
	}

	words := splitWords(text)

	freq := make(map[string]int, len(words))
	totalRunes := 0
	longest := ""
	for _, w := range words {
		freq[strings.ToLower(w)]++

		n := utf8.RuneCountInString(w)
		totalRunes += n
		if n > utf8.RuneCountInString(longest) {
			longest = w
		}
	}

	return Result{
		Characters:    utf8.RuneCountInString(text),
		Bytes:         len(text),
		Words:         len(words),
		UniqueWords:   len(freq),
		Sentences:     countSentences(text),
		Lines:         countLines(text),
		AvgWordLength: float64(totalRunes) / float64(len(words)),
		LongestWord:   longest,
		TopWords:      topWords(freq, a.topN()),
	}, nil
}

func (a Analyzer) topN() int {
	if a.TopN <= 0 {
		return DefaultTopN
	}
	return a.TopN
}

// isWordRune reports whether r can be part of a word.
// Works for any alphabet: Latin, Cyrillic, etc.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

// splitWords splits the text into words: maximal sequences of letters/digits.
func splitWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !isWordRune(r) })
}

// countSentences counts sentences. A sentence ends with '.', '!' or '?'.
// Repeated terminators ("Wait...", "What?!") end only one sentence, and a
// trailing fragment without a terminator still counts as a sentence.
func countSentences(text string) int {
	count := 0
	inSentence := false
	for _, r := range text {
		switch {
		case isWordRune(r):
			inSentence = true
		case r == '.' || r == '!' || r == '?':
			if inSentence {
				count++
				inSentence = false
			}
		}
	}
	if inSentence {
		count++
	}
	return count
}

// countLines counts lines; trailing newlines do not create empty lines.
func countLines(text string) int {
	trimmed := strings.TrimRight(text, "\r\n")
	return strings.Count(trimmed, "\n") + 1
}

// topWords returns the n most frequent words. Ties are broken alphabetically
// so the result is deterministic (map iteration order in Go is random).
func topWords(freq map[string]int, n int) []WordCount {
	list := make([]WordCount, 0, len(freq))
	for w, c := range freq {
		list = append(list, WordCount{Word: w, Count: c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Word < list[j].Word
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

package textanalysis

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr error // nil means the text is valid
	}{
		{"valid text", "Hello Go world", nil},
		{"valid cyrillic", "Привет, мир", nil},
		{"empty", "", ErrEmptyText},
		{"spaces only", "   ", ErrEmptyText},
		{"tabs and newlines", "\t\n \r\n", ErrEmptyText},
		{"punctuation only", "?!... ---", ErrNoWords},
		{"invalid utf8", "abc\xff", ErrInvalidUTF8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Analyzer{}.Validate(tc.text)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate(%q) error = %v, want %v", tc.text, err, tc.wantErr)
			}
		})
	}
}

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Result
	}{
		{
			name: "example from the specification",
			text: "Hello Go world",
			want: Result{
				Characters: 14, Bytes: 14, Words: 3, UniqueWords: 3,
				Sentences: 1, Lines: 1, AvgWordLength: 4, LongestWord: "Hello",
				TopWords: []WordCount{{"go", 1}, {"hello", 1}, {"world", 1}},
			},
		},
		{
			name: "repeated words are counted case-insensitively",
			text: "Go is fun. Go is fast! Is Go simple?",
			want: Result{
				Characters: 36, Bytes: 36, Words: 9, UniqueWords: 5,
				Sentences: 3, Lines: 1, AvgWordLength: 25.0 / 9, LongestWord: "simple",
				TopWords: []WordCount{{"go", 3}, {"is", 3}, {"fast", 1}},
			},
		},
		{
			name: "cyrillic: characters are runes, not bytes",
			text: "Привет, мир!",
			want: Result{
				Characters: 12, Bytes: 21, Words: 2, UniqueWords: 2,
				Sentences: 1, Lines: 1, AvgWordLength: 4.5, LongestWord: "Привет",
				TopWords: []WordCount{{"мир", 1}, {"привет", 1}},
			},
		},
		{
			name: "multiple lines, ellipsis ends one sentence",
			text: "Wait...\nWhat?!\nok\n",
			want: Result{
				Characters: 18, Bytes: 18, Words: 3, UniqueWords: 3,
				Sentences: 3, Lines: 3, AvgWordLength: 10.0 / 3, LongestWord: "Wait",
				TopWords: []WordCount{{"ok", 1}, {"wait", 1}, {"what", 1}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Analyzer{}.Analyze(tc.text)
			if err != nil {
				t.Fatalf("Analyze(%q) unexpected error: %v", tc.text, err)
			}
			// Floats are compared with a tolerance, the rest exactly.
			if math.Abs(got.AvgWordLength-tc.want.AvgWordLength) > 1e-9 {
				t.Errorf("AvgWordLength = %v, want %v", got.AvgWordLength, tc.want.AvgWordLength)
			}
			got.AvgWordLength, tc.want.AvgWordLength = 0, 0
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Analyze(%q)\n got  %+v\n want %+v", tc.text, got, tc.want)
			}
		})
	}
}

func TestAnalyzeRejectsInvalidInput(t *testing.T) {
	_, err := Analyzer{}.Analyze("  ")
	if !errors.Is(err, ErrEmptyText) {
		t.Fatalf("error = %v, want ErrEmptyText", err)
	}
}

func TestAnalyzeTopN(t *testing.T) {
	r, err := Analyzer{TopN: 1}.Analyze("b a b c b a")
	if err != nil {
		t.Fatal(err)
	}
	want := []WordCount{{"b", 3}}
	if !reflect.DeepEqual(r.TopWords, want) {
		t.Fatalf("TopWords = %v, want %v", r.TopWords, want)
	}
}

func TestResultCloneIsIndependent(t *testing.T) {
	orig := Result{TopWords: []WordCount{{"go", 2}}}
	c := orig.Clone()
	c.TopWords[0].Count = 100

	if orig.TopWords[0].Count != 2 {
		t.Fatalf("modifying the clone changed the original: %v", orig.TopWords)
	}
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// leadLine (answer.go:854, the finding's 0.0%) is the first line of a verb's output, for a
// row's why. The table pins the main path: of a verb's multi-line output, the first line,
// with the padding around it trimmed away.
func TestAnswerCoverLeadLineTakesTheFirstLineOfAVerbsOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"first of several lines", "REFUSED: the provider declined\nsecond line\nthird line", "REFUSED: the provider declined"},
		{"first line with no second", "the one line", "the one line"},
		{"padding before the first line trimmed, the line's own tail kept", "\n\t  the first line  \n\tthe second\n", "the first line  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, leadLine(tc.in))
		})
	}
}

// The function has no refusal of its own, so the empty path stands in for one: output that
// is empty, or nothing but whitespace, answers an empty why, never a line of padding.
func TestAnswerCoverLeadLineAnswersNothingWhenTheOutputHasNoFirstLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"empty output", ""},
		{"nothing but whitespace", " \t\n\r\n  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, leadLine(tc.in))
		})
	}
}

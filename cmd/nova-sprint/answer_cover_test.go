package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// leadLine is a verb's first output line, the why a row carries: it trims the
// whole output and cuts at the first newline, so a multi-line refusal collapses
// to its first line. The function cannot refuse an input — every string has a
// first line, and the empty string's is empty — so the cases below pin the main
// path and its boundaries: an empty input, whitespace only, a leading blank
// line and whitespace that only the whole-output trim reaches.
func TestAnswerCoverLeadLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, in, want string
	}{
		{name: "one line unchanged", in: "nova-sprint score: 3 rows", want: "nova-sprint score: 3 rows"},
		{name: "the first of many", in: "first\nsecond\nthird", want: "first"},
		{name: "outer whitespace trimmed", in: "  first\nsecond  ", want: "first"},
		{name: "an empty input", in: "", want: ""},
		{name: "whitespace only", in: "   \n\t\n ", want: ""},
		{name: "a leading blank line", in: "\n\nonly after blanks", want: "only after blanks"},
		{name: "inner trailing spaces kept", in: "kept  \nsecond", want: "kept  "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, leadLine(c.in))
		})
	}
}

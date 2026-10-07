package oneline

import (
	"math"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// security#30 L13: Cap panicked on inputs where the minimum one-byte budget
// reached or exceeded the input length (the unchecked s[cut]), on empty input
// with a non-positive budget (the same index), and on a minimum-int budget
// (n - widest overflowing). The contract is unchanged: never erase a nonempty
// tail, cut on a rune boundary, mark what was dropped, return the input
// unchanged when nothing needs to be dropped.
func TestCapNeverPanicsAndNeverErasesNonemptyInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"one byte, zero budget", "x", 0},
		{"one byte, negative budget", "x", -1},
		{"empty, zero budget", "", 0},
		{"empty, negative budget", "", -5},
		{"one byte, budget equals length", "x", 1},
		{"one multibyte rune, zero budget", "\u00e9", 0},
		{"one multibyte rune, negative budget", "\u4e2d", -1},
		{"short mixed, zero budget", "h\u00e9llo w\u00f6rld", 0},
		{"invalid utf8, zero budget", "a\xff\xfe", 0},
		{"invalid utf8, negative budget", "\xff\xfe\xfd", -3},
		{"minimum int budget", "hello", math.MinInt},
		{"minimum int budget on long input", "the quick brown fox jumps over the lazy dog", math.MinInt},
		{"one byte, minimum int budget", "x", math.MinInt},
		{"budget exactly the widest mark", "hello world this is long", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Cap(tc.in, tc.n)
			if len(tc.in) > 0 {
				require.NotEmpty(t, out, "Cap(%q, %d) erased a nonempty input to empty", tc.in, tc.n)
			}
			if utf8.ValidString(tc.in) {
				require.True(t, utf8.ValidString(out), "Cap(%q, %d) = %q, made valid UTF-8 invalid", tc.in, tc.n, out)
			}
		})
	}
}

// A real cut of a multibyte string cuts on a rune boundary and keeps the first
// rune whole. (The exact cuts and the truthful no-drop cases are rows of
// TestCapExamples.)
func TestCapCutsOnRuneBoundaryAndKeepsFirstRune(t *testing.T) {
	t.Parallel()

	out := Cap("\u4e2d\u6587\u5b57\u7b26\u4e32", 4)
	first, size := utf8.DecodeRuneInString(out)
	require.NotEqual(t, 0, size, "Cap(multibyte, 4) = %q, first rune not the original", out)
	require.Equal(t, '\u4e2d', first, "Cap(multibyte, 4) = %q, first rune not the original", out)
}

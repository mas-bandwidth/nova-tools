package oneline

import (
	"math"
	"testing"
	"unicode/utf8"
)

// security#30 L13: Cap panicked on inputs where the minimum one-byte budget
// reached or exceeded the input length (the unchecked s[cut]), on empty input
// with a non-positive budget (the same index), and on a minimum-int budget
// (n - widest overflowing). The contract is unchanged: never erase a nonempty
// tail, cut on a rune boundary, mark what was dropped, return the input
// unchanged when nothing needs to be dropped.
func TestCapNeverPanicsAndNeverErasesNonemptyInput(t *testing.T) {
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
			if len(tc.in) > 0 && len(out) == 0 {
				t.Fatalf("Cap(%q, %d) erased a nonempty input to empty", tc.in, tc.n)
			}
			if utf8.ValidString(tc.in) && !utf8.ValidString(out) {
				t.Fatalf("Cap(%q, %d) = %q, made valid UTF-8 invalid", tc.in, tc.n, out)
			}
		})
	}
}

// The truncation marker stays truthful: when nothing is actually dropped the
// input is returned unchanged, with no zero-dropped marker.
func TestCapZeroDropReturnsInputUnchanged(t *testing.T) {
	in := "hello"
	if out := Cap(in, len(in)); out != in {
		t.Fatalf("Cap(input, len(input)) = %q, want %q", out, in)
	}
	if out := Cap(in, 1000); out != in {
		t.Fatalf("Cap(input, 1000) = %q, want %q", out, in)
	}
	// A budget too small for the mark plus one rune still keeps the first rune,
	// and when that rune is the whole input, nothing is dropped.
	if out := Cap("\u4e2d", 0); out != "\u4e2d" {
		t.Fatalf("Cap(one rune, 0) = %q, want the input unchanged", out)
	}
}

// A real cut still cuts on a rune boundary and reports truthfully-dropped bytes.
func TestCapCutsOnRuneBoundaryAndKeepsFirstRune(t *testing.T) {
	// ASCII: the cut lands mid-string and the mark counts the rest.
	// widest for an 11-byte input is len("...+11B") = 7, so budget = 5-7 clamped to 1.
	out := Cap("hello world", 5)
	if out != "h...+10B" {
		t.Fatalf("Cap(\"hello world\", 5) = %q, want %q", out, "h...+10B")
	}
	// A budget that leaves room for the mark and more than one byte cuts mid-string.
	out = Cap("hello world hello", 12)
	if out != "hello...+12B" {
		t.Fatalf("Cap(\"hello world\", 12) = %q, want %q", out, "hello...+12B")
	}
	// The first rune of a multibyte string survives whole.
	out = Cap("\u4e2d\u6587\u5b57\u7b26\u4e32", 4)
	first, size := utf8.DecodeRuneInString(out)
	if size == 0 || first != '\u4e2d' {
		t.Fatalf("Cap(multibyte, 4) = %q, first rune not the original", out)
	}
}

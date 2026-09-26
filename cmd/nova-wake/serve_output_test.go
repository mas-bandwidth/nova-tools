package main

import (
	"testing"
)

// The tail keeps the last max bytes of what was written, whatever came
// before, as one line.
func TestTailWriterKeepsTheLastBytes(t *testing.T) {
	t.Parallel()
	w := &tailWriter{max: 8}
	for _, s := range []string{"abc", "def\n", "ghij", "kl"} {
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d %v", s, n, err)
		}
	}
	if got := w.line(); got != "f ghijkl" {
		t.Fatalf("line = %q, want the last 8 bytes as one line", got)
	}
	if got := (&tailWriter{max: 8}).line(); got != "" {
		t.Fatalf("an empty tail is %q, want empty", got)
	}
}

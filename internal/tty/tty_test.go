package tty

import (
	"os"
	"path/filepath"
	"testing"
)

// A file that is not a terminal is not one and has no size: a regular file,
// the null device (a character device that is not a terminal) and a file
// already closed. No test opens a terminal.
func TestNothingButATerminalIsOneOrHasASize(t *testing.T) {
	t.Parallel()
	regular, err := os.Create(filepath.Join(t.TempDir(), "plain"))
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	closed, err := os.Create(filepath.Join(t.TempDir(), "closed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]*os.File{"a regular file": regular, "the null device": null, "a closed file": closed} {
		if IsTerminal(f) {
			t.Errorf("%s is a terminal", name)
		}
		if rows, cols := Size(f); rows != 0 || cols != 0 {
			t.Errorf("%s has a size, %d rows by %d columns", name, rows, cols)
		}
	}
}

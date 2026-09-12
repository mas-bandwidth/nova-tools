//go:build !windows

package main

import (
	"os"
	"testing"
)

// makeUnreadable makes an EXISTING file refuse os.Open, and returns the release that
// gives it back. It is the one mechanism rule 3's tests have: a source that is listed and
// cannot be read.
//
// On unix that is mode 000, which root ignores -- so a run as root skips, naming why.
// The windows file beside this one does the same thing by the only means that OS has.
func makeUnreadable(t *testing.T, path string) (release func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so a refused read cannot be produced here")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	done := false
	release = func() {
		if done {
			return
		}
		done = true
		_ = os.Chmod(path, 0o644)
	}
	t.Cleanup(release)
	// PROVE it, here as on windows: a fixture that cannot be observed to work is the bug
	// this helper exists to catch.
	if f, err := os.Open(path); err == nil {
		f.Close()
		release()
		t.Fatalf("mode 000 did not make %s refuse a read", path)
	}
	return release
}

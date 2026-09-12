//go:build windows

package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests beside this one model windows with winDir, because the body that runs is
// darwin's and a Mac cannot walk a windows path. This one runs on the real thing, so that
// the model cannot drift away from filepath's own answer without a windows CI job saying so.
func TestWinDirAgreesWithFilepathDir(t *testing.T) {
	for _, p := range []string{
		`C:\Users\runneradmin\AppData\Local\Temp\job\w`,
		`C:\Users`,
		`C:\`,
		`\a\b`,
		`\`,
	} {
		if got, want := winDir(p), filepath.Dir(p); got != want {
			t.Errorf("winDir(%q) = %q, filepath.Dir = %q; the model has drifted", p, got, want)
		}
	}
}

// The 600s timeout of run 34663812025, on the platform it happened on and with a deadline of
// its own: a test must never wait without one.
func TestAncestorsTerminatesOnThisWindowsMachine(t *testing.T) {
	dir := t.TempDir()
	done := make(chan []string, 1)
	go func() { done <- Ancestors(filepath.Join(dir, "w", "sub")) }()
	select {
	case got := <-done:
		if len(got) == 0 {
			t.Fatalf("no ancestors for a path under %q", dir)
		}
		for _, d := range got {
			if filepath.Dir(d) == d {
				t.Errorf("the volume root %q is an ancestor; it is granted above, not walked to", d)
			}
		}
		if !strings.HasPrefix(got[len(got)-1], filepath.VolumeName(dir)) {
			t.Errorf("ancestors = %v, which does not stay on the volume of %q", got, dir)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ancestors did not return in 10s: the walk up the tree has no stop above the volume root")
	}
}

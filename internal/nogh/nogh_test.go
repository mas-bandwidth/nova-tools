package nogh

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWrittenGhRefuses: the written gh exits 2 with Refusal and is found
// first through PathFirst, ahead of a gh later on PATH.
func TestWrittenGhRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusing gh is a /bin/sh script")
	}
	if strings.Contains(Refusal, "'") {
		t.Fatalf("Refusal holds a single quote, which Script cannot quote")
	}
	dir := filepath.Join(t.TempDir(), "shim")
	path, err := Install(dir)
	if err != nil || path != filepath.Join(dir, Name) {
		t.Fatalf("Install = %q, %v", path, err)
	}
	if _, err := Install(dir); err != nil {
		t.Fatalf("a second Install over the first: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("shim dir holds %d entries, want only gh (no temp left behind)", len(entries))
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	cmd := exec.Command(sh, "-c", "gh api user")
	cmd.Env = PathFirst([]string{"PATH=/usr/bin:/bin"}, dir)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || strings.TrimSpace(string(out)) != Refusal {
		t.Fatalf("gh: err=%v out=%q, want exit 2 and the refusal", err, out)
	}
}

func TestPathFirst(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := PathFirst([]string{"A=1", "PATH=/x", "PATH="}, "/s")
	want := []string{"A=1", "PATH=/s" + sep + "/x", "PATH=/s"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("PathFirst = %q, want %q", got, want)
	}
	if got := PathFirst([]string{"A=1"}, "/s"); strings.Join(got, "|") != "A=1|PATH=/s" {
		t.Fatalf("no PATH: %q", got)
	}
	if got := PathFirst([]string{"PATH=/x"}, ""); got[0] != "PATH=/x" {
		t.Fatalf("empty dir changed env: %q", got)
	}
}

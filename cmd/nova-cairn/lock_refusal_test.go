package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A lock refusal names its own cause and next action, so it is not sent on to
// the help banner, which says nothing more about it: exit 2, one line, and no
// "run: nova-cairn help" at its end.
func TestALockRefusalIsNotSentToTheHelpBanner(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this account")
	}
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "s1.md"), []byte("# s1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(store, 0o755)
	if f, err := os.OpenFile(filepath.Join(store, "probe"), os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
		t.Skip("the directory stayed writable")
	}
	code, _, errOut := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e1", "--text", "x", "--publish", "manual")
	if code != 2 || strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "cannot create the lock file") {
		t.Fatalf("append in a read-only store: %d %q", code, errOut)
	}
	if strings.Contains(errOut, "run: nova-cairn help") {
		t.Fatalf("the refusal names its own next action and needs no pointer to the help: %q", errOut)
	}
}

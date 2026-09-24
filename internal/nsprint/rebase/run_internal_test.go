package rebase

import (
	"os"
	"path/filepath"
	"testing"
)

// The card runs through /bin/sh, never by exec'ing the file just written: a
// script with no execute bit still runs, which is the property that keeps the
// Linux ETXTBSY race (fork/exec of a file a concurrent fork holds open for
// writing, dev run 36015701004) out of the runner. Exec'ing the file itself
// refuses a 0644 script with "permission denied".
func TestExecScriptRunsTheCardThroughShNotExec(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "card.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"ran $1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code, err := execScript(path, "arg1")
	if err != nil || code != 0 {
		t.Fatalf("execScript = code %d err %v stderr %q; want the card run by sh", code, err, stderr)
	}
	if stdout != "ran arg1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "ran arg1\n")
	}
}

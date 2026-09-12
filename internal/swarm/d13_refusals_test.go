package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// DOGFOOD D13 (2026-09-11): a clean read printed `refusals=27` and the harness had refused
// nothing at all.
//
// `refusals=<n>` is a DIAGNOSIS (SPEC-SWARM.md:98-100, 775): "reads the harness log for the
// HARNESS'S OWN refusal lines", so that a `plan-only` result beside `refusals=1` says why.
// The marks included the bare English words `refused` and `refusing`, and a harness log is
// a transcript: it holds the diff the worker read, the git log it printed, its own prose
// and the RESULT.md it wrote. All 27 of that run's matches were content -- commit subjects
// ("a refused carried list is a failed poll"), a test name
// (`TestARefreshWhoseCarriedListWasRefusedIsAFailedPoll`), a quoted `INBOX REFUSED` fixture.
// A diagnosis that fires on the word for the thing, wherever it appears, is not a
// diagnosis: it is noise in the one field a reader was told to trust.
//
// What counts is a DENIED OPERATION in the harness's or the OS's own words. A worker
// writing about refusals is doing the job it was given.
func TestOnlyADeniedOperationIsARefusal(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "harness.log")

	// A clean read that talks about refusals all day long: zero.
	prose := "" +
		"8659d36 nova-wake: a refused carried list is a failed poll, not a quiet one\n" +
		"+func TestARefreshWhoseCarriedListWasRefusedIsAFailedPoll(t *testing.T) {\n" +
		"+	const standing = \"bus:line:INBOX REFUSED a line that stands\"\n" +
		"| the pass's own refused state write is pinned | green | internal/merge/pass.go:88 |\n" +
		"the real `nova-bus inbox --advance` refused every advance and the watcher was blind\n" +
		"a refusing bus is broken and not a change; WAKE REFUSED: --advance-cursor\n"
	write(t, log, prose)
	if got := CountRefusals(log); got != 0 {
		t.Errorf("a clean read that writes ABOUT refusals refused nothing: refusals=%d, want 0", got)
	}

	// And the refusal the count exists for, in the shapes a harness and an OS write it.
	for _, line := range []string{
		"fake harness: read of /etc/somewhere: permission denied (refused)",
		"Error: EACCES: permission denied, open '/tmp/scratch.txt'",
		"open /var/db/x: operation not permitted",
		"tool refused: the path is outside the working directory",
		"EPERM: operation not permitted, unlink '/x'",
		"write /usr/local/y: read-only file system",
	} {
		write(t, log, prose+line+"\n")
		if got := CountRefusals(log); got != 1 {
			t.Errorf("a denied operation is the refusal the diagnosis names: %q counted %d, want 1", line, got)
		}
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DEFECT THIS IS FOR. `nova-wake serve --as Johnny` is a process started BESIDE a
// line, not the line itself. It polls by running `nova-bus wait`, and every wait writes
// and pushes a BEAT for --as <name>; Johnny's own harness loop beats for Johnny too. The
// two writers collide on from-johnny/BEAT forever, and the measured failure is #1517:
//
//	WAKE BUS LINE WAIT NOTE beat push failed: the rebase over what arrived conflicted on
//	from-johnny/BEAT, which this tool will not settle for you
//
// The asked shape is a READ-ONLY poll: `wait --no-beat` fetches, lists and beats nothing.
// The control is in the SAME test on purpose: a fix that made every wait stop beating
// would satisfy "no BEAT written" and would break presence everywhere, so the default
// must still write and commit its BEAT in the same run of this test.
func TestIssue1517WaitWithNoBeatWritesNoBeat(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	beat := filepath.Join(checkout, "from-ada", "BEAT")
	if _, err := os.Stat(beat); !os.IsNotExist(err) {
		t.Fatalf("the fixture already holds a BEAT (%v); this test needs a line with none", err)
	}
	beatCommits := func() string {
		return strings.TrimSpace(gitIn(t, checkout, "log", "--format=%H", "--", "from-ada/BEAT"))
	}
	if before := beatCommits(); before != "" {
		t.Fatalf("a BEAT commit already exists before the wait: %s", before)
	}

	poll := func(extra ...string) result {
		return invoke(t, "", waitFlags(checkout, "Ada", "300ms", extra...)...)
	}

	// THE FLAG: no entry beat, no tick beat, no beat commit, no beat file.
	quiet := poll("--no-beat").mustCode(t, 0).mustContain(t, "stdout", "WAIT TIMEOUT")
	if _, err := os.Stat(beat); !os.IsNotExist(err) {
		t.Fatalf("wait --no-beat wrote from-ada/BEAT: %v", err)
	}
	if after := beatCommits(); after != "" {
		t.Fatalf("wait --no-beat committed a BEAT: %s", after)
	}

	// THE CONTROL: the default still beats, so the guard reached the flag and not the
	// default path.
	control := poll().mustCode(t, 0).mustContain(t, "stdout", "WAIT TIMEOUT")
	if _, err := os.Stat(beat); err != nil {
		t.Fatalf("the control wait (no --no-beat) did not write from-ada/BEAT: %v", err)
	}
	if after := beatCommits(); after == "" {
		t.Fatalf("the control wait (no --no-beat) committed no BEAT:\n%s", control.stdout)
	}

	// THE SAME CALL OTHERWISE: the exit code and the terminal WAIT line's cursor are the
	// same with the flag as without -- the flag turns off the beat and nothing else.
	if quiet.code != control.code {
		t.Fatalf("exit with --no-beat = %d, without = %d", quiet.code, control.code)
	}
	cursor := func(out string) string {
		return field(t, out[strings.Index(out, "WAIT TIMEOUT"):], "cursor=")
	}
	if got, want := cursor(quiet.stdout), cursor(control.stdout); got != want {
		t.Fatalf("the WAIT TIMEOUT cursor differs: --no-beat %q, default %q", got, want)
	}
}

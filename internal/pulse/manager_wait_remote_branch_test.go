package pulse

// manager-wait-passes-remote-and-branch (#1879): the one `nova-bus wait` a cycle makes must
// carry `--remote`/`--branch`, because `nova-bus wait` itself refuses to guess a remote to
// fetch from. Without them every cycle of a real shift fails identically until three strikes
// end it early.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestManagerWaitBusPassesRemoteAndBranch(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "wait-timeout=3m\nfloor=0\n")

	var out, errs bytes.Buffer
	stamp := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	code := Manager(ManagerInput{
		Policy: p, Queue: b.queue, Roots: b.roots, Bus: b.bus, As: "Rowan",
		Remote: "origin", Branch: "bus-main",
		Hours: 0, Max: 20, Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return stamp },
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, out.String(), errs.String())
	}

	lines := strings.Split(strings.TrimSpace(b.argv(t)), "\n")
	if len(lines) != 1 {
		t.Fatalf("one cycle ran %d children %v; the wait is the only call", len(lines), lines)
	}
	want := "nova-bus wait --bus " + b.bus + " --as Rowan --timeout 3m --advance --remote origin --branch bus-main"
	if lines[0] != want {
		t.Fatalf("nova-bus wait argv =\n  %q\nwant\n  %q", lines[0], want)
	}
}

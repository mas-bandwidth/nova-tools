package main

import (
	"errors"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"strings"
	"testing"
)

// The footguns a new line walked into on a first run, each with the line that would have
// saved them. Lessons 30, 96 and 110: a tool that could not look says so; lesson 15: a
// remedy is a command a person can run.

// FG-1: a failed host or git call became base_state=UNKNOWN and exit 0 with no other line.
// A new line reads STATUS OK over a lane the tool cannot see.
func TestStatusSaysWhenItCouldNotReadTheBase(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.Err = errors.New("gh: could not resolve host github.invalid")
	exit, stdout, stderr := l.run("status", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("status REPORTS and exits 0 whatever the lane holds: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "base_state=UNKNOWN")
	// The NOTE names WHAT could not be read, so UNKNOWN is a sentence and not a shrug.
	contains(t, stderr, "STATUS NOTE")
	contains(t, stderr, "could not be read")
	contains(t, stderr, "could not resolve host")
}

// FG-4: `packet --pr N` for an entry that IS in the lane printed `PACKET OK entries=0
// holds=0`, exit 0, no reason -- and `--all` silently meant "entries with needs_read=yes".
// A coordinator sees one entry of three and concludes the rest need no read.
func TestPacketSaysWhyAnEntryWasSkipped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// One entry that needs a read, one that does not.
	setupPR(t, l, 951, "feature-a", "a.txt", false) // needs_read=no
	oid := l.branch("feature-b", "b.txt", "b\n", "b")
	l.host.PRs[952] = merge.PR{Number: 952, Author: "pat", Base: "main", HeadRef: "feature-b",
		HeadOID: oid, Mergeable: "MERGEABLE"}
	l.host.SetChecks(oid, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "952", "--needs-read"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	// Asked for by name, and skipped: the reason is printed.
	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--who", "emma", "--pr", "951")
	if exit != 0 {
		t.Fatalf("packet reports: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "PACKET OK entries=0")
	contains(t, stderr, "PACKET NOTE entry=951")
	contains(t, stderr, "needs_read=no")
	// And --all says how many of the lane it passed over.
	_, stdout, stderr = l.run("packet", "--lane", l.lane, "--who", "emma", "--all")
	contains(t, stdout, "PACKET ENTRY entry=952")
	contains(t, stderr, "PACKET NOTE entry=951")
}

// FG-6: after STOP OK, `status` printed a normal lane and `dry-run` printed stopped=0.
// Only run read the file, so the one signal that says "start nothing new" was invisible to
// every verb a person checks first.
func TestAStoppedLaneSaysSoOnEveryVerb(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("stop", "--lane", l.lane); exit != 0 {
		t.Fatalf("stop: %s", errb)
	}
	for _, args := range [][]string{
		{"status", "--lane", l.lane},
		{"dry-run", "--lane", l.lane},
		{"add", "--lane", l.lane, "--pr", "952"},
		{"packet", "--lane", l.lane, "--who", "emma", "--all"},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, stdout, stderr := l.run(args...)
			if !strings.Contains(stdout+stderr, "a stop file is present in this lane") {
				t.Errorf("%s says nothing about the stop file:\n%s\n%s", args[0], stdout, stderr)
			}
		})
	}
}

// FG-8: an unreachable host was exit 1 -- the code for "the tool ran and said no" -- with
// the whole gh body on one line and a RUN NOTE about a RED entry that did not exist. A
// tool that could not look is exit 2, and its remedy names the call that failed.
func TestAnUnreachableHostIsExitTwoWithABoundedLine(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.Err = errors.New("gh: HTTP 404: Not Found (https://api.github.com/repos/o/n/commits/main/check-runs)\n" +
		strings.Repeat("a very long body the host returned, which is not a line of this tool's grammar. ", 40))
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 2 {
		t.Fatalf("a tool that could not look is exit 2, not 1: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN REFUSED")
	for _, line := range strings.Split(stdout+stderr, "\n") {
		if len(line) > 1200 {
			t.Errorf("a line of %d bytes: the host's body is DATA and it is capped, not pasted whole\n%s", len(line), line[:200])
		}
	}
	// The one remedy is about the host, not about a RED entry the lane does not have.
	absent(t, stdout, "a RED entry needs its failing job read")
}

// FG-9: two remedies were not commands. `gate it with --head <sha> --base-sha <sha>
// --merge <sha> --from <lane>/repo` has no verb, and no --from flag exists on anything.
func TestEveryRemedyIsACommandAPersonCanRun(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "RUN NOTE ") {
			continue
		}
		note := strings.TrimPrefix(line, "RUN NOTE ")
		if !strings.HasPrefix(note, "nova-merge ") {
			t.Errorf("a remedy is a command a person can run, and this one has no verb:\n%s", note)
		}
		if strings.Contains(note, "--from ") {
			t.Errorf("the remedy names --from, which is a flag on no verb of this tool:\n%s", note)
		}
	}
}

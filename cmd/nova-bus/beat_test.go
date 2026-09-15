package main

import (
	"strings"
	"testing"
)

// THE BEAT NEVER BLOCKS SEND (#488, #459).
//
// `wait` writes its lane's BEAT on every poll tick so that a line which is merely waiting
// still reads awake to `nova-wake awake`. That file is the WAITER'S OWN machinery, and
// every friend on the bus hit the day it stopped being treated as such: a wait that
// returned inside one --beat left BEAT modified and uncommitted, and the next `send` ran
// checkoutReady and refused over "changes that are not this note" -- naming a file the
// writer never touched, in a checkout they had done nothing to. The friend's answer did
// not go out. The same file committed but not yet pushed is the second half of it: a local
// commit touching only from-<me>/BEAT leaves the branch ahead of the remote, which is the
// state the ahead-of-remote guard exists to refuse.
//
// The sentences these tests hold: a wait leaves the checkout clean, a send after a wait
// succeeds, and a send over an unpushed beat commit carries the beat out with the note
// rather than refusing. Every one of them is about the state one verb LEAVES for the next,
// which is why no single-verb test caught it.

// dirty is the assertion all three share: `git status --porcelain` empty, reported with
// what is in the way, because a verb that leaves the checkout dirty has broken the next
// verb whatever its own output said.
func mustBeClean(t *testing.T, checkout, after string) {
	t.Helper()
	if out := strings.TrimSpace(gitIn(t, checkout, "status", "--porcelain", "--untracked-files=all")); out != "" {
		t.Fatalf("%s left the checkout dirty, so the next verb refuses over a file the friend never touched:\n%s", after, out)
	}
}

// A wait records its own beat; it does not leave it lying in the working tree. The beat is
// written every tick and committed every tick -- the PUSH is the part bounded by --beat,
// because the push is the only part that costs somebody's server -- so a wait that returns
// after one tick or after a hundred hands the next verb a clean checkout either way.
func TestWaitLeavesCheckoutClean(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)
	mustBeClean(t, checkout, "the fixture")

	// A timeout well inside the default --beat of sixty seconds: the case where the old
	// code wrote the beat and never committed it.
	invoke(t, "", waitFlags(checkout, "Ada", "300ms")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT TIMEOUT")

	mustBeClean(t, checkout, "wait")
	// And the beat is RECORDED, not merely absent: a wait that wrote no beat at all would
	// also pass the cleanliness assertion and would have broken presence instead.
	if out := gitIn(t, checkout, "log", "--format=%s", "-1"); !strings.Contains(out, "beat ada") {
		t.Fatalf("the wait's beat is not committed; the last commit is %q", strings.TrimSpace(out))
	}
}

// THE SEQUENCE THAT BROKE: wait, then send. The send must go out, and must leave the
// checkout clean behind it -- whether the wait's beat reaches it as an uncommitted file (a
// wait killed mid-tick, an older beat still in the tree) or as a commit of its own.
func TestSendAfterWaitBeatSucceeds(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	invoke(t, "", waitFlags(checkout, "Ada", "300ms")...).mustCode(t, 0)

	// Whatever the wait left, a send is not refused over the caller's own beat. The extra
	// dirty beat here is the killed-wait case, written on top of whatever the wait left:
	// send folds the caller's own BEAT into its commit rather than refusing.
	writeFile(t, checkout, "from-ada/BEAT", "2026-09-09T12:35:10Z - until=2026-09-09T12:45:10Z\n")

	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "pushed=true")

	mustBeClean(t, checkout, "send")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the note is not on the remote:\n%s", files)
	}
}

// A local commit touching only from-<me>/BEAT leaves the branch ahead of the remote. That
// is the tool's own work, left behind by a beat whose push did not land, and the next send
// CARRIES it: a push publishes the branch, and the branch is the note plus the beat. The
// refusal the guard is for -- somebody's unfinished work published under a name they did
// not run -- is a commit that is not the caller's own beat, and that one still refuses.
func TestSendFoldsUnpushedOwnBeatCommit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	// The state a beat push that could not land leaves: committed as Ada, carrying the
	// tool's trailer, touching from-ada/BEAT and nothing else, not on the remote.
	writeFile(t, checkout, "from-ada/BEAT", "2026-09-09T12:35:00Z - until=2026-09-09T12:45:00Z\n")
	gitIn(t, checkout, "add", "--", "from-ada/BEAT")
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com",
		"commit", "-q", "-m", "beat ada\n\nNova-Bus: beat\n")
	if ahead := strings.TrimSpace(gitIn(t, checkout, "rev-list", "--count", "origin/main..HEAD")); ahead != "1" {
		t.Fatalf("the fixture is not one commit ahead of the remote, it is %s", ahead)
	}

	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "pushed=true")

	mustBeClean(t, checkout, "send")
	// Both landed: the note the caller wrote, and the beat that was waiting behind it.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-") {
		t.Fatalf("the note is not on the remote:\n%s", files)
	}
	if !strings.Contains(files, "from-ada/BEAT") {
		t.Fatalf("the beat commit was not carried out with the note:\n%s", files)
	}
	if ahead := strings.TrimSpace(gitIn(t, checkout, "rev-list", "--count", "origin/main..HEAD")); ahead != "0" {
		t.Fatalf("the checkout is still %s commits ahead of the remote after a pushed send", ahead)
	}
}

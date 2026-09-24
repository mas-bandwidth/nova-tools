package main

import (
	"os"
	"path/filepath"

	"strings"
	"testing"
)

// The footguns a new line walked into on a first run, each with the line that would have
// saved them. Lessons 30, 96 and 110: a tool that could not look says so; lesson 15: a
// remedy is a command a person can run.

// FG-2: `read` recorded and PUSHED an immutable approve for an entry not in the lane, and
// for a head that is not a commit, and said nothing about either. A typo'd entry records an
// approve nothing will ever fold, and the record cannot be taken back.
//
// It is a NOTE and not a refusal because rule 22 says the lane's order is the
// coordinator's and is NOT shared: a reader on another machine has a lane listing none of
// the entries and records for them anyway.
func TestAReadNamesAnEntryThisLaneDoesNotHold(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--branch", "nosuch", "--who", "emma",
		"--head", strings.Repeat("a", 40), "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK")
	contains(t, stderr, "READ NOTE entry=nosuch")
	contains(t, stderr, "does not list it")
}

// And a head the lane's clone does not hold is NAMED. It may simply not be fetched yet, so
// it is a note and not a wall -- but "READ OK ... pushed=true" for forty zeros said
// nothing at all.
func TestAReadNamesAHeadNoCloneHolds(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", strings.Repeat("0", 40), "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stderr, "READ NOTE")
	contains(t, stderr, "no commit with this sha")
}

// FG-5: `add --pr N` defaults needs_read=no -- the direction that merges with zero reads --
// as a field and not a warning, and the number was checked against nothing.
func TestAddSaysWhatNeedsReadNoMeansAndChecksTheNumber(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	_, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", "952")
	contains(t, stdout, "ADD OK")
	contains(t, stderr, "ADD NOTE")
	contains(t, stderr, "needs_read=no")
	contains(t, stderr, "--needs-read")
	// A number no host could ever answer for is refused rather than queued forever, AND
	// THE REFUSAL IS THE ONE FOR THAT MISTAKE: a flag nobody typed is a missing argument,
	// a number typed and wrong is a wrong number, and the two sentences were behind one
	// `*pr < 1` where the second could never print. Asserting only "--pr" could not tell.
	for _, typed := range []string{"0", "-3"} {
		exit, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", typed)
		if exit != 2 {
			t.Fatalf("a pull request number is positive: --pr %s exit %d\n%s\n%s", typed, exit, stdout, stderr)
		}
		contains(t, stderr, "which is positive, got "+typed)
		absent(t, stderr, "refusing to guess")
	}
	// And a --pr nobody typed is the missing-argument sentence, not the number one.
	exit, stdout, stderr := l.run("add", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("add with no --pr: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "refusing to guess")
	absent(t, stderr, "which is positive")
}

// FG-7: `GATE OK ... in_lane=true` means "the ENTRY is in the lane", not "the merge object
// is in the lane's clone" -- which is the phrase the tool's own help uses for the
// predicate. A green gate for a merge commit held by neither the clone nor the remote
// printed in_lane=true newest=true: a green receipt for a useless record.
func TestAGateNamesAMergeObjectNoCloneHolds(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	ghost := strings.Repeat("b", 40)
	exit, stdout, stderr := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", ghost, "--verdict", "green", "--summary", l.summary("ghost"))
	if exit != 0 {
		t.Fatalf("gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "GATE OK entry=951")
	contains(t, stderr, "GATE NOTE")
	contains(t, stderr, "no commit with this sha")
	contains(t, stderr, "will not satisfy")
}

// FG-3: a refused `init` left a half-built lane and poisoned every retry. checkout() ran
// git init, wrote .gitignore, committed and pushed BEFORE merge.Init wrote state.json, and
// cleaned nothing up -- so the second attempt failed with "On branch nova-merge/lane", a
// refusal about nothing, and the directory was permanently un-initializable.
func TestARefusedInitLeavesNothingBehindAndRetriesTheSame(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.urlFor = func(string) string { return filepath.Join(l.dir, "no-such-repository.git") }
	lane := filepath.Join(l.dir, "fresh")
	first := ""
	for i := 0; i < 2; i++ {
		exit, stdout, stderr := l.run("init", "--lane", lane, "--repo", "o/n", "--base", "main",
			"--lane-branch", "nova-merge/lane")
		if exit == 0 {
			t.Fatalf("this fixture points init at a repository that is not there: exit 0\n%s", stdout)
		}
		contains(t, stderr, "INIT REFUSED")
		if i == 0 {
			first = stderr
			// Nothing half-built: the directory init made is gone.
			if _, err := os.Stat(lane); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(lane)
				t.Fatalf("a refused init left %v behind in %s", entries, lane)
			}
			continue
		}
		// THE SAME REFUSAL, the second time: a retry that says something different is a
		// retry that is refusing about the wreckage rather than about the cause.
		if stderr != first {
			t.Errorf("the retry gives a different refusal, so the first one left state behind:\nfirst: %s\nthen:  %s", first, stderr)
		}
	}
}

package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded tests 5, 18 and 21, end to end: hosted green admits, the integration commit is
// built, the gate proves THAT OBJECT by sha, and publication is a compare-and-swap push
// that moves the base to it and to nothing else.

// setupPR is the shape every test here starts from: a lane, one pull request entry whose
// head is a real commit at the fixture remote, and a base with green checks.
func setupPR(t *testing.T, l *lab, n int, branch, file string, needsRead bool) (oid string) {
	t.Helper()
	l.init("main")
	oid = l.branch(branch, file, "the "+branch+" change\n", "a change on "+branch)
	l.host.PRs[n] = merge.PR{Number: n, Author: "pat", Base: "main", HeadRef: branch,
		HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/" + branch}
	l.host.SetChecks(oid, 3, 0)
	l.host.SetChecks(l.baseSHA(), 3, 0)
	args := []string{"add", "--lane", l.lane, "--pr", itoa(n)}
	if needsRead {
		args = append(args, "--needs-read")
	}
	if exit, _, errb := l.run(args...); exit != 0 {
		t.Fatalf("add: exit %d: %s", exit, errb)
	}
	return oid
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestOnlyTheGatedObjectIsPublished(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	base := l.baseSHA()

	// Hosted green is a CANDIDATE: it earns the entry NEEDS-GATE, the build and the gate
	// command on RUN NOTE, and nothing else.
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("a lane waiting on a gate is not a failure: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	// The entry needs a read AND a gate; the state names the person-shaped blocker and
	// the pass builds the integration commit anyway, so the two round trips overlap.
	contains(t, stdout, "state=NEEDS-READ")
	contains(t, stdout, "RUN BUILT entry=951")
	absent(t, stdout, "MERGE OK")
	m := mergeSHAOf(t, stdout, "951")

	// The gate proves THAT OBJECT, by sha.
	exit, stdout, stderr = l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("green-"+m[:8]))
	if exit != 0 {
		t.Fatalf("gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "GATE OK entry=951")
	contains(t, stdout, "pushed=true")

	// The read condition is not satisfied yet: an approve for this head, by a line that
	// is not the author.
	exit, stdout, _ = l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "state=NEEDS-READ")
	absent(t, stdout, "MERGE OK")

	// An approve from the AUTHOR is recorded and does not count.
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "pat",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: exit %d: %s", exit, errb)
	}
	_, stdout, _ = l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "state=NEEDS-READ")

	// A read by somebody else, for this head.
	exit, stdout, stderr = l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stdout, "current=true")
	contains(t, stdout, "pushed=true")

	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("the merge should have landed: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=951")
	contains(t, stdout, "admitted=hosted")
	contains(t, stdout, "published=push")
	contains(t, stdout, "verified="+m[:12])
	contains(t, stdout, "read=emma")

	// The base is the gated object, by sha, and nothing else was published.
	if got := l.refreshBase(); got != m {
		t.Errorf("the base is %s and the gated object was %s; the object published is the object gated", got, m)
	}
	// Every push the remote received moved a ref forward by exactly one commit on the
	// base, and never rewrote anything.
	basePushes := 0
	for _, p := range l.pushes() {
		fields := strings.Fields(p)
		if fields[0] != "refs/heads/main" {
			continue
		}
		basePushes++
		// A fast-forward by exactly one commit: the old tip is an ancestor of the new
		// one, and the new one is one step along the FIRST-PARENT chain -- which is
		// what "the ref moves forward by one gated commit" means for a merge commit
		// that also brings its second parent's history.
		l.git(l.work, "fetch", "-q", "origin")
		l.git(l.work, "merge-base", "--is-ancestor", fields[1], fields[2])
		if n := l.git(l.work, "rev-list", "--count", "--first-parent", fields[1]+".."+fields[2]); n != "1" {
			t.Errorf("the base moved %s commits along its first-parent chain on one push; the ref moves forward by one gated commit", n)
		}
	}
	if basePushes != 1 {
		t.Errorf("the base was pushed %d times for one merge, want 1", basePushes)
	}
}

func TestAMovedBaseIsRacedBeforeAnythingIsPublished(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g1")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	// A HAND AT ANOTHER KEYBOARD, inside the window: the base moves after this pass's
	// last read of it and before the push. A re-read "immediately before the merge"
	// would not catch this, which is why the guard is a precondition on the write.
	moved := ""
	l.beforePush(func() {
		l.git(l.work, "checkout", "-q", "main")
		l.write("hand.txt", "somebody else\n")
		moved = l.commit("a hand at another keyboard")
		l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/main")
		l.host.SetChecks(moved, 3, 0)
	})

	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a raced base is a refusal: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "MERGE RACED entry=951")
	contains(t, stderr, "nothing was published")
	absent(t, stdout, "MERGE OK")
	if got := l.refreshBase(); got != moved {
		t.Errorf("the remote's base is %s; the race must leave it exactly where the hand left it (%s)", got, moved)
	}
	// The unpublished object stays under refs/nova-merge/integration/ as evidence.
	clone := merge.NewGit(filepath.Join(l.lane, merge.RepoDir), 0, nil)
	_ = clone
	out := l.git(filepath.Join(l.lane, merge.RepoDir), "rev-parse", "--verify", m)
	if out != m {
		t.Errorf("the unpublished object is named and kept: got %q", out)
	}
}

func TestARecordNamingAnObjectThisCloneDoesNotHoldIsMergeFail(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	// A green record for (A, X) naming a merge sha nobody built: publication refuses and
	// the tool never builds a replacement on the way to the push.
	ghost := strings.Repeat("b", 40)
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", ghost, "--verdict", "green", "--summary", l.summary("ghost")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("exit %d, want 1\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "MERGE FAIL entry=951")
	contains(t, stderr, "not in the lane's clone")
	for _, p := range l.pushes() {
		if strings.HasPrefix(p, "refs/heads/main") {
			t.Errorf("nothing may be pushed to the base: %s", p)
		}
	}
}

func TestAGateIsForThePairAndTheNewestRecordDecides(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	gate := func(verdict, at string) {
		t.Helper()
		l.now = mustTime(at)
		if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
			"--base-sha", base, "--merge", m, "--verdict", verdict, "--summary", l.summary(verdict+at)); exit != 0 {
			t.Fatalf("gate: %s", errb)
		}
	}
	// Green at 13:00, red at 13:05: an older green never survives a newer red.
	gate("green", "2026-09-11T13:00:00Z")
	gate("red", "2026-09-11T13:05:00Z")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a red for the pair stops the entry: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "state=RED")
	absent(t, stdout, "MERGE OK")
	// A green after a red is a re-run that passed, and it merges.
	gate("green", "2026-09-11T13:09:00Z")
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("red then green merges: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=951")
}

func TestATieBetweenAGreenAndARedFoldsRedLast(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	for _, verdict := range []string{"green", "red"} {
		l.now = mustTime("2026-09-11T13:00:00Z") // ONE `at` to the second, both of them
		if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
			"--base-sha", base, "--merge", m, "--verdict", verdict, "--summary", l.summary("tie-"+verdict)); exit != 0 {
			t.Fatalf("gate: %s", errb)
		}
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a tie folds RED LAST, so a red never loses a tie: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "state=RED")
}

func TestOnMainAHostedRedStopsTheEntryWhateverTheGateSays(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	l.host.SetChecks(oid, 3, 0, "tables-java-versioning")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("exit %d, want 1\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "state=RED")
	absent(t, stdout, "MERGE OK")
}

func TestBelowMainAHostedRedIsNamedOnTheMergeLineAndNeverBlocks(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// A lane whose base is not main: the local gate is the candidate evidence.
	l.git(l.work, "checkout", "-q", "-B", "rowan/step-2", "origin/main")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/rowan/step-2")
	l.git(l.work, "checkout", "-q", "main")
	l.init("rowan/step-2")
	oid := l.branch("feature-b", "b.txt", "b\n", "b")
	l.host.PRs[942] = merge.PR{Number: 942, Author: "pat", Base: "rowan/step-2", HeadRef: "feature-b",
		HeadOID: oid, Mergeable: "MERGEABLE"}
	l.host.SetChecks(oid, 2, 0, "tables-java-fixedform")
	base := l.git(l.work, "rev-parse", "refs/remotes/origin/rowan/step-2")
	l.host.SetChecks(base, 0, 0) // the base has no checks at all: PENDING until gated
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "942"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	// A BASE GATE: the base merged onto itself is itself, so the three shas are equal,
	// and no parent check applies.
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--branch", "rowan/step-2", "--head", base,
		"--base-sha", base, "--merge", base, "--verdict", "green", "--summary", l.summary("base")); exit != 0 {
		t.Fatalf("base gate: %s", errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "RUN BASE base=rowan/step-2")
	contains(t, stdout, "gate=green")
	// The hosted red did not stop it: below main the hosted lane is not the evidence.
	contains(t, stdout, "state=NEEDS-GATE")
	m := mergeSHAOf(t, stdout, "942")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "942", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g942")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("below main a hosted red is printed by name, never blocking: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=942")
	contains(t, stdout, "admitted=gate")
	contains(t, stdout, "hosted_red=tables-java-fixedform")
}

func mustTime(s string) (t timeValue) { return parseStamp(s) }

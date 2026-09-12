package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 18's host half and rule 21(a): ON A HOST THAT OFFERS A TWO-PRECONDITION
// MERGE, BOTH PRECONDITIONS ARE SUPPLIED -- `--match-head-commit <oid>` AND THE BASE SHA.
//
// Branch (a) of rule 21 had no test at all: FakeHost.Atomic was never set true anywhere
// and FakeHost.Merges was never read, so the host path was dead in the tests and the
// read-back after it was unreachable.

// atomicHost makes the fake offer the primitive, and makes it really move the base, so
// that MERGE OK's verified= is the read-back of something that happened.
func (l *lab) atomicHost() {
	l.host.Atomic = true
	l.host.Do = func(_ int, _, _, mergeSHA string) error {
		l.git(filepath.Join(l.lane, merge.RepoDir), "push", "-q", "origin", mergeSHA+":refs/heads/main")
		return nil
	}
}

// gatedPR is a lane with one entry, built and gated green: the state every test here
// starts publication from.
func gatedPR(t *testing.T, l *lab) (oid, base, m string) {
	t.Helper()
	oid = setupPR(t, l, 951, "feature-a", "a.txt", false)
	base = l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m = mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	return oid, base, m
}

func TestOnATwoPreconditionHostBothPreconditionsAreSupplied(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid, base, m := gatedPR(t, l)
	l.atomicHost()

	// Every argument the tool hands a subprocess, so the test can say what was NOT run.
	var ran [][]string
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(_ string, args []string) {
		ran = append(ran, append([]string(nil), args...))
	}}

	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("the host primitive publishes: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=951")
	contains(t, stdout, "published=host")
	contains(t, stdout, "verified="+m[:12])

	// BOTH preconditions, on the one call.
	if len(l.host.Merges) != 1 {
		t.Fatalf("the host primitive was called %d times for one merge: %v", len(l.host.Merges), l.host.Merges)
	}
	call := l.host.Merges[0]
	for _, want := range []string{"match-head-commit=" + oid, "base=" + base, "merge=" + m} {
		if !strings.Contains(call, want) {
			t.Errorf("the host merge call is %q; it must carry %q -- an expected head is not a precondition on the base", call, want)
		}
	}

	// And the lease was never used: on a host that offers the primitive, rule 21 takes
	// branch (a) and nothing force-pushes the base.
	for _, args := range ran {
		for _, a := range args {
			if strings.HasPrefix(a, "--force-with-lease") {
				t.Errorf("branch (a) was taken and a lease was pushed anyway: %v", args)
			}
		}
	}
	if got := l.refreshBase(); got != m {
		t.Errorf("the base is %s and the gated object was %s", got, m)
	}
}

// Rule 21's read-back: additional evidence for a person, never the guard. A host that
// says it merged and left the base somewhere else is MERGE FAIL, with the call already
// recorded as sent.
func TestAHostThatLeavesTheBaseElsewhereIsMergeFail(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid, _, m := gatedPR(t, l)
	l.host.Atomic = true
	elsewhere := ""
	l.host.Do = func(_ int, _, _, _ string) error {
		// The host moved the base -- to the entry's own head, not to the gated object.
		l.git(l.work, "fetch", "-q", "origin")
		l.git(l.work, "push", "-q", "origin", oid+":refs/heads/main")
		elsewhere = oid
		return nil
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a read-back that is not the merge is exit 1: %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "MERGE FAIL entry=951")
	contains(t, stderr, "published object not at base")
	contains(t, stderr, "reads back as "+elsewhere[:12])
	absent(t, stdout, "MERGE OK")
	if len(l.host.Merges) != 1 {
		t.Errorf("the call is recorded as SENT even though the read-back refused: %v", l.host.Merges)
	}
	if got := l.refreshBase(); got == m {
		t.Errorf("this fixture must leave the base somewhere other than the gated object")
	}
}

// The fake is a guard, not a recorder: a two-precondition merge that arrives without both
// preconditions is refused, so no test can pass branch (a) by accident.
func TestTheHostPrimitiveRefusesAMergeMissingAPrecondition(t *testing.T) {
	t.Parallel()
	h := merge.NewFakeHost()
	h.Atomic = true
	if err := h.Merge(1, strings.Repeat("a", 40), "", strings.Repeat("c", 40)); err == nil {
		t.Error("a merge with no expected base is not a two-precondition merge")
	}
	if err := h.Merge(1, "", strings.Repeat("b", 40), strings.Repeat("c", 40)); err == nil {
		t.Error("a merge with no expected head is not a two-precondition merge")
	}
	if err := h.Merge(1, strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)); err != nil {
		t.Errorf("both preconditions supplied and the fake refused: %v", err)
	}
}

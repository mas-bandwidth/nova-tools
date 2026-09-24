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

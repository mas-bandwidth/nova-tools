//go:build functional

package main

import (
	"path/filepath"

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

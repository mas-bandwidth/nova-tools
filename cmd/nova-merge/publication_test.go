package main

import (
	"strconv"
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

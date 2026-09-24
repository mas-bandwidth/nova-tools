package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Rule S: --admin is refused on a non-revert pull request, and on any pull request with
// an open HOLD on it or on a pull request its body carries ('carries #n'). #858 landed by
// --admin carrying #843/#848/#849 while HOLDs stood, dev went red and #871 reverted it.

// adminAddPR is setupPR without the init, so a test can put more than one entry in one
// lane. It makes a real commit at the fixture remote and queues it, gates green.
func adminAddPR(t *testing.T, l *lab, n int, branch, file string) string {
	t.Helper()
	oid := l.branch(branch, file, "the "+branch+" change\n", "a change on "+branch)
	l.host.PRs[n] = merge.PR{Number: n, Author: "pat", Base: "main", HeadRef: branch,
		HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/" + branch}
	l.host.SetChecks(oid, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
		t.Fatalf("add %d: exit %d: %s", n, exit, errb)
	}
	return oid
}

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

// adminLane is the fixture: a held carried pull request (#843) and a carrying one (#858)
// that is hosted-green and gated green, so #858 reaches the publication helper. title and
// body are what the host says about #858; holdCarried records the open HOLD on #843.
func adminLane(t *testing.T, title, body string, holdCarried bool) (l *lab, oid858 string) {
	t.Helper()
	l = newLab(t)
	l.init("main")
	l.host.SetChecks(l.baseSHA(), 3, 0)
	oid843 := adminAddPR(t, l, 843, "feature-held", "held.txt")
	if holdCarried {
		if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "843", "--who", "stella",
			"--head", oid843, "--verdict", "hold", "--note", "not yet"); exit != 0 {
			t.Fatalf("read hold: exit %d: %s", exit, errb)
		}
	}
	oid858 = adminAddPR(t, l, 858, "feature-carry", "carry.txt")
	pr := l.host.PRs[858]
	pr.Subject, pr.Body = title, body
	l.host.PRs[858] = pr
	// Build #858's integration commit, then prove it green, so only rule S stands
	// between it and publication.
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "858")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "858", "--head", oid858,
		"--base-sha", l.baseSHA(), "--merge", m, "--verdict", "green", "--summary", l.summary("g-"+m[:8])); exit != 0 {
		t.Fatalf("gate: exit %d: %s", exit, errb)
	}
	return l, oid858
}

func TestAdminMergeRefusedOnNonRevert(t *testing.T) {
	t.Parallel()
	l, _ := adminLane(t, "land the carry", "", false)
	exit, _, stderr := l.run("run", "--lane", l.lane, "--once", "--admin")
	if exit != 1 {
		t.Fatalf("a refused admin merge is exit 1, got %d\n%s", exit, stderr)
	}
	contains(t, stderr, "MERGE FAIL entry=858")
	contains(t, stderr, "not a revert")
}

func TestAdminMergeRefusedOverCarriedHold(t *testing.T) {
	t.Parallel()
	l, _ := adminLane(t, `Revert "land the carry"`, "This reverts commit deadbeef.\n\ncarries #843", true)
	exit, _, stderr := l.run("run", "--lane", l.lane, "--once", "--admin")
	if exit != 1 {
		t.Fatalf("a refused admin merge is exit 1, got %d\n%s", exit, stderr)
	}
	contains(t, stderr, "MERGE FAIL entry=858")
	contains(t, stderr, "#843")
	contains(t, stderr, "HOLD")
}

func TestAdminMergeAllowedForRevertWithNoHold(t *testing.T) {
	t.Parallel()
	l, _ := adminLane(t, `Revert "land the carry"`, "This reverts commit deadbeef.", false)
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once", "--admin")
	if exit != 0 {
		t.Fatalf("a revert with no open hold may land by --admin: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=858")
}

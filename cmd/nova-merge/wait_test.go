package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The wait verb through the binary's own run(), against the package's existing
// fake GitHub server (lab.host, a merge.FakeHost): merged on the second poll
// prints MERGED once; a failing check prints RED with the check name; a
// never-finishing check prints TIMEOUT with the pending name.

func TestWaitMergedOnSecondPollPrintsMergedOnce(t *testing.T) {
	l := newLab(t)
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mergeSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	l.host.OnPR = func(n, call int) (merge.PR, bool) {
		if call < 2 {
			return merge.PR{Number: 12, HeadOID: head}, true
		}
		return merge.PR{Number: 12, HeadOID: head, Merged: true, Closed: true, MergeSHA: mergeSHA}, true
	}
	l.host.SetChecks(head, 1, 0)
	exit, stdout, _ := l.run("wait", "--repo", "o/n", "--pr", "12", "--timeout", "5m", "--interval", "1ms")
	if exit != 0 {
		t.Fatalf("merged wait wants exit 0, got %d in %q", exit, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT MERGED pr=12 ") {
		t.Fatalf("merged on the second poll prints MERGED once, got %q", stdout)
	}
	if !strings.Contains(lines[0], "sha="+mergeSHA) {
		t.Fatalf("MERGED line carries the merge sha, got %q", lines[0])
	}
	_ = time.Second
}

func TestWaitFailingCheckPrintsRedWithCheckName(t *testing.T) {
	l := newLab(t)
	head := "cccccccccccccccccccccccccccccccccccccccc"
	l.host.PRs[13] = merge.PR{Number: 13, HeadOID: head}
	l.host.SetCheckDetails(head, merge.CheckDetail{Name: "fast-lane", Conclusion: "failure"})
	exit, stdout, _ := l.run("wait", "--repo", "o/n", "--pr", "13", "--timeout", "5m", "--interval", "1ms")
	if exit != 2 {
		t.Fatalf("red wait wants exit 2, got %d in %q", exit, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT RED pr=13 ") {
		t.Fatalf("a failing check prints RED once, got %q", stdout)
	}
	if !strings.Contains(lines[0], "check=fast-lane") {
		t.Fatalf("RED line names the failing check, got %q", lines[0])
	}
}

func TestWaitTimeoutPrintsPendingName(t *testing.T) {
	l := newLab(t)
	head := "dddddddddddddddddddddddddddddddddddddddd"
	l.host.PRs[14] = merge.PR{Number: 14, HeadOID: head}
	l.host.SetCheckDetails(head, merge.CheckDetail{Name: "slow-job", Conclusion: "pending"})
	exit, stdout, _ := l.run("wait", "--repo", "o/n", "--pr", "14", "--timeout", "3ms", "--interval", "1ms")
	if exit != 3 {
		t.Fatalf("timeout wait wants exit 3, got %d in %q", exit, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT TIMEOUT pr=14 ") {
		t.Fatalf("a never-finishing check prints TIMEOUT once, got %q", stdout)
	}
	if !strings.Contains(lines[0], "slow-job") {
		t.Fatalf("TIMEOUT line names the pending check, got %q", lines[0])
	}
}

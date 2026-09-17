package merge

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The wait verb polls the SAME host reader the lane uses for PR state and checks:
// FakeHost here stands in for the fake GitHub server. Three polls, three endings.

func waitClock(start time.Time) (func() time.Time, func(time.Duration), *time.Time) {
	now := start
	return func() time.Time { return now },
		func(d time.Duration) { now = now.Add(d) },
		&now
}

func TestWaitMergedOnSecondPollPrintsMergedOnce(t *testing.T) {
	host := NewFakeHost()
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mergeSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	host.OnPR = func(n, call int) (PR, bool) {
		if call < 2 {
			return PR{Number: 7, HeadOID: head}, true
		}
		return PR{Number: 7, HeadOID: head, Merged: true, Closed: true, MergeSHA: mergeSHA}, true
	}
	host.SetChecks(head, 1, 0)
	start := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	nowFn, sleepFn, _ := waitClock(start)
	var out bytes.Buffer
	exit := Wait(host, 7, 5*time.Minute, time.Millisecond, nowFn, sleepFn, &out)
	if exit != 0 {
		t.Fatalf("merged wait wants exit 0, got %d in %q", exit, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT MERGED pr=7 ") {
		t.Fatalf("merged on the second poll prints MERGED once, got %q", out.String())
	}
	if !strings.Contains(lines[0], "sha="+mergeSHA) {
		t.Fatalf("MERGED line carries the merge sha, got %q", lines[0])
	}
}

func TestWaitFailingCheckPrintsRedWithCheckName(t *testing.T) {
	host := NewFakeHost()
	head := "cccccccccccccccccccccccccccccccccccccccc"
	host.PRs[7] = PR{Number: 7, HeadOID: head}
	host.SetCheckDetails(head, CheckDetail{Name: "fast-lane", Conclusion: "failure"})
	start := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	nowFn, sleepFn, _ := waitClock(start)
	var out bytes.Buffer
	exit := Wait(host, 7, 5*time.Minute, time.Millisecond, nowFn, sleepFn, &out)
	if exit != 2 {
		t.Fatalf("red wait wants exit 2, got %d in %q", exit, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT RED pr=7 ") {
		t.Fatalf("a failing check prints RED once, got %q", out.String())
	}
	if !strings.Contains(lines[0], "check=fast-lane") {
		t.Fatalf("RED line names the failing check, got %q", lines[0])
	}
}

func TestWaitTimeoutPrintsPendingName(t *testing.T) {
	host := NewFakeHost()
	head := "dddddddddddddddddddddddddddddddddddddddd"
	host.PRs[7] = PR{Number: 7, HeadOID: head}
	host.SetCheckDetails(head, CheckDetail{Name: "slow-job", Conclusion: "pending"})
	start := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	nowFn, sleepFn, _ := waitClock(start)
	var out bytes.Buffer
	exit := Wait(host, 7, 3*time.Millisecond, time.Millisecond, nowFn, sleepFn, &out)
	if exit != 3 {
		t.Fatalf("timeout wait wants exit 3, got %d in %q", exit, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "MERGE WAIT TIMEOUT pr=7 ") {
		t.Fatalf("a never-finishing check prints TIMEOUT once, got %q", out.String())
	}
	if !strings.Contains(lines[0], "slow-job") {
		t.Fatalf("TIMEOUT line names the pending check, got %q", lines[0])
	}
}

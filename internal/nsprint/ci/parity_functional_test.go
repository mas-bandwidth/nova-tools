//go:build functional

package ci_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/redis/go-redis/v9"
)

const (
	headA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headA2 = "abababababababababababababababababababab"
	headA3 = "acacacacacacacacacacacacacacacacacacacac"
	headB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC  = "cccccccccccccccccccccccccccccccccccccccc"
	headD  = "dddddddddddddddddddddddddddddddddddddddd"
	headE  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	headX  = "9999999999999999999999999999999999999999"
)

// ours requests our CI for one PR head (one declared check) and, unless rc is
// negative, runs it to its word through the ci_run.lua functions a bench
// calls: rc 0 ends green, anything else red (max_attempts is 1 here).
func (f *fixture) ours(prN int, sha string, rc int) {
	f.t.Helper()
	r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: repo, SHA: sha, PR: prN, Checks: []string{"go-build"}})
	if err != nil || r.Status != "CREATED" {
		f.t.Fatalf("request %d %s = %v, %v; want CREATED", prN, sha[:8], r, err)
	}
	if rc < 0 {
		return
	}
	c, ok, err := ci.Claim(f.ctx, f.st, "ctl-a", time.Minute, nil)
	if err != nil || !ok || c.SHA != sha {
		f.t.Fatalf("claim %s = %+v, %v, %v", sha[:8], c, ok, err)
	}
	res, err := ci.WriteReceipt(f.ctx, f.st, ci.ReceiptRecord{Repo: repo, SHA: sha, Check: "go-build", Token: c.Token,
		RC: rc, WallMS: 1, Log: "fixture", Bench: "ctl-a", Lease: time.Minute})
	want := ci.SummaryGreen
	if rc != 0 {
		want = ci.SummaryRed
	}
	if err != nil || res.Summary != want {
		f.t.Fatalf("receipt %s = %+v, %v; want %s", sha[:8], res, err, want)
	}
}

// actions appends one completed workflow_run delivery to ev:github, the shape
// the webhook receiver (#2657) writes; id is the stream id, "*" for now.
func (f *fixture) actions(id, prN, sha, workflow, conclusion string) {
	f.t.Helper()
	values, err := ghevent.Fields(ghevent.Entry{
		Repo: "mas-bandwidth/" + repo, Kind: "workflow_run", Number: prN, Head: sha,
		Action: "completed", At: "2026-09-25T19:00:00Z", Sender: "ctl",
		RunID: "1", Workflow: workflow, Status: "completed", Conclusion: conclusion,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: ghevent.Stream, ID: id, Values: values}).Err(); err != nil {
		f.t.Fatal(err)
	}
}

// nowMS is the store's clock, the one the stream ids are cut from.
func (f *fixture) nowMS() int64 {
	f.t.Helper()
	tm, err := f.client.Time(f.ctx).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return tm.UnixMilli()
}

func (f *fixture) parity(minHeads int) (string, int) {
	f.t.Helper()
	p, err := ci.ReadParity(f.ctx, f.st, ci.ParityRequest{Sprint: f.sprint, Min: minHeads})
	if err != nil {
		f.t.Fatal(err)
	}
	var out bytes.Buffer
	code := ci.WriteParity(&out, p)
	return out.String(), code
}

// TestParityCountsEveryActionsPassedHead is nova-tools #3041 (#2756 10.8.1):
// over one sprint's window (s:<S> opened_at..closed_at), every head of a
// sprint PR that Actions passed (completed workflow_run entries on ev:github)
// must read OK on its record ci:<repo>:<sha>. A head Actions passed whose
// record is FAIL, PENDING, or absent (MISSING) prints `PARITY FAIL <head>` and
// the verb exits 1. Heads Actions failed, PRs our CI never saw, and runs
// outside the window are not counted.
func TestParityCountsEveryActionsPassedHead(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	f := newFixture(t, "ctl-a")
	f.client.HSet(f.ctx, ci.MaxAttemptsKey, "max_attempts", "1")

	// Before the sprint: Actions passed a head our CI failed. Out of the window.
	f.ours(104, headD, 1)
	f.actions("1000-0", "104", headD, "ci", "success")
	if _, err := ci.ReadParity(f.ctx, f.st, ci.ParityRequest{Sprint: f.sprint}); err == nil ||
		!strings.Contains(err.Error(), "no opened_at") {
		t.Fatalf("a sprint with no opened_at: err = %v; want the refusal naming opened_at", err)
	}
	f.client.HSet(f.ctx, "s:"+f.sprint, "opened_at", strconv.FormatInt(f.nowMS(), 10))
	time.Sleep(3 * time.Millisecond)

	f.ours(101, headA, 0)  // Actions passed, ours OK: parity
	f.ours(102, headB, 1)  // Actions passed, ours FAIL: PARITY FAIL
	f.ours(103, headC, 1)  // Actions failed: not counted
	f.ours(105, headE, -1) // Actions passed, ours still PENDING: PARITY FAIL

	f.actions("*", "101", headA, "ci", "failure") // a re-run of the same workflow passed later
	f.actions("*", "101", headA, "ci", "success")
	f.actions("*", "101", headA, "lint", "skipped")
	f.actions("*", "101", headA2, "ci", "success") // a new head of a sprint PR, never requested: MISSING
	f.actions("*", "102", headB, "ci", "success")
	f.actions("*", "103", headC, "ci", "success")
	f.actions("*", "103", headC, "lint", "failure")
	f.actions("*", "104", headD, "ci", "success") // the pre-sprint head passes again in the window
	f.actions("*", "105", headE, "ci", "success")
	f.actions("*", "999", headX, "ci", "success") // a PR our CI never saw

	out, code := f.parity(0)
	if code != ci.ExitParity {
		t.Fatalf("exit = %d; want %d\n%s", code, ci.ExitParity, out)
	}
	for _, want := range []string{
		"PARITY FAIL " + headA2 + " repo=nova-tools pr=101 key=MISSING remedy: nova-sprint ci request --repo nova-tools --sha " + headA2 + " --pr 101",
		"PARITY FAIL " + headB + " repo=nova-tools pr=102 key=FAIL remedy: nova-sprint ci status --repo nova-tools --sha " + headB,
		"PARITY FAIL " + headD + " repo=nova-tools pr=104 key=FAIL",
		"PARITY FAIL " + headE + " repo=nova-tools pr=105 key=PENDING remedy: nova-sprint ci run",
		"PARITY 1/5 sprint=" + f.sprint,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{headA + " ", headC, headX} {
		if strings.Contains(out, "PARITY FAIL "+not) {
			t.Fatalf("output fails a head it must not count (%s):\n%s", not, out)
		}
	}

	// The same sprint at parity: the missing head is requested, the failed
	// heads are requested again, and the bench runs all four green (the
	// pending one included).
	f.ours(101, headA2, -1)
	for _, h := range []string{headB, headD} {
		if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: repo, SHA: h, Again: true}); err != nil || r.Status != "RESET" {
			t.Fatalf("request --again %s = %v, %v", h[:8], r, err)
		}
	}
	for i := 0; i < 4; i++ {
		c, ok, err := ci.Claim(f.ctx, f.st, "ctl-a", time.Minute, nil)
		if err != nil || !ok {
			t.Fatalf("claim %d = %+v, %v, %v", i, c, ok, err)
		}
		if _, err := ci.WriteReceipt(f.ctx, f.st, ci.ReceiptRecord{Repo: repo, SHA: c.SHA, Check: "go-build", Token: c.Token,
			RC: 0, WallMS: 1, Log: "fixture", Bench: "ctl-a", Lease: time.Minute}); err != nil {
			t.Fatal(err)
		}
	}
	f.client.HSet(f.ctx, "s:"+f.sprint, "closed_at", strconv.FormatInt(f.nowMS(), 10))
	time.Sleep(3 * time.Millisecond)
	f.actions("*", "101", headA3, "ci", "success") // after the close: the next sprint's
	out, code = f.parity(5)
	if code != ci.ExitOK || !strings.Contains(out, "PARITY 5/5 sprint="+f.sprint) || strings.Contains(out, "PARITY FAIL") {
		t.Fatalf("at parity: exit %d; want 0 and PARITY 5/5\n%s", code, out)
	}

	// Parity over too few heads is not the gate: n/n with n under --min exits 1.
	out, code = f.parity(20)
	if code != ci.ExitParity || !strings.Contains(out, "PARITY 5/5 sprint="+f.sprint+" short: 5 < 20 remedy:") {
		t.Fatalf("short sample: exit %d; want 1 and the short line\n%s", code, out)
	}
}

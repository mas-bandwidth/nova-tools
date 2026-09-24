package ci_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

const (
	headA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headA2 = "abababababababababababababababababababab"
	headB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC  = "cccccccccccccccccccccccccccccccccccccccc"
	headX  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

// cutEnd cuts the ci card for one PR head, deals it and ends it with verdict.
func (f *fixture) cutEnd(prN int, sha, verdict string) {
	f.t.Helper()
	r, err := ci.Cut(f.ctx, f.st, ci.CutRequest{Sprint: f.sprint, Repo: repo, PR: prN, Head: sha, Base: base, Actor: "ctl"})
	if err != nil || r.Status != "CREATED" {
		f.t.Fatalf("cut %d %s = %v, %v; want CREATED", prN, sha[:8], r, err)
	}
	if verdict == "" {
		return
	}
	label := ci.Label(prN, sha)
	token, identity := f.deal(label, "ctl-a")
	if r := f.end(label, token, identity, "DONE", "done", verdict, "internal/x", "TestX"); r.Status != "ENDED" {
		f.t.Fatalf("end %s = %v; want ENDED", label, r)
	}
}

// run appends one completed workflow_run delivery to ev:github, the shape the
// webhook receiver (#2657) writes.
func (f *fixture) run(prN, sha, workflow, conclusion string) {
	f.t.Helper()
	_, err := ghevent.Publish(f.ctx, f.client, ghevent.Entry{
		Repo: "mas-bandwidth/" + repo, Kind: "workflow_run", Number: prN, Head: sha,
		Action: "completed", At: "2026-09-23T19:00:00Z", Sender: "ctl",
		RunID: "1", Workflow: workflow, Status: "completed", Conclusion: conclusion,
	})
	if err != nil {
		f.t.Fatal(err)
	}
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
// over one sprint, every head of a sprint PR that Actions passed must have
// ci:<repo>:<sha> OK. A head Actions passed whose key is FAIL, or that has no
// key at all (MISSING), prints `PARITY FAIL <head>` and the verb exits 1.
// Heads Actions failed, and PRs the sprint never cut, are not counted.
func TestParityCountsEveryActionsPassedHead(t *testing.T) {
	f := newFixture(t, "ctl-a", "ctl-b")
	f.cutEnd(101, headA, ci.OK)   // Actions passed, key OK: parity
	f.cutEnd(102, headB, ci.Fail) // Actions passed, key FAIL: PARITY FAIL
	f.cutEnd(103, headC, ci.Fail) // Actions failed: not counted

	f.run("101", headA, "ci", "failure") // a re-run of the same workflow passed later
	f.run("101", headA, "ci", "success")
	f.run("101", headA, "lint", "skipped")
	f.run("101", headA2, "ci", "success") // a new head of a sprint PR, never cut: MISSING
	f.run("102", headB, "ci", "success")
	f.run("103", headC, "ci", "success")
	f.run("103", headC, "lint", "failure")
	f.run("999", headX, "ci", "success") // not a sprint PR

	out, code := f.parity(0)
	if code != 1 {
		t.Fatalf("exit = %d; want 1\n%s", code, out)
	}
	for _, want := range []string{
		"PARITY FAIL " + headA2 + " repo=nova-tools pr=101 key=MISSING",
		"PARITY FAIL " + headB + " repo=nova-tools pr=102 key=FAIL",
		"PARITY 1/3",
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

	// The same sprint at parity: the missing head is cut and passes, and the
	// failed head's key reads OK (what a typed APPROVE on a FLAKY record writes).
	f.cutEnd(101, headA2, ci.OK)
	f.client.HSet(f.ctx, ci.RecordKey(repo, headB), "verdict", ci.OK)
	out, code = f.parity(3)
	if code != 0 || !strings.Contains(out, "PARITY 3/3") || strings.Contains(out, "PARITY FAIL") {
		t.Fatalf("at parity: exit %d; want 0 and PARITY 3/3\n%s", code, out)
	}

	// Parity over too few heads is not the gate: n/n with n under --min exits 1.
	out, code = f.parity(20)
	if code != 1 || !strings.Contains(out, "PARITY 3/3 short: 3 < 20") {
		t.Fatalf("short sample: exit %d; want 1 and the short line\n%s", code, out)
	}
}

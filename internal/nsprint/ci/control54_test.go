package ci_test

// Control 54 (nova-tools #3099, spec 10.13): a cancelled or timed-out CI leg
// re-runs exactly once, and a second cancel or time-out ends the head red
// with the why on the record and no third run. The issue was filed against
// GitHub runner rows re-run by REST; our own CI (#3597, #3715) is now the
// leg, so the control holds on its record: a cancel is a claim whose lease
// lapses with no receipt (the bench was lost), a time-out is a check that
// runs past ci run's per-check timeout (rc 124). cfg:ci max_attempts
// (default 2) is the one-rerun budget, the head's pool member is the idem
// key, and the capping claim prints the CAPPED line.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestControl54CancelledLegRerunsOnceThenEndsRed(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.PRKey(runRepo, 54), "head", f.sha)
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, PR: 54, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	const lease = 60 * time.Millisecond
	lapse := func() { time.Sleep(lease + 60*time.Millisecond) }

	// Attempt 1 is claimed and cancelled: no receipt, the lease lapses.
	first, ok, err := ci.Claim(f.ctx, f.st, "b1", lease, nil)
	if err != nil || !ok || first.Attempt != 1 {
		t.Fatalf("first claim = %+v %v %v", first, ok, err)
	}
	if _, ok, _ := ci.Claim(f.ctx, f.st, "b2", lease, nil); ok {
		t.Fatal("a live lease was claimed twice")
	}
	lapse()

	// Exactly one rerun: attempt 2 on another bench, the cancelled token fenced.
	second, ok, err := ci.Claim(f.ctx, f.st, "b2", lease, nil)
	if err != nil || !ok || second.Attempt != 2 || second.Token == first.Token {
		t.Fatalf("rerun claim = %+v %v %v", second, ok, err)
	}
	late, err := ci.WriteReceipt(f.ctx, f.st, ci.ReceiptRecord{Repo: runRepo, SHA: f.sha, Check: "head",
		Token: first.Token, RC: 0, Bench: "b1", Lease: time.Minute})
	if err != nil || late.Status != "FENCED" {
		t.Fatalf("receipt from the cancelled attempt = %+v, %v; want FENCED", late, err)
	}
	lapse()

	// The second cancel ends it: one CAPPED line, no third run.
	var out bytes.Buffer
	res, err := ci.Run(f.ctx, f.st, ci.RunOptions{Bench: "b3", Scratch: filepath.Join(f.root, "scratch"),
		ResultsRoot: filepath.Join(f.root, "results"), Lease: time.Minute, Timeout: 30 * time.Second, Out: &out})
	if err != nil || res.Claimed {
		t.Fatalf("third run = %+v, %v; want nothing claimed\n%s", res, err, out.String())
	}
	if n := strings.Count(out.String(), "CAPPED "+runRepo+":"+f.sha+" FAIL"); n != 1 {
		t.Fatalf("want one CAPPED line, got %d\n%s", n, out.String())
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || rec["attempt"] != "2" ||
		rec["why"] != "no verdict in 2 attempts (leases lapsed)" {
		t.Fatalf("record after the second cancel: %v", rec)
	}
	if pr := f.client.HGetAll(f.ctx, ci.PRKey(runRepo, 54)).Val(); pr["ci"] != ci.SummaryRed || pr["ci_why"] != rec["why"] {
		t.Fatalf("pr record: %v", pr)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 0 {
		t.Fatalf("pool has %d members after the cap", n)
	}
	if c, ok, _ := ci.Claim(f.ctx, f.st, "b4", lease, nil); ok {
		t.Fatalf("a third run was claimed: %+v", c)
	}
}

func TestControl54TimedOutLegRerunsOnceThenEndsRed(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test runs a real child and waits on the wall clock; it failed under
	// the whole parallel suite on the 2026-09-25 Studio run and passes alone. Skipped
	// 2026-09-25 by Glenn's rule ("unit tests must not have real sleeps or waits"): it
	// becomes a mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "head,slow", "check:slow", "sleep 5")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	run := func(bench string) (ci.RunResult, string) {
		t.Helper()
		var out bytes.Buffer
		res, err := ci.Run(f.ctx, f.st, ci.RunOptions{Bench: bench, Scratch: filepath.Join(f.root, "scratch"),
			ResultsRoot: filepath.Join(f.root, "results"), Lease: time.Minute, Timeout: 200 * time.Millisecond, Out: &out})
		if err != nil {
			t.Fatalf("run on %s: %v\n%s", bench, err, out.String())
		}
		return res, out.String()
	}
	timedOut := func(res ci.RunResult) bool {
		for _, c := range res.Checks {
			if c.Name == "slow" {
				return c.RC == 124
			}
		}
		return false
	}

	res, out := run("b1")
	if !res.Claimed || res.Attempt != 1 || !timedOut(res) || res.Summary != ci.SummaryRetry {
		t.Fatalf("first run = %+v; want a timed-out leg sent back for its one rerun\n%s", res, out)
	}
	res, out = run("b2")
	if !res.Claimed || res.Attempt != 2 || !timedOut(res) || res.Summary != ci.SummaryRed {
		t.Fatalf("rerun = %+v; want the second time-out to end red\n%s", res, out)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || rec["attempt"] != "2" ||
		!strings.HasPrefix(rec["why"], "slow: ") || !strings.Contains(rec["why"], "timed out") {
		t.Fatalf("record after the second time-out: %v", rec)
	}
	if res, out = run("b3"); res.Claimed {
		t.Fatalf("a third run: %+v\n%s", res, out)
	}
}

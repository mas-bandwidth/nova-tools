package ci_test

// The 2026-09-25 retry storm: hulk logged `CLAIMED nova-tools@47e596c6
// attempt=22` and later attempt=244 on another head, while space and vision
// (no GitHub key) claimed and released it back to the pool every tick.
// Attempts are capped at cfg:ci max_attempts (default 2, one run plus one
// retry for flake): a head is claimed at most that many times, then it ends
// FAIL with the why on the record and leaves the pool for good; only
// `ci request --again` puts it back.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestCIFailingHeadIsClaimedTwiceNeverAThirdTime(t *testing.T) {
	t.Parallel()

	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "head,boom", "check:boom", "git rev-parse --verify FAIL-no-such-ref")
	f.client.HSet(f.ctx, ci.PRKey(runRepo, 9), "head", f.sha)
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, PR: 9, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	claims := 0
	for i, bench := range []string{"b1", "b2", "b3", "b4"} {
		res, out, err := f.run(t, bench)
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, out)
		}
		if res.Claimed {
			claims++
		}
	}
	if claims != 2 {
		t.Fatalf("claimed %d times, want 2", claims)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 0 {
		t.Fatalf("pool has %d members after the cap", n)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || rec["attempt"] != "2" || rec["token"] != "" ||
		rec["why"] != "boom: fatal: Needed a single revision" {
		t.Fatalf("record at the cap: %v", rec)
	}
	pr := f.client.HGetAll(f.ctx, ci.PRKey(runRepo, 9)).Val()
	if pr["ci"] != ci.SummaryRed || pr["ci_why"] != rec["why"] {
		t.Fatalf("pr record: %v", pr)
	}

	// A plain second request changes nothing; --again resets to attempt 0.
	if r, _ := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); r.Status != "EXISTS" {
		t.Fatalf("second request = %v, want EXISTS", r)
	}
	r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url, Again: true})
	if err != nil || r.Status != "RESET" || r.ExitCode() != ci.ExitOK {
		t.Fatalf("again = %v, %v; want RESET", r, err)
	}
	rec = f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryPending || rec["attempt"] != "0" || rec["final"] != "" || rec["why"] != "" ||
		f.client.ZCard(f.ctx, ci.PoolKey).Val() != 1 || f.client.Exists(f.ctx, ci.ReceiptKey(runRepo, f.sha, "boom")).Val() != 0 {
		t.Fatalf("record after --again: %v", rec)
	}
	if res, out, err := f.run(t, "b5"); err != nil || !res.Claimed || res.Attempt != 1 {
		t.Fatalf("run after --again = %+v, %v\n%s", res, err, out)
	}
}

func TestCIReleaseStormEndsAtTheCapWithTheBlockedWhy(t *testing.T) {
	t.Parallel()

	f := newRunFixture(t)
	bad := "file://" + filepath.Join(t.TempDir(), "missing.git")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: bad}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	for _, bench := range []string{"space", "vision"} {
		if _, out, err := f.run(t, bench); !errors.Is(err, ci.ErrBlocked) {
			t.Fatalf("%s: err=%v, want blocked\n%s", bench, err, out)
		}
	}
	res, out, err := f.run(t, "hulk")
	if err != nil || res.Claimed || !strings.Contains(out, "CAPPED "+runRepo+":"+f.sha+" FAIL") {
		t.Fatalf("third claim = %+v, %v; want nothing claimed and one CAPPED line\n%s", res, err, out)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || rec["attempt"] != "2" || !strings.HasPrefix(rec["why"], "blocked: clone:") {
		t.Fatalf("record: %v", rec)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 0 {
		t.Fatalf("pool has %d members", n)
	}
	// cfg:ci max_attempts raises the cap for the next head.
	f.client.HSet(f.ctx, ci.MaxAttemptsKey, "max_attempts", "3")
	if r, _ := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: bad, Again: true}); r.Status != "RESET" {
		t.Fatalf("again = %v", r)
	}
	for _, bench := range []string{"a", "b", "c"} {
		if _, out, err := f.run(t, bench); !errors.Is(err, ci.ErrBlocked) {
			t.Fatalf("%s under cap 3: err=%v\n%s", bench, err, out)
		}
	}
	if res, _, _ := f.run(t, "d"); res.Claimed {
		t.Fatal("a fourth claim under cap 3")
	}
}

func TestFirstFail(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"ok a\n--- FAIL: TestX (0.1s)\n    x_test.go:3: boom\nFAIL\n": "--- FAIL: TestX (0.1s)",
		"building\nFAIL\tgithub.com/x/y [build failed]\n":             "FAIL github.com/x/y [build failed]",
		"fatal: Needed a single revision\n\n":                         "fatal: Needed a single revision",
		"":                                                            "",
	}
	for in, want := range cases {
		if got := ci.FirstFail([]byte(in)); got != want {
			t.Fatalf("FirstFail(%q) = %q, want %q", in, got, want)
		}
	}
}

//go:build functional

package ci_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// termScript writes an executable check that prints killedOutput and exits 1;
// with passAfter it does that once and passes on every later run.
func termScript(t *testing.T, passAfter bool) string {
	t.Helper()
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	body := "#!/bin/sh\n"
	if passAfter {
		body += "if [ -e " + mark + " ]; then echo ok; exit 0; fi\n: > " + mark + "\n"
	}
	body += "printf '%s' '" + killedOutput + "'\nexit 1\n"
	p := filepath.Join(dir, "term.sh")
	if err := testbin.WriteExecutable(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCITerminatedOnlyCheckIsReRunNotRed(t *testing.T) {
	t.Parallel()

	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "head,test", "check:test", termScript(t, true))
	f.client.HSet(f.ctx, ci.PRKey(runRepo, 11), "head", f.sha)
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, PR: 11, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	res, out, err := f.run(t, "studio")
	if !errors.Is(err, ci.ErrInfra) || res.Summary != "" || res.Blocked != "infra: test: signal: terminated" ||
		!strings.Contains(out, "INFRA "+runRepo+"@"+f.sha[:8]+" test signal: terminated RELEASED rerun") {
		t.Fatalf("killed run: res=%+v err=%v\n%s", res, err, out)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryPending || rec["attempt"] != "0" || rec["infra"] != "1" || rec["token"] != "" ||
		rec["blocked"] != "infra: test: signal: terminated" {
		t.Fatalf("record after the kill: %v", rec)
	}
	if f.client.Exists(f.ctx, ci.ReceiptKey(runRepo, f.sha, "test")).Val() != 0 {
		t.Fatal("the killed check wrote a receipt: a bench kill is no evidence about the head")
	}
	if pr := f.client.HGet(f.ctx, ci.PRKey(runRepo, 11), "ci").Val(); pr != "" {
		t.Fatalf("pr record ci=%q after a bench kill, want nothing", pr)
	}
	res, out, err = f.run(t, "space")
	if err != nil || res.Summary != ci.SummaryGreen || res.Attempt != 1 {
		t.Fatalf("rerun: res=%+v err=%v\n%s", res, err, out)
	}
	if pr := f.client.HGet(f.ctx, ci.PRKey(runRepo, 11), "ci").Val(); pr != ci.SummaryGreen {
		t.Fatalf("pr record ci=%q after the rerun, want green", pr)
	}
}

func TestCIHeadThatAlwaysDiesEndsRedAfterBoundedReruns(t *testing.T) {
	t.Parallel()

	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "head,test", "check:test", termScript(t, false))
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	claims := 0
	for i := 0; i < 6; i++ {
		res, out, err := f.run(t, "b")
		if res.Claimed {
			claims++
			if !errors.Is(err, ci.ErrInfra) {
				t.Fatalf("claim %d: err=%v\n%s", claims, err, out)
			}
		}
	}
	// max_attempts 2: two kills given back, then two counted, then the cap.
	if claims != 4 {
		t.Fatalf("claimed %d times, want 4", claims)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || rec["attempt"] != "2" || rec["infra"] != "4" ||
		rec["why"] != "blocked: infra: test: signal: terminated" {
		t.Fatalf("record at the cap: %v", rec)
	}
	if r, _ := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url, Again: true}); r.Status != "RESET" {
		t.Fatalf("again = %v", r)
	}
	if got := f.client.HGet(f.ctx, ci.RecordKey(runRepo, f.sha), "infra").Val(); got != "0" {
		t.Fatalf("infra after --again = %q, want 0", got)
	}
}

package ci_test

// A head's checks run concurrently (Glenn 2026-09-25 "Can we speed up CI...
// we have a large fleet"): measured on hetzner the five nova-tools checks
// took 211 s one after another, the longest alone 149 s. Every check of a
// claimed head starts at once, bounded by cfg:ci:<repo> parallel (default
// 4); each `go test` check carries -p cores/parallel so the checks do not
// oversubscribe the bench; a red check still yields ci=red with every
// receipt present.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestCIRunChecksConcurrently(t *testing.T) {
	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "s1,s2,s3",
		"check:s1", "sleep 1", "check:s2", "sleep 1", "check:s3", "sleep 1")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	began := time.Now()
	res, out, err := f.run(t, "b1")
	wall := time.Since(began) - time.Duration(res.Clone.MS)*time.Millisecond
	if err != nil || res.Summary != ci.SummaryGreen || len(res.Checks) != 3 {
		t.Fatalf("run = %+v, %v\n%s", res, err, out)
	}
	if wall >= 2*time.Second {
		t.Fatalf("three 1-second checks took %v wall (clone excluded), want under 2s: they ran one after another\n%s", wall, out)
	}
	for _, name := range []string{"s1", "s2", "s3"} {
		if rc := f.client.HGet(f.ctx, ci.ReceiptKey(runRepo, f.sha, name), "rc").Val(); rc != "0" {
			t.Fatalf("receipt %s rc=%q, want 0", name, rc)
		}
	}
}

func TestCIRunRedCheckAmongConcurrentChecksIsRedWithEveryReceipt(t *testing.T) {
	f := newRunFixture(t)
	f.client.HSet(f.ctx, "cfg:ci", "max_attempts", "1")
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "s1,bad,s3",
		"check:s1", "sleep 1", "check:bad", "false", "check:s3", "sleep 1")
	f.client.HSet(f.ctx, ci.PRKey(runRepo, 9), "head", f.sha)
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, PR: 9, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	res, out, err := f.run(t, "b1")
	if err != nil || res.Summary != ci.SummaryRed || len(res.Checks) != 3 {
		t.Fatalf("run = %+v, %v; want red with three checks\n%s", res, err, out)
	}
	want := map[string]string{"s1": "0", "bad": "1", "s3": "0"}
	for name, rc := range want {
		if got := f.client.HGet(f.ctx, ci.ReceiptKey(runRepo, f.sha, name), "rc").Val(); got != rc {
			t.Fatalf("receipt %s rc=%q, want %s", name, got, rc)
		}
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryRed || rec["final"] != "FAIL" || !strings.HasPrefix(rec["why"], "bad: ") {
		t.Fatalf("record = %v; want ci=red final=FAIL why naming bad", rec)
	}
	if pr := f.client.HGet(f.ctx, ci.PRKey(runRepo, 9), "ci").Val(); pr != ci.SummaryRed {
		t.Fatalf("pr record ci=%q, want red", pr)
	}
}

// fakeGo is an executable named go that prints its arguments, so a check's
// log shows the argv the runner gave it.
func fakeGo(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"ARGV $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCIRunGoTestChecksCarryPCoresOverParallel(t *testing.T) {
	f := newRunFixture(t)
	g := fakeGo(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "parallel", "2", "checks", "unit,pinned,build",
		"check:unit", g+" test -count=1 ./cmd/...", "check:pinned", g+" test -p 3 ./x/...", "check:build", g+" build ./...")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	res, out, err := f.run(t, "b1")
	if err != nil || res.Summary != ci.SummaryGreen || len(res.Checks) != 3 {
		t.Fatalf("run = %+v, %v\n%s", res, err, out)
	}
	p := runtime.NumCPU() / 2
	if p < 1 {
		p = 1
	}
	want := map[string]string{
		"unit":   "ARGV test -p " + strconv.Itoa(p) + " -count=1 ./cmd/...",
		"pinned": "ARGV test -p 3 ./x/...",
		"build":  "ARGV build ./...",
	}
	for _, cr := range res.Checks {
		b, err := os.ReadFile(cr.Log)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(b)); got != want[cr.Name] {
			t.Fatalf("check %s ran %q, want %q\n%s", cr.Name, got, want[cr.Name], out)
		}
	}
	if !strings.Contains(out, "PARALLEL 2 go-test-p="+strconv.Itoa(p)+" ") {
		t.Fatalf("run output names no PARALLEL line\n%s", out)
	}
}

func TestCIRunTimedOutCheckIsKilledWithItsGroup(t *testing.T) {
	f := newRunFixture(t)
	// The check's child holds the log pipe: only a kill of the whole group
	// lets the check end at its timeout.
	script := filepath.Join(t.TempDir(), "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.client.HSet(f.ctx, "cfg:ci", "max_attempts", "1")
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "hang,s1", "check:hang", script, "check:s1", "sleep 1")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	var out strings.Builder
	began := time.Now()
	res, err := ci.Run(f.ctx, f.st, ci.RunOptions{Bench: "b1", Scratch: filepath.Join(f.root, "scratch"),
		ResultsRoot: filepath.Join(f.root, "results"), Lease: time.Minute, Timeout: 2 * time.Second, Out: &out})
	if err != nil || res.Summary != ci.SummaryRed || time.Since(began) > 10*time.Second {
		t.Fatalf("run = %+v, %v after %v; want red within the timeout\n%s", res, err, time.Since(began), out.String())
	}
	if rc := f.client.HGet(f.ctx, ci.ReceiptKey(runRepo, f.sha, "hang"), "rc").Val(); rc != "124" {
		t.Fatalf("hang rc=%q, want 124\n%s", rc, out.String())
	}
	if rc := f.client.HGet(f.ctx, ci.ReceiptKey(runRepo, f.sha, "s1"), "rc").Val(); rc != "0" {
		t.Fatalf("s1 rc=%q, want 0\n%s", rc, out.String())
	}
}

func TestCIRunStoppedMidwayIsReleasedAsInfraWithNoRedReceipt(t *testing.T) {
	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "fast,slow", "check:fast", "true", "check:slow", "sleep 30")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	time.AfterFunc(1500*time.Millisecond, cancel)
	var out strings.Builder
	began := time.Now()
	res, err := ci.Run(ctx, f.st, ci.RunOptions{Bench: "b1", Scratch: filepath.Join(f.root, "scratch"),
		ResultsRoot: filepath.Join(f.root, "results"), Lease: time.Minute, Timeout: time.Minute, Out: &out})
	if !errors.Is(err, ci.ErrInfra) || res.Summary != "" || time.Since(began) > 10*time.Second {
		t.Fatalf("stopped run = %+v, %v after %v; want ErrInfra at once\n%s", res, err, time.Since(began), out.String())
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryPending || rec["token"] != "" || rec["attempt"] != "0" || !strings.HasPrefix(rec["blocked"], "infra: ") {
		t.Fatalf("record after a stopped run: %v", rec)
	}
	if f.client.Exists(f.ctx, ci.ReceiptKey(runRepo, f.sha, "slow")).Val() != 0 {
		t.Fatal("the check cut short wrote a receipt")
	}
}

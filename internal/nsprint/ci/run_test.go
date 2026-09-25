package ci_test

// The DONE-WHEN of nova-tools #3597 / #3349 for our own CI: request -> run
// (one fake check that passes, one that fails) -> status shows red with the
// failing check; a second run of the same sha does nothing; the Redis steps
// take under one second excluding the checks. The store is the throwaway
// redis-server every Lua-backed control uses (miniredis has no FUNCTION or
// FCALL, and every step here is one nova_sprint Function call); the repo is
// a local bare fixture with one commit.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const runRepo = "fixture"

// runFixture is a loaded store and a bare repository with one commit on dev.
type runFixture struct {
	ctx    context.Context
	st     *store.Store
	client *redis.Client
	url    string
	sha    string
	root   string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	remote := testutil.NewLocalRemote(t, "dev")
	// The runner fetches the exact sha; a bare fixture must allow that.
	git(t, remote.Dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	work := filepath.Join(t.TempDir(), "work")
	git(t, t.TempDir(), "clone", "-q", remote.URL, work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "README")
	git(t, work, "commit", "-q", "-m", "one")
	git(t, work, "push", "-q", "origin", "HEAD:dev")
	sha := git(t, work, "rev-parse", "HEAD")
	// The declared set: one check that passes (and proves the checkout is at
	// the sha) and one that fails.
	client.HSet(ctx, ci.ConfigKey(runRepo), "checks", "head,fail",
		"check:head", "git log -1 --format=%H", "check:fail", "false")
	return &runFixture{ctx: ctx, st: store.New(client), client: client, url: remote.URL, sha: sha, root: t.TempDir()}
}

func (f *runFixture) run(t *testing.T, bench string) (ci.RunResult, string, error) {
	t.Helper()
	var out bytes.Buffer
	res, err := ci.Run(f.ctx, f.st, ci.RunOptions{Bench: bench, Scratch: filepath.Join(f.root, "scratch"),
		ResultsRoot: filepath.Join(f.root, "results"), Lease: time.Minute, Timeout: 30 * time.Second, Out: &out})
	return res, out.String(), err
}

func TestCIRequestRunStatusIsRedWithTheFailingCheck(t *testing.T) {
	f := newRunFixture(t)
	f.client.HSet(f.ctx, ci.PRKey(runRepo, 7), "head", f.sha)

	r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, PR: 7, URL: f.url})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v; want CREATED", r, err)
	}
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "EXISTS" {
		t.Fatalf("second request = %v, %v; want EXISTS", r, err)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 1 {
		t.Fatalf("pool has %d members, want 1", n)
	}

	// Attempt 1 is red below the cap (cfg:ci max_attempts, default 2): the
	// head goes back to the pool for one flake retry, still pending.
	res, out, err := f.run(t, "b1")
	if err != nil || !res.Claimed || res.Summary != ci.SummaryRetry {
		t.Fatalf("first run = %+v, %v; want retry\n%s", res, err, out)
	}
	if rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val(); rec["ci"] != ci.SummaryPending || rec["last"] != "red" {
		t.Fatalf("record after a red attempt below the cap: %v", rec)
	}
	res, out, err = f.run(t, "b1")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !res.Claimed || res.Summary != ci.SummaryRed || len(res.Checks) != 2 {
		t.Fatalf("run = %+v\n%s", res, out)
	}
	if res.StoreMS >= 1000 {
		t.Fatalf("store steps took %d ms, want under one second", res.StoreMS)
	}
	if !strings.Contains(out, "CHECK head rc=0") || !strings.Contains(out, "CHECK fail rc=1") || !strings.Contains(out, " red bench=b1 ") {
		t.Fatalf("run output:\n%s", out)
	}
	headLog, err := os.ReadFile(filepath.Join(f.root, "results", "ci", runRepo, f.sha, "head.log"))
	if err != nil || strings.TrimSpace(string(headLog)) != f.sha {
		t.Fatalf("head.log = %q, %v; want the sha (the clone must be at it)", headLog, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "scratch")); err == nil {
		if entries, _ := os.ReadDir(filepath.Join(f.root, "scratch")); len(entries) != 0 {
			t.Fatalf("scratch clone left behind: %v", entries)
		}
	}

	rows, err := ci.ReadRows(f.ctx, f.st, runRepo, f.sha)
	if err != nil || !rows.Found || rows.Summary() != ci.SummaryRed {
		t.Fatalf("rows = %+v, %v; want red", rows, err)
	}
	var status bytes.Buffer
	if code := ci.WriteRows(&status, rows); code != 0 {
		t.Fatalf("status exit %d:\n%s", code, status.String())
	}
	if !strings.Contains(status.String(), " red bench=b1 attempt=2 pr=7") ||
		!strings.Contains(status.String(), "  fail red rc=1 ") || !strings.Contains(status.String(), "  head green rc=0 ") {
		t.Fatalf("status:\n%s", status.String())
	}
	if word := f.client.HGet(f.ctx, ci.PRKey(runRepo, 7), "ci").Val(); word != ci.SummaryRed {
		t.Fatalf("pr record ci=%q, want red", word)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 0 {
		t.Fatalf("pool still has %d members after the summary", n)
	}
	for _, b := range []string{"b1", "b2"} {
		if legs := f.client.HGet(f.ctx, "bench:"+b+":desired", "legs").Val(); legs != "" {
			t.Fatalf("runner-only: bench %s legs=%q were written", b, legs)
		}
	}

	// Another run of the same sha does nothing: the pool is empty, no
	// receipt changes, the record keeps attempt 2.
	before := f.client.HGetAll(f.ctx, ci.ReceiptKey(runRepo, f.sha, "fail")).Val()
	res2, out2, err := f.run(t, "b2")
	if err != nil || res2.Claimed {
		t.Fatalf("second run = %+v, %v\n%s; want nothing claimed", res2, err, out2)
	}
	if after := f.client.HGetAll(f.ctx, ci.ReceiptKey(runRepo, f.sha, "fail")).Val(); after["at"] != before["at"] || after["bench"] != "b1" {
		t.Fatalf("second run touched the receipt: %v -> %v", before, after)
	}
	if a := f.client.HGet(f.ctx, ci.RecordKey(runRepo, f.sha), "attempt").Val(); a != "2" {
		t.Fatalf("attempt=%s after the idle run, want 2", a)
	}
}

func TestCIRequestRefusesAnUndeclaredCheckAndAReservedName(t *testing.T) {
	f := newRunFixture(t)
	r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, Checks: []string{"nope"}})
	if err != nil || r.Status != "REFUSED" || !strings.Contains(r.Detail, "declared: head,fail") {
		t.Fatalf("undeclared check = %v, %v", r, err)
	}
	if _, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, Checks: []string{"gids"}}); err == nil {
		t.Fatal("a reserved suffix was accepted as a check name")
	}
	// --checks narrows the set: only the passing check runs, and the head is green.
	r, err = ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url, Checks: []string{"head"}})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("narrowed request = %v, %v", r, err)
	}
	res, out, err := f.run(t, "b1")
	if err != nil || res.Summary != ci.SummaryGreen || len(res.Checks) != 1 {
		t.Fatalf("narrowed run = %+v, %v\n%s", res, err, out)
	}
	// The default table serves a repo with no cfg: nova-tools has its checks.
	r, err = ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: "nova-tools", SHA: f.sha})
	if err != nil || r.Status != "CREATED" {
		t.Fatalf("default request = %v, %v", r, err)
	}
	if got := f.client.HGet(f.ctx, ci.RecordKey("nova-tools", f.sha), "checks").Val(); got != "go-build,go-vet,go-test-cmd,go-test-internal,internal-ci" {
		t.Fatalf("default checks = %q", got)
	}
}

func TestCIRunReleasesWhenTheCloneFailsAndFencesALapsedToken(t *testing.T) {
	f := newRunFixture(t)
	bad := "file://" + filepath.Join(t.TempDir(), "missing.git")
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: bad}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	res, out, err := f.run(t, "b1")
	if !errors.Is(err, ci.ErrBlocked) || res.Summary != "" || !strings.HasPrefix(res.Blocked, "clone:") {
		t.Fatalf("clone failure: res=%+v err=%v\n%s", res, err, out)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.sha)).Val()
	if rec["ci"] != ci.SummaryPending || rec["bench"] != "" || rec["token"] != "" || rec["attempt"] != "1" || !strings.HasPrefix(rec["blocked"], "clone:") {
		t.Fatalf("record after release: %v", rec)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 1 {
		t.Fatalf("pool has %d members after release, want 1 (back for another bench)", n)
	}
	if got := f.client.Exists(f.ctx, ci.ReceiptKey(runRepo, f.sha, "head"), ci.ReceiptKey(runRepo, f.sha, "fail")).Val(); got != 0 {
		t.Fatalf("a clone failure wrote %d receipts; no evidence is not negative evidence", got)
	}

	// A claim by another bench, then a receipt with the released token: FENCED.
	c, ok, err := ci.Claim(f.ctx, f.st, "b2", time.Minute)
	if err != nil || !ok || c.Attempt != 2 || len(c.Checks) != 2 {
		t.Fatalf("claim = %+v, %v, %v", c, ok, err)
	}
	r, err := ci.WriteReceipt(f.ctx, f.st, ci.ReceiptRecord{Repo: runRepo, SHA: f.sha, Check: "head", Token: "stale", RC: 0, Bench: "b1", Lease: time.Minute})
	if err != nil || r.Status != "FENCED" {
		t.Fatalf("stale receipt = %+v, %v; want FENCED", r, err)
	}
	if f.client.Exists(f.ctx, ci.ReceiptKey(runRepo, f.sha, "head")).Val() != 0 {
		t.Fatal("a fenced receipt was written")
	}
	// A second claim while the lease is live sees nothing.
	if _, ok, err := ci.Claim(f.ctx, f.st, "b3", time.Minute); err != nil || ok {
		t.Fatalf("claim under a live lease = %v, %v; want nothing", ok, err)
	}
}

func TestCICompareFetchesOnceAndNamesTheDiffer(t *testing.T) {
	f := newRunFixture(t)
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request = %v, %v", r, err)
	}
	for i := 0; i < 2; i++ { // red, then red again at the cap
		if _, out, err := f.run(t, "b1"); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
	}
	var calls atomic.Int64
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]string{
			{"name": "head", "status": "completed", "conclusion": "success"},
			{"name": "fail", "status": "completed", "conclusion": "success"},
		}})
	}))
	defer srv.Close()
	p, err := ci.Compare(f.ctx, f.st, ci.CompareRequest{Repo: runRepo, SHA: f.sha, Owner: "o", BaseURL: srv.URL, HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || path != "/repos/o/"+runRepo+"/commits/"+f.sha+"/check-runs" {
		t.Fatalf("calls=%d path=%s", calls.Load(), path)
	}
	var out bytes.Buffer
	if code := ci.WriteCompare(&out, p); code != 1 {
		t.Fatalf("compare exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "AGREE head ours=green actions=green via=head") ||
		!strings.Contains(out.String(), "DIFFER fail ours=red actions=green via=fail") ||
		!strings.Contains(out.String(), "PARITY "+runRepo+"@"+f.sha[:8]+" agree=1/2 ours=red actions=green") {
		t.Fatalf("compare:\n%s", out.String())
	}
	if word := f.client.HGet(f.ctx, ci.RecordKey(runRepo, f.sha), "parity").Val(); word != "differ" {
		t.Fatalf("parity field=%q", word)
	}
	if _, err := ci.Compare(f.ctx, f.st, ci.CompareRequest{Repo: runRepo, SHA: strings.Repeat("9", 40), BaseURL: srv.URL}); !errors.Is(err, ci.ErrNoRecord) {
		t.Fatalf("compare of an unrequested sha = %v; want ErrNoRecord (and no REST call)", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("an unrequested sha made a REST call (%d)", calls.Load())
	}
}

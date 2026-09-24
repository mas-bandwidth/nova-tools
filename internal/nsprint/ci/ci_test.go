package ci_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const (
	repo = "nova-tools"
	head = "1111111111111111111111111111111111111111"
	base = "2222222222222222222222222222222222222222"
	pr   = 4242
)

// startRedis starts a throwaway redis-server for a control sprint, the same
// shape as internal/nsprint/task. A missing binary fails under NOVA_CI and
// skips otherwise (internal/nsprint/testutil).
func startRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}

// fixture is a control sprint with fixture benches that beat and carry go.
type fixture struct {
	t      *testing.T
	ctx    context.Context
	st     *store.Store
	client *redis.Client
	sprint string
}

func newFixture(t *testing.T, benches ...string) *fixture {
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	f := &fixture{t: t, ctx: ctx, st: store.New(client), client: client, sprint: "control-c1c1c1c1"}
	client.HSet(ctx, "s:"+f.sprint, "status", "open")
	for _, b := range benches {
		client.SAdd(ctx, "benches", b)
		client.HSet(ctx, "bench:"+b+":desired", "slots", "4", "machine", b, "paused", "0", "legs", "go")
		client.HSet(ctx, "bench:"+b+":beat", "host", b, "at", "1")
	}
	return f
}

func (f *fixture) cut() string {
	f.t.Helper()
	r, err := ci.Cut(f.ctx, f.st, ci.CutRequest{Sprint: f.sprint, Repo: repo, PR: pr, Head: head, Base: base, Actor: "ctl"})
	if err != nil || r.Status != "CREATED" {
		f.t.Fatalf("cut = %v, %v; want CREATED", r, err)
	}
	return ci.Label(pr, head)
}

// deal stands in for the dealer (#2756 5.3, not in this slice): it gives the
// queued ci card a bench, the attempt identity and a token, and returns the
// token and identity the wrapper would carry. A rerun pinned to a bench must
// be dealt there.
func (f *fixture) deal(label, bench string) (token, identity string) {
	f.t.Helper()
	key := "s:" + f.sprint + ":card:" + label
	vals, err := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
	if err != nil || vals[0] != "queued" {
		f.t.Fatalf("deal %s: state %v, %v; want queued", label, vals, err)
	}
	if pinned, _ := vals[3].(string); pinned != "" && pinned != bench {
		f.t.Fatalf("deal %s on %s: the card is pinned to %s", label, bench, pinned)
	}
	attempt := vals[1].(string)
	identity = fmt.Sprintf("%s/%s/%s/%s/%s", f.sprint, label, vals[2], bench, attempt)
	token = attempt + ".0123456789abcdef0123456789abcdef"
	f.client.HSet(f.ctx, key, "state", "dealt", "bench", bench, "identity", identity, "token", token, "token_sha", "abcdefabcdef")
	f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
	f.client.ZRem(f.ctx, "s:"+f.sprint+":pool", label)
	f.client.ZRem(f.ctx, "s:"+f.sprint+":bench:"+bench+":queue", label)
	return token, identity
}

func (f *fixture) end(label, token, identity, outcome, reason, verdict, pkg, test string) ci.Result {
	f.t.Helper()
	r, err := ci.End(f.ctx, f.st, ci.EndRecord{
		Sprint: f.sprint, Label: label, Token: token, Identity: identity,
		Outcome: outcome, Reason: reason, Verdict: verdict, Pkg: pkg, Test: test,
		WallS: 42, Log: "results/" + identity, Tree: "3333333333333333333333333333333333333333", Actor: "ctl-wrapper",
	})
	if err != nil {
		f.t.Fatalf("end: %v", err)
	}
	return r
}

func (f *fixture) record() map[string]string {
	f.t.Helper()
	m, err := f.client.HGetAll(f.ctx, ci.RecordKey(repo, head)).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) landReady() (bool, string) {
	f.t.Helper()
	ok, why, err := ci.LandReady(f.ctx, f.st, repo, head, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return ok, why
}

func (f *fixture) logCount(kind, attempt string) int {
	f.t.Helper()
	msgs, err := f.client.XRange(f.ctx, "s:"+f.sprint+":log", "-", "+").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Values["kind"] == kind && (attempt == "" || m.Values["attempt"] == attempt) {
			n++
		}
	}
	return n
}

func (f *fixture) unresolved() map[string]string {
	f.t.Helper()
	m, err := f.client.HGetAll(f.ctx, "s:"+f.sprint+":unresolved").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

// TestControl29VerdictRecordAndShow is #2756 control 29, the record and
// `ci show` clauses (the pr-to-read and lander clauses are #2941 and the
// lander's): the ci card ends and ci:<repo>:<sha> holds verdict, pkg, test,
// wall, bench, attempt, log, head, base, tree; `ci show <sha>` prints it; a
// head with no record is MISSING, exit 5, and never land-ready.
func TestControl29VerdictRecordAndShow(t *testing.T) {
	f := newFixture(t, "ctl-a", "ctl-b")
	label := f.cut()

	if got := f.record()["verdict"]; got != ci.Pending {
		t.Fatalf("after cut verdict = %q; want PENDING in the same call", got)
	}
	if ok, why := f.landReady(); ok {
		t.Fatalf("PENDING head is land-ready (%s)", why)
	}
	if again, _ := ci.Cut(f.ctx, f.st, ci.CutRequest{Sprint: f.sprint, Repo: repo, PR: pr, Head: head, Base: base}); again.Status != "EXISTS" || again.ExitCode() != 0 {
		t.Fatalf("second cut = %v; want EXISTS exit 0", again)
	}

	token, identity := f.deal(label, "ctl-a")
	if r := f.end(label, "1.wrong", identity, "DONE", "done", ci.OK, "", ""); r.Status != "FENCED" || r.ExitCode() != 3 {
		t.Fatalf("wrong token = %v; want FENCED exit 3", r)
	}
	if r := f.end(label, token, strings.Replace(identity, "/1", "/2", 1), "DONE", "done", ci.OK, "", ""); r.Status != "NOTHING" {
		t.Fatalf("another attempt's record = %v; want NOTHING", r)
	}
	if got := f.record()["verdict"]; got != ci.Pending {
		t.Fatalf("a fenced or foreign end wrote the record: verdict %q", got)
	}
	if r := f.end(label, token, identity, "DONE", "done", ci.OK, "", ""); r.Status != "ENDED" || r.Detail != ci.OK {
		t.Fatalf("end = %v; want ENDED OK", r)
	}

	rec := f.record()
	for _, field := range []string{"verdict", "pkg", "test", "wall_s", "bench", "attempt", "log", "head", "base", "tree", "cut_at", "end_at", "card", "source", "at"} {
		if _, ok := rec[field]; !ok {
			t.Errorf("record has no %s field", field)
		}
	}
	want := map[string]string{"verdict": "OK", "wall_s": "42", "bench": "ctl-a", "attempt": "1",
		"head": head, "base": base, "tree": "3333333333333333333333333333333333333333",
		"log": "results/" + identity, "card": f.sprint + "/" + label, "source": "card"}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record %s = %q; want %q", k, rec[k], v)
		}
	}
	if ok, why := f.landReady(); !ok {
		t.Fatalf("OK head is not land-ready: %s", why)
	}

	shown, err := ci.Read(f.ctx, f.st, repo, head)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := ci.WriteShow(&out, shown); code != 0 {
		t.Fatalf("ci show exit = %d; want 0\n%s", code, out.String())
	}
	for _, line := range []string{"ci:nova-tools:" + head + " OK", "  bench=ctl-a", "  tree=3333", "attempt 1 ci cut", "attempt 1 ci end DONE done"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("ci show lacks %q:\n%s", line, out.String())
		}
	}

	other := "4444444444444444444444444444444444444444"
	missing, err := ci.Read(f.ctx, f.st, repo, other)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := ci.WriteShow(&out, missing); code != 5 || !strings.Contains(out.String(), "MISSING") {
		t.Fatalf("ci show of an unrun head = exit %d %q; want MISSING exit 5", code, out.String())
	}
	if ok, why, _ := ci.LandReady(f.ctx, f.st, repo, other, ""); ok || why != "ci: MISSING" {
		t.Fatalf("unrun head land-ready = %v %q; want false ci: MISSING", ok, why)
	}
	if ok, why, _ := ci.LandReady(f.ctx, f.st, repo, head, other); ok {
		t.Fatalf("OK on another base counted as land-ready (%s)", why)
	}
}

// TestControl31RerunAndFlaky is #2756 control 31.
func TestControl31RerunAndFlaky(t *testing.T) {
	t.Run("FAIL on A, OK on B is FLAKY until a typed disposition", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		if r := f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX"); r.Detail != ci.Fail {
			t.Fatalf("end on A = %v; want FAIL", r)
		}
		if ok, _ := f.landReady(); ok {
			t.Fatal("FAIL head is land-ready")
		}

		reruns, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if r := reruns[label]; r.Status != "RERUN" || r.Attempt != 2 || r.Detail != "ctl-b" {
			t.Fatalf("reconciler rerun = %v; want RERUN attempt 2 on ctl-b", r)
		}
		rec := f.record()
		if rec["verdict"] != ci.Pending || rec["attempt"] != "2" {
			t.Fatalf("after the rerun cut verdict=%q attempt=%q; want PENDING 2 in the same call", rec["verdict"], rec["attempt"])
		}
		if ok, _ := f.landReady(); ok {
			t.Fatal("head with a pending rerun is land-ready")
		}
		if again, _ := ci.ReconcileReruns(f.ctx, f.st, f.sprint); len(again) != 0 {
			t.Fatalf("a second reconciler pass cut %v; want nothing while the rerun is pending", again)
		}

		token, identity = f.deal(label, "ctl-b")
		if r := f.end(label, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.Flaky {
			t.Fatalf("OK on B after FAIL on A = %v; want FLAKY", r)
		}
		rec = f.record()
		if rec["flaky"] != "ctl-a:FAIL,ctl-b:OK" {
			t.Fatalf("flaky = %q; want ctl-a:FAIL,ctl-b:OK", rec["flaky"])
		}
		if n := f.logCount("ci end", "1"); n != 1 {
			t.Fatalf("attempt 1 end receipts = %d; the FAIL receipt must stay in the log", n)
		}
		if ok, why := f.landReady(); ok || !strings.Contains(why, "FLAKY") {
			t.Fatalf("FLAKY head land-ready = %v %q; want not ready until a typed disposition", ok, why)
		}
		item := fmt.Sprintf("%d:%s:flaky:internal/x", pr, head)
		if _, ok := f.unresolved()[item]; !ok {
			t.Fatalf("no unresolved item %s: %v", item, f.unresolved())
		}

		if r, _ := ci.Rerun(f.ctx, f.st, f.sprint, label, "rowan", "third run"); r.ExitCode() != 2 {
			t.Fatalf("third run = %v; want refused exit 2", r)
		}
		if r, err := ci.Dispose(f.ctx, f.st, f.sprint, repo, head, "APPROVE", "stella", "https://example.test/disp"); err != nil || r.Status != "APPROVE" {
			t.Fatalf("dispose = %v, %v; want APPROVE", r, err)
		}
		if rec := f.record(); rec["verdict"] != ci.OK || rec["log"] != "https://example.test/disp" {
			t.Fatalf("after APPROVE verdict=%q log=%q; want OK with the disposition url", rec["verdict"], rec["log"])
		}
		if ok, why := f.landReady(); !ok {
			t.Fatalf("approved head not land-ready: %s", why)
		}
		if _, ok := f.unresolved()[item]; ok {
			t.Fatal("the flaky item stays open after its disposition")
		}
	})

	t.Run("FAIL on B with the same failure is FAIL with one fix item, and a third run is refused", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		if _, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint); err != nil {
			t.Fatal(err)
		}
		token, identity = f.deal(label, "ctl-b")
		if r := f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX"); r.Detail != ci.Fail {
			t.Fatalf("same failure on B = %v; want FAIL", r)
		}
		if again, _ := ci.ReconcileReruns(f.ctx, f.st, f.sprint); len(again) != 0 {
			t.Fatalf("reconciler cut a third run: %v", again)
		}
		if r, _ := ci.Rerun(f.ctx, f.st, f.sprint, label, "rowan", "by hand"); r.Status != "SPENT" || r.ExitCode() != 2 {
			t.Fatalf("third run by hand = %v; want SPENT exit 2", r)
		}
		fixes := 0
		for k := range f.unresolved() {
			if strings.Contains(k, ":ci-fail:") {
				fixes++
				if k != fmt.Sprintf("%d:%s:ci-fail:internal/x", pr, head) {
					t.Errorf("fix item key %q; want <pr>:<head>:ci-fail:<pkg>", k)
				}
			}
		}
		if fixes != 1 {
			t.Fatalf("fix items = %d; want exactly one", fixes)
		}
		if ok, _ := f.landReady(); ok {
			t.Fatal("FAIL head is land-ready")
		}
	})

	t.Run("no other healthy bench is a visible blocked state", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		f.client.HSet(f.ctx, "bench:ctl-b:desired", "paused", "1")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")
		reruns, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if r := reruns[label]; r.Status != "BLOCKED" {
			t.Fatalf("rerun with ctl-b paused = %v; want BLOCKED", r)
		}
		if got := f.record()["rerun"]; got != "blocked: no alternate bench" {
			t.Fatalf("record rerun = %q; want blocked: no alternate bench", got)
		}
		s, err := ci.ReadStatus(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s.String(), "blocked: no alternate bench 1") {
			t.Fatalf("ci line %q does not show the blocked state", s)
		}
	})
}

// TestControl34ExecutionStateAndVerdict is #2756 control 34, the negative
// pair: red tests end the card DONE with FAIL in the same call and no
// tests-red fix card; a killed wrapper ends FAILED crash with no verdict
// (MISSING), the reconciler cuts the one rerun on another bench, both
// attempts are receipted for cost, and the ci x/y counts one head.
func TestControl34ExecutionStateAndVerdict(t *testing.T) {
	t.Run("red tests end DONE with FAIL", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		if r := f.end(label, token, identity, "FAILED", "tests-red", "", "internal/x", "TestX"); r.Status != "RECORD" {
			t.Fatalf("FAILED tests-red on a ci card = %v; want refused RECORD", r)
		}
		if r := f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX"); r.Status != "ENDED" || r.Detail != ci.Fail {
			t.Fatalf("red tests = %v; want ENDED FAIL", r)
		}
		card, _ := f.client.HMGet(f.ctx, "s:"+f.sprint+":card:"+label, "state", "outcome", "reason").Result()
		if card[0] != "ended" || card[1] != "DONE" || card[2] != "done" {
			t.Fatalf("card = %v; want ended DONE done", card)
		}
		if got := f.record()["verdict"]; got != ci.Fail {
			t.Fatalf("verdict = %q; want FAIL written in the end call", got)
		}
		for k := range f.unresolved() {
			if strings.Contains(k, "tests-red") {
				t.Fatalf("a tests-red fix item exists for a ci card: %s", k)
			}
		}
		s, err := ci.ReadStatus(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if s.Required != 1 || s.OK != 0 || s.Fail != 1 {
			t.Fatalf("ci line %q; want the head required and not OK", s)
		}
	})

	t.Run("a killed wrapper ends FAILED crash with MISSING and one rerun", func(t *testing.T) {
		f := newFixture(t, "ctl-a", "ctl-b")
		label := f.cut()
		token, identity := f.deal(label, "ctl-a")
		if r := f.end(label, token, identity, "FAILED", "crash", ci.OK, "", ""); r.Status != "RECORD" {
			t.Fatalf("a crash carrying a verdict = %v; want refused RECORD", r)
		}
		if r := f.end(label, token, identity, "FAILED", "crash", "", "", ""); r.Status != "ENDED" || r.Detail != "" {
			t.Fatalf("crash end = %v; want ENDED with no verdict", r)
		}
		rec, err := ci.Read(f.ctx, f.st, repo, head)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if code := ci.WriteShow(&out, rec); code != 5 || rec.Verdict() != ci.Missing {
			t.Fatalf("after a crash ci show exit %d verdict %s; want MISSING exit 5\n%s", code, rec.Verdict(), out.String())
		}
		if ok, _ := f.landReady(); ok {
			t.Fatal("MISSING head is land-ready")
		}

		reruns, err := ci.ReconcileReruns(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if r := reruns[label]; r.Status != "RERUN" || r.Detail != "ctl-b" {
			t.Fatalf("rerun after crash = %v; want RERUN on ctl-b", r)
		}
		if again, _ := ci.ReconcileReruns(f.ctx, f.st, f.sprint); len(again) != 0 {
			t.Fatalf("second pass cut %v; want the one rerun only", again)
		}
		token, identity = f.deal(label, "ctl-b")
		if r := f.end(label, token, identity, "DONE", "done", ci.OK, "", ""); r.Detail != ci.OK {
			t.Fatalf("OK after a crash = %v; want OK (a crash is not a discordant verdict)", r)
		}
		if n := f.logCount("ci end", ""); n != 2 {
			t.Fatalf("ci end receipts = %d; want both attempts charged", n)
		}
		// Every attempt's duration is on its own end receipt: the record keeps
		// only the latest attempt's wall_s, the stream keeps each one.
		msgs, err := f.client.XRange(f.ctx, "s:"+f.sprint+":log", "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		timed := 0
		for _, m := range msgs {
			if m.Values["kind"] == "ci end" && strings.Contains(fmt.Sprint(m.Values["evidence"]), " wall_s=42 ") {
				timed++
			}
		}
		if timed != 2 {
			t.Fatalf("ci end receipts carrying wall_s = %d; want 2 (one per attempt)", timed)
		}
		s, err := ci.ReadStatus(f.ctx, f.st, f.sprint)
		if err != nil {
			t.Fatal(err)
		}
		if s.Required != 1 || s.OK != 1 {
			t.Fatalf("ci line %q; want 1/1: one head however many attempts", s)
		}
	})
}

// TestCutRefusesARunnerOnlyLeg: ci cut refuses rather than cut a card no
// bench can take (4.8, 10.4 item 3); a bench that declares no legs carries
// none.
func TestCutRefusesARunnerOnlyLeg(t *testing.T) {
	f := newFixture(t, "ctl-a")
	f.client.HDel(f.ctx, "bench:ctl-a:desired", "legs")
	r, err := ci.Cut(f.ctx, f.st, ci.CutRequest{Sprint: f.sprint, Repo: repo, PR: pr, Head: head, Base: base})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "RUNNER-ONLY" || r.ExitCode() != 2 {
		t.Fatalf("cut with no go bench = %v; want RUNNER-ONLY exit 2", r)
	}
	if n, _ := f.client.Exists(f.ctx, ci.RecordKey(repo, head)).Result(); n != 0 {
		t.Fatal("a refused cut wrote the record")
	}
}

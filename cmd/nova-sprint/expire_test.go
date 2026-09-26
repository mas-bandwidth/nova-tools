//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// ---- fixtures ----

// expireForge serves only POST /repos/ctl-org/ctl-repo/pulls, with the reply
// chosen per head. Any other method or path is a forge read and fails the
// test (#2930 rev 5: GitHub is a git remote only).
type expireForge struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	posts   map[string]int
	prs     map[string]int
	replies map[string][2]string // head -> {status, body}
	lost    map[string]bool      // head -> the PR is made and the reply lost
	reads   atomic.Int64
	next    int
}

func newExpireForge(t *testing.T) *expireForge {
	f := &expireForge{t: t, posts: map[string]int{}, prs: map[string]int{}, replies: map[string][2]string{}, lost: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/ctl-org/ctl-repo/pulls" {
			f.reads.Add(1)
			t.Errorf("forge read: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		head := body["head"]
		f.posts[head]++
		if rep, ok := f.replies[head]; ok {
			code, _ := strconv.Atoi(rep[0])
			w.WriteHeader(code)
			_, _ = w.Write([]byte(rep[1]))
			return
		}
		f.next++
		f.prs[head]++
		if f.lost[head] {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": fmt.Sprintf("https://github.test/ctl-org/ctl-repo/pull/%d", f.next)})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *expireForge) host() reconcile.RESTPRHost {
	return reconcile.RESTPRHost{BaseURL: f.srv.URL, Token: "ctl", HTTP: f.srv.Client()}
}

func (f *expireForge) counts(head string) (posts, prs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[head], f.prs[head]
}

// fakeProber is the benches' evidence session (CI-NET: no host). It answers
// from a fixed map, or fails a whole bench named in down.
type fakeProber struct {
	mu    sync.Mutex
	ev    map[string]reconcile.Evidence // sprint/label -> evidence
	down  map[string]bool               // bench -> unreachable
	calls map[string]int                // bench -> Probe calls
}

func (p *fakeProber) Probe(_ context.Context, b deal.Bench, cards []reconcile.Suspect) (map[string]reconcile.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[b.Name]++
	if p.down[b.Name] {
		return nil, errors.New("ssh: connect to host " + b.Name + ": no route to host")
	}
	out := map[string]reconcile.Evidence{}
	for _, c := range cards {
		if e, ok := p.ev[c.Key()]; ok {
			out[c.Key()] = e
		}
	}
	return out, nil
}

func (p *fakeProber) total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		n += c
	}
	return n
}

// expireTrips counts round trips: single commands and pipelines, and the
// FCALLs inside them.
type expireTrips struct {
	singles, pipes, fcalls atomic.Int64
}

func (h *expireTrips) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (h *expireTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.singles.Add(1)
		if strings.EqualFold(cmd.Name(), "fcall") {
			h.fcalls.Add(1)
		}
		return next(ctx, cmd)
	}
}

func (h *expireTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipes.Add(1)
		for _, c := range cmds {
			if strings.EqualFold(c.Name(), "fcall") {
				h.fcalls.Add(1)
			}
		}
		return next(ctx, cmds)
	}
}

func (h *expireTrips) reset() { h.singles.Store(0); h.pipes.Store(0); h.fcalls.Store(0) }

type expireFixture struct {
	t    *testing.T
	ctx  context.Context
	addr string
	c    *redis.Client
	st   *store.Store
}

func newExpireFixture(t *testing.T) *expireFixture {
	t.Helper()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return &expireFixture{t: t, ctx: ctx, addr: addr, c: c, st: store.New(c)}
}

func (x *expireFixture) must(err error) {
	x.t.Helper()
	if err != nil && !errors.Is(err, redis.Nil) {
		x.t.Fatal(err)
	}
}

func (x *expireFixture) now() int64 {
	x.t.Helper()
	t, err := x.c.Time(x.ctx).Result()
	x.must(err)
	return t.UnixMilli()
}

func (x *expireFixture) sprint(S string, policy ...string) {
	x.t.Helper()
	x.must(x.c.SAdd(x.ctx, "sprints", S).Err())
	x.must(x.c.ZAdd(x.ctx, "sprint:order", redis.Z{Score: 1, Member: S}).Err())
	x.must(x.c.HSet(x.ctx, "s:"+S, "status", "open").Err())
	fields := append([]string{"share", "1", "backpressure_missing", "open"}, policy...)
	x.must(x.c.HSet(x.ctx, reconcile.PolicyKey(S), fields).Err())
}

func (x *expireFixture) bench(b string) {
	x.t.Helper()
	x.must(x.c.SAdd(x.ctx, "benches", b).Err())
	x.must(x.c.HSet(x.ctx, "bench:"+b+":desired", "slots", "0").Err())
	x.must(x.c.HSet(x.ctx, "bench:"+b+":beat", "host", b, "at", "1").Err())
}

// seed writes one card hash in state with fields, in its state index.
func (x *expireFixture) seed(S, label, state string, fields ...string) {
	x.t.Helper()
	h := map[string]string{"state": state, "attempt": "1", "retries": "0", "priority": "5",
		"bench": "exp-bench", "base_sha": "0123abcd", "token_sha": "sha-" + label,
		"identity": S + "/" + label + "/0123abcd/exp-bench/1"}
	for i := 0; i+1 < len(fields); i += 2 {
		h[fields[i]] = fields[i+1]
	}
	x.must(x.c.HSet(x.ctx, card.CardKey(S, label), h).Err())
	x.must(x.c.SAdd(x.ctx, card.IdxKey(S, state), label).Err())
	if state == "ended" {
		x.must(x.c.SAdd(x.ctx, card.BenchEndedKey(S, h["bench"]), label).Err())
	}
}

func (x *expireFixture) hash(key string) map[string]string {
	x.t.Helper()
	h, err := x.c.HGetAll(x.ctx, key).Result()
	x.must(err)
	return h
}

func (x *expireFixture) card(S, label string) map[string]string {
	return x.hash(card.CardKey(S, label))
}

// receipts counts s:<S>:log entries for id with the given to.
func (x *expireFixture) receipts(S, id, to string) int {
	x.t.Helper()
	msgs, err := x.c.XRange(x.ctx, card.LogKey(S), "-", "+").Result()
	x.must(err)
	n := 0
	for _, m := range msgs {
		if m.Values["id"] == id && m.Values["to"] == to {
			n++
		}
	}
	return n
}

func (x *expireFixture) inPool(S, label string) bool {
	_, err := x.c.ZScore(x.ctx, "s:"+S+":pool", label).Result()
	return err == nil
}

func (x *expireFixture) lease(host string) *reconcile.Lease {
	x.t.Helper()
	l, err := reconcile.Acquire(x.ctx, x.st, reconcile.AcquireOptions{Host: host})
	if err != nil {
		x.t.Fatalf("acquire: %v", err)
	}
	return l
}

// agePending moves a pending idem key's begin time (its value's at_ms and its
// index score) ms into the past against Redis TIME: CI-WAITS, no sleep.
func (x *expireFixture) agePending(S, key string, ms int64) {
	x.t.Helper()
	v, err := x.c.HGet(x.ctx, card.IdemKey(S), key).Result()
	x.must(err)
	if !strings.HasPrefix(v, "pending:") {
		x.t.Fatalf("idem %s = %q, want pending:*", key, v)
	}
	at := x.now() - ms
	who := v[len("pending:"):strings.LastIndex(v, ":")]
	x.must(x.c.HSet(x.ctx, card.IdemKey(S), key, fmt.Sprintf("pending:%s:%d", who, at)).Err())
	x.must(x.c.ZAdd(x.ctx, reconcile.PendingIndexKey(S), redis.Z{Score: float64(at), Member: key}).Err())
}

// onlyExpireDuty runs the verb with the refill and the expire duty only, over
// the fixture seams, and restores the registry after the test.
func onlyExpireDuty(t *testing.T, p reconcile.Prober) {
	t.Helper()
	seams, registered, prober := reconcileSeams, reconcileDuties, expireProber
	t.Cleanup(func() { reconcileSeams, reconcileDuties, expireProber = seams, registered, prober })
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
	expireProber = func() reconcile.Prober { return p }
	var only []reconcileDutyBuilder
	for _, b := range registered {
		if b.Name == "expire" {
			only = append(only, b)
		}
	}
	if len(only) != 1 {
		t.Fatalf("the expire duty is registered %d times, want once", len(only))
	}
	reconcileDuties = only
}

// ---- the DONE-WHEN control ----

// TestExpireDutyReplacesSprintRequeue is #2930 rev 5's DONE-WHEN: one
// `nova-sprint reconcile --once` pass, through the registered expire duty,
// does what rowan-tools bin/sprint-requeue did and what it never could: it
// feeds a crashed card back once, expires dealt and silent cards, resolves
// reconcile-required cards from bench evidence, and flips a PR open that is
// pending past open_ms to ambiguous, all from Redis, with the forge only
// ever POSTed to.
func TestExpireDutyReplacesSprintRequeue(t *testing.T) {
	x := newExpireFixture(t)
	const S, B = "expire-2930a0b1", "exp-bench"
	const repo = "ctl-org/ctl-repo"
	const headF, headG = "nova/expire-2930a0b1/f-card-a1", "nova/expire-2930a0b1/g-card-a1"
	x.sprint(S)
	x.bench(B)
	now := x.now()
	old := func(ms int64) string { return strconv.FormatInt(now-ms, 10) }

	// (a) one retryable end, one that already used its retry.
	x.seed(S, "a-idle", "ended", "outcome", "FAILED", "reason", "idle-killed", "exit", "137", "pushed_sha", "")
	x.seed(S, "a-second", "ended", "outcome", "FAILED", "reason", "idle-killed", "exit", "137", "pushed_sha", "",
		"attempt", "2", "retries", "1", "retry_of", "1")
	// (b) ends that may have left an effect or a finding.
	x.seed(S, "b-red", "ended", "outcome", "FAILED", "reason", "tests-red", "exit", "1")
	x.seed(S, "b-timeout", "ended", "outcome", "FAILED", "reason", "timeout", "exit", "-1")
	x.seed(S, "b-blocked", "ended", "outcome", "BLOCKED", "reason", "blocked", "exit", "0")
	x.seed(S, "b-abstain", "ended", "outcome", "ABSTAIN", "reason", "abstain", "exit", "0")
	x.seed(S, "b-pushed", "ended", "outcome", "FAILED", "reason", "idle-killed", "exit", "137",
		"pushed_sha", "89abcdef0123456789abcdef0123456789abcdef")
	// (c) a silent running card and a dealt card with no ack and no live identity.
	x.seed(S, "c-silent", "running", "launched_at", old(400000), "beat_at", old(181000))
	x.must(x.c.ZAdd(x.ctx, card.BenchWorkingKey(B), redis.Z{Score: float64(now), Member: S + "/c-silent/1"}).Err())
	x.seed(S, "c-dealt", "dealt", "dealt_at", old(61000))
	x.must(x.c.ZAdd(x.ctx, card.BenchWorkingKey(B), redis.Z{Score: float64(now), Member: S + "/c-dealt/1"}).Err())
	// (d) reconcile-required cards: one whose branch exists, one proven absent.
	x.seed(S, "d-branch", "reconcile-required", "branch", "nova/"+S+"/d-branch-a1", "jobdir", "/jobs/d-branch", "repo", repo)
	x.seed(S, "d-absent", "reconcile-required", "branch", "nova/"+S+"/d-absent-a1", "jobdir", "/jobs/d-absent", "repo", repo)
	prober := &fakeProber{ev: map[string]reconcile.Evidence{
		S + "/d-branch": {Branch: "nova/" + S + "/d-branch-a1", PushedSHA: "0123456789abcdef0123456789abcdef01234567"},
		S + "/d-absent": {Absent: true},
	}}

	// (f) and (g) run under a worker's lease before the pass.
	forge := newExpireForge(t)
	forge.lost[headF] = true
	forge.replies[headG] = [2]string{"422", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"}]}`}
	keyF, keyG := reconcile.PRKey(repo, headF), reconcile.PRKey(repo, headG)
	worker := x.lease("ctl-worker")
	ensure := func(head, who string) reconcile.PRResult {
		t.Helper()
		r, _ := reconcile.EnsurePR(x.ctx, x.st, forge.host(), reconcile.PRRequest{Sprint: S, Repo: repo, Branch: head,
			Base: "dev", Title: "t", Body: "b", Who: who, Fence: worker.Token()})
		return r
	}
	if _, err := reconcile.EnsurePR(x.ctx, x.st, forge.host(), reconcile.PRRequest{Sprint: S, Repo: repo, Branch: headF,
		Base: "dev", Title: "t", Body: "b", Who: "harvest-1", Fence: worker.Token()}); err == nil {
		t.Fatal("(f) EnsurePR with the reply lost returned no error")
	}
	var inFlight []string
	for i := range 5 {
		inFlight = append(inFlight, ensure(headF, fmt.Sprintf("harvest-2.%d", i)).Status)
	}
	rejected := ensure(headG, "harvest-1")
	x.agePending(S, keyF, 61000)
	x.must(worker.Release(x.ctx))
	unresolvedBefore := x.hash("s:" + S + ":unresolved")

	onlyExpireDuty(t, prober)
	var out, errOut bytes.Buffer
	if code := runReconcile(x.ctx, []string{"--redis", x.addr, "--host", "ctl-host", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("reconcile --once exit %d; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "DUTIES refill,expire\n") {
		t.Fatalf("stdout %q, want `DUTIES refill,expire`", out.String())
	}
	proc := x.hash(reconcile.ProcKey)

	t.Run("a_crash_requeued_once", func(t *testing.T) {
		h := x.card(S, "a-idle")
		if h["state"] != "queued" || h["reason"] != "retry:idle-killed" || h["retry_of"] != "1" || h["retries"] != "1" || h["retried_at"] == "" {
			t.Fatalf("a-idle after the pass: %v", h)
		}
		if h["attempt"] != "1" || h["outcome"] != "FAILED" {
			t.Fatalf("a-idle: attempt %q outcome %q; the next deal takes attempt 2, the end fields stay the wrapper's", h["attempt"], h["outcome"])
		}
		if !x.inPool(S, "a-idle") || x.receipts(S, "a-idle", "queued") != 1 {
			t.Fatalf("a-idle: in pool %v, queued receipts %d; want in pool, 1", x.inPool(S, "a-idle"), x.receipts(S, "a-idle", "queued"))
		}
		if idem, _ := x.c.HGet(x.ctx, card.IdemKey(S), "retry:"+S+"/a-idle/1").Result(); idem == "" {
			t.Fatal("a-idle: no idem key retry:<S>/a-idle/1")
		}
		if h := x.card(S, "a-second"); h["state"] != "ended" || x.inPool(S, "a-second") {
			t.Fatalf("a-second (retries=1, retry_max=1) was fed back: %v", h)
		}
	})
	t.Run("b_effect_or_finding_never_requeued", func(t *testing.T) {
		for _, l := range []string{"b-red", "b-timeout", "b-blocked", "b-abstain", "b-pushed"} {
			if h := x.card(S, l); h["state"] != "ended" || x.inPool(S, l) || x.receipts(S, l, "queued") != 0 {
				t.Errorf("%s was requeued: %v", l, h)
			}
		}
	})
	t.Run("c_silent_required_dealt_queued", func(t *testing.T) {
		if h := x.card(S, "c-silent"); h["state"] != "reconcile-required" || h["reason"] != "beat-lost" || x.inPool(S, "c-silent") {
			t.Fatalf("c-silent: %v", h)
		}
		if h := x.card(S, "c-dealt"); h["state"] != "queued" || h["reason"] != "spawn-timeout" || !x.inPool(S, "c-dealt") {
			t.Fatalf("c-dealt: %v", h)
		}
	})
	t.Run("d_required_from_evidence", func(t *testing.T) {
		if h := x.card(S, "d-branch"); h["state"] != "orphan-effect" {
			t.Fatalf("d-branch: %v", h)
		}
		if v, _ := x.c.HGet(x.ctx, "s:"+S+":unresolved", "d-branch:orphan-effect:1").Result(); v == "" {
			t.Fatal("d-branch: no orphan-effect unresolved item")
		}
		if h := x.card(S, "d-absent"); h["state"] != "queued" || h["reason"] != "lost" || !x.inPool(S, "d-absent") {
			t.Fatalf("d-absent: %v", h)
		}
		if n := prober.total(); n != 1 {
			t.Fatalf("evidence sessions = %d, want 1 (one per bench)", n)
		}
	})
	t.Run("e_pass_counters", func(t *testing.T) {
		if proc["retried"] != "1" || proc["expired"] != "2" || proc["ambiguous"] != "1" {
			t.Fatalf("proc:reconciler retried=%q expired=%q ambiguous=%q, want 1 2 1", proc["retried"], proc["expired"], proc["ambiguous"])
		}
		d, _ := strconv.Atoi(proc["dealt"])
		r, _ := strconv.Atoi(proc["routed"])
		if proc["n"] != strconv.Itoa(d+r+2+1+1) {
			t.Fatalf("proc:reconciler n=%q, want dealt+routed+expired+retried+ambiguous = %d", proc["n"], d+r+4)
		}
		if proc[reconcile.ExpireStampField(S)] == "" {
			t.Fatal("no expire_at stamp for the sprint")
		}
	})
	t.Run("f_lost_reply_pending_then_ambiguous", func(t *testing.T) {
		for i, s := range inFlight {
			if s != "IN-FLIGHT" {
				t.Fatalf("retry %d before the pass: %s, want IN-FLIGHT", i, s)
			}
		}
		v, _ := x.c.HGet(x.ctx, card.IdemKey(S), keyF).Result()
		if !strings.HasPrefix(v, "ambiguous:harvest-1:") {
			t.Fatalf("idem %s = %q after the pass, want ambiguous:harvest-1:*", keyF, v)
		}
		if n, _ := x.c.ZCard(x.ctx, reconcile.PendingIndexKey(S)).Result(); n != 0 {
			t.Fatalf("pending index holds %d after the pass, want 0", n)
		}
		if got := x.receipts(S, keyF, "ambiguous"); got != 1 {
			t.Fatalf("ambiguous receipts = %d, want 1", got)
		}
		if v, _ := x.c.HGet(x.ctx, "s:"+S+":unresolved", reconcile.UnresolvedField(keyF)).Result(); v == "" {
			t.Fatal("no pr-ambiguous unresolved item")
		}
		after := x.lease("ctl-worker-2")
		for i := range 5 {
			r, err := reconcile.EnsurePR(x.ctx, x.st, forge.host(), reconcile.PRRequest{Sprint: S, Repo: repo, Branch: headF,
				Base: "dev", Title: "t", Body: "b", Who: "harvest-3", Fence: after.Token()})
			if err != nil || r.Status != "AMBIGUOUS" {
				t.Fatalf("retry %d after the pass: %+v %v, want AMBIGUOUS", i, r, err)
			}
		}
		x.must(after.Release(x.ctx))
		if posts, prs := forge.counts(headF); posts != 1 || prs != 1 || forge.reads.Load() != 0 {
			t.Fatalf("forge on %s: %d POSTs, %d PRs, %d reads; want 1 1 0", headF, posts, prs, forge.reads.Load())
		}
	})
	t.Run("g_validation_422_rejected_counted_nowhere", func(t *testing.T) {
		if rejected.Status != "REJECTED" {
			t.Fatalf("EnsurePR on %s: %+v, want REJECTED", headG, rejected)
		}
		if v, _ := x.c.HGet(x.ctx, card.IdemKey(S), keyG).Result(); v != "" {
			t.Fatalf("idem %s = %q, want no key", keyG, v)
		}
		if _, err := x.c.ZScore(x.ctx, reconcile.PendingIndexKey(S), keyG).Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("pending index still holds %s", keyG)
		}
		if got := x.receipts(S, keyG, "rejected-open"); got != 1 {
			t.Fatalf("rejected-open receipts = %d, want 1", got)
		}
		after := x.hash("s:" + S + ":unresolved")
		if _, ok := after[reconcile.UnresolvedField(keyG)]; ok {
			t.Fatalf("a rejected open raised %s", reconcile.UnresolvedField(keyG))
		}
		if len(unresolvedBefore) != 0 {
			t.Fatalf("unresolved before the pass: %v, want none", unresolvedBefore)
		}
		if proc["ambiguous"] != "1" {
			t.Fatalf("proc:reconciler ambiguous=%q, want 1: a rejected open is counted nowhere", proc["ambiguous"])
		}
		if posts, prs := forge.counts(headG); posts != 1 || prs != 0 || forge.reads.Load() != 0 {
			t.Fatalf("forge on %s: %d POSTs, %d PRs, %d reads; want 1 0 0", headG, posts, prs, forge.reads.Load())
		}
	})
}

// ---- controls ----

// TestExpireStaleFenceWritesNothing: an instance whose lease another instance
// took runs a sweep over due candidates of every kind and writes nothing: no
// card, index, idem, receipt, counter or expire_at stamp.
func TestExpireStaleFenceWritesNothing(t *testing.T) {
	t.Parallel()

	x := newExpireFixture(t)
	const S = "expire-fence0001"
	x.sprint(S)
	x.bench("exp-bench")
	now := x.now()
	x.seed(S, "silent", "running", "beat_at", strconv.FormatInt(now-181000, 10))
	x.seed(S, "dealt", "dealt", "dealt_at", strconv.FormatInt(now-61000, 10))
	x.seed(S, "crashed", "ended", "outcome", "FAILED", "reason", "crash", "exit", "-1")
	x.seed(S, "required", "reconcile-required", "branch", "b", "jobdir", "/j", "repo", "o/r")
	key := reconcile.PRKey("o/r", "b")
	x.must(x.c.HSet(x.ctx, card.IdemKey(S), key, fmt.Sprintf("pending:w:%d", now-61000)).Err())
	x.must(x.c.ZAdd(x.ctx, reconcile.PendingIndexKey(S), redis.Z{Score: float64(now - 61000), Member: key}).Err())
	x.must(x.c.HSet(x.ctx, reconcile.ProcKey, "expired", "7", reconcile.ExpireStampField(S), strconv.FormatInt(now-60000, 10)).Err())

	stale := x.lease("ctl-stale")
	// Another instance holds the lease now.
	x.must(x.c.HSet(x.ctx, "lease:reconciler", "token", "999.other", "instance", "other").Err())

	snap := func() string {
		var b strings.Builder
		keys, err := x.c.Keys(x.ctx, "s:"+S+":*").Result()
		x.must(err)
		slices.Sort(keys)
		keys = append(keys, reconcile.ProcKey, card.BenchWorkingKey("exp-bench"), card.BenchWorkingKey("exp-bench"))
		for _, k := range keys {
			typ, _ := x.c.Type(x.ctx, k).Result()
			switch typ {
			case "hash":
				fmt.Fprintf(&b, "%s %v\n", k, x.hash(k))
			case "set":
				m, _ := x.c.SMembers(x.ctx, k).Result()
				fmt.Fprintf(&b, "%s %d %v\n", k, len(m), m)
			case "zset":
				m, _ := x.c.ZRangeWithScores(x.ctx, k, 0, -1).Result()
				fmt.Fprintf(&b, "%s %v\n", k, m)
			case "stream":
				n, _ := x.c.XLen(x.ctx, k).Result()
				fmt.Fprintf(&b, "%s xlen=%d\n", k, n)
			}
		}
		return b.String()
	}
	before := snap()
	prober := &fakeProber{ev: map[string]reconcile.Evidence{S + "/required": {Absent: true}}}
	duty := &reconcile.Expire{Client: x.c, Prober: prober}
	c, err := duty.Run(x.ctx, stale)
	if !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("stale sweep: %+v %v, want ErrFenced", c, err)
	}
	if c != (reconcile.Counts{}) {
		t.Fatalf("stale sweep counted %+v", c)
	}
	if after := snap(); after != before {
		t.Fatalf("a stale sweep wrote:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	r, err := x.c.FCall(x.ctx, "ns_expire_stamp", nil, stale.Token(), S).Text()
	if err != nil || !strings.HasPrefix(r, "3|FENCED|") {
		t.Fatalf("ns_expire_stamp with a stale token: %q %v, want FENCED", r, err)
	}
}

// TestExpireSweepThreeRoundTrips: a sweep over 200 cards (every kind due) and
// 50 pending keys is at most 3 Redis pipelines after its gate read, and no
// single command.
func TestExpireSweepThreeRoundTrips(t *testing.T) {
	t.Parallel()

	x := newExpireFixture(t)
	const S = "expire-trips0001"
	x.sprint(S)
	x.bench("exp-bench")
	prober := &fakeProber{ev: map[string]reconcile.Evidence{}}
	duty := &reconcile.Expire{Client: x.c, Prober: prober}
	l := x.lease("ctl-trips")
	// A first sweep over the empty sprint loads the library and learns the index.
	if _, err := duty.Run(x.ctx, l); err != nil {
		t.Fatal(err)
	}
	now := x.now()
	for i := range 200 {
		label := fmt.Sprintf("card-%03d", i)
		switch i % 4 {
		case 0:
			x.seed(S, label, "running", "beat_at", strconv.FormatInt(now-181000, 10))
		case 1:
			x.seed(S, label, "dealt", "dealt_at", strconv.FormatInt(now-61000, 10))
		case 2:
			x.seed(S, label, "ended", "outcome", "FAILED", "reason", "idle-killed")
		case 3:
			x.seed(S, label, "reconcile-required", "branch", "nova/"+label, "jobdir", "/j/"+label, "repo", "o/r")
			prober.ev[S+"/"+label] = reconcile.Evidence{Absent: true}
		}
	}
	for i := range 50 {
		key := reconcile.PRKey("o/r", fmt.Sprintf("head-%02d", i))
		x.must(x.c.HSet(x.ctx, card.IdemKey(S), key, fmt.Sprintf("pending:w:%d", now-61000)).Err())
		x.must(x.c.ZAdd(x.ctx, reconcile.PendingIndexKey(S), redis.Z{Score: float64(now - 61000), Member: key}).Err())
	}
	x.must(x.c.HDel(x.ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Err())

	trips := &expireTrips{}
	x.c.AddHook(trips)
	c, err := duty.Run(x.ctx, l)
	settle(t, duty)
	if err != nil {
		t.Fatal(err)
	}
	if want := (reconcile.Counts{Expired: 100, Retried: 50, Ambiguous: 50}); c != want {
		t.Fatalf("sweep counts %+v, want %+v", c, want)
	}
	if s, p := trips.singles.Load(), trips.pipes.Load(); s != 0 || p-1 > 3 {
		t.Fatalf("sweep made %d single commands and %d pipelines after the gate; want 0 and at most 3", s, p-1)
	}
	if n, _ := x.c.SCard(x.ctx, card.IdxKey(S, "reconcile-required")).Result(); n != 50 {
		t.Fatalf("reconcile-required = %d after the sweep, want 50 (the silent ones; the absent ones queued)", n)
	}
}

// TestExpireGatedToPolicy: the duty sweeps a sprint at most once per
// expire_every_ms. A pass inside the interval is one round trip for every
// sprint (the gate), no FCALL and no evidence session.
func TestExpireGatedToPolicy(t *testing.T) {
	t.Parallel()

	x := newExpireFixture(t)
	sprints := []string{"expire-gate0001", "expire-gate0002"}
	x.bench("exp-bench")
	for _, S := range sprints {
		x.sprint(S, "expire_every_ms", "10000")
	}
	prober := &fakeProber{}
	duty := &reconcile.Expire{Client: x.c, Prober: prober}
	l := x.lease("ctl-gate")
	silent := func(label string) {
		for _, S := range sprints {
			x.seed(S, label, "running", "beat_at", strconv.FormatInt(x.now()-181000, 10))
			x.seed(S, label+"-req", "reconcile-required", "branch", "b", "jobdir", "/j", "repo", "o/r")
		}
	}
	silent("first")
	c, err := duty.Run(x.ctx, l)
	settle(t, duty)
	if err != nil || c.Expired != 2 {
		t.Fatalf("first pass: %+v %v, want a sweep expiring 2", c, err)
	}
	for _, S := range sprints {
		if v, _ := x.c.HGet(x.ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Result(); v == "" {
			t.Fatalf("first pass left no expire_at:%s", S)
		}
	}
	sessions := prober.total()

	trips := &expireTrips{}
	x.c.AddHook(trips)
	gated := func(what string) {
		t.Helper()
		procBefore := x.hash(reconcile.ProcKey)
		calls := prober.total()
		trips.reset()
		c, err := duty.Run(x.ctx, l)
		settle(t, duty)
		if err != nil || c != (reconcile.Counts{}) {
			t.Fatalf("%s: %+v %v, want nothing", what, c, err)
		}
		if s, p, f := trips.singles.Load(), trips.pipes.Load(), trips.fcalls.Load(); s != 0 || p != 1 || f != 0 {
			t.Fatalf("%s: %d singles, %d pipelines, %d FCALLs; want 0 1 0 (the gate read for both sprints)", what, s, p, f)
		}
		if prober.total() != calls {
			t.Fatalf("%s: an evidence session ran", what)
		}
		if after := x.hash(reconcile.ProcKey); !sameMap(procBefore, after) {
			t.Fatalf("%s: proc:reconciler changed: %v -> %v", what, procBefore, after)
		}
		for _, S := range sprints {
			if st := x.card(S, "second")["state"]; st != "running" {
				t.Fatalf("%s: %s/second is %q, want running (no sweep)", what, S, st)
			}
		}
	}
	silent("second")
	// The very next 1 s pass: no second sweep.
	gated("the next pass")
	// expire_at 1000 ms before Redis TIME for both sprints.
	for _, S := range sprints {
		x.must(x.c.HSet(x.ctx, reconcile.ProcKey, reconcile.ExpireStampField(S), strconv.FormatInt(x.now()-1000, 10)).Err())
	}
	gated("1000 ms after the stamp")
	// 10000 ms before: sweeps again.
	for _, S := range sprints {
		x.must(x.c.HSet(x.ctx, reconcile.ProcKey, reconcile.ExpireStampField(S), strconv.FormatInt(x.now()-10000, 10)).Err())
	}
	trips.reset()
	c, err = duty.Run(x.ctx, l)
	settle(t, duty)
	if err != nil || c.Expired != 2 || trips.fcalls.Load() == 0 {
		t.Fatalf("10000 ms after the stamp: %+v %v (%d FCALLs), want a sweep expiring 2", c, err, trips.fcalls.Load())
	}
	if prober.total() <= sessions {
		t.Fatal("the second sweep ran no evidence session")
	}
}

// TestUnreachableBenchIsNotAbsence: a bench that cannot be reached, or whose
// git ls-remote failed, gives no evidence; its reconcile-required cards stay
// reconcile-required, with no receipt and no unresolved item, while a
// reachable bench's card resolves in the same sweep.
func TestUnreachableBenchIsNotAbsence(t *testing.T) {
	t.Parallel()

	x := newExpireFixture(t)
	const S = "expire-down0001"
	x.sprint(S)
	x.bench("down-bench")
	x.bench("up-bench")
	x.seed(S, "on-down", "reconcile-required", "bench", "down-bench", "branch", "b1", "jobdir", "/j1", "repo", "o/r")
	x.seed(S, "on-up", "reconcile-required", "bench", "up-bench", "branch", "b2", "jobdir", "/j2", "repo", "o/r")
	prober := &fakeProber{
		down: map[string]bool{"down-bench": true},
		ev: map[string]reconcile.Evidence{
			S + "/on-down": {Absent: true}, // never returned: the bench is down
			S + "/on-up":   {Absent: true},
		},
	}
	duty := &reconcile.Expire{Client: x.c, Prober: prober}
	l := x.lease("ctl-down")
	if _, err := duty.Run(x.ctx, l); err != nil {
		t.Fatal(err)
	}
	settle(t, duty)
	if h := x.card(S, "on-down"); h["state"] != "reconcile-required" || x.inPool(S, "on-down") {
		t.Fatalf("a card on an unreachable bench left reconcile-required: %v", h)
	}
	if n := x.receipts(S, "on-down", "queued") + x.receipts(S, "on-down", "orphan-effect"); n != 0 {
		t.Fatalf("on-down receipts = %d, want 0", n)
	}
	if u := x.hash("s:" + S + ":unresolved"); len(u) != 0 {
		t.Fatalf("unresolved %v, want none", u)
	}
	if h := x.card(S, "on-up"); h["state"] != "queued" {
		t.Fatalf("on-up with proven absence: %v, want queued", h)
	}

	// The bench's own answer: absence needs no dir, no pid and an ls-remote
	// that answered; an ls-remote that failed, or a dir still there, is none.
	cards := []reconcile.Suspect{
		{Sprint: S, Label: "gone", Branch: "b"},
		{Sprint: S, Label: "ls-failed", Branch: "b"},
		{Sprint: S, Label: "dir-left", Branch: "b"},
		{Sprint: S, Label: "pushed", Branch: "nova/b"},
		{Sprint: S, Label: "alive", Branch: "b"},
	}
	out := strings.Join([]string{
		S + " gone dir=0 pid=- ls=ok sha=-",
		S + " ls-failed dir=0 pid=- ls=err sha=-",
		S + " dir-left dir=1 pid=- ls=ok sha=-",
		S + " pushed dir=0 pid=- ls=ok sha=0123456789abcdef0123456789abcdef01234567",
		S + " alive dir=1 pid=4242 ls=err sha=-",
		"garbage line",
	}, "\n")
	ev := parseProbe([]byte(out), cards)
	if !ev[S+"/gone"].Absent {
		t.Errorf("gone: %+v, want proven absence", ev[S+"/gone"])
	}
	for _, l := range []string{"ls-failed", "dir-left"} {
		if e, ok := ev[S+"/"+l]; ok {
			t.Errorf("%s: %+v, want no evidence (no evidence is not negative evidence)", l, e)
		}
	}
	if e := ev[S+"/pushed"]; e.Branch != "nova/b" || e.PushedSHA == "" || e.Absent {
		t.Errorf("pushed: %+v, want the branch as an effect", e)
	}
	if e := ev[S+"/alive"]; e.LivePID != "4242" || e.Absent {
		t.Errorf("alive: %+v, want the live pid as an effect", e)
	}
}

// settle waits for the duty's evidence workers (#3802: they run off the
// pass path), so what they write is there to read.
func settle(t *testing.T, duty *reconcile.Expire) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // wall-ok: a test's give-up, not a product bound
	defer cancel()
	if !duty.Wait(ctx) {
		t.Fatal("the expire duty's evidence workers did not end within 30 s")
	}
}

// slowProber is an evidence session that does not answer until the test
// releases it (#3802: the 10.7 s bench ssh; an event, not a clock). It
// reports whether it is still in flight; ctx ending first is no evidence.
type slowProber struct {
	ev       map[string]reconcile.Evidence
	release  chan struct{}
	started  chan struct{} // gets one value per session started
	calls    atomic.Int64
	inFlight atomic.Int64
}

func (p *slowProber) Probe(ctx context.Context, _ deal.Bench, cards []reconcile.Suspect) (map[string]reconcile.Evidence, error) {
	p.calls.Add(1)
	p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	p.started <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := map[string]reconcile.Evidence{}
	for _, c := range cards {
		if e, ok := p.ev[c.Key()]; ok {
			out[c.Key()] = e
		}
	}
	return out, nil
}

// TestExpirePassNeverBlocksOnSSH is #3802's DONE-WHEN: the expire duty's
// pass returns while a bench's evidence session is still in flight (on dev
// it waited for the session, up to ExpireDeadline); the session runs in a
// bounded worker, one per bench (a pass while it is in flight starts no
// second one), and when it ends it resolves its cards and writes its row
// proc:expire:<bench>.
func TestExpirePassNeverBlocksOnSSH(t *testing.T) {
	t.Parallel()

	x := newExpireFixture(t)
	const S, B = "expire-slow3802", "slow-bench"
	x.sprint(S)
	x.bench(B)
	x.seed(S, "gone", "reconcile-required", "bench", B, "branch", "nova/"+S+"/gone-a1", "jobdir", "/j/gone", "repo", "o/r")
	prober := &slowProber{ev: map[string]reconcile.Evidence{S + "/gone": {Absent: true}}, release: make(chan struct{}), started: make(chan struct{}, 4)}
	duty := &reconcile.Expire{Client: x.c, Prober: prober}
	l := x.lease("ctl-slow")

	if _, err := duty.Run(x.ctx, l); err != nil {
		t.Fatal(err)
	}
	select {
	case <-prober.started:
	case <-time.After(30 * time.Second): // wall-ok: a test's give-up, not a product bound
		t.Fatal("no evidence session started within 30 s")
	}
	if n, f := prober.calls.Load(), prober.inFlight.Load(); n != 1 || f != 1 {
		t.Fatalf("the pass returned with %d sessions started and %d in flight, want 1 and 1 (the pass never waits on ssh)", n, f)
	}
	if h := x.card(S, "gone"); h["state"] != "reconcile-required" {
		t.Fatalf("gone resolved before its session ended: %v", h)
	}
	// Due again at once: the next pass sweeps, and the bench's worker is
	// still in flight, so no second session starts.
	x.must(x.c.HDel(x.ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Err())
	if _, err := duty.Run(x.ctx, l); err != nil {
		t.Fatal(err)
	}
	if n, f := prober.calls.Load(), prober.inFlight.Load(); n != 1 || f != 1 {
		t.Fatalf("second pass: %d sessions, %d in flight; want 1 and 1 (one worker per bench)", n, f)
	}

	close(prober.release)
	settle(t, duty)
	if h := x.card(S, "gone"); h["state"] != "queued" || h["reason"] != "lost" {
		t.Fatalf("gone after its session: %v, want queued (lost)", h)
	}
	row := x.hash(reconcile.ProbeProcKey(B))
	if row["cards"] != "1" || row["resolved"] != "1" || row["err"] != "-" || row["at"] == "" || row["took_ms"] == "" {
		t.Fatalf("proc:expire:%s = %v, want cards=1 resolved=1 err=- at=<ms> took_ms=<ms>", B, row)
	}
}

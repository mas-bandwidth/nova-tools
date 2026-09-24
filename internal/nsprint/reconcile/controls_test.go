package reconcile_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const fence = "rc-1.0123456789abcdef"

// Short windows: the clocks are policy, the age is Redis TIME.
var fast = reconcile.Windows{Start: 150 * time.Millisecond, Beat: 250 * time.Millisecond}

// Control 23: a card whose beat is lost while the child runs on, pushes and
// writes its end record DONE is reconcile-required at the beat window, never
// queued and never launched again; the reconciler resolves it once, from the
// record, to ended(DONE). A dealt-only reservation with no live identity
// requeues under a new attempt; one whose identity is live is launched.
func TestControl23NoSecondLaunch(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const sprint, label, bench = "control-23a0b1c2", "c23", "ctl-bench"
	id := card.Identity{Sprint: sprint, Label: label, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	token := attemptToken(1)
	runCard(t, ctx, st, client, id, token)

	// Before the window: nothing moves.
	res := reclaim(t, ctx, st, sprint, label)
	if res.Status != "NOTHING" || stateOf(t, ctx, client, sprint, label) != "running" {
		t.Fatalf("reclaim inside the beat window: %+v", res)
	}
	// A stale reconciler instance cannot reclaim.
	stale, err := reconcile.Reclaim(ctx, st, reconcile.ReclaimRequest{Sprint: sprint, Label: label, Fence: "rc-0.old", Windows: fast})
	if err != nil || stale.Code != 3 {
		t.Fatalf("stale fence reclaim: %+v %v", stale, err)
	}

	// Kill the beat; the child keeps running.
	time.Sleep(fast.Beat + 50*time.Millisecond)
	res = reclaim(t, ctx, st, sprint, label)
	if res.Status != "REQUIRED" || res.Receipt == "" {
		t.Fatalf("beat lost: %+v", res)
	}
	h := hashOf(t, ctx, client, sprint, label)
	if h["state"] != "reconcile-required" || h["reason"] != "beat-lost" || h["token"] != "" || h["attempt"] != "1" {
		t.Fatalf("beat-lost hash: %+v", h)
	}
	member := fmt.Sprintf("%s/%s/1", sprint, label)
	if zHas(t, ctx, client, card.BenchLivingKey(bench), member) {
		t.Fatal("beat-lost left the slot leased")
	}
	if inPool(t, ctx, client, sprint, label) {
		t.Fatal("beat-lost requeued the card")
	}
	idem, _ := client.HGet(ctx, card.IdemKey(sprint), "launch:"+member).Result()
	if idem != res.Receipt {
		t.Fatalf("launch idem key = %q, want %q", idem, res.Receipt)
	}

	// The late child's calls are fenced: its beat exits 3 and moves nothing.
	late, err := card.Beat(ctx, st, card.BeatRequest{Sprint: sprint, Label: label, Token: token})
	if err != nil || late.Code != 3 {
		t.Fatalf("late beat: %+v %v", late, err)
	}
	// Sweeps keep coming: never queued, never dealt, never a second launch.
	for i := 0; i < 3; i++ {
		again := reclaim(t, ctx, st, sprint, label)
		if again.Status != "NOTHING" {
			t.Fatalf("sweep %d on reconcile-required: %+v", i, again)
		}
	}
	// With no end record yet and no evidence gathered, it stays required.
	results := canonicalResults(t, id)
	res = required(t, ctx, st, sprint, label, results, reconcile.Evidence{})
	if res.Status != "NOTHING" || stateOf(t, ctx, client, sprint, label) != "reconcile-required" {
		t.Fatalf("no record, no evidence: %+v", res)
	}

	// The child pushes its branch and writes its end record DONE, then exits.
	pushed := "89abcdef0123456789abcdef0123456789abcdef"
	writeRecord(t, results, card.EndRecord{Identity: id, Outcome: "DONE", Reason: "done", ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: pushed, At: "2026-09-23T12:00:00Z"})
	// Its own card end is fenced: the record, not the call, ends it.
	end, err := card.End(ctx, st, card.EndRequest{Sprint: sprint, Label: label, Token: token, Outcome: "DONE", Reason: "done", ResultsDir: results})
	if err != nil || end.Code != 3 {
		t.Fatalf("fenced card end: %+v %v", end, err)
	}
	ev := reconcile.Evidence{Branch: "nova/" + sprint + "/" + label + "-a1", PushedSHA: pushed}
	res = required(t, ctx, st, sprint, label, results, ev)
	if res.Status != "ENDED" || res.Receipt == "" {
		t.Fatalf("resolve from the end record: %+v", res)
	}
	h = hashOf(t, ctx, client, sprint, label)
	if h["state"] != "ended" || h["outcome"] != "DONE" || h["pushed_sha"] != pushed || h["attempt"] != "1" {
		t.Fatalf("ended hash: %+v", h)
	}
	// Resolves once: a repeat returns the same receipt and writes nothing.
	n := xlen(t, ctx, client, sprint)
	again := required(t, ctx, st, sprint, label, results, ev)
	if again.Status != "ENDED" || again.Receipt != res.Receipt || xlen(t, ctx, client, sprint) != n {
		t.Fatalf("second resolve: %+v (log %d -> %d)", again, n, xlen(t, ctx, client, sprint))
	}
	if got := receiptsTo(t, ctx, client, sprint, label, "ended"); got != 1 {
		t.Fatalf("ended receipts = %d, want 1", got)
	}
	if got := receiptsTo(t, ctx, client, sprint, label, "launched"); got != 1 {
		t.Fatalf("launched receipts = %d, want 1 (a second launch of attempt 1)", got)
	}
	if got := receiptsTo(t, ctx, client, sprint, label, "queued"); got != 0 {
		t.Fatalf("queued receipts = %d, want 0", got)
	}

	// A dealt-only reservation with no live identity requeues under a new attempt.
	const lost = "c23-dealt"
	lid := card.Identity{Sprint: sprint, Label: lost, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	seedDealt(t, ctx, client, lid, attemptToken(1))
	if r := reclaim(t, ctx, st, sprint, lost); r.Status != "NOTHING" {
		t.Fatalf("dealt inside the start window: %+v", r)
	}
	time.Sleep(fast.Start + 50*time.Millisecond)
	r := reclaim(t, ctx, st, sprint, lost)
	if r.Status != "QUEUED" {
		t.Fatalf("dealt past the start window: %+v", r)
	}
	h = hashOf(t, ctx, client, sprint, lost)
	if h["state"] != "queued" || h["reason"] != "spawn-timeout" || h["retries"] != "1" || h["token"] != "" || !inPool(t, ctx, client, sprint, lost) {
		t.Fatalf("spawn-timeout hash: %+v pool=%v", h, inPool(t, ctx, client, sprint, lost))
	}
	if zHas(t, ctx, client, card.BenchStartingKey(bench), sprint+"/"+lost+"/1") {
		t.Fatal("spawn-timeout left the reservation")
	}

	// A dealt reservation whose identity is live is launched, not requeued.
	const alive = "c23-live"
	aid := card.Identity{Sprint: sprint, Label: alive, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	seedDealt(t, ctx, client, aid, attemptToken(1))
	must(t, client.SAdd(ctx, "bench:"+bench+":live", sprint+"/"+alive+"/1").Err())
	time.Sleep(fast.Start + 50*time.Millisecond)
	if r := reclaim(t, ctx, st, sprint, alive); r.Status != "LAUNCHED" || stateOf(t, ctx, client, sprint, alive) != "launched" || inPool(t, ctx, client, sprint, alive) {
		t.Fatalf("live dealt: %+v", r)
	}
}

// Control 25. NEGATIVE: the card pushes its branch, its tests fail, it is
// killed before any end record and its beat is lost: reconcile-required, then
// orphan-effect; never ended(DONE); no harvest or review task; one unresolved
// item. POSITIVE: the same run whose wrapper wrote an end record FAILED
// tests-red before dying ends FAILED tests-red.
func TestControl25OrphanNeverDone(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const sprint, bench = "control-25a0b1c2", "ctl-bench"

	// NEGATIVE.
	const label = "c25-orphan"
	id := card.Identity{Sprint: sprint, Label: label, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	token := attemptToken(1)
	runCard(t, ctx, st, client, id, token)
	time.Sleep(fast.Beat + 50*time.Millisecond)
	if r := reclaim(t, ctx, st, sprint, label); r.Status != "REQUIRED" {
		t.Fatalf("beat lost: %+v", r)
	}
	results := canonicalResults(t, id)
	// A record of ANOTHER attempt in another directory is no evidence of an end.
	other := id
	other.Attempt = 2
	writeRecord(t, canonicalResults(t, other), card.EndRecord{Identity: other, Outcome: "DONE", Reason: "done",
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "2026-09-23T12:00:00Z"})
	ev := reconcile.Evidence{Branch: "nova/" + sprint + "/" + label + "-a1", PushedSHA: "0011223344556677889900112233445566778899"}
	res := required(t, ctx, st, sprint, label, results, ev)
	if res.Status != "ORPHAN" || res.Receipt == "" {
		t.Fatalf("effect with no end record: %+v", res)
	}
	h := hashOf(t, ctx, client, sprint, label)
	if h["state"] != "orphan-effect" || h["outcome"] != "" || !strings.Contains(h["orphan_evidence"], ev.Branch) {
		t.Fatalf("orphan hash: %+v", h)
	}
	if n, _ := client.SCard(ctx, card.IdxKey(sprint, "orphan-effect")).Result(); n != 1 {
		t.Fatalf("idx orphan-effect = %d, want 1", n)
	}
	if setHas(t, ctx, client, card.IdxKey(sprint, "ended"), label) {
		t.Fatal("orphan is in the ended index")
	}
	item, _ := client.HGet(ctx, "s:"+sprint+":unresolved", label+":orphan-effect:1").Result()
	if !strings.Contains(item, id.String()) {
		t.Fatalf("unresolved item = %q", item)
	}
	// Repeated sweeps: one receipt, one item, never DONE, never relaunched.
	n := xlen(t, ctx, client, sprint)
	for i := 0; i < 3; i++ {
		again := required(t, ctx, st, sprint, label, results, ev)
		if again.Status != "NOTHING" || stateOf(t, ctx, client, sprint, label) != "orphan-effect" {
			t.Fatalf("sweep %d on the orphan: %+v", i, again)
		}
		if r := reclaim(t, ctx, st, sprint, label); r.Status != "NOTHING" {
			t.Fatalf("reclaim of the orphan: %+v", r)
		}
	}
	if xlen(t, ctx, client, sprint) != n {
		t.Fatalf("orphan sweeps wrote receipts: %d -> %d", n, xlen(t, ctx, client, sprint))
	}
	if items, _ := client.HLen(ctx, "s:"+sprint+":unresolved").Result(); items != 1 {
		t.Fatalf("unresolved items = %d, want 1", items)
	}
	// No harvest task and no review task exist for it.
	if keys, _ := client.Keys(ctx, "s:"+sprint+":task:*").Result(); len(keys) != 0 {
		t.Fatalf("tasks exist for an orphan: %v", keys)
	}
	if got := receiptsTo(t, ctx, client, sprint, label, "ended"); got != 0 {
		t.Fatalf("orphan has %d ended receipts", got)
	}

	// POSITIVE: the wrapper wrote FAILED tests-red before it died.
	const failed = "c25-red"
	fid := card.Identity{Sprint: sprint, Label: failed, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	ftoken := attemptToken(1)
	runCard(t, ctx, st, client, fid, ftoken)
	fresults := canonicalResults(t, fid)
	writeRecord(t, fresults, card.EndRecord{Identity: fid, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: card.TokenSHA(ftoken), PushedSHA: "0011223344556677889900112233445566778899", At: "2026-09-23T12:00:00Z"})
	time.Sleep(fast.Beat + 50*time.Millisecond)
	if r := reclaim(t, ctx, st, sprint, failed); r.Status != "REQUIRED" {
		t.Fatalf("beat lost: %+v", r)
	}
	fev := reconcile.Evidence{Branch: "nova/" + sprint + "/" + failed + "-a1"}
	if r := required(t, ctx, st, sprint, failed, fresults, fev); r.Status != "ENDED" {
		t.Fatalf("record FAILED: %+v", r)
	}
	h = hashOf(t, ctx, client, sprint, failed)
	if h["state"] != "ended" || h["outcome"] != "FAILED" || h["reason"] != "tests-red" {
		t.Fatalf("positive hash: %+v", h)
	}
	if setHas(t, ctx, client, card.IdxKey(sprint, "orphan-effect"), failed) {
		t.Fatal("a card with its end record became an orphan")
	}

	// Proven absence of every effect requeues under a new attempt (reason lost).
	const gone = "c25-gone"
	gid := card.Identity{Sprint: sprint, Label: gone, BaseSHA: "0123abcd", Bench: bench, Attempt: 1}
	runCard(t, ctx, st, client, gid, attemptToken(1))
	time.Sleep(fast.Beat + 50*time.Millisecond)
	reclaim(t, ctx, st, sprint, gone)
	if r := required(t, ctx, st, sprint, gone, canonicalResults(t, gid), reconcile.Evidence{Absent: true}); r.Status != "QUEUED" {
		t.Fatalf("proven absence: %+v", r)
	}
	if h := hashOf(t, ctx, client, sprint, gone); h["reason"] != "lost" || !inPool(t, ctx, client, sprint, gone) {
		t.Fatalf("lost hash: %+v", h)
	}
}

// Control 8: the forge opened the PR and the worker crashed before the URL
// was recorded. The restart finds the PR by REST and records it: one PR.
func TestControl08NoSecondPR(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const sprint, repo, branch = "control-08a0b1c2", "ctl-org/ctl-repo", "nova/control-08a0b1c2/c8-a1"
	forge := newFakeForge(t)
	forge.crashAfterOpen.Store(true) // the reply is lost after the PR exists
	host := reconcile.RESTPRHost{BaseURL: forge.srv.URL, Token: "t"}
	req := reconcile.PRRequest{Sprint: sprint, Repo: repo, Branch: branch, Base: "dev", Title: "c8", Who: "harvest-a", Fence: fence}

	if _, err := reconcile.EnsurePR(ctx, st, host, req); err == nil {
		t.Fatal("the crashed open reported success")
	}
	if got := forge.opens.Load(); got != 1 {
		t.Fatalf("opens after the crash = %d, want 1", got)
	}
	pending, _ := client.HGet(ctx, card.IdemKey(sprint), reconcile.PRKey(repo, branch)).Result()
	if !strings.HasPrefix(pending, "pending:") {
		t.Fatalf("idem key after the crash = %q, want pending", pending)
	}

	// Restart: a new worker instance.
	forge.crashAfterOpen.Store(false)
	req.Who = "harvest-b"
	got, err := reconcile.EnsurePR(ctx, st, host, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Opened || got.URL != forge.url(1) || got.Receipt == "" {
		t.Fatalf("restart: %+v", got)
	}
	if n := forge.opens.Load(); n != 1 {
		t.Fatalf("opens after the restart = %d, want 1 (a second PR)", n)
	}
	// Every later call returns the recorded URL without asking the forge.
	calls := forge.calls.Load()
	again, err := reconcile.EnsurePR(ctx, st, host, req)
	if err != nil || again.URL != got.URL || forge.calls.Load() != calls || forge.opens.Load() != 1 {
		t.Fatalf("recorded key: %+v %v (forge calls %d -> %d)", again, err, calls, forge.calls.Load())
	}
	// One receipt for the recorded effect.
	if n := receiptsTo(t, ctx, client, sprint, reconcile.PRKey(repo, branch), "recorded"); n != 1 {
		t.Fatalf("effect receipts = %d, want 1", n)
	}

	// A clean first open of another branch opens once and records it.
	req2 := req
	req2.Branch = "nova/control-08a0b1c2/c8b-a1"
	first, err := reconcile.EnsurePR(ctx, st, host, req2)
	if err != nil || !first.Opened || forge.opens.Load() != 2 {
		t.Fatalf("clean open: %+v %v opens=%d", first, err, forge.opens.Load())
	}

	// A stale reconciler writes no idem key and asks the forge nothing.
	idem := card.IdemKey(sprint)
	before := hashAll(t, ctx, client, idem)
	calls = forge.calls.Load()
	stale := req
	stale.Branch, stale.Fence = "nova/control-08a0b1c2/c8c-a1", "rc-0.old"
	if _, err := reconcile.EnsurePR(ctx, st, host, stale); err == nil || !strings.Contains(err.Error(), "FENCED") {
		t.Fatalf("stale fence ensure pr: err = %v, want FENCED", err)
	}
	if forge.calls.Load() != calls {
		t.Fatalf("stale fence asked the forge: calls %d -> %d", calls, forge.calls.Load())
	}
	sameHash(t, "stale ensure pr", before, hashAll(t, ctx, client, idem))
	// Each function refuses on its own: begin, and commit over a pending key.
	pendingKey := reconcile.PRKey(repo, "nova/control-08a0b1c2/c8d-a1")
	if got := fcall(t, ctx, client, "ns_idem_begin", sprint, pendingKey, "harvest-c", "rc-0.old"); got != "3|FENCED||" {
		t.Fatalf("stale ns_idem_begin = %q, want 3|FENCED||", got)
	}
	sameHash(t, "stale begin", before, hashAll(t, ctx, client, idem))
	if got := fcall(t, ctx, client, "ns_idem_begin", sprint, pendingKey, "harvest-c", fence); got != "0|BEGUN||" {
		t.Fatalf("ns_idem_begin = %q, want 0|BEGUN||", got)
	}
	before = hashAll(t, ctx, client, idem)
	logLen := xlen(t, ctx, client, sprint)
	if got := fcall(t, ctx, client, "ns_idem_commit", sprint, pendingKey, forge.url(9), "harvest-c", "pr-found", "rc-0.old"); got != "3|FENCED||" {
		t.Fatalf("stale ns_idem_commit = %q, want 3|FENCED||", got)
	}
	sameHash(t, "stale commit", before, hashAll(t, ctx, client, idem))
	if n := xlen(t, ctx, client, sprint); n != logLen {
		t.Fatalf("stale commit wrote a receipt: log %d -> %d", logLen, n)
	}
	// A missing lease fences even the last good token.
	must(t, client.HDel(ctx, "lease:reconciler", "token").Err())
	if got := fcall(t, ctx, client, "ns_idem_commit", sprint, pendingKey, forge.url(9), "harvest-c", "pr-found", fence); got != "3|FENCED||" {
		t.Fatalf("no-lease ns_idem_commit = %q, want 3|FENCED||", got)
	}
	if got := fcall(t, ctx, client, "ns_idem_begin", sprint, reconcile.PRKey(repo, "nova/control-08a0b1c2/c8e-a1"), "harvest-c", fence); got != "3|FENCED||" {
		t.Fatalf("no-lease ns_idem_begin = %q, want 3|FENCED||", got)
	}
	sameHash(t, "no lease", before, hashAll(t, ctx, client, idem))
}

// fcall calls one loaded nova_sprint function directly (the Go wrappers have
// loaded the library by the time a control uses it).
func fcall(t *testing.T, ctx context.Context, client *redis.Client, name string, args ...any) string {
	t.Helper()
	out, err := client.FCall(ctx, name, nil, args...).Text()
	must(t, err)
	return out
}

func hashAll(t *testing.T, ctx context.Context, client *redis.Client, key string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(ctx, key).Result()
	must(t, err)
	return h
}

func sameHash(t *testing.T, what string, want, got map[string]string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: idem hash changed: %d fields -> %d (%v)", what, len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: idem %s changed: %q -> %q", what, k, v, got[k])
		}
	}
}

// Control 9: two concurrent refills route one ready task, and a crash can
// land only before or after the one function call: exactly one assignment
// and one durable receipt, and the retry after a crash writes nothing new.
func TestControl09OneAssignmentOneReceipt(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const sprint, id = "control-09a0b1c2", "t9"
	must(t, client.SAdd(ctx, "friends", "ctl-a", "ctl-b").Err())
	must(t, client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "owner", "", "attempt", "0", "kind", "build").Err())
	must(t, client.ZAdd(ctx, "s:"+sprint+":ready", redis.Z{Score: 5, Member: id}).Err())

	var wg sync.WaitGroup
	results := make([]reconcile.Result, 32)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			consumer := []string{"ctl-a", "ctl-b"}[i%2]
			r, err := reconcile.Assign(ctx, st, reconcile.AssignRequest{Sprint: sprint, ID: id, Consumer: consumer, Fence: fence})
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	owner, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "owner").Result()
	if owner != "ctl-a" && owner != "ctl-b" {
		t.Fatalf("owner = %q", owner)
	}
	for i, r := range results {
		consumer := []string{"ctl-a", "ctl-b"}[i%2]
		if consumer == owner && r.Code != 0 {
			t.Fatalf("winner's call %d: %+v", i, r)
		}
		if consumer != owner && r.Code == 0 {
			t.Fatalf("loser's call %d succeeded: %+v", i, r)
		}
	}
	queued := 0
	for _, c := range []string{"ctl-a", "ctl-b"} {
		if _, err := client.ZScore(ctx, "s:"+sprint+":open:"+c, id).Result(); err == nil {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("task is in %d consumer queues, want 1", queued)
	}
	if n, _ := client.ZCard(ctx, "s:"+sprint+":ready").Result(); n != 0 {
		t.Fatal("task still ready after assignment")
	}
	if n := receiptsTo(t, ctx, client, sprint, id, "open:"+owner); n != 1 {
		t.Fatalf("assignment receipts = %d, want 1", n)
	}

	// Crash: the client dies with the call in flight. The function ran whole
	// or not at all; the retry returns the stored receipt and adds none.
	must(t, client.HSet(ctx, "s:"+sprint+":task:u9", "state", "open", "owner", "", "attempt", "0").Err())
	must(t, client.ZAdd(ctx, "s:"+sprint+":ready", redis.Z{Score: 1, Member: "u9"}).Err())
	dying := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(time.Millisecond); cancel() }()
	_, _ = reconcile.Assign(cctx, store.New(dying), reconcile.AssignRequest{Sprint: sprint, ID: "u9", Consumer: "ctl-a", Fence: fence})
	_ = dying.Close()
	time.Sleep(50 * time.Millisecond)
	r, err := reconcile.Assign(ctx, st, reconcile.AssignRequest{Sprint: sprint, ID: "u9", Consumer: "ctl-a", Fence: fence})
	if err != nil || r.Code != 0 {
		t.Fatalf("retry after crash: %+v %v", r, err)
	}
	ownerU, _ := client.HGet(ctx, "s:"+sprint+":task:u9", "owner").Result()
	if ownerU != "ctl-a" || receiptsTo(t, ctx, client, sprint, "u9", "open:ctl-a") != 1 {
		t.Fatalf("after crash and retry: owner %q receipts %d", ownerU, receiptsTo(t, ctx, client, sprint, "u9", "open:ctl-a"))
	}
	// Every owner has its receipt: no state without its receipt.
	for _, tid := range []string{id, "u9"} {
		o, _ := client.HGet(ctx, "s:"+sprint+":task:"+tid, "owner").Result()
		if receiptsTo(t, ctx, client, sprint, tid, "open:"+o) != 1 {
			t.Fatalf("%s owned by %s without exactly one receipt", tid, o)
		}
	}
	// A stale reconciler cannot route.
	must(t, client.HSet(ctx, "s:"+sprint+":task:v9", "state", "open", "owner", "", "attempt", "0").Err())
	must(t, client.ZAdd(ctx, "s:"+sprint+":ready", redis.Z{Score: 1, Member: "v9"}).Err())
	if r, _ := reconcile.Assign(ctx, st, reconcile.AssignRequest{Sprint: sprint, ID: "v9", Consumer: "ctl-a", Fence: "rc-0.old"}); r.Code != 3 {
		t.Fatalf("stale fence assign: %+v", r)
	}
}

// ---- fixtures ----

type fakeForge struct {
	srv            *httptest.Server
	mu             sync.Mutex
	prs            map[string]int // head branch -> number
	opens, calls   atomic.Int64
	crashAfterOpen atomic.Bool
}

func (f *fakeForge) url(n int) string {
	return fmt.Sprintf("https://github.test/ctl-org/ctl-repo/pull/%d", n)
}

func newFakeForge(t *testing.T) *fakeForge {
	f := &fakeForge{prs: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/repos/ctl-org/ctl-repo/pulls" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			head := strings.TrimPrefix(r.URL.Query().Get("head"), "ctl-org:")
			out := []map[string]string{}
			if n, ok := f.prs[head]; ok && r.URL.Query().Get("state") == "open" {
				out = append(out, map[string]string{"html_url": f.url(n)})
			}
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			// This forge does not refuse a second PR on one head, so a
			// worker that opens without its idem key opens a second PR.
			f.opens.Add(1)
			n := int(f.opens.Load())
			if _, ok := f.prs[body["head"]]; !ok {
				f.prs[body["head"]] = n
			}
			if f.crashAfterOpen.Load() {
				// The PR exists; the reply never reaches the worker.
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"html_url": f.url(n)})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func reclaim(t *testing.T, ctx context.Context, st *store.Store, sprint, label string) reconcile.Result {
	t.Helper()
	r, err := reconcile.Reclaim(ctx, st, reconcile.ReclaimRequest{Sprint: sprint, Label: label, Fence: fence, Windows: fast})
	if err != nil {
		t.Fatal(err)
	}
	if r.Code != 0 {
		t.Fatalf("reclaim %s: %+v", label, r)
	}
	return r
}

func required(t *testing.T, ctx context.Context, st *store.Store, sprint, label, results string, ev reconcile.Evidence) reconcile.Result {
	t.Helper()
	r, err := reconcile.ResolveRequired(ctx, st, reconcile.RequiredRequest{Sprint: sprint, Label: label, Fence: fence, ResultsDir: results, Evidence: ev})
	if err != nil {
		t.Fatal(err)
	}
	if r.Code != 0 {
		t.Fatalf("required %s: %+v", label, r)
	}
	return r
}

// runCard seeds a dealt card and drives it to running through the real
// launched and beat functions.
func runCard(t *testing.T, ctx context.Context, st *store.Store, client *redis.Client, id card.Identity, token string) {
	t.Helper()
	seedDealt(t, ctx, client, id, token)
	branch := fmt.Sprintf("nova/%s/%s-a%d", id.Sprint, id.Label, id.Attempt)
	if r, err := card.Launched(ctx, st, card.LaunchRequest{Sprint: id.Sprint, Label: id.Label, Token: token, Branch: branch, JobDir: t.TempDir()}); err != nil || !r.Resolved {
		t.Fatalf("launched: %+v %v", r, err)
	}
	if r, err := card.Beat(ctx, st, card.BeatRequest{Sprint: id.Sprint, Label: id.Label, Token: token}); err != nil || !r.Resolved {
		t.Fatalf("beat: %+v %v", r, err)
	}
}

func seedDealt(t *testing.T, ctx context.Context, client *redis.Client, id card.Identity, token string) {
	t.Helper()
	now, err := client.Time(ctx).Result()
	must(t, err)
	must(t, client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
		"state": "dealt", "attempt": fmt.Sprint(id.Attempt), "token": token, "token_sha": card.TokenSHA(token),
		"identity": id.String(), "bench": id.Bench, "base_sha": id.BaseSHA, "priority": "10",
		"dealt_at": fmt.Sprint(now.UnixMilli()),
	}).Err())
	must(t, client.SAdd(ctx, card.IdxKey(id.Sprint, "dealt"), id.Label).Err())
	must(t, client.ZAdd(ctx, card.BenchStartingKey(id.Bench), redis.Z{Score: float64(now.UnixMilli()), Member: fmt.Sprintf("%s/%s/%d", id.Sprint, id.Label, id.Attempt)}).Err())
}

func attemptToken(attempt int) string {
	return fmt.Sprintf("%d.%032x", attempt, time.Now().UnixNano())
}

func canonicalResults(t *testing.T, id card.Identity) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id.Sprint, id.Label, id.BaseSHA, id.Bench, fmt.Sprint(id.Attempt))
	must(t, os.MkdirAll(dir, 0o755))
	return dir
}

func writeRecord(t *testing.T, dir string, rec card.EndRecord) {
	t.Helper()
	must(t, card.WriteEndRecord(dir, rec))
}

func hashOf(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(ctx, card.CardKey(sprint, label)).Result()
	must(t, err)
	return h
}

func stateOf(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string) string {
	return hashOf(t, ctx, client, sprint, label)["state"]
}

func inPool(t *testing.T, ctx context.Context, client *redis.Client, sprint, label string) bool {
	_, err := client.ZScore(ctx, "s:"+sprint+":pool", label).Result()
	return err == nil
}

func setHas(t *testing.T, ctx context.Context, client *redis.Client, key, member string) bool {
	ok, err := client.SIsMember(ctx, key, member).Result()
	must(t, err)
	return ok
}

func zHas(t *testing.T, ctx context.Context, client *redis.Client, key, member string) bool {
	_, err := client.ZScore(ctx, key, member).Result()
	return err == nil
}

func xlen(t *testing.T, ctx context.Context, client *redis.Client, sprint string) int64 {
	n, err := client.XLen(ctx, card.LogKey(sprint)).Result()
	must(t, err)
	return n
}

// receiptsTo counts log entries for id whose to field is to.
func receiptsTo(t *testing.T, ctx context.Context, client *redis.Client, sprint, id, to string) int {
	t.Helper()
	msgs, err := client.XRange(ctx, card.LogKey(sprint), "-", "+").Result()
	must(t, err)
	n := 0
	for _, m := range msgs {
		if m.Values["id"] == id && m.Values["to"] == to {
			n++
		}
	}
	return n
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newSprint(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	// The reconciler lease this test acts under (#2726 owns its renewal).
	must(t, client.HSet(context.Background(), "lease:reconciler", "instance", "ctl", "token", fence).Err())
	return store.New(client), client
}

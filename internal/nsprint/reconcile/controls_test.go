package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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

// Control 8 (#2930 rev 5): the PR open is idempotent from Redis alone. The
// forge serves only POST /pulls; any read of it fails the test (GitHub is a
// git remote only). A lost reply leaves the key pending (IN-FLIGHT, never a
// second open); a pending key past open_ms and every 422 that does not prove
// "no PR" is ambiguous (terminal for the machine, one unresolved item); only
// the exact validation allowlist is REJECTED and clears the key; a friend's
// `idem resolve` is the one writer of the ambiguous transition, fenced by
// compare-and-set on --was.
func TestControl08NoSecondPR(t *testing.T) {
	ctx := context.Background()
	st, client := newSprint(t)
	const sprint, repo = "control-08a0b1c2", "ctl-org/ctl-repo"
	forge := newFakeForge(t)
	host := reconcile.RESTPRHost{BaseURL: forge.srv.URL, Token: "t"}
	idem := card.IdemKey(sprint)
	unresolved := "s:" + sprint + ":unresolved"
	pendingIdx := reconcile.PendingIndexKey(sprint)
	head := func(s string) string { return "nova/" + sprint + "/" + s + "-a1" }
	request := func(branch, who string) reconcile.PRRequest {
		return reconcile.PRRequest{Sprint: sprint, Repo: repo, Branch: branch, Base: "dev", Title: "c8", Who: who, Fence: fence}
	}
	ensure := func(t *testing.T, req reconcile.PRRequest) reconcile.PRResult {
		t.Helper()
		got, err := reconcile.EnsurePR(ctx, st, host, req)
		if err != nil {
			t.Fatalf("ensure pr %s: %v", req.Branch, err)
		}
		return got
	}
	// retries runs n EnsurePR calls by a new worker and wants status each
	// time, with no forge request at all.
	retries := func(t *testing.T, branch, status string, n int) {
		t.Helper()
		calls := forge.calls.Load()
		for i := 0; i < n; i++ {
			got := ensure(t, request(branch, fmt.Sprintf("harvest-r%d", i)))
			if got.Code != 4 || got.Status != status || got.URL != "" {
				t.Fatalf("retry %d on %s: %+v, want code 4 %s", i, branch, got, status)
			}
		}
		if forge.calls.Load() != calls {
			t.Fatalf("retries on %s asked the forge: calls %d -> %d", branch, calls, forge.calls.Load())
		}
	}
	value := func(branch string) string {
		v, _ := client.HGet(ctx, idem, reconcile.PRKey(repo, branch)).Result()
		return v
	}
	ambiguousItems := func(branch string) int {
		n := 0
		for f := range hashAll(t, ctx, client, unresolved) {
			if f == reconcile.UnresolvedField(reconcile.PRKey(repo, branch)) {
				n++
			}
		}
		return n
	}
	wantAmbiguousOnce := func(t *testing.T, branch string) {
		t.Helper()
		key := reconcile.PRKey(repo, branch)
		if v := value(branch); !strings.HasPrefix(v, "ambiguous:") {
			t.Fatalf("idem %s = %q, want ambiguous:*", key, v)
		}
		if zHas(t, ctx, client, pendingIdx, key) {
			t.Fatalf("%s still in the pending index", key)
		}
		if n := ambiguousItems(branch); n != 1 {
			t.Fatalf("pr-ambiguous items for %s = %d, want 1", branch, n)
		}
		if n := receiptsTo(t, ctx, client, sprint, key, "ambiguous"); n != 1 {
			t.Fatalf("ambiguous receipts for %s = %d, want 1", key, n)
		}
	}
	onePost := func(t *testing.T, branch string) {
		t.Helper()
		if p := forge.postsTo(branch); p != 1 {
			t.Fatalf("POSTs for %s = %d, want 1", branch, p)
		}
		if g := forge.reads.Load(); g != 0 {
			t.Fatalf("forge reads = %d, want 0", g)
		}
	}

	t.Run("crash_after_open_in_flight", func(t *testing.T) {
		a := head("c8")
		forge.crashAfterOpen.Store(true) // the PR exists; the reply is lost
		if _, err := reconcile.EnsurePR(ctx, st, host, request(a, "harvest-a")); err == nil {
			t.Fatal("the crashed open reported success")
		}
		forge.crashAfterOpen.Store(false)
		onePost(t, a)
		if v := value(a); !strings.HasPrefix(v, "pending:harvest-a:") {
			t.Fatalf("idem key after the crash = %q, want pending:harvest-a:<at_ms>", v)
		}
		if !zHas(t, ctx, client, pendingIdx, reconcile.PRKey(repo, a)) {
			t.Fatal("the pending key is not in the pending index")
		}
		retries(t, a, "IN-FLIGHT", 5)
		onePost(t, a)
	})

	t.Run("pending_past_open_ms_ambiguous", func(t *testing.T) {
		a := head("c8")
		key := reconcile.PRKey(repo, a)
		was := value(a)
		// Seed the pending age past open_ms (the sweep's selection); the
		// transition itself is the sweep's one call.
		must(t, client.ZAdd(ctx, pendingIdx, redis.Z{Score: 1, Member: key}).Err())
		res, err := reconcile.MarkAmbiguous(ctx, st, sprint, key, was, fence)
		if err != nil || res.Code != 0 || res.Status != "AMBIGUOUS" || res.Receipt == "" {
			t.Fatalf("mark ambiguous: %+v %v", res, err)
		}
		wantAmbiguousOnce(t, a)
		n := xlen(t, ctx, client, sprint)
		again, err := reconcile.MarkAmbiguous(ctx, st, sprint, key, "", fence)
		if err != nil || again.Receipt != res.Receipt || xlen(t, ctx, client, sprint) != n {
			t.Fatalf("ambiguous replay: %+v %v (log %d -> %d)", again, err, n, xlen(t, ctx, client, sprint))
		}
		retries(t, a, "AMBIGUOUS", 5)
		wantAmbiguousOnce(t, a)
		onePost(t, a)
		if n := forge.prsOn(a); n != 1 {
			t.Fatalf("PRs on %s = %d, want 1", a, n)
		}
	})

	t.Run("conflict_422_ambiguous", func(t *testing.T) {
		b := head("c8b")
		forge.reply(b, http.StatusUnprocessableEntity, `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for ctl-org:`+b+`."}]}`)
		got := ensure(t, request(b, "harvest-a"))
		if got.Code != 4 || got.Status != "AMBIGUOUS" || got.Opened {
			t.Fatalf("conflict: %+v", got)
		}
		onePost(t, b)
		wantAmbiguousOnce(t, b)
		retries(t, b, "AMBIGUOUS", 5)
		onePost(t, b)
		if n := forge.prsOn(b); n > 1 {
			t.Fatalf("a second PR on %s", b)
		}
	})

	t.Run("unreadable_422_ambiguous", func(t *testing.T) {
		c := head("c8c")
		forge.reply(c, http.StatusUnprocessableEntity, "")
		got := ensure(t, request(c, "harvest-a"))
		if got.Code != 4 || got.Status != "AMBIGUOUS" {
			t.Fatalf("empty 422: %+v", got)
		}
		onePost(t, c)
		wantAmbiguousOnce(t, c)
	})

	t.Run("unknown_422_ambiguous", func(t *testing.T) {
		for i, body := range []string{`{}`, `{"message":"Validation Failed","errors":[{"code":"custom","message":"something new"}]}`} {
			d := head(fmt.Sprintf("c8d%d", i))
			forge.reply(d, http.StatusUnprocessableEntity, body)
			got := ensure(t, request(d, "harvest-a"))
			if got.Code != 4 || got.Status != "AMBIGUOUS" {
				t.Fatalf("unknown 422 %s: %+v", body, got)
			}
			onePost(t, d)
			wantAmbiguousOnce(t, d)
			retries(t, d, "AMBIGUOUS", 5)
			onePost(t, d)
		}
	})

	t.Run("validation_422_rejected", func(t *testing.T) {
		for i, msgOf := range []func(string) string{
			func(string) string {
				return `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"}]}`
			},
			func(branch string) string {
				return `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"No commits between dev and ` + branch + `"}]}`
			},
		} {
			e := head(fmt.Sprintf("c8e%d", i))
			key := reconcile.PRKey(repo, e)
			forge.reply(e, http.StatusUnprocessableEntity, msgOf(e))
			items, _ := client.HLen(ctx, unresolved).Result()
			got := ensure(t, request(e, "harvest-a"))
			if got.Code != 4 || got.Status != "REJECTED" || got.Msg == "" || got.Opened {
				t.Fatalf("validation 422 %d: %+v", i, got)
			}
			onePost(t, e)
			if v := value(e); v != "" {
				t.Fatalf("idem %s after REJECTED = %q, want absent", key, v)
			}
			if zHas(t, ctx, client, pendingIdx, key) {
				t.Fatalf("%s still in the pending index", key)
			}
			if n := receiptsTo(t, ctx, client, sprint, key, "rejected-open"); n != 1 {
				t.Fatalf("rejected-open receipts = %d, want 1", n)
			}
			if after, _ := client.HLen(ctx, unresolved).Result(); after != items {
				t.Fatalf("a rejected open added an unresolved item: %d -> %d", items, after)
			}
			// A replay of the rejection returns the first receipt and writes nothing.
			first, was := rejectedReceipt(t, ctx, client, sprint, key)
			before, n := hashAll(t, ctx, client, idem), xlen(t, ctx, client, sprint)
			if r := fcall(t, ctx, client, "ns_idem_rejected", sprint, key, was, "422", "replay", fence); r != "0|REJECTED||"+first {
				t.Fatalf("ns_idem_rejected replay = %q, want 0|REJECTED||%s", r, first)
			}
			sameHash(t, "rejected replay", before, hashAll(t, ctx, client, idem))
			if xlen(t, ctx, client, sprint) != n {
				t.Fatal("rejected replay wrote a receipt")
			}
			// The caller fixes its request; the forge now opens: one more POST.
			forge.reply(e, http.StatusCreated, "")
			again := ensure(t, request(e, "harvest-a"))
			if again.Code != 0 || !again.Opened || again.URL == "" || value(e) != again.URL {
				t.Fatalf("open after the fix: %+v (idem %q)", again, value(e))
			}
			if p := forge.postsTo(e); p != 2 {
				t.Fatalf("POSTs for %s after the fix = %d, want 2", e, p)
			}
		}
	})

	t.Run("classify422", func(t *testing.T) {
		unknown := `{"code":"custom","message":"something new"}`
		base := `{"resource":"PullRequest","field":"base","code":"invalid"}`
		rows := []struct {
			name, body string
			rejected   bool
		}{
			{"conflict", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for ctl-org:x."}]}`, false},
			{"conflict_wins", `{"message":"Validation Failed","errors":[` + base + `,{"resource":"PullRequest","code":"custom","message":"A pull request already exists for ctl-org:x."}]}`, false},
			{"base_invalid", `{"message":"Validation Failed","errors":[` + base + `]}`, true},
			{"head_invalid", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"head","code":"invalid"}]}`, true},
			{"no_commits", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"No commits between dev and x"}]}`, true},
			{"empty_body", ``, false},
			{"non_json", `<html>busy</html>`, false},
			{"empty_object", `{}`, false},
			{"message_only", `{"message":"Validation Failed"}`, false},
			{"empty_errors", `{"message":"Validation Failed","errors":[]}`, false},
			{"unknown_entry", `{"errors":[` + unknown + `]}`, false},
			{"allowlisted_with_unknown", `{"message":"Validation Failed","errors":[` + base + `,` + unknown + `]}`, false},
			{"base_other_code", `{"errors":[{"resource":"PullRequest","field":"base","code":"missing_field"}]}`, false},
			{"head_other_resource", `{"errors":[{"resource":"Issue","field":"head","code":"invalid"}]}`, false},
			{"conflict_top_level_wins", `{"message":"A pull request already exists for ctl-org:x.","errors":[` + base + `]}`, false},
			{"conflict_whitespace", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"  A pull request already exists for ctl-org:x.  "}]}`, false},
			{"no_commits_whitespace", `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"  No commits between dev and x  "}]}`, true},
		}
		for i, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				branch := head(fmt.Sprintf("c8t%d", i))
				forge.reply(branch, http.StatusUnprocessableEntity, row.body)
				_, err := host.Open(ctx, repo, branch, "dev", "c8", "")
				var rej *reconcile.ErrForgeRejected
				switch {
				case row.rejected && !errors.As(err, &rej):
					t.Fatalf("%s: err = %v, want ErrForgeRejected", row.name, err)
				case row.rejected && rej.Msg == "":
					t.Fatalf("%s: ErrForgeRejected with no Msg", row.name)
				case !row.rejected && !errors.Is(err, reconcile.ErrForgeHasPR):
					t.Fatalf("%s: err = %v, want ErrForgeHasPR", row.name, err)
				}
				onePost(t, branch)
			})
		}
	})

	t.Run("resolve_url", func(t *testing.T) {
		a := head("c8")
		key := reconcile.PRKey(repo, a)
		was := value(a)
		url := forge.url(forge.prsNumber(a))
		res, err := reconcile.ResolveIdem(ctx, st, reconcile.IdemResolveRequest{Sprint: sprint, Key: key, Was: was, URL: url, Who: "ctl-friend"})
		if err != nil || res.Code != 0 || res.Status != "RESOLVED" || res.Receipt == "" {
			t.Fatalf("resolve --url: %+v %v", res, err)
		}
		if ambiguousItems(a) != 0 || value(a) != url {
			t.Fatalf("after resolve --url: idem %q items %d", value(a), ambiguousItems(a))
		}
		if n := receiptsTo(t, ctx, client, sprint, key, "resolved"); n != 1 {
			t.Fatalf("resolved receipts = %d, want 1", n)
		}
		calls := forge.calls.Load()
		got := ensure(t, request(a, "harvest-z"))
		if got.Code != 0 || got.URL != url || forge.calls.Load() != calls {
			t.Fatalf("after resolve --url: %+v (forge calls %d -> %d)", got, calls, forge.calls.Load())
		}
	})

	t.Run("resolve_none", func(t *testing.T) {
		c := head("c8c")
		key := reconcile.PRKey(repo, c)
		res, err := reconcile.ResolveIdem(ctx, st, reconcile.IdemResolveRequest{Sprint: sprint, Key: key, Was: value(c), None: true, Who: "ctl-friend"})
		if err != nil || res.Code != 0 || res.Status != "RESOLVED" || res.Receipt == "" {
			t.Fatalf("resolve --none: %+v %v", res, err)
		}
		if value(c) != "" || ambiguousItems(c) != 0 {
			t.Fatalf("after resolve --none: idem %q items %d", value(c), ambiguousItems(c))
		}
		forge.reply(c, http.StatusCreated, "")
		got := ensure(t, request(c, "harvest-z"))
		if got.Code != 0 || !got.Opened || forge.postsTo(c) != 2 {
			t.Fatalf("open after resolve --none: %+v posts=%d, want one more POST", got, forge.postsTo(c))
		}
		again := ensure(t, request(c, "harvest-y"))
		if again.URL != got.URL || forge.postsTo(c) != 2 {
			t.Fatalf("second ensure after resolve --none: %+v posts=%d", again, forge.postsTo(c))
		}
	})

	t.Run("resolve_stale_was_state", func(t *testing.T) {
		d := head("c8d0")
		f := head("c8f")
		// A pending key on f.
		if r := fcall(t, ctx, client, "ns_idem_begin", sprint, reconcile.PRKey(repo, f), "harvest-p", fence); !strings.HasPrefix(r, "0|BEGUN||pending:harvest-p:") {
			t.Fatalf("ns_idem_begin = %q", r)
		}
		cases := []struct{ name, key, was string }{
			{"stale_was", reconcile.PRKey(repo, d), value(d) + "0"},
			{"other_friend_was", reconcile.PRKey(repo, d), "ambiguous:someone-else:1"},
			{"pending", reconcile.PRKey(repo, f), value(f)},
			{"url", reconcile.PRKey(repo, head("c8")), value(head("c8"))},
			{"absent", reconcile.PRKey(repo, head("c8-none")), "ambiguous:harvest-a:1"},
		}
		for _, c := range cases {
			before, items, n, calls := hashAll(t, ctx, client, idem), hashAll(t, ctx, client, unresolved), xlen(t, ctx, client, sprint), forge.calls.Load()
			stored, _ := client.HGet(ctx, idem, c.key).Result()
			for _, none := range []bool{false, true} {
				req := reconcile.IdemResolveRequest{Sprint: sprint, Key: c.key, Was: c.was, Who: "ctl-friend", None: none}
				if !none {
					req.URL = forge.url(99)
				}
				res, err := reconcile.ResolveIdem(ctx, st, req)
				if err != nil || res.Code != 2 || res.Status != "STATE" || res.Value != stored {
					t.Fatalf("%s (none=%v): %+v %v, want STATE value=%q", c.name, none, res, err, stored)
				}
			}
			sameHash(t, c.name+" idem", before, hashAll(t, ctx, client, idem))
			sameHash(t, c.name+" unresolved", items, hashAll(t, ctx, client, unresolved))
			if xlen(t, ctx, client, sprint) != n || forge.calls.Load() != calls {
				t.Fatalf("%s wrote a receipt or asked the forge", c.name)
			}
		}
	})

	t.Run("ensure_pr_validation", func(t *testing.T) {
		for _, bad := range []struct {
			name string
			h    reconcile.PRHost
			req  reconcile.PRRequest
		}{
			{"nil host", nil, request("head", "w")},
			{"missing repo", host, reconcile.PRRequest{Sprint: sprint, Branch: "head", Who: "w"}},
			{"missing branch", host, reconcile.PRRequest{Sprint: sprint, Repo: repo, Who: "w"}},
			{"missing who", host, reconcile.PRRequest{Sprint: sprint, Repo: repo, Branch: "head"}},
		} {
			calls := forge.calls.Load()
			_, err := reconcile.EnsurePR(ctx, st, bad.h, bad.req)
			if err == nil || !strings.Contains(err.Error(), "host, repo, branch and who are required") {
				t.Fatalf("%s: err = %v, want required error", bad.name, err)
			}
			if forge.calls.Load() != calls {
				t.Fatalf("%s asked the forge", bad.name)
			}
		}
	})

	t.Run("stale_fence_writes_nothing", func(t *testing.T) {
		d := head("c8d1")
		g := head("c8g")
		before, items, n, calls := hashAll(t, ctx, client, idem), hashAll(t, ctx, client, unresolved), xlen(t, ctx, client, sprint), forge.calls.Load()
		stale := request(g, "harvest-s")
		stale.Fence = "rc-0.old"
		if _, err := reconcile.EnsurePR(ctx, st, host, stale); err == nil || !strings.Contains(err.Error(), "FENCED") {
			t.Fatalf("stale fence ensure pr: err = %v, want FENCED", err)
		}
		pendingF := reconcile.PRKey(repo, head("c8f"))
		was := value(head("c8f"))
		checks := func(label, token string) {
			for _, c := range []struct {
				name string
				args []any
			}{
				{"ns_idem_begin", []any{sprint, reconcile.PRKey(repo, g), "harvest-s", token}},
				{"ns_idem_ambiguous", []any{sprint, pendingF, token, was}},
				{"ns_idem_rejected", []any{sprint, pendingF, was, "422", "x", token}},
				{"ns_idem_commit", []any{sprint, pendingF, forge.url(9), "harvest-s", "pr-opened", token}},
			} {
				if got := fcall(t, ctx, client, c.name, c.args...); got != "3|FENCED||" {
					t.Fatalf("%s %s = %q, want 3|FENCED||", label, c.name, got)
				}
			}
			if r, err := reconcile.MarkAmbiguous(ctx, st, sprint, pendingF, was, token); err != nil || r.Code != 3 || r.Status != "FENCED" {
				t.Fatalf("%s MarkAmbiguous: %+v %v, want 3 FENCED", label, r, err)
			}
			if r, err := reconcile.MarkAmbiguous(ctx, st, sprint, reconcile.PRKey(repo, d), "", token); err != nil || r.Code != 3 {
				t.Fatalf("%s MarkAmbiguous replay: %+v %v, want 3 FENCED", label, r, err)
			}
		}
		checks("stale", "rc-0.old")
		checks("missing", "")
		// A missing lease fences even the last good token.
		must(t, client.HDel(ctx, "lease:reconciler", "token").Err())
		checks("no-lease", fence)
		must(t, client.HSet(ctx, "lease:reconciler", "token", fence).Err())
		sameHash(t, "stale fence idem", before, hashAll(t, ctx, client, idem))
		sameHash(t, "stale fence unresolved", items, hashAll(t, ctx, client, unresolved))
		if xlen(t, ctx, client, sprint) != n || forge.calls.Load() != calls {
			t.Fatalf("a fenced call wrote a receipt or asked the forge: log %d -> %d, calls %d -> %d",
				n, xlen(t, ctx, client, sprint), calls, forge.calls.Load())
		}
	})
}

// TestNoForgeReadInReconcile: GitHub is a git remote only (#2930 rev 5). No
// file of the reconcile package names http.MethodGet or FindOpen.
func TestNoForgeReadInReconcile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	must(t, err)
	if len(files) == 0 {
		t.Fatal("no Go files in the reconcile package directory")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		must(t, err)
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "http" && (x.Sel.Name == "MethodGet" || x.Sel.Name == "MethodHead") {
					t.Errorf("%s: http.%s in the reconcile package (a forge read)", fset.Position(x.Pos()), x.Sel.Name)
				}
				if x.Sel.Name == "FindOpen" {
					t.Errorf("%s: FindOpen in the reconcile package (a forge read)", fset.Position(x.Pos()))
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && (x.Value == `"GET"` || x.Value == `"HEAD"`) {
					t.Errorf("%s: %s literal in the reconcile package (a forge read)", fset.Position(x.Pos()), x.Value)
				}
			case *ast.Ident:
				if x.Name == "FindOpen" {
					t.Errorf("%s: FindOpen declared in the reconcile package (a forge read)", fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
}

// rejectedReceipt returns the rejected-open receipt id for key and the
// pending value it rejected (the replay's --was).
func rejectedReceipt(t *testing.T, ctx context.Context, client *redis.Client, sprint, key string) (string, string) {
	t.Helper()
	msgs, err := client.XRange(ctx, card.LogKey(sprint), "-", "+").Result()
	must(t, err)
	for _, m := range msgs {
		if m.Values["id"] == key && m.Values["to"] == "rejected-open" {
			if m.Values["status"] != "422" || m.Values["msg"] == "" || m.Values["at"] == "" {
				t.Fatalf("rejected-open receipt fields: %v", m.Values)
			}
			return m.ID, fmt.Sprint(m.Values["from"])
		}
	}
	t.Fatalf("no rejected-open receipt for %s", key)
	return "", ""
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

// fakeForge serves only POST /repos/ctl-org/ctl-repo/pulls. Any other method
// or path is a forge read and fails the test (#2930 rev 5). The reply is 201
// with a new PR unless reply() set a status and body for that head; a 201 on
// a head records one PR there.
type fakeForge struct {
	srv            *httptest.Server
	mu             sync.Mutex
	prs            map[string][]int // head branch -> PR numbers opened there
	posts          map[string]int   // head branch -> POSTs
	replies        map[string]forgeReply
	next           int
	calls, reads   atomic.Int64
	crashAfterOpen atomic.Bool
}

type forgeReply struct {
	status int
	body   string
}

func (f *fakeForge) url(n int) string {
	return fmt.Sprintf("https://github.test/ctl-org/ctl-repo/pull/%d", n)
}

func (f *fakeForge) reply(head string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[head] = forgeReply{status: status, body: body}
}

func (f *fakeForge) postsTo(head string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[head]
}

func (f *fakeForge) prsOn(head string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prs[head])
}

func (f *fakeForge) prsNumber(head string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prs[head]) == 0 {
		return 0
	}
	return f.prs[head][0]
}

func newFakeForge(t *testing.T) *fakeForge {
	f := &fakeForge{prs: map[string][]int{}, posts: map[string]int{}, replies: map[string]forgeReply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
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
		headName := body["head"]
		f.posts[headName]++
		rep, ok := f.replies[headName]
		if ok && rep.status != http.StatusCreated {
			if strings.Contains(rep.body, "A pull request already exists") && len(f.prs[headName]) == 0 {
				// The conflict is real: a PR is on the head.
				f.next++
				f.prs[headName] = append(f.prs[headName], f.next)
			}
			w.WriteHeader(rep.status)
			_, _ = w.Write([]byte(rep.body))
			return
		}
		// This forge does not refuse a second PR on one head, so a worker
		// that opens without its idem key opens a second PR.
		f.next++
		f.prs[headName] = append(f.prs[headName], f.next)
		if f.crashAfterOpen.Load() {
			// The PR exists; the reply never reaches the worker.
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": f.url(f.next)})
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

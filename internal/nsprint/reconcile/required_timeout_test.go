//go:build functional

package reconcile_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestRequiredTimeoutEndsStuckCard is nova-tools #3803's DONE-WHEN: the probe
// card probe-nova-card-570c7852 sat in reconcile-required for 12 h because no
// duty resolves a card whose bench gives no evidence. The expire duty now ends
// a reconcile-required card after cfg:reconcile max_required_s (default 3600)
// as done/fail, state ended, reason reconcile-timeout with a why, through the
// one move; a younger card stays; a replay writes nothing; fsck is clean.
func TestRequiredTimeoutEndsStuckCard(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil && err != redis.Nil {
			t.Fatal(err)
		}
	}
	const S, bench, stream = "control-570c7852", "exp-bench", "swarm: cards"
	must(c.SAdd(ctx, "sprints", S).Err())
	must(c.HSet(ctx, "s:"+S, "status", "open").Err())
	must(c.HSet(ctx, reconcile.PolicyKey(S), "share", "1").Err())
	must(c.SAdd(ctx, "benches", bench).Err())
	must(c.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1").Err())

	tm, err := c.Time(ctx).Result()
	must(err)
	now := tm.UnixMilli()
	seed := func(label string, ageMS int64) {
		must(c.HSet(ctx, card.CardKey(S, label), map[string]any{
			"state": "reconcile-required", "attempt": "1", "bench": bench, "stream": stream,
			"token_sha": "sha-" + label, "identity": S + "/" + label + "/0123abcd/" + bench + "/1",
			"reason": "beat-lost", "required_at": strconv.FormatInt(now-ageMS, 10),
			"branch": "nova/" + S + "/" + label, "repo": "ctl-org/ctl-repo", "jobdir": "/j/" + label,
		}).Err())
		must(c.SAdd(ctx, card.IdxKey(S, "reconcile-required"), label).Err())
	}
	seed("probe-nova-card-570c7852", 12*3600*1000) // the 12 h card
	seed("young", 200*1000)                        // 200 s: under the 3600 s default
	// Link the records into the card model (the one-time adoption) so the
	// control starts from a clean fsck.
	if r, err := card.Fsck(ctx, c, S, true); err != nil || r.Working != 2 {
		t.Fatalf("adopt: %+v %v", r, err)
	}
	if r, err := card.Fsck(ctx, c, S, false); err != nil || r.Drift != 0 {
		t.Fatalf("fsck before: %+v %v", r, err)
	}

	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-3803"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	duty := &reconcile.Expire{Client: c} // no Prober: no bench gives evidence
	sweep := func() reconcile.Counts {
		t.Helper()
		must(c.HDel(ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Err())
		n, err := duty.Run(ctx, l)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		return n
	}
	get := func(label string) map[string]string {
		h, err := c.HGetAll(ctx, card.CardKey(S, label)).Result()
		must(err)
		return h
	}

	if n := sweep(); n.Expired != 1 {
		t.Fatalf("first sweep expired %d, want 1 (the 12 h card)", n.Expired)
	}
	h := get("probe-nova-card-570c7852")
	if h["state"] != "ended" || h["where"] != "done" || h["where_ok"] != "fail" ||
		h["outcome"] != "FAILED" || h["reason"] != "reconcile-timeout" {
		t.Fatalf("the 12 h card: %v, want ended done/fail FAILED reconcile-timeout", h)
	}
	if !strings.Contains(h["why"], "3600") || h["why_at"] == "" || h["ended_at"] == "" {
		t.Fatalf("the 12 h card why=%q why_at=%q ended_at=%q, want the window named and stamped", h["why"], h["why_at"], h["ended_at"])
	}
	id := card.CardKey(S, "probe-nova-card-570c7852")
	if _, err := c.ZScore(ctx, "ws:"+stream+":done", id).Result(); err != nil {
		t.Fatalf("the 12 h card is not in ws:%s:done: %v", stream, err)
	}
	if _, err := c.ZScore(ctx, card.BenchCardsKeyAt(0, bench, "fail"), id).Result(); err != nil {
		t.Fatalf("the 12 h card is not in %s: %v", card.BenchCardsKeyAt(0, bench, "fail"), err)
	}
	moves, err := c.XRange(ctx, "sprint:"+S+":moves", "-", "+").Result()
	must(err)
	var moved int
	for _, m := range moves {
		if m.Values["id"] == id && m.Values["to"] == "done/fail" && m.Values["why"] == "reconcile-timeout" && m.Values["by"] == "reconciler" {
			moved++
		}
	}
	if moved != 1 {
		t.Fatalf("%d working -> done/fail moves with why reconcile-timeout, want 1: %v", moved, moves)
	}
	if y := get("young"); y["state"] != "reconcile-required" || y["where"] != "working" {
		t.Fatalf("the 200 s card left reconcile-required under the 3600 s default: %v", y)
	}

	// A replay writes nothing more.
	sweep()
	receipts := func() int {
		msgs, err := c.XRange(ctx, card.LogKey(S), "-", "+").Result()
		must(err)
		n := 0
		for _, m := range msgs {
			if m.Values["id"] == "probe-nova-card-570c7852" && m.Values["to"] == "ended" {
				n++
			}
		}
		return n
	}
	if n := receipts(); n != 1 {
		t.Fatalf("%d ended receipts for the 12 h card after a replay, want 1", n)
	}

	// cfg:reconcile max_required_s governs the window.
	must(c.HSet(ctx, reconcile.ConfigKey, "max_required_s", "100").Err())
	if n := sweep(); n.Expired != 1 {
		t.Fatalf("sweep at max_required_s=100 expired %d, want 1 (the 200 s card)", n.Expired)
	}
	if y := get("young"); y["state"] != "ended" || y["where_ok"] != "fail" || !strings.Contains(y["why"], "100") {
		t.Fatalf("the 200 s card at max_required_s=100: %v", y)
	}

	r, err := card.Fsck(ctx, c, S, false)
	if err != nil || r.Drift != 0 || r.Fail != 2 || r.Working != 0 || r.Cards != 2 {
		t.Fatalf("fsck after: %+v %v, want clean with 2 done/fail", r, err)
	}
}

// TestRequiredWithoutTimeStampsFirst: a reconcile-required card with no
// required_at (none of its times) is never ended on its first sighting: the
// sweep stamps required_at from Redis TIME and the window runs from there.
func TestRequiredWithoutTimeStampsFirst(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	const S, bench = "required-notime", "exp-bench"
	for _, err := range []error{
		c.SAdd(ctx, "sprints", S).Err(),
		c.SAdd(ctx, "benches", bench).Err(),
		c.HSet(ctx, card.CardKey(S, "bare"), "state", "reconcile-required", "attempt", "1", "bench", bench,
			"identity", S+"/bare/0123abcd/"+bench+"/1").Err(),
		c.SAdd(ctx, card.IdxKey(S, "reconcile-required"), "bare").Err(),
		c.HSet(ctx, reconcile.ConfigKey, "max_required_s", "1").Err(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-3803b"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	duty := &reconcile.Expire{Client: c}
	if _, err := duty.Run(ctx, l); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	h, err := c.HGetAll(ctx, card.CardKey(S, "bare")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if h["state"] != "reconcile-required" || h["required_at"] == "" {
		t.Fatalf("a card with no time: %v, want still reconcile-required with required_at stamped", h)
	}
}

// deadProber is a bench that answers no evidence session (unreachable).
type deadProber struct{}

func (deadProber) Probe(context.Context, deal.Bench, []reconcile.Suspect) (map[string]reconcile.Evidence, error) {
	return nil, errors.New("ssh: connect to host exp-bench: no route")
}

// TestRequiredTimeoutInEvidenceWorker: #3803's window with #3802's evidence
// workers. A card on a registered bench whose probe fails has no evidence;
// the bench's worker (off the pass path) ends it by the window, writes
// timeouts=1 and the probe's err on proc:expire:<bench>, and the next Run
// counts it in Expired.
func TestRequiredTimeoutInEvidenceWorker(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil && err != redis.Nil {
			t.Fatal(err)
		}
	}
	const S, bench, stream, label = "control-3802-3803", "exp-bench", "swarm: cards", "stuck"
	must(c.SAdd(ctx, "sprints", S).Err())
	must(c.HSet(ctx, "s:"+S, "status", "open").Err())
	must(c.HSet(ctx, reconcile.PolicyKey(S), "share", "1").Err())
	must(c.SAdd(ctx, "benches", bench).Err())
	must(c.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1").Err())
	tm, err := c.Time(ctx).Result()
	must(err)
	must(c.HSet(ctx, card.CardKey(S, label), map[string]any{
		"state": "reconcile-required", "attempt": "1", "bench": bench, "stream": stream,
		"token_sha": "sha-" + label, "identity": S + "/" + label + "/0123abcd/" + bench + "/1",
		"reason": "beat-lost", "required_at": strconv.FormatInt(tm.UnixMilli()-12*3600*1000, 10),
		"branch": "nova/" + S + "/" + label, "repo": "ctl-org/ctl-repo", "jobdir": "/j/" + label,
	}).Err())
	must(c.SAdd(ctx, card.IdxKey(S, "reconcile-required"), label).Err())
	if r, err := card.Fsck(ctx, c, S, true); err != nil || r.Working != 1 {
		t.Fatalf("adopt: %+v %v", r, err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-3802-3803"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	duty := &reconcile.Expire{Client: c, Prober: deadProber{}}
	sweep := func() reconcile.Counts {
		t.Helper()
		must(c.HDel(ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Err())
		n, err := duty.Run(ctx, l)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		return n
	}
	wait := func() {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second) // wall-ok: a test's give-up, not a product bound
		defer cancel()
		if !duty.Wait(wctx) {
			t.Fatal("the evidence worker did not end within 30 s")
		}
	}

	if n := sweep(); n.Expired != 0 {
		t.Fatalf("the pass itself expired %d: the window belongs to the bench's worker", n.Expired)
	}
	wait()
	h, err := c.HGetAll(ctx, card.CardKey(S, label)).Result()
	must(err)
	if h["state"] != "ended" || h["where_ok"] != "fail" || h["reason"] != "reconcile-timeout" {
		t.Fatalf("the 12 h card on an unreachable bench: %v, want ended fail reconcile-timeout", h)
	}
	row, err := c.HGetAll(ctx, reconcile.ProbeProcKey(bench)).Result()
	must(err)
	if row["cards"] != "1" || row["resolved"] != "0" || row["timeouts"] != "1" || !strings.Contains(row["err"], "no route") {
		t.Fatalf("proc:expire:%s = %v, want cards=1 resolved=0 timeouts=1 err=<the probe's>", bench, row)
	}
	if n := sweep(); n.Expired != 1 {
		t.Fatalf("the next Run expired %d, want the worker's 1", n.Expired)
	}
	wait()
}

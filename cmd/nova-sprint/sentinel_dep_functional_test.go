//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// The one dependency rule on every door (the land-duty round #4373, the
// sentinel #4318): a DEPENDS-ON edge on a stream's sentinel is met when that
// sentinel is landed, in the task queue (DEP.holds and the release on a
// move), the waiting resolver, the dealer's refusal and ready --why.

const (
	sdSprint = "sd-sprint"
	sdSHA    = "0123456789abcdef0123456789abcdef01234567"
)

// sdStore is a throwaway store with the library loaded, one open sprint and
// one registered friend f1. It reads no environment and swaps no package
// variable, so every test here runs in parallel: ready --why gets the
// client and a map forge as values (sdWhy, readyReport).
func sdStore(t *testing.T) (*redis.Client, *store.Store) {
	t.Helper()
	_, c := wstest.Start(t)
	ctx := context.Background()
	pipe := c.Pipeline()
	pipe.HSet(ctx, "s:"+sdSprint, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sdSprint})
	pipe.SAdd(ctx, "sprints", sdSprint)
	pipe.SAdd(ctx, "friends", "f1")
	pipe.HSet(ctx, "friend:f1:desired", "slots", 4, "paused", "0")
	pipe.HSet(ctx, "friend:f1:beat", "host", "fixture", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return c, store.New(c)
}

// sdQueue pushes one task-queue task (ns_task_push) to f1.
func sdQueue(t *testing.T, st *store.Store, id, deps string) task.PushResult {
	t.Helper()
	res, err := task.PushChecked(context.Background(), st, task.PushRequest{
		Sprint: sdSprint, ID: id, Kind: task.KindWork, Title: "queue task " + id,
		Effects: task.EffectsNone, To: "f1", DependsOn: deps, Actor: "test",
	})
	if err != nil {
		t.Fatalf("task push %s: %v", id, err)
	}
	return res
}

// sdCard pushes one stream card (ns_tcard_push).
func sdCard(t *testing.T, c *redis.Client, id, stream, deps string) error {
	t.Helper()
	where := "ready"
	if deps != "" {
		where = "waiting"
	}
	_, err := taskcard.Push(context.Background(), c, taskcard.PushRequest{ID: id, Where: where, Stream: stream, Kind: "build",
		Title: "STREAM: " + stream + " | " + id, By: "test", DependsOn: deps})
	return err
}

// sdLand takes card id to working and lands it at the sha.
func sdLand(t *testing.T, c *redis.Client, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := taskcard.Move(ctx, c, id, "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatalf("take %s: %v", id, err)
	}
	if _, err := taskcard.Land(ctx, c, id, "test", sdSHA, "merged"); err != nil {
		t.Fatalf("land %s: %v", id, err)
	}
}

// sdWhy is `ready --why <id>` (readyReport, the verb's body) on the
// throwaway store with a map forge (no entry here is a PR): its one line and
// exit code.
func sdWhy(t *testing.T, c *redis.Client, id string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := readyReport(context.Background(), c, mapForge{}, "", id, &out, &errOut)
	if code == 2 {
		t.Fatalf("ready --why %s could not run: %s", id, errOut.String())
	}
	return strings.TrimSpace(out.String()), code
}

func sdField(t *testing.T, c *redis.Client, id, f string) string {
	t.Helper()
	return c.HGet(context.Background(), "task:"+id, f).Val()
}

// TestSentinelDepReleasedWhenStopLands is the card's DONE-WHEN: a queue task
// B and a stream card C with DEPENDS-ON alpha:sentinel wait while alpha's
// stop waits, ready --why names the sentinel, the dealer refuses C naming
// it; alpha's last card lands, its stop lands by structure, B is released
// in that same call and C by the resolver's pass. Probe 2 rides along: a
// landed card and a closed task meet an edge, a card done/fail does not;
// and probe 1: a sentinel set done by hand (not landed) meets nothing.
func TestSentinelDepReleasedWhenStopLands(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	const stop = "alpha:sentinel"

	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	if w := sdField(t, c, stop, "where"); w != "waiting" {
		t.Fatalf("alpha's stop is %q, want waiting", w)
	}
	if res := sdQueue(t, st, "B", stop); res.Status != task.PushCreated || res.Waiting != 1 {
		t.Fatalf("push B: %+v", res)
	}
	if d, w := sdField(t, c, "B", "depends_on"), sdField(t, c, "B", "waits_on"); d != "task:"+stop || w != "task:"+stop {
		t.Fatalf("B depends_on %q waits_on %q, want the bare sentinel stored as task:%s", d, w, stop)
	}
	if err := sdCard(t, c, "C", "beta", stop); err != nil {
		t.Fatal(err)
	}
	if got, code := sdWhy(t, c, "B"); got != "WAIT task:"+stop+" waiting" || code != 1 {
		t.Fatalf("ready --why B = %q exit %d", got, code)
	}
	if got, code := sdWhy(t, c, "C"); got != "WAIT "+stop+" waiting" || code != 1 {
		t.Fatalf("ready --why C = %q exit %d", got, code)
	}
	k, _ := taskcard.ParseConsumer("friend:f1")
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{"C"}, By: "test"}); err == nil ||
		!strings.Contains(err.Error(), "DEPENDS task:C waits on task:"+stop+" waiting (of "+stop+")") {
		t.Fatalf("deal C before the stop landed: %v", err)
	}

	// Probe 2's rows: D waits on a card that will land; E on a queue task
	// that will close; F on a card done/fail.
	if res := sdQueue(t, st, "D", "task:A1"); res.Waiting != 1 {
		t.Fatalf("push D: %+v", res)
	}
	if res := sdQueue(t, st, "T", ""); res.Status != task.PushCreated {
		t.Fatalf("push T: %+v", res)
	}
	if res := sdQueue(t, st, "E", "task:T"); res.Waiting != 1 {
		t.Fatalf("push E: %+v", res)
	}
	if err := sdCard(t, c, "X", "gamma", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Cancel(ctx, c, "X", "test", "not needed"); err != nil {
		t.Fatal(err)
	}
	// a card done/fail meets nothing: its class is dead, and the queue door
	// refuses the edge by class instead of letting F wait for ever
	if res := sdQueue(t, st, "F", "task:X"); res.Status != task.PushInvalid || !strings.HasPrefix(res.Reason, "DEAD task:X dead done/fail") {
		t.Fatalf("push F on a card done/fail: %+v, want refused DEAD", res)
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "T", As: "f1"})
	if err != nil || !ok {
		t.Fatalf("take T: %v %v", ok, err)
	}
	if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sdSprint, ID: "T", Token: claim.Token, Evidence: "https://example.test/T"}); err != nil || got != task.DoneClosed {
		t.Fatalf("done T: %s %v", got, err)
	}
	if s := sdField(t, c, "E", "state"); s != "open" {
		t.Fatalf("E after T closed: %q, want open", s)
	}

	// alpha's last card lands: its stop lands at the same sha, and B and D
	// are released in that call.
	sdLand(t, c, "A1")
	if w := sdField(t, c, stop, "where"); w != "landed" {
		t.Fatalf("alpha's stop after A1 landed: %q", w)
	}
	for _, id := range []string{"B", "D"} {
		if s, w := sdField(t, c, id, "state"), sdField(t, c, id, "waits_on"); s != "open" || w != "" {
			t.Fatalf("%s after the landing: state %q waits_on %q, want open", id, s, w)
		}
	}
	if n := c.Exists(ctx, "task:F").Val(); n != 0 {
		t.Fatal("task:F written by a refused push")
	}
	if got, code := sdWhy(t, c, "B"); got != "READY "+sdSprint+"/B" || code != 0 {
		t.Fatalf("ready --why B after the stop landed = %q exit %d", got, code)
	}
	var readies int
	for _, e := range c.XRange(ctx, "s:"+sdSprint+":log", "-", "+").Val() {
		if e.Values["kind"] == "task ready" && e.Values["id"] == "B" {
			readies++
		}
	}
	if readies != 1 {
		t.Fatalf("B's task ready receipts %d, want 1", readies)
	}

	// The take's needs (#2939) are the same rule: a landed card and a landed
	// stop are met, not only the sprint's closed index.
	if _, err := task.PushChecked(ctx, st, task.PushRequest{Sprint: sdSprint, ID: "N", Kind: task.KindWork, Title: "queue task N",
		Effects: task.EffectsNone, To: "f1", Needs: []string{"A1", stop}, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "N", As: "f1"}); err != nil || !ok {
		t.Fatalf("take N needing a landed card and a landed stop: ok %v err %v", ok, err)
	}

	// C is a stream card: the dealer names the met edge and leaves it to the
	// resolver, whose one pass releases it.
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{"C"}, By: "test"}); err == nil ||
		!strings.Contains(err.Error(), "its task edges are met: the waiting resolver releases it") {
		t.Fatalf("deal C after the stop landed: %v", err)
	}
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if counts, err := (&reconcile.WaitingResolve{Client: c, Out: &out}).Run(ctx, lease); err != nil || counts.Routed != 1 {
		t.Fatalf("resolver: routed %d err %v (%s)", counts.Routed, err, out.String())
	}
	if w := sdField(t, c, "C", "where"); w != "ready" {
		t.Fatalf("C after the resolver: %q", w)
	}
	t.Logf("receipt: %s", strings.TrimSpace(out.String()))

	// Probe 1: a sentinel done by hand. The graph refuses the move; a hand
	// edit to done/ok still meets nothing: its class is dead, so both push
	// doors refuse an edge on it, and an edge written around them is DEAD.
	const gstop = "gamma:sentinel"
	if _, err := taskcard.Move(ctx, c, gstop, "done", taskcard.Opts{By: "test", OK: "ok", Why: "by hand"}); err == nil ||
		!strings.Contains(err.Error(), "SENTINEL task:"+gstop) {
		t.Fatalf("task done on a sentinel: %v", err)
	}
	if err := c.HSet(ctx, "task:"+gstop, "where", "done", "where_ok", "ok", "state", "closed").Err(); err != nil {
		t.Fatal(err)
	}
	if res := sdQueue(t, st, "G", gstop); res.Status != task.PushInvalid || !strings.HasPrefix(res.Reason, "DEAD task:"+gstop+" dead done/ok") {
		t.Fatalf("push G on a sentinel done by hand: %+v, want refused DEAD", res)
	}
	if err := sdCard(t, c, "K", "epsilon", gstop); err == nil || !strings.Contains(err.Error(), "DEAD task:"+gstop+" dead done/ok") {
		t.Fatalf("card push K on a sentinel done by hand: %v, want refused DEAD", err)
	}
	// K's edge written around the push (a hand edit)
	if err := sdCard(t, c, "K", "epsilon", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "K", "waiting", taskcard.Opts{By: "test", Why: "hand"}); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:K", "blocked_on", gstop).Err(); err != nil {
		t.Fatal(err)
	}
	got, code := sdWhy(t, c, "K")
	if got != "DEAD "+gstop+" dead done/ok" || code != 1 {
		t.Fatalf("ready --why K = %q exit %d", got, code)
	}
	t.Logf("probe 1: ready --why K = %s", got)

	// ws show --order's edge state is the same table: C's edge on alpha's
	// landed stop is met, K's on gamma's stop done by hand is dead.
	streams, err := ws.Show(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	edges := map[string]string{}
	for _, s := range streams {
		for _, card := range s.Cards {
			for _, d := range card.Deps {
				edges[card.ID+" "+d.Raw] = ws.DepText(d.Class, d.Detail)
			}
		}
	}
	if cl := edges["C "+stop]; cl != "met landed" {
		t.Fatalf("ws show: C's edge on %s is %q, want met landed", stop, cl)
	}
	if cl := edges["K "+gstop]; cl != "dead done/ok" {
		t.Fatalf("ws show: K's edge on %s is %q, want dead done/ok", gstop, cl)
	}
}

// TestSentinelDepUnknownStreamIsNamed is probe 3: a sentinel of a stream
// that never registered has no record; a push naming it is refused UNKNOWN
// by name at both doors with nothing written, and an edge written around
// the push is never met and is named: UNKNOWN by ready --why, unknown= by
// the resolver.
func TestSentinelDepUnknownStreamIsNamed(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	const ghost = "nosuch:sentinel"
	res := sdQueue(t, st, "H", ghost)
	if res.Status != task.PushInvalid || !strings.Contains(res.Reason, "UNKNOWN task:"+ghost+" has no record: no stream with the slug nosuch is registered") {
		t.Fatalf("queue push on a ghost stop: %+v", res)
	}
	err := sdCard(t, c, "I", "delta", ghost)
	if err == nil || !strings.Contains(err.Error(), "UNKNOWN task:"+ghost) {
		t.Fatalf("card push on a ghost stop: %v", err)
	}
	for _, id := range []string{"H", "I"} {
		if n := c.Exists(ctx, "task:"+id).Val(); n != 0 {
			t.Fatalf("task:%s written by a refused push", id)
		}
	}
	// An edge written around the push (a hand edit): never met, named.
	if err := sdCard(t, c, "J", "delta", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "J", "waiting", taskcard.Opts{By: "test", Why: "hand"}); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:J", "blocked_on", ghost).Err(); err != nil {
		t.Fatal(err)
	}
	got, code := sdWhy(t, c, "J")
	if got != "UNKNOWN "+ghost+" unknown" || code != 1 {
		t.Fatalf("ready --why J = %q exit %d", got, code)
	}
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if counts, err := (&reconcile.WaitingResolve{Client: c, Out: &out}).Run(ctx, lease); err != nil || counts.Routed != 0 {
		t.Fatalf("resolver: routed %d err %v", counts.Routed, err)
	}
	if !strings.Contains(out.String(), "unknown="+ghost) {
		t.Fatalf("resolver receipt %q does not name the ghost stop", out.String())
	}
	t.Logf("probe 3: %s; ready --why J = %s; %s", res.Reason, got, strings.TrimSpace(out.String()))
}

// TestSentinelDepRaceReleasesOnce is probe 4: two resolver passes at once,
// 50 trials, release the dependent card exactly once each: one ws:log
// waiting -> ready entry and one Ready line across the two passes.
func TestSentinelDepRaceReleasesOnce(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	const trials = 50
	for i := 0; i < trials; i++ {
		a, b := fmt.Sprintf("race a%d", i), fmt.Sprintf("race b%d", i)
		first, dep := fmt.Sprintf("RA%d", i), fmt.Sprintf("RB%d", i)
		if err := sdCard(t, c, first, a, ""); err != nil {
			t.Fatal(err)
		}
		if err := sdCard(t, c, dep, b, ws.SentinelID(a)); err != nil {
			t.Fatal(err)
		}
		sdLand(t, c, first)
		if err := lease.Renew(ctx); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		lines := make([][]reconcile.ResolveLine, 2)
		errs := make([]error, 2)
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				lines[g], errs[g] = (&reconcile.WaitingResolve{Client: c}).Pass(ctx, lease)
			}(g)
		}
		wg.Wait()
		ready := 0
		for g := 0; g < 2; g++ {
			if errs[g] != nil {
				t.Fatalf("trial %d pass %d: %v", i, g, errs[g])
			}
			for _, l := range lines[g] {
				for _, id := range l.Ready {
					if id == dep {
						ready++
					}
				}
			}
		}
		moves := 0
		for _, e := range c.XRange(ctx, "ws:log", "-", "+").Val() {
			if e.Values["id"] == dep && e.Values["to"] == "ready" {
				moves++
			}
		}
		if ready != 1 || moves != 1 || sdField(t, c, dep, "where") != "ready" {
			t.Fatalf("trial %d: Ready lines %d, ws:log releases %d, where %q; want exactly one release", i, ready, moves, sdField(t, c, dep, "where"))
		}
	}
	t.Logf("probe 4: %d trials, each released exactly once", trials)
}

// TestSentinelDepCycleRefusedAtPush is probe 5: a DEPENDS-ON that closes a
// cycle through a sentinel is refused CYCLE by name at push, the path
// spelled out, and nothing is written: a card on its own stream's stop, a
// card whose edge leads through another stream back to its own stop, and
// the same through the task queue's door.
func TestSentinelDepCycleRefusedAtPush(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	if err := sdCard(t, c, "P1", "p", ""); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "Q1", "q", "p:sentinel"); err != nil {
		t.Fatal(err)
	}
	before := c.ZRange(ctx, ws.Key("p", "waiting"), 0, -1).Val()

	err := sdCard(t, c, "P2", "p", "p:sentinel")
	if err == nil || !strings.Contains(err.Error(), "CYCLE task:P2 -> p:sentinel (the stop of stream p, which waits on task:P2)") {
		t.Fatalf("a card on its own stop: %v", err)
	}
	err = sdCard(t, c, "P3", "p", "q:sentinel")
	want := "CYCLE task:P3 -> q:sentinel -> Q1 -> p:sentinel (the stop of stream p, which waits on task:P3)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a cycle through q's stop: %v", err)
	}
	res, qerr := task.PushChecked(ctx, st, task.PushRequest{Sprint: sdSprint, ID: "P4", Kind: task.KindWork,
		Title: "STREAM: p | P4", Effects: task.EffectsNone, To: "f1", DependsOn: "q:sentinel", Actor: "test"})
	if qerr != nil || res.Status != task.PushInvalid || !strings.Contains(res.Reason, "CYCLE task:P4 -> q:sentinel -> Q1 -> p:sentinel") {
		t.Fatalf("queue push closing the cycle: %+v %v", res, qerr)
	}
	for _, id := range []string{"P2", "P3", "P4"} {
		if n := c.Exists(ctx, "task:"+id).Val(); n != 0 {
			t.Fatalf("task:%s written by a refused push", id)
		}
	}
	if after := c.ZRange(ctx, ws.Key("p", "waiting"), 0, -1).Val(); strings.Join(after, " ") != strings.Join(before, " ") {
		t.Fatalf("stream p waiting %v -> %v: a refused push wrote", before, after)
	}
	t.Logf("probe 5: %v | %s", err, res.Reason)
}

// sdSprintCard seeds one queued card of the sprint store (s:<S>:card:<label>
// in the pool) with a DEPENDS-ON, the record the Go dealer's gate reads.
func sdSprintCard(t *testing.T, c *redis.Client, label, deps string) {
	t.Helper()
	ctx := context.Background()
	if err := c.HSet(ctx, "s:"+sdSprint+":card:"+label, "state", "queued", "depends_on", deps).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, "s:"+sdSprint+":pool", redis.Z{Score: 1, Member: label}).Err(); err != nil {
		t.Fatal(err)
	}
}

// sdGate is the Go dealer's DEPENDS-ON gate (deal.Ready) on the store as
// deal.RedisSource reads it: each held sprint card's why, keyed by label.
func sdGate(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	in, err := deal.RedisSource{Client: c}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, blocked := deal.Ready(ctx, in, mapForge{})
	why := map[string]string{}
	for _, b := range blocked {
		why[b.Label] = b.Why
	}
	return why
}

// sdLease is the reconciler lease a test's resolver passes run under.
func sdLease(t *testing.T, st *store.Store) *reconcile.Lease {
	t.Helper()
	lease, err := reconcile.Acquire(context.Background(), st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

// sdResolve is one waiting-resolver pass: its receipt lines.
func sdResolve(c *redis.Client, lease *reconcile.Lease) (string, int, error) {
	var out bytes.Buffer
	counts, err := (&reconcile.WaitingResolve{Client: c, Out: &out}).Run(context.Background(), lease)
	return out.String(), counts.Routed, err
}

// sdEdges is ws show's class text for every edge, keyed "<card> <entry>".
func sdEdges(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	streams, err := ws.Show(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	edges := map[string]string{}
	for _, s := range streams {
		for _, card := range s.Cards {
			for _, d := range card.Deps {
				edges[card.ID+" "+d.Raw] = ws.DepText(d.Class, d.Detail)
			}
		}
	}
	return edges
}

// TestDepClassEveryReader is the DOORS clause: one table of target states,
// and for each row every reader of an edge on that target gives the same
// class word (ws.DepClass / NS.dep.class_of): task take's needs, the queue
// door (task push, both spellings task:<id> and the bare <id>), ready --why,
// the dealer (the stream deal's DEPENDS line and the Go gate deal.Ready),
// the waiting resolver and ws show. The cycle row is the write doors': the
// card push and the queue push refuse it and nothing is written, so no
// reader ever meets one.
func TestDepClassEveryReader(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	rows := []struct {
		name, target, class, text string
	}{
		{"met", "XM", ws.DepClassMet, "met landed"},
		{"waiting", "XW", ws.DepClassWaiting, "waiting working"},
		{"parked", "XP", ws.DepClassParked, "parked"},
		{"dead", "XD", ws.DepClassDead, "dead done/fail"},
		{"unknown", "GH", ws.DepClassUnknown, "unknown"},
	}
	// The targets start ready in stream tgt (GH is never written), and each
	// row's edges are written while its target is live: a stream card
	// C<row> in stream dep-<row>, a sprint card D<row>, a queue task
	// N<row> that needs it.
	for _, r := range rows {
		if r.target != "GH" {
			if err := sdCard(t, c, r.target, "tgt", ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := sdCard(t, c, "C"+r.name, "dep-"+r.name, r.target); err != nil {
			t.Fatalf("card push C%s on %s: %v", r.name, r.target, err)
		}
		sdSprintCard(t, c, "D"+r.name, r.target)
		if _, err := task.PushChecked(ctx, st, task.PushRequest{Sprint: sdSprint, ID: "N" + r.name, Kind: task.KindWork,
			Title: "needs " + r.target, Effects: task.EffectsNone, To: "f1", Needs: []string{r.target}, Actor: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	// Each target moves to its row's state.
	sdLand(t, c, "XM")
	if _, err := taskcard.Move(ctx, c, "XW", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "XP", "parked", taskcard.Opts{By: "test", Why: "held"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Cancel(ctx, c, "XD", "test", "not needed"); err != nil {
		t.Fatal(err)
	}

	gate := sdGate(t, c)
	edges := sdEdges(t, c)
	var receipts []string
	for _, r := range rows {
		met := r.class == ws.DepClassMet
		lead := map[string]string{ws.DepClassWaiting: "WAIT", ws.DepClassParked: "WAIT", ws.DepClassDead: "DEAD", ws.DepClassUnknown: "UNKNOWN"}[r.class]

		// the queue door, both spellings, pushed now
		for _, spell := range []string{"task:" + r.target, r.target} {
			id := "Q" + r.name
			if spell == r.target {
				id = "QB" + r.name
			}
			res := sdQueue(t, st, id, spell)
			switch r.class {
			case ws.DepClassMet:
				if res.Status != task.PushCreated || res.Waiting != 0 {
					t.Errorf("%s: push %s on %s: %+v, want created open", r.name, id, spell, res)
				}
			case ws.DepClassDead:
				if res.Status != task.PushInvalid || !strings.HasPrefix(res.Reason, "DEAD task:"+r.target+" "+r.text) {
					t.Errorf("%s: push %s on %s: %+v, want refused DEAD", r.name, id, spell, res)
				}
				continue
			default:
				if res.Status != task.PushCreated || res.Classes != "task:"+r.target+" "+r.text {
					t.Errorf("%s: push %s on %s: %+v, want waiting on task:%s %s", r.name, id, spell, res, r.target, r.text)
				}
			}
			if d := sdField(t, c, id, "depends_on"); d != "task:"+r.target {
				t.Errorf("%s: %s depends_on %q, want task:%s", r.name, id, d, r.target)
			}
			if got, _ := sdWhy(t, c, id); !met && got != lead+" task:"+r.target+" "+r.text {
				t.Errorf("%s: ready --why %s = %q, want %s task:%s %s", r.name, id, got, lead, r.target, r.text)
			} else if met && got != "READY "+sdSprint+"/"+id && !strings.HasPrefix(got, "WAIT PATHS ") {
				// met: no dependency blocks it (XW, working with no PATHS,
				// overlaps it: the whole repo)
				t.Errorf("%s: ready --why %s = %q, want no dependency blocker", r.name, id, got)
			}
		}

		// ready --why on the stream card (met: only its release is left)
		got, _ := sdWhy(t, c, "C"+r.name)
		want := lead + " " + r.target + " " + r.text
		if met {
			want = "WAIT release"
		}
		if got != want {
			t.Errorf("%s: ready --why C%s = %q, want %q", r.name, r.name, got, want)
		}

		// task take's needs (after the queue door: a claim is in flight, and
		// ready would name its PATHS)
		_, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "N" + r.name, As: "f1"})
		var blocked *task.BlockedError
		switch {
		case met && (!ok || err != nil):
			t.Errorf("%s: take N%s: ok %v err %v, want claimed", r.name, r.name, ok, err)
		case !met && (!errors.As(err, &blocked) || blocked.Unmet() != r.target+" "+r.text):
			t.Errorf("%s: take N%s: %v, want BLOCKED needs %s %s", r.name, r.name, err, r.target, r.text)
		}

		// the dealer: the stream deal's DEPENDS line and the Go gate
		k, _ := taskcard.ParseConsumer("friend:f1")
		_, derr := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{"C" + r.name}, By: "test"})
		wantDeal := "waits on task:" + r.target + " " + r.text + " (of " + r.target + ")"
		if met {
			wantDeal = "its task edges are met"
		}
		if derr == nil || !strings.Contains(derr.Error(), wantDeal) {
			t.Errorf("%s: deal C%s: %v, want %q", r.name, r.name, derr, wantDeal)
		}
		if w, held := gate["D"+r.name]; met && held || !met && w != r.target+" "+r.text {
			t.Errorf("%s: gate D%s held %v why %q, want %q", r.name, r.name, held, w, r.target+" "+r.text)
		}

		// ws show
		if e := edges["C"+r.name+" "+r.target]; e != r.text {
			t.Errorf("%s: ws show C%s's edge = %q, want %q", r.name, r.name, e, r.text)
		}
		receipts = append(receipts, fmt.Sprintf("%s: take=%q deal=%q", r.name, blockedText(blocked), wantDeal))
	}

	// the waiting resolver, one pass: its line per stream names each
	// unmet edge under its class, and releases the met row's card
	out, routed, err := sdResolve(c, sdLease(t, st))
	if err != nil || routed != 1 || sdField(t, c, "Cmet", "where") != "ready" {
		t.Fatalf("resolver: routed %d err %v (%s)", routed, err, out)
	}
	for _, r := range rows {
		want := "RESOLVE stream=dep-" + r.name + " ready=0 still=1 "
		for _, cl := range []string{ws.DepClassWaiting, ws.DepClassParked, ws.DepClassDead, ws.DepClassUnknown} {
			v := "-"
			if cl == r.class {
				v = r.target
			}
			want += cl + "=" + v + " "
		}
		want = strings.TrimSpace(want)
		if r.class == ws.DepClassMet {
			want = "RESOLVE stream=dep-met ready=1 still=0 waiting=- parked=- dead=- unknown=-"
		}
		if !strings.Contains(out, want+"\n") {
			t.Errorf("%s: resolver receipt lacks %q:\n%s", r.name, want, out)
		}
	}

	// the cycle row: the write doors refuse it by class, nothing written
	if err := sdCard(t, c, "CY1", "cy", "cy:sentinel"); err == nil || !strings.Contains(err.Error(), "CYCLE task:CY1 -> cy:sentinel") {
		t.Errorf("cycle: card push CY1 on its own stop: %v", err)
	}
	res, qerr := task.PushChecked(ctx, st, task.PushRequest{Sprint: sdSprint, ID: "CY2", Kind: task.KindWork,
		Title: "STREAM: cy | CY2", Effects: task.EffectsNone, To: "f1", DependsOn: "cy:sentinel", Actor: "test"})
	if qerr != nil || res.Status != task.PushInvalid || !strings.HasPrefix(res.Reason, "CYCLE task:CY2 -> cy:sentinel") {
		t.Errorf("cycle: queue push CY2 on its own stop: %+v %v", res, qerr)
	}
	for _, id := range []string{"CY1", "CY2"} {
		if c.Exists(ctx, "task:"+id).Val() != 0 {
			t.Errorf("cycle: task:%s written by a refused push", id)
		}
	}
	t.Logf("doors: %s", strings.Join(receipts, " | "))
	t.Logf("doors: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
}

func blockedText(b *task.BlockedError) string {
	if b == nil {
		return "CLAIMED"
	}
	return "BLOCKED needs " + b.Unmet()
}

// TestSentinelDepCancelledNeedBlocksTake is probe (a): a need cancelled
// (state closed, where done/fail, in the sprint's closed index) is dead, not
// met: task take refuses the task naming the need and its class, and
// nothing is written.
func TestSentinelDepCancelledNeedBlocksTake(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	if res := sdQueue(t, st, "X", ""); res.Status != task.PushCreated {
		t.Fatalf("push X: %+v", res)
	}
	if _, err := taskcard.Cancel(ctx, c, "X", "test", "not needed"); err != nil {
		t.Fatalf("cancel X: %v", err)
	}
	h := c.HGetAll(ctx, "task:X").Val()
	if !c.SIsMember(ctx, "s:"+sdSprint+":idx:task:closed", "X").Val() || h["where"] != "done" || h["where_ok"] != "fail" {
		t.Fatalf("X after cancel: %v closed-index %v, want done/fail in the closed index", h, c.SIsMember(ctx, "s:"+sdSprint+":idx:task:closed", "X").Val())
	}
	if _, err := task.PushChecked(ctx, st, task.PushRequest{Sprint: sdSprint, ID: "N", Kind: task.KindWork, Title: "needs X",
		Effects: task.EffectsNone, To: "f1", Needs: []string{"X"}, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "N", As: "f1"})
	var blocked *task.BlockedError
	if ok || !errors.As(err, &blocked) || blocked.Unmet() != "X dead done/fail" {
		t.Fatalf("take N needing cancelled X: ok %v err %v, want BLOCKED needs X dead done/fail", ok, err)
	}
	if s := sdField(t, c, "N", "state"); s != "open" || sdField(t, c, "N", "attempt") != "0" {
		t.Fatalf("N after the refused take: state %q attempt %q, want open 0", s, sdField(t, c, "N", "attempt"))
	}
	t.Logf("probe a: BLOCKED needs %s", blocked.Unmet())
}

// TestSentinelDepGhostIsUnknownEverywhere is probe (b): task:ghost, an id no
// record has, is unknown in ready --why, in both dealers and in the
// resolver, and the queue door names it unknown while it waits.
func TestSentinelDepGhostIsUnknownEverywhere(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	res := sdQueue(t, st, "G", "task:ghost")
	if res.Status != task.PushCreated || res.Waiting != 1 || res.Classes != "task:ghost unknown" {
		t.Fatalf("push G on task:ghost: %+v", res)
	}
	if err := sdCard(t, c, "J", "delta", "task:ghost"); err != nil {
		t.Fatal(err)
	}
	sdSprintCard(t, c, "D", "task:ghost")
	var lines []string
	for _, id := range []string{"G", "J"} {
		got, code := sdWhy(t, c, id)
		if got != "UNKNOWN task:ghost unknown" || code != 1 {
			t.Fatalf("ready --why %s = %q exit %d", id, got, code)
		}
		lines = append(lines, "ready --why "+id+" = "+got)
	}
	k, _ := taskcard.ParseConsumer("friend:f1")
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{"J"}, By: "test"}); err == nil ||
		!strings.Contains(err.Error(), "DEPENDS task:J waits on task:ghost unknown") {
		t.Fatalf("deal J: %v", err)
	} else {
		lines = append(lines, "deal J: "+err.Error())
	}
	if why := sdGate(t, c)["D"]; why != "task:ghost unknown" {
		t.Fatalf("gate D: %q", why)
	}
	out, routed, err := sdResolve(c, sdLease(t, st))
	if err != nil || routed != 0 || !strings.Contains(out, "RESOLVE stream=delta ready=0 still=1 waiting=- parked=- dead=- unknown=task:ghost\n") {
		t.Fatalf("resolver: routed %d err %v: %q", routed, err, out)
	}
	lines = append(lines, "gate D: task:ghost unknown", strings.TrimSpace(out))
	t.Logf("probe b: %s", strings.Join(lines, " | "))
}

// TestSentinelDepBareIDAtQueueDoor is probe (c): a bare P3 at the queue door
// is stored as task:P3, exactly as task:P3 is, and both are met and released
// in the call that lands P3.
func TestSentinelDepBareIDAtQueueDoor(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	if err := sdCard(t, c, "P3", "pstream", ""); err != nil {
		t.Fatal(err)
	}
	for id, deps := range map[string]string{"QB": "P3", "QT": "task:P3"} {
		res := sdQueue(t, st, id, deps)
		if res.Status != task.PushCreated || res.Waiting != 1 || res.Classes != "task:P3 waiting ready" {
			t.Fatalf("push %s on %q: %+v", id, deps, res)
		}
		if d, w := sdField(t, c, id, "depends_on"), sdField(t, c, id, "waits_on"); d != "task:P3" || w != "task:P3" {
			t.Fatalf("%s on %q: depends_on %q waits_on %q, want task:P3", id, deps, d, w)
		}
	}
	if res := sdQueue(t, st, "QX", "P3 junk"); res.Status != task.PushInvalid || res.Reason != "depends-on P3 junk" {
		t.Fatalf("push on two words: %+v, want INVALID", res)
	}
	sdLand(t, c, "P3")
	for _, id := range []string{"QB", "QT"} {
		if s, w := sdField(t, c, id, "state"), sdField(t, c, id, "waits_on"); s != "open" || w != "" {
			t.Fatalf("%s after P3 landed: state %q waits_on %q, want open", id, s, w)
		}
	}
	t.Logf("probe c: QB (P3) and QT (task:P3) stored task:P3, both open after P3 landed")
}

// wsGuard is the guarded release's parse in ws.lua's ns_ws_move_many; a
// library without it is dev be3e5f8ef's.
const wsGuard = "local f, t = string.match(to or '', '^(%a+)>(%a+)$')"

// TestSentinelDepStaleLibraryRefuses is probe (d): on a store whose library
// has no guarded release, one resolver pass prints RESOLVE REFUSED with the
// loaded and wanted library and the remedy, and moves nothing; the release
// of a card a dealer already dealt is refused, not pulled back to ready.
// After the remedy (fn load, the embedded library) the pass releases.
func TestSentinelDepStaleLibraryRefuses(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	if err := sdCard(t, c, "SX", "sx", ""); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "SB", "sy", "SX"); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "SW", "sz", ""); err != nil {
		t.Fatal(err)
	}
	sdLand(t, c, "SX")
	if _, err := taskcard.Move(ctx, c, "SW", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	src, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(src, wsGuard, "local f, t = nil, nil", 1)
	if stale == src {
		t.Fatal("ws.lua carries no guard line to strip")
	}
	if err := c.FunctionLoadReplace(ctx, stale).Err(); err != nil {
		t.Fatal(err)
	}
	logBefore := c.XLen(ctx, "ws:log").Val()

	lease := sdLease(t, st)
	out, routed, err := sdResolve(c, lease)
	want := fmt.Sprintf("RESOLVE REFUSED library=%s wants=%s remedy=%q\n", fn.Sum(stale), fn.Sum(src), "nova-sprint fn load")
	var ue *reconcile.UnguardedError
	if routed != 0 || !errors.As(err, &ue) || !errors.Is(err, ws.ErrUnguarded) || !strings.HasSuffix(out, want) {
		t.Fatalf("stale pass: routed %d err %v out %q, want %q", routed, err, out, want)
	}
	if _, err := ws.Release(ctx, c, "test", "a stale read", []string{"SW"}); !errors.Is(err, ws.ErrUnguarded) {
		t.Fatalf("release of dealt SW on the stale library: %v, want ErrUnguarded", err)
	}
	if w, b := sdField(t, c, "SW", "where"), sdField(t, c, "SB", "where"); w != "working" || b != "waiting" {
		t.Fatalf("after the refused pass: SW %q SB %q, want working and waiting", w, b)
	}
	if n := c.XLen(ctx, "ws:log").Val(); n != logBefore {
		t.Fatalf("ws:log %d -> %d: the refused pass wrote", logBefore, n)
	}

	if _, _, err := fn.Ensure(ctx, c); err != nil {
		t.Fatal(err)
	}
	if out2, routed, err := sdResolve(c, lease); err != nil || routed != 1 || sdField(t, c, "SB", "where") != "ready" {
		t.Fatalf("after fn load: routed %d err %v (%s)", routed, err, out2)
	}
	t.Logf("probe d: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
}

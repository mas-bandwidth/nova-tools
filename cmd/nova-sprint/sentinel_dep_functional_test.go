//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
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
// one registered friend f1.
func sdStore(t *testing.T) (string, *redis.Client, *store.Store) {
	t.Helper()
	t.Setenv(store.UserEnv, "")
	addr, c := wstest.Start(t)
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
	old := readyForge
	t.Cleanup(func() { readyForge = old })
	readyForge = func(*store.Store) deal.PRs { return mapForge{} } // no forge: no entry here is a PR
	return addr, c, store.New(c)
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

// sdWhy is `ready --why <id>`: its one line and exit code.
func sdWhy(t *testing.T, addr, id string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run([]string{"ready", "--redis", addr, "--why", id}, &out, &errOut)
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
	addr, c, st := sdStore(t)
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
	if got, code := sdWhy(t, addr, "B"); got != "WAIT task:"+stop+" sentinel-waiting" || code != 1 {
		t.Fatalf("ready --why B = %q exit %d", got, code)
	}
	if got, code := sdWhy(t, addr, "C"); got != "WAIT "+stop+" sentinel-waiting" || code != 1 {
		t.Fatalf("ready --why C = %q exit %d", got, code)
	}
	k, _ := taskcard.ParseConsumer("friend:f1")
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{"C"}, By: "test"}); err == nil ||
		!strings.Contains(err.Error(), "DEPENDS task:C waits on task:"+stop) {
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
	if res := sdQueue(t, st, "F", "task:X"); res.Waiting != 1 {
		t.Fatalf("push F on a card done/fail: %+v, want waiting", res)
	}
	if got, _ := sdWhy(t, addr, "F"); got != "DEAD task:X task-done/fail" {
		t.Fatalf("ready --why F = %q", got)
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
	if s := sdField(t, c, "F", "state"); s != "waiting" {
		t.Fatalf("F: %q, want waiting (a card done/fail never meets)", s)
	}
	if got, code := sdWhy(t, addr, "B"); got != "READY "+sdSprint+"/B" || code != 0 {
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
	// edit to done/ok still meets nothing.
	const gstop = "gamma:sentinel"
	if _, err := taskcard.Move(ctx, c, gstop, "done", taskcard.Opts{By: "test", OK: "ok", Why: "by hand"}); err == nil ||
		!strings.Contains(err.Error(), "SENTINEL task:"+gstop) {
		t.Fatalf("task done on a sentinel: %v", err)
	}
	if err := c.HSet(ctx, "task:"+gstop, "where", "done", "where_ok", "ok", "state", "closed").Err(); err != nil {
		t.Fatal(err)
	}
	if res := sdQueue(t, st, "G", gstop); res.Waiting != 1 || res.OnMet {
		t.Fatalf("push G on a sentinel done by hand: %+v, want waiting", res)
	}
	got, code := sdWhy(t, addr, "G")
	if got != "WAIT task:"+gstop+" sentinel-done" || code != 1 {
		t.Fatalf("ready --why G = %q exit %d", got, code)
	}
	t.Logf("probe 1: ready --why G = %s", got)

	// ws show --order's edge state is the same rule: C's edge on alpha's
	// landed stop is met, K's on gamma's stop done by hand is not.
	if err := sdCard(t, c, "K", "epsilon", gstop); err != nil {
		t.Fatal(err)
	}
	streams, err := ws.Show(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	edges := map[string]bool{}
	for _, s := range streams {
		for _, card := range s.Cards {
			for _, d := range card.Deps {
				edges[card.ID+" "+d.Raw] = d.Landed
			}
		}
	}
	if met, ok := edges["C "+stop]; !ok || !met {
		t.Fatalf("ws show: C's edge on %s met=%v (present %v), want met", stop, met, ok)
	}
	if met, ok := edges["K "+gstop]; !ok || met {
		t.Fatalf("ws show: K's edge on %s met=%v (present %v), want not met", gstop, met, ok)
	}
}

// TestSentinelDepUnknownStreamIsNamed is probe 3: a sentinel of a stream
// that never registered has no record; a push naming it is refused UNKNOWN
// by name at both doors with nothing written, and an edge written around
// the push is never met and is named: UNKNOWN by ready --why, unknown= by
// the resolver.
func TestSentinelDepUnknownStreamIsNamed(t *testing.T) {
	addr, c, st := sdStore(t)
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
	got, code := sdWhy(t, addr, "J")
	if got != "UNKNOWN "+ghost+" no-such-stream" || code != 1 {
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
	_, c, st := sdStore(t)
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
	_, c, st := sdStore(t)
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

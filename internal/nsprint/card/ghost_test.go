//go:build functional

package card_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	sprintv "github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// No ghost cards (nova-tools#3925). Glenn 2026-09-25 12:25 PM ET: "we
// should not have ghost cards". Found that day: bench:<b>:cards:ready on six
// benches held cards of the closed quack-0925 forever, and
// bench:hetzner:living held three fleet-probe leases nothing released, so
// the bench ran 2 of 5 slots. Sprint close retires every card, leases are
// reaped, and card fsck --repair is a reconciler duty over every sprint.

const (
	ghBench  = "ctl-3925"
	ghStream = "swarm: cards"
)

// ghWorld is one throwaway redis-server with the library, one registered
// bench of 5 slots and the reconciler lease.
type ghWorld struct {
	ctx    context.Context
	st     *store.Store
	c      *redis.Client
	lease  *reconcile.Lease
	repo   string
	tokens map[string]string
}

func newGhWorld(t *testing.T) *ghWorld {
	t.Helper()
	ctx := context.Background()
	st, client := newSprint(t)
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test-3925"})
	if err != nil {
		t.Fatal(err)
	}
	w := &ghWorld{ctx: ctx, st: st, c: client, lease: lease, repo: repoServer(t).URL + "/acme/public.git", tokens: map[string]string{}}
	w.do(t,
		[]any{"SADD", "benches", ghBench},
		[]any{"HSET", "bench:" + ghBench + ":state", "state", "UP"},
		[]any{"HSET", "bench:" + ghBench + ":desired", "slots", "5", "paused", "0"},
	)
	return w
}

func (w *ghWorld) do(t *testing.T, cmds ...[]any) {
	t.Helper()
	for _, cmd := range cmds {
		if err := w.c.Do(w.ctx, cmd...).Err(); err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
	}
}

func (w *ghWorld) open(t *testing.T, s string, at int) {
	t.Helper()
	w.do(t,
		[]any{"HSET", "s:" + s, "status", "open"},
		[]any{"SADD", "sprints", s},
		[]any{"ZADD", "sprint:order", at, s},
	)
}

// closeByOtherPath marks s closed the way no verb does: the status alone.
func (w *ghWorld) closeByOtherPath(t *testing.T, s string) {
	t.Helper()
	w.do(t, []any{"HSET", "s:" + s, "status", "closed"}, []any{"SREM", "sprints", s})
}

func (w *ghWorld) push(t *testing.T, s, label, depends, place string) {
	t.Helper()
	f := validCard(w.repo)
	f.label, f.depends = label, depends
	body := string(f.render()) + "STREAM: " + ghStream + "\nORIGIN: mas-bandwidth/nova-tools#3925\n"
	if res := card.Push(w.ctx, w.c, s, []byte(body)); res.Code != 0 || !strings.Contains(res.Stdout, "place="+place+"\n") {
		t.Fatalf("push %s/%s place=%s: %+v", s, label, place, res)
	}
}

// deal deals labels of sprint s to the bench in one ns_card_deal call.
func (w *ghWorld) deal(t *testing.T, s string, labels ...string) {
	t.Helper()
	args := []any{ghBench, w.lease.Token(), "test", "3925-" + s}
	for i, l := range labels {
		tok := attemptToken(1, fmt.Sprintf("%032x", len(w.tokens)+i+1))
		w.tokens[s+"/"+l] = tok
		args = append(args, s, l, "1", tok, card.TokenSHA(tok))
	}
	reply, err := w.c.FCall(w.ctx, "ns_card_deal", nil, args...).StringSlice()
	if err != nil || len(reply) != 1+4*len(labels) {
		t.Fatalf("deal %v = %v, %v", labels, reply, err)
	}
}

func (w *ghWorld) launch(t *testing.T, s, l string, beat bool) {
	t.Helper()
	tok := w.tokens[s+"/"+l]
	if got, err := card.Launched(w.ctx, w.st, card.LaunchRequest{
		Sprint: s, Label: l, Token: tok, Branch: card.WrapperBranch(s, l, 1), JobDir: "/jobs/" + l,
	}); err != nil || !got.Resolved {
		t.Fatalf("launched %s/%s = %+v, %v", s, l, got, err)
	}
	if !beat {
		return
	}
	if got, err := card.Beat(w.ctx, w.st, card.BeatRequest{Sprint: s, Label: l, Token: tok}); err != nil || !got.Resolved {
		t.Fatalf("beat %s/%s = %+v, %v", s, l, got, err)
	}
}

func (w *ghWorld) end(t *testing.T, s, l string) {
	t.Helper()
	tok := w.tokens[s+"/"+l]
	id, err := card.ParseIdentity(hashOf(t, w.ctx, w.c, s, l)["identity"])
	if err != nil {
		t.Fatal(err)
	}
	results := canonicalResults(t, id)
	writeRecord(t, results, card.EndRecord{
		Identity: id, Outcome: "DONE", Reason: "done", TokenSHA: card.TokenSHA(tok),
		PushedSHA: "89abcdef0123456789abcdef0123456789abcdef", At: "1970-01-01T00:00:00Z",
	})
	if got, err := card.End(w.ctx, w.st, card.EndRequest{
		Sprint: s, Label: l, Token: tok, Outcome: "DONE", Reason: "done", ResultsDir: results,
	}); err != nil || !got.Resolved {
		t.Fatalf("end %s/%s = %+v, %v", s, l, got, err)
	}
}

// liveSets are every bench and stream set a card of a closed sprint must
// have left: each live place of the bench and _pool (working is the bench's
// one lease ledger, #3998), the stream's live places and the dealer's lists.
func liveSets(s string) []string {
	var keys []string
	for _, b := range []string{ghBench, "_pool"} {
		for _, p := range []string{"waiting", "ready", "working", "parked"} {
			keys = append(keys, card.BenchCardsKey(b, p))
		}
	}
	for _, p := range []string{"waiting", "ready", "working", "parked"} {
		keys = append(keys, "ws:"+ghStream+":"+p)
	}
	return append(keys, "s:"+s+":pool", "s:"+s+":bench:"+ghBench+":queue")
}

// membersOf lists the members of every live set that belong to sprint s.
func membersOf(t *testing.T, w *ghWorld, s string) []string {
	t.Helper()
	var out []string
	for _, k := range liveSets(s) {
		ms, err := w.c.ZRange(w.ctx, k, 0, -1).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			if strings.HasPrefix(m, "s:"+s+":card:") || strings.HasPrefix(m, s+"/") || strings.HasPrefix(k, "s:"+s+":") {
				out = append(out, k+" "+m)
			}
		}
	}
	if n := w.c.SCard(w.ctx, "s:"+s+":waiting").Val(); n != 0 {
		out = append(out, fmt.Sprintf("s:%s:waiting holds %d", s, n))
	}
	return out
}

func fsckClean(t *testing.T, w *ghWorld, s, step string) card.FsckReport {
	t.Helper()
	rep, err := card.Fsck(w.ctx, w.c, s, false)
	if err != nil {
		t.Fatalf("%s: fsck %s: %v", step, s, err)
	}
	if rep.Drift != 0 {
		t.Fatalf("%s: %s\n%s", step, rep.Line("FSCK"), strings.Join(rep.Lines, "\n"))
	}
	return rep
}

// TestSprintCloseRetiresEveryCard: sprint close moves every card of the
// sprint that is not done to done/fail through the one move, in the same
// call, and its leases go; no bench or stream set keeps a card of it, fsck
// is clean, the done card keeps its ok, and a wrapper still running under a
// retired card's token is fenced.
func TestSprintCloseRetiresEveryCard(t *testing.T) {
	t.Parallel()

	w := newGhWorld(t)
	const s = "close-3925"
	w.open(t, s, 1)
	for _, l := range []string{"ca", "cb", "cc", "cd", "ce"} {
		w.push(t, s, l, "none", "pool")
	}
	w.push(t, s, "cf", "ce", "waiting")
	w.deal(t, s, "ca", "cb", "cc", "cd") // working: four leases in the one ledger
	w.launch(t, s, "ca", true)           // running
	w.launch(t, s, "cb", true)           // running
	w.launch(t, s, "cc", false)          // launched
	w.end(t, s, "ca")                    // done/ok
	fsckClean(t, w, s, "before close")
	// the one lease ledger is the bench's cards:working (#3998)
	if n := w.c.ZCard(w.ctx, card.BenchCardsKey(ghBench, "working")).Val(); n != 3 {
		t.Fatalf("leases before close = %d, want 3 (cb, cc and cd working)", n)
	}

	status, retired, refused, err := sprintv.SetClosed(w.ctx, w.st, s, time.Now())
	if err != nil || refused != "" || status != sprintv.Closed {
		t.Fatalf("close = %q %q %v", status, refused, err)
	}
	if retired != 5 {
		t.Fatalf("close retired %d, want 5 (cb cc cd working, ce ready, cf waiting)", retired)
	}
	for _, l := range []string{"ca", "cb", "cc", "cd", "ce", "cf"} {
		h := hashOf(t, w.ctx, w.c, s, l)
		if h["where"] != "done" {
			t.Fatalf("%s after close: where=%q state=%q, want done", l, h["where"], h["state"])
		}
		if l == "ca" {
			if h["where_ok"] != "ok" || h["state"] != "ended" {
				t.Fatalf("ca (done before close) = %s/%s %s, want done/ok ended", h["where"], h["where_ok"], h["state"])
			}
			continue
		}
		if h["where_ok"] != "fail" || h["state"] != "superseded" || h["reason"] != "sprint-closed" || h["token"] != "" {
			t.Fatalf("%s after close = %s/%s state=%s reason=%s token=%q, want done/fail superseded sprint-closed, token cleared",
				l, h["where"], h["where_ok"], h["state"], h["reason"], h["token"])
		}
	}
	if left := membersOf(t, w, s); len(left) > 0 {
		t.Fatalf("closed sprint still in live sets:\n%s", strings.Join(left, "\n"))
	}
	rep := fsckClean(t, w, s, "after close")
	if rep.Done != 6 || rep.OK != 1 || rep.Fail != 5 {
		t.Fatalf("after close: %s", rep.Line("FSCK"))
	}
	if got, err := card.Beat(w.ctx, w.st, card.BeatRequest{Sprint: s, Label: "cb", Token: w.tokens[s+"/cb"]}); err != nil || got.Resolved || got.Code != 3 {
		t.Fatalf("beat under a retired card's token = %+v, %v; want code 3 (fenced)", got, err)
	}
	// The move wrote one receipt per retired card.
	moves, err := w.c.XRange(w.ctx, "sprint:"+s+":moves", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range moves {
		if m.Values["by"] == "sprint-close" && m.Values["to"] == "done/fail" {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("sprint-close receipts to done/fail = %d, want 5", n)
	}
}

// TestNoGhostCards: the reaper frees the slot a closed sprint's card held in
// the bench's one lease ledger (cards:working, #3998) by retiring it through
// the move, fenced, and keeps the ones a card beat or its bench's beat
// carried; the fsck duty
// fixes injected ghosts over every sprint in one pass, retires a sprint
// closed by another path, records the finding, and leaves the host table's
// ZCARDs equal to the records.
func TestNoGhostCards(t *testing.T) {
	t.Parallel()

	w := newGhWorld(t)
	const s, q, r = "live-3925", "quack-0925", "gate-3925"
	w.open(t, s, 1)
	w.open(t, q, 2)
	w.open(t, r, 3)
	for _, l := range []string{"gx", "gy", "gz"} {
		w.push(t, s, l, "none", "pool")
	}
	w.deal(t, s, "gx", "gy", "gz")
	for _, l := range []string{"gx", "gy", "gz"} {
		w.launch(t, s, l, true)
	}
	w.push(t, q, "qa", "none", "pool")
	w.deal(t, q, "qa")
	w.launch(t, q, "qa", true)
	w.push(t, r, "ra", "none", "pool")
	w.closeByOtherPath(t, q)
	w.closeByOtherPath(t, r)

	now := time.Now().UnixMilli()
	stale := strconv.FormatInt(now-100_000, 10)
	w.do(t,
		// gx and gz last beat 100 s ago; gz's bench beat carries it below.
		[]any{"HSET", card.CardKey(s, "gx"), "beat_at", stale},
		[]any{"HSET", card.CardKey(s, "gz"), "beat_at", stale},
	)
	if res, err := life.BenchBeat(w.ctx, w.st, life.BenchRequest{
		Bench: ghBench, Session: "beat-3925", Actor: "bench", Live: []string{s + "/gz/1"},
	}); err != nil || !res.Accepted {
		t.Fatalf("bench beat = %+v, %v", res, err)
	}
	if at, _ := strconv.ParseInt(hashOf(t, w.ctx, w.c, s, "gz")["beat_at"], 10, 64); at < now {
		t.Fatalf("gz beat_at %d after its bench beat, want >= %d: the bench beat carries the card's lease beat", at, now)
	}
	if h := hashOf(t, w.ctx, w.c, s, "gx")["beat_at"]; h != stale {
		t.Fatalf("gx beat_at %s changed without a beat", h)
	}

	// Fenced: another token reaps nothing. The one lease ledger is the
	// bench's cards:working (#3998): gx, gy, gz and quack's qa.
	working := card.BenchCardsKey(ghBench, "working")
	if _, err := reconcile.ReapLeases(w.ctx, w.c, "not-the-token", reconcile.LeaseStale); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("reap with a stale token = %v, want ErrFenced", err)
	}
	if n := w.c.ZCard(w.ctx, working).Val(); n != 4 {
		t.Fatalf("working after a fenced reap = %d, want 4", n)
	}

	// The reap frees the closed sprint's lease by retiring its card through
	// the move; a live card's beat is the sweep's, never a dropped lease.
	expire := &reconcile.Expire{Client: w.c}
	c, err := expire.Run(w.ctx, w.lease)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if c.Reaped != 1 {
		t.Fatalf("expire reaped %d, want 1 (quack's qa); %s", c.Reaped, c.Line())
	}
	if zHas(t, w.ctx, w.c, working, card.CardKey(q, "qa")) {
		t.Fatal("quack's qa still holds a lease in the bench's working set")
	}
	for _, l := range []string{"gy", "gz"} {
		if !zHas(t, w.ctx, w.c, working, card.CardKey(s, l)) {
			t.Fatalf("%s lost its lease: its beat is live", l)
		}
	}
	if h := hashOf(t, w.ctx, w.c, q, "qa"); h["where"] != "done" || h["where_ok"] != "fail" || h["state"] != "superseded" {
		t.Fatalf("quack's qa after reap = %s/%s %s, want retired done/fail superseded", h["where"], h["where_ok"], h["state"])
	}
	if got := w.c.XRevRangeN(w.ctx, "cap:log", "+", "-", 1).Val(); len(got) != 1 || got[0].Values["reason"] != "lease-reap" || got[0].Values["slots"] != "1" {
		t.Fatalf("cap:log last = %v, want one slot-freed lease-reap slots=1", got)
	}

	// Ghosts the reaper does not touch: a closed sprint's done card and a
	// card of a sprint nothing lists, both in the bench's ready set, and the
	// done card again in the stream's ready set.
	qa := card.CardKey(q, "qa")
	w.do(t,
		[]any{"ZADD", card.BenchCardsKey(ghBench, "ready"), 1, qa},
		[]any{"ZADD", card.BenchCardsKey(ghBench, "ready"), 2, "s:gone-0924:card:ghost-1"},
		[]any{"ZADD", "ws:" + ghStream + ":ready", 1, qa},
	)
	duty := &reconcile.Fsck{Client: w.c, Every: time.Nanosecond}
	c, err = duty.Run(w.ctx, w.lease)
	if err != nil {
		t.Fatalf("fsck duty: %v", err)
	}
	if c.Repaired != 4 || duty.Last.Fixed != 3 || duty.Last.Retired != 1 {
		t.Fatalf("fsck duty repaired %d (%s), want 4: three ghosts and gate's ra retired", c.Repaired, duty.Last.Line())
	}
	if duty.Last.Sprints < 4 {
		t.Fatalf("fsck duty walked %d sprints, want every one (live, quack, gate and gone): %s", duty.Last.Sprints, duty.Last.Line())
	}
	proc := w.c.HGetAll(w.ctx, reconcile.ProcKey).Val()
	if !strings.HasPrefix(proc["finding"], "fsck fixed=3 retired=1") || proc["finding_at"] == "" || proc["fsck_at"] == "" {
		t.Fatalf("proc:reconciler finding=%q finding_at=%q fsck_at=%q, want the repair as a finding", proc["finding"], proc["finding_at"], proc["fsck_at"])
	}
	for _, sp := range []string{q, r} {
		if left := membersOf(t, w, sp); len(left) > 0 {
			t.Fatalf("closed %s still in live sets:\n%s", sp, strings.Join(left, "\n"))
		}
	}
	if zHas(t, w.ctx, w.c, card.BenchCardsKey(ghBench, "ready"), "s:gone-0924:card:ghost-1") {
		t.Fatal("the unlisted sprint's ghost is still in the bench's ready set")
	}
	for _, sp := range []string{s, q, r} {
		fsckClean(t, w, sp, "after the fsck duty")
	}
	hostEqualsRecords(t, w, s, q, r)

	// The next pass finds nothing: no DUTY line (Counts zero), and the
	// finding stays for a reader.
	c, err = duty.Run(w.ctx, w.lease)
	if err != nil || !c.Zero() {
		t.Fatalf("second fsck pass = %s, %v; want nothing fixed", c.Line(), err)
	}
	if got := w.c.HGet(w.ctx, reconcile.ProcKey, "finding").Val(); got != proc["finding"] {
		t.Fatalf("finding after a clean pass = %q, want %q kept", got, proc["finding"])
	}
}

// hostEqualsRecords: every host table cell (bench and _pool, each place and
// ok/fail) is the count of records that name it, over every sprint.
func hostEqualsRecords(t *testing.T, w *ghWorld, sprints ...string) {
	t.Helper()
	want := map[string]int64{}
	for _, sp := range sprints {
		for _, id := range w.c.ZRange(w.ctx, card.RosterKey(sp), 0, -1).Val() {
			h := w.c.HMGet(w.ctx, id, "bench", "where", "where_ok").Val()
			b, _ := h[0].(string)
			where, _ := h[1].(string)
			ok, _ := h[2].(string)
			if b == "" {
				b = "_pool"
			}
			if where == "" {
				continue
			}
			want[b+"/"+where]++
			if where == "done" {
				want[b+"/"+ok]++
			}
		}
	}
	for _, b := range []string{ghBench, "_pool"} {
		for _, p := range append(append([]string{}, card.Places...), "ok", "fail") {
			if got := w.c.ZCard(w.ctx, card.BenchCardsKey(b, p)).Val(); got != want[b+"/"+p] {
				t.Fatalf("host cell %s/%s ZCARD %d, records %d", b, p, got, want[b+"/"+p])
			}
		}
	}
}

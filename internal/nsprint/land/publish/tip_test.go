package publish_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/publish"
)

// The tip-gate control of #3139 rev 7 section 11 (B11, 8.4), against a
// throwaway redis-server and the local bare remote. The test plays the gate
// worker (claim, the worker's own train or revert construction, a receipt);
// the tip tick, the freeze and the publisher are the real ones.

// planOn puts one batch on the chain on tip.
func (f *fx) planOn(batch, tip string, members ...string) string {
	f.t.Helper()
	token, _, err := land.CallBatchPlan(f.ctx, f.rdb, f.sprint, f.repo, f.base, batch,
		f.lease("pub-a"), strings.Join(members, ","), "", "go", tip, "in-"+batch)
	if err != nil {
		f.t.Fatalf("plan %s on %s: %v", batch, tip, err)
	}
	return token
}

// tick runs one ns_tip_tick and returns its lines.
func (f *fx) tick(w *land.TipWatch) []string {
	f.t.Helper()
	rep, err := w.Tick(f.ctx)
	if err != nil || rep.Refused != "" || rep.NoTip {
		f.t.Fatalf("tip tick: %+v %v", rep, err)
	}
	return rep.Lines
}

func hasLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// claim claims a queued batch's current attempt as bench-1/slot-1.
func (f *fx) claim(batch string) (int, string) {
	f.t.Helper()
	v := f.rdb.HMGet(f.ctx, land.BatchKey(f.repo, f.base, batch), "attempt", "token", "state").Val()
	if fmt.Sprint(v[2]) != "queued" {
		f.t.Fatalf("batch %s is %v, want queued", batch, v[2])
	}
	attempt, _ := strconv.Atoi(fmt.Sprint(v[0]))
	token := fmt.Sprint(v[1])
	if r, err := land.CallGateClaim(f.ctx, f.rdb, f.repo, f.base, batch, attempt, token, "bench-1", "slot-1"); err != nil || r != "OK" {
		f.t.Fatalf("claim %s: %s %v", batch, r, err)
	}
	return attempt, token
}

// tipVerdict plays the worker on the tip gate of sha: a full gate of the tip itself.
func (f *fx) tipVerdict(sha, verdict string) {
	f.t.Helper()
	id := land.TipGateID(sha)
	attempt, token := f.claim(id)
	if r, err := land.CallGateReceipt(f.ctx, f.rdb, f.repo, f.base, id, attempt, token, verdict, "bench-1", "w1",
		sha, "", "", "full", "", "", "", "1"); err != nil || r != "OK" {
		f.t.Fatalf("tip receipt %s: %s %v", id, r, err)
	}
}

// TestL21: a red tip receipt freezes publishing within one tick; gating
// continues; the landed tips since the last green one are gated in parallel;
// the first red one names its batch; the revert train of exactly that batch is
// gated and lands through the freeze; each author gets a fix task; a green tip
// thaws and publishing resumes.
func TestL21(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	lease := f.lease("pub-a")
	pub := f.publisher(lease, nil)
	w := &land.TipWatch{Client: f.rdb, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: lease, Every: -1}

	// The starting tip is green.
	if lines := f.tick(w); !hasLine(lines, "TIP GATE "+f.tip[:8]) {
		t.Fatalf("first tick %q: the tip was not gated", lines)
	}
	f.tipVerdict(f.tip, "GREEN")
	if lines := f.tick(w); !hasLine(lines, "TIP GREEN "+f.tip[:8]) {
		t.Fatalf("green tick %q", lines)
	}

	// Three batches land: b1, b2 (the one that turns the base red), b3.
	tips := []string{f.tip}
	var units, heads []string
	for _, b := range []string{"b1", "b2", "b3"} {
		u, h := f.member()
		units, heads = append(units, u), append(heads, h)
		tok := f.planOn(b, tips[len(tips)-1], u+"@"+h)
		tr := f.gate(b, tok)
		if r := f.land(pub); r.Outcome != publish.Landed {
			t.Fatalf("land %s: %+v", b, r)
		}
		tips = append(tips, tr.TrainHead)
	}
	t0, t1, t2, t3 := tips[0], tips[1], tips[2], tips[3]
	_ = t0

	// The tip is gated and comes back red: frozen within one tick.
	if lines := f.tick(w); !hasLine(lines, "TIP GATE "+t3[:8]) {
		t.Fatalf("tick on %s: %q, want its tip gate", t3[:8], lines)
	}
	f.tipVerdict(t3, "RED")
	gid := land.GID("tip", f.base, t3, "req-1", "pol-1", "runner-1")
	if v := f.hget(land.CITipKey(f.repo, f.base, t3, gid), "verdict"); v != "FAIL" {
		t.Fatalf("tip receipt %s verdict %q, want FAIL", t3[:8], v)
	}
	lines := f.tick(w)
	if !hasLine(lines, "FROZEN "+f.base+" tip-red "+t3[:8]) {
		t.Fatalf("red tick %q: not frozen", lines)
	}
	if src := f.hget(land.FreezeKey(f.repo, f.base), "source"); src != "tip" {
		t.Fatalf("freeze source %q, want tip", src)
	}
	// The same tick gates every landed tip since the last green one, all at once.
	for _, tip := range []string{t1, t2} {
		if !hasLine(lines, "TIP GATE "+tip[:8]) {
			t.Fatalf("red tick %q: landed tip %s not gated", lines, tip[:8])
		}
	}

	// Gating continues while frozen; publishing does not.
	u4, h4 := f.member()
	tok4 := f.planOn("b4", t3, u4+"@"+h4)
	f.gate("b4", tok4)
	if st := f.hget(land.BatchKey(f.repo, f.base, "b4"), "state"); st != "green" {
		t.Fatalf("b4 gated while frozen: state %q, want green", st)
	}
	if r := f.land(pub); r.Outcome != publish.Refused || !strings.HasPrefix(r.Line, "REFUSED frozen tip remedy=") || r.Pushes != 0 {
		t.Fatalf("publish while frozen: %+v, want REFUSED frozen with no push", r)
	}
	if got := f.remoteTip(); got != t3 {
		t.Fatalf("remote moved while frozen: %s, want %s", got, t3)
	}

	// The first red tip names its batch: b1's tip is green, b2's red.
	f.tipVerdict(t1, "GREEN")
	f.tipVerdict(t2, "RED")
	lines = f.tick(w)
	rid := land.RevertID("b2")
	if !hasLine(lines, "REVERT b2 in "+rid+" on "+t3[:8]) {
		t.Fatalf("scan tick %q: want the revert of b2", lines)
	}
	if c := f.hget(land.FreezeKey(f.repo, f.base), "culprit"); c != "b2" {
		t.Fatalf("freeze culprit %q, want b2", c)
	}
	chain := f.rdb.ZRange(f.ctx, land.ChainKey(f.repo, f.base), 0, -1).Val()
	if len(chain) != 1 || chain[0] != rid {
		t.Fatalf("chain %v, want only %s (b4 was gated on a red base)", chain, rid)
	}
	if st := f.hget(land.UnitKey(f.sprint, u4), "state"); st != "landable" {
		t.Fatalf("b4's member %s: %q, want landable for a re-plan", u4, st)
	}
	fixes := f.rdb.XRange(f.ctx, "q:emma", "-", "+").Val()
	var fixed []string
	for _, e := range fixes {
		if fmt.Sprint(e.Values["kind"]) == "fix" {
			fixed = append(fixed, fmt.Sprint(e.Values["unit"]))
		}
	}
	if len(fixed) != 1 || fixed[0] != units[1] {
		t.Fatalf("fix tasks for %v, want one for b2's %s", fixed, units[1])
	}
	// A second tick plans nothing more.
	if lines := f.tick(w); hasLine(lines, "REVERT") {
		t.Fatalf("second scan tick %q planned another revert", lines)
	}

	// The revert train is gated like any batch (the worker's construction) and
	// lands through the freeze.
	rb := f.rdb.HMGet(f.ctx, land.BatchKey(f.repo, f.base, rid), "from_tip", "revert_head", "revert_parent", "created_at").Val()
	if fmt.Sprint(rb[0]) != t3 || fmt.Sprint(rb[1]) != t2 || fmt.Sprint(rb[2]) != t1 {
		t.Fatalf("revert batch %v, want from_tip %s revert %s to %s", rb, t3[:8], t2[:8], t1[:8])
	}
	rtr, err := land.BuildRevert(f.ctx, land.RevertParams{GitDir: f.work, FromTip: t3, RevertHead: t2,
		RevertParent: t1, BatchID: rid, CreatedAt: fmt.Sprint(rb[3])})
	if err != nil {
		t.Fatalf("build revert: %v", err)
	}
	attempt, token := f.claim(rid)
	if r, err := land.CallGateReceipt(f.ctx, f.rdb, f.repo, f.base, rid, attempt, token, "GREEN", "bench-1", "w1",
		rtr.TrainHead, rtr.TrainTree, "", "full", "", "", "", "1"); err != nil || r != "OK" {
		t.Fatalf("revert receipt: %s %v", r, err)
	}
	if n := f.rdb.Exists(f.ctx, land.CITipKey(f.repo, f.base, t3, land.GID("tip", f.base, t3, "req-1", "pol-1", "runner-1"))).Val(); n != 1 {
		t.Fatalf("the red tip receipt is gone")
	}
	if r := f.land(pub); r.Outcome != publish.Landed {
		t.Fatalf("revert through the freeze: %+v, want LANDED", r)
	}
	rev := f.remoteTip()
	if rev != rtr.TrainHead {
		t.Fatalf("remote %s, want the revert train %s", rev, rtr.TrainHead)
	}

	// Exactly its batch: the revert's diff is b2's diff reversed, and nothing else.
	if got, want := f.git(f.work, "diff", t3, rev), f.git(f.work, "diff", t2, t1); got != want || got == "" {
		t.Fatalf("revert diff\n%s\nwant b2 reversed\n%s", got, want)
	}
	if got := f.git(f.work, "diff", "--name-status", t3, rev); got != "D\tm2.txt" {
		t.Fatalf("revert touched %q, want only b2's m2.txt removed", got)
	}
	for _, p := range []string{"m1.txt", "m3.txt", "base.txt"} {
		f.git(f.work, "cat-file", "-e", rev+":"+p)
	}

	// Still frozen until the new tip is green; a green tip thaws.
	if lines := f.tick(w); !hasLine(lines, "TIP GATE "+rev[:8]) {
		t.Fatalf("tick after the revert %q: the new tip was not gated", lines)
	}
	f.tipVerdict(rev, "GREEN")
	if lines := f.tick(w); !hasLine(lines, "THAW "+f.base+" green tip "+rev[:8]) {
		t.Fatalf("green tick %q: not thawed", lines)
	}
	if n := f.rdb.Exists(f.ctx, land.FreezeKey(f.repo, f.base)).Val(); n != 0 {
		t.Fatalf("freeze still set after a green tip")
	}

	// Publishing resumes on the new base.
	tok5 := f.planOn("b5", rev, u4+"@"+h4)
	tr5 := f.gate("b5", tok5)
	if r := f.land(pub); r.Outcome != publish.Landed || f.remoteTip() != tr5.TrainHead {
		t.Fatalf("publish after thaw: %+v", r)
	}
}

// TestL21Hand: land freeze by hand stops publishing, a green tip does not thaw
// it, land thaw does; a hand thaw of a red tip holds until the next tip verdict.
func TestL21Hand(t *testing.T) {
	t.Parallel()
	f := newFx(t)
	lease := f.lease("pub-a")
	pub := f.publisher(lease, nil)
	w := &land.TipWatch{Client: f.rdb, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: lease, Every: -1}

	if st, _, err := land.CallFreeze(f.ctx, f.rdb, f.repo, f.base, "release window", "glenn"); err != nil || st != "OK" {
		t.Fatalf("freeze: %s %v", st, err)
	}
	if st, reason, _ := land.CallFreeze(f.ctx, f.rdb, f.repo, f.base, "again", "glenn"); st != "ALREADY" || reason != "release window" {
		t.Fatalf("second freeze: %s %q, want ALREADY with the standing reason", st, reason)
	}
	u, h := f.member()
	tok := f.planOn("b1", f.tip, u+"@"+h)
	tr := f.gate("b1", tok)
	if r := f.land(pub); r.Outcome != publish.Refused || !strings.HasPrefix(r.Line, "REFUSED frozen hand") {
		t.Fatalf("publish under a hand freeze: %+v", r)
	}
	f.tick(w)
	f.tipVerdict(f.tip, "GREEN")
	if lines := f.tick(w); hasLine(lines, "THAW") {
		t.Fatalf("a green tip thawed a hand freeze: %q", lines)
	}
	if st, src, err := land.CallThaw(f.ctx, f.rdb, f.repo, f.base, "glenn"); err != nil || st != "OK" || src != "hand" {
		t.Fatalf("thaw: %s %s %v", st, src, err)
	}
	if st, _, _ := land.CallThaw(f.ctx, f.rdb, f.repo, f.base, "glenn"); st != "NOTFROZEN" {
		t.Fatalf("second thaw: %s, want NOTFROZEN", st)
	}
	if r := f.land(pub); r.Outcome != publish.Landed || f.remoteTip() != tr.TrainHead {
		t.Fatalf("publish after the hand thaw: %+v", r)
	}

	// A red tip freezes; a hand thaw holds until the tip is gated again.
	f.tick(w)
	f.tipVerdict(tr.TrainHead, "RED")
	if lines := f.tick(w); !hasLine(lines, "FROZEN") {
		t.Fatalf("red tick %q", lines)
	}
	if st, _, _ := land.CallThaw(f.ctx, f.rdb, f.repo, f.base, "glenn"); st != "OK" {
		t.Fatalf("hand thaw of a red tip: %s", st)
	}
	if lines := f.tick(w); hasLine(lines, "FROZEN") {
		t.Fatalf("the same red verdict froze again after a hand thaw: %q", lines)
	}

	// The fence: a tick without the lease writes nothing.
	stale := &land.TipWatch{Client: f.rdb, Sprint: f.sprint, Repo: f.repo, Base: f.base, Lease: f.lease("other"), Every: -1}
	if rep, err := stale.Tick(f.ctx); err != nil || rep.Refused != "lease mismatch" {
		t.Fatalf("stale tick: %+v %v, want REFUSED lease mismatch", rep, err)
	}
}

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The worker and fleet verbs' tests (item IT20) drive the composed twin
// (sprintfn.Twin over tset.Mem, with X, the derivation, J, the parts and the
// queries of the stack) through Env and a counting client, as the store's
// binding will be driven after G0. The helpers here are this item's own
// (named wf...), beside the driver's.

const wfPrefix = "wf:"

var wfNames = sprint.Names{Prefix: wfPrefix}

// wfColumns are the set columns of the four tables, as the write path's own
// tests define them.
var wfColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// wf is one sprint on the twin: the four tables defined, initialised and
// running, a clock the test moves, and an Env over a counting client.
type wf struct {
	t   *testing.T
	tw  *sprintfn.Twin
	log *sprintfn.LogStub
	cc  *Counting
	env *Env
	mu  sync.Mutex
	now time.Time
}

func newWF(t *testing.T) *wf {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(wfPrefix, table, tset.TableDefinition{Columns: wfColumns[table],
			MemberPrefix: wfNames.TSetMemberPrefix(table), EpochKey: wfNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	w := &wf{t: t, log: sprintfn.NewLogStub(), now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	w.tw = sprintfn.NewTwin(m, w.log, wfNames)
	w.tw.UseQueries()
	w.tw.UseIntents()
	w.tw.SetClock(func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.now
	})
	w.cc = &Counting{C: w.tw}
	w.env = &Env{C: w.cc, Names: wfNames, Actor: "coord"}
	ctx := context.Background()
	cfg := config.NewMem()
	if _, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: "coord", Fields: map[string]string{"slots": "1", "tiers": "pro"}}, "test"); err != nil {
		t.Fatalf("config friend: %v", err)
	}
	if _, _, err := cfg.Update(ctx, config.KindSprint, config.KindSprint, map[string]string{"coordinator": "coord"}, "test"); err != nil {
		t.Fatalf("config coordinator: %v", err)
	}
	if _, err := Init(ctx, w.env, InitReq{Config: cfg}); err != nil {
		t.Fatalf("init: %v", err)
	}
	w.advance(time.Second)
	if _, err := Start(ctx, w.env, ClockReq{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	w.advance(time.Second)
	// The score counter (U2): every score the tests place lies below it.
	counter := &sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "test"},
		Sprint: &sprintfn.SprintPart{Counter: &sprintfn.CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "1000000"}}}}
	if res, err := sprintfn.Step(ctx, w.tw, counter); err != nil || res.Refusal != nil || res.Err != nil {
		t.Fatalf("counter: %v %v %v", err, res.Refusal, res.Err)
	}
	w.cc.Reset()
	return w
}

// advance moves the store's clock on.
func (w *wf) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// r is the running time R now, as the store computes it.
func (w *wf) r() int64 {
	w.t.Helper()
	res, _, err := w.tw.KeyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock})
	if err != nil {
		w.t.Fatalf("clock: %v", err)
	}
	n, err := strconv.ParseInt(res.(sprintfn.ClockResult).R, 10, 64)
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// raw sends one step to the twin outside the verbs (the test's own seeding),
// and fails the test on a refusal.
func (w *wf) raw(entries ...tset.Entry) {
	w.t.Helper()
	req := &sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "test"}, Body: sprintfn.Body{Entries: entries}}
	res, err := sprintfn.Step(context.Background(), w.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("seed step: err %v, refusal %v, result err %v", err, res.Refusal, res.Err)
	}
}

// member seeds a fleet member: its row and its control card at a status.
func (w *wf) member(m, status string) {
	w.t.Helper()
	id := sprint.CtlID(m)
	w.raw(tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: []string{m}},
		tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":ctl", IDs: []string{id}, Scores: []string{"0"}, About: []string{id},
			Set: map[string]string{"kind": "member", "status": status}})
}

// reader seeds a reader's row.
func (w *wf) reader(r string) {
	w.t.Helper()
	w.raw(tset.Entry{Kind: "rows", Table: sprint.Readers, Add: []string{r}})
}

// dealt seeds primaries of a stream working, each with its attempt-1 work card
// dealt to member m's ready cell at generation gen, its untaken clock started
// at R. Primaries and work cards go in steps of at most 2,000 changed members.
func (w *wf) dealt(m, stream string, gen int, primaries ...string) {
	w.t.Helper()
	w.raw(tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{stream}})
	r := w.r()
	for lo := 0; lo < len(primaries); lo += 2000 {
		ps := primaries[lo:min(lo+2000, len(primaries))]
		pe := tset.Entry{Kind: "create", Table: sprint.Work, To: stream + ":working", Set: map[string]string{"kind": "work", "attempt": "1"}}
		fe := tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":ready",
			Set: map[string]string{"kind": "work", "stream": stream, "attempt": "1", "gen": strconv.Itoa(gen), "member": m,
				"untaken_r": wkMsOf(r), "due_untaken": wkMsOf(r + 15*60*1000)}}
		for i, p := range ps {
			score := strconv.Itoa(lo + i + 1)
			pe.IDs, pe.Scores, pe.About = append(pe.IDs, p), append(pe.Scores, score), append(pe.About, p)
			pe.Each = append(pe.Each, map[string]string{"work": sprint.WorkCardID(p, 1)})
			fe.IDs, fe.Scores, fe.About = append(fe.IDs, sprint.WorkCardID(p, 1)), append(fe.Scores, score), append(fe.About, p)
			fe.Each = append(fe.Each, map[string]string{"primary": p})
		}
		w.raw(pe)
		w.raw(fe)
	}
}

// finished seeds a member's finished cells: ok cards in m:ok and failed ones
// in m:failed, of primaries that are not on the table (only the counts matter).
func (w *wf) finished(m string, ok, failed int) {
	w.t.Helper()
	for col, n := range map[string]int{"ok": ok, "failed": failed} {
		if n == 0 {
			continue
		}
		e := tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":" + col, Set: map[string]string{"kind": "work"}}
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("old-%s-%s-%d", m, col, i)
			e.IDs, e.Scores, e.About = append(e.IDs, sprint.WorkCardID(p, 1)), append(e.Scores, strconv.Itoa(i+1)), append(e.About, p)
		}
		w.raw(e)
	}
}

// rec is one record of a table as the store holds it.
func (w *wf) rec(table, id string) tset.MemberRecord {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, &sprintfn.ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "ids", Table: table, IDs: []string{id}}}})
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("read %s %s: %v %v %v", table, id, err, res.Refusal, res.Err)
	}
	return res.Read.Tset[0].Records[0]
}

// at says a record is placed at row:col.
func (w *wf) at(table, id, cell string) tset.MemberRecord {
	w.t.Helper()
	r := w.rec(table, id)
	if got := wkPlaceOf(r); got != cell {
		w.t.Fatalf("%s %s is at %s, want %s", table, id, got, cell)
	}
	return r
}

// zscore is a member's score in a sprint sorted set of epoch 0, and whether
// it is there.
func (w *wf) zscore(name, member string) (float64, bool) {
	kv, ok := w.tw.SprintKeys()[wfNames.Key(name)+"@0"]
	if !ok {
		return 0, false
	}
	s, ok := kv.ZSet[member]
	return s, ok
}

// sprintKey is one of the sprint's own keys without an epoch.
func (w *wf) sprintKey(name string) (sprintfn.KeyValue, bool) {
	kv, ok := w.tw.SprintKeys()[wfNames.Key(name)]
	return kv, ok
}

// wfNote is a note line as the log holds it.
type wfNote struct {
	Op, Type, Text, Kind string
	About                []string
}

// notes are the note lines of epoch 0 of a type, in order.
func (w *wf) notes(typ string) []wfNote {
	w.t.Helper()
	var out []wfNote
	for _, raw := range w.log.Lines(wfPrefix, "0") {
		var line struct {
			Kind  string          `json:"kind"`
			About []string        `json:"about"`
			Meta  json.RawMessage `json:"meta"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			w.t.Fatal(err)
		}
		if line.Kind != "note" {
			continue
		}
		var n wfNote
		if err := json.Unmarshal(line.Meta, &n); err != nil {
			w.t.Fatal(err)
		}
		n.About = line.About
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func wfCg(card string, gen int) CardGen { return CardGen{Card: card, Gen: gen} }

// wfRefusedWith says err is a Refused with the code, and its message holds want.
func wfRefusedWith(t *testing.T, err error, code, want string) *Refused {
	t.Helper()
	var rf *Refused
	if !errors.As(err, &rf) || rf.Code() != code {
		t.Fatalf("err %v, want a refusal %s", err, code)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal %q does not say %q", err.Error(), want)
	}
	return rf
}

// TestParseCardGen: <card>@<gen> and <card> read; anything else is refused.
func TestParseCardGen(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]CardGen{"p1.w1@3": {"p1.w1", 3}, "p1.r1.r2": {"p1.r1.r2", 0}} {
		got, err := ParseCardGen(in)
		if err != nil || got != want || got.String() != in {
			t.Fatalf("%q: %+v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "@1", "p1.w1@0", "p1.w1@01", "p1.w1@x", "a b@1"} {
		if _, err := ParseCardGen(in); err == nil {
			t.Fatalf("%q read", in)
		}
	}
}

// TestTakeByGenerationIdempotent: take moves the member's cards ready ->
// working in one step, stamping first_taken_r and due_unfinished (the
// unfinished entry by derivation) and ending the untaken clock (1.2); the same
// take again writes nothing and costs one round trip; a stale generation and
// another member's card are refused, naming the live generation and the
// member; a set of taken and untaken cards takes only the untaken (1.5.5).
func TestTakeByGenerationIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Up)
	w.member("m2", sprint.Up)
	w.dealt("m1", "s1", 1, "p1", "p2", "p3")
	if _, ok := w.zscore("due", "untaken:p1.w1"); !ok {
		t.Fatal("the seeded card has no untaken entry")
	}
	w.cc.Reset()
	r := w.r()
	res, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("p2.w1", 1), wfCg("p1.w1", 1)}})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if res.Step == nil || res.Replay || w.cc.Trips() != 2 || res.Trips != 2 {
		t.Fatalf("step %v replay %v trips %d (result %d); want a step in 2 round trips", res.Step != nil, res.Replay, w.cc.Trips(), res.Trips)
	}
	for _, id := range []string{"p1.w1", "p2.w1"} {
		rec := w.at(sprint.Fleet, id, "m1:working")
		if wkFieldStr(rec, wkfFirstTakenR) != wkMsOf(r) || wkFieldStr(rec, wkfDueUnfinish) != wkMsOf(r+2*3600*1000) {
			t.Fatalf("%s: first_taken_r %q due_unfinished %q, want %d and R + 2 h", id, wkFieldStr(rec, wkfFirstTakenR), wkFieldStr(rec, wkfDueUnfinish), r)
		}
		if _, has := wkFieldOf(rec, wkfUntakenR); has {
			t.Fatalf("%s still has untaken_r", id)
		}
		if _, ok := w.zscore("due", "untaken:"+id); ok {
			t.Fatalf("%s: the untaken entry outlived the take", id)
		}
		if s, ok := w.zscore("due", "unfinished:"+id); !ok || int64(s) != r+2*3600*1000 {
			t.Fatalf("%s: unfinished entry %v %v, want R + 2 h", id, s, ok)
		}
	}
	rev := w.rec(sprint.Fleet, "p1.w1").Revision

	t.Run("the same take again writes nothing", func(t *testing.T) {
		w.cc.Reset()
		res, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("p1.w1", 1), wfCg("p2.w1", 1)}})
		if err != nil || res.Step != nil || !res.Replay || w.cc.Trips() != 1 {
			t.Fatalf("err %v step %v replay %v trips %d; want a replay in one round trip", err, res.Step != nil, res.Replay, w.cc.Trips())
		}
		if got := w.rec(sprint.Fleet, "p1.w1").Revision; got != rev {
			t.Fatalf("revision %s, want %s: a repeat changed the card", got, rev)
		}
		if !strings.Contains(res.Said, "2 already taken") {
			t.Fatalf("said %q", res.Said)
		}
	})
	t.Run("a stale generation is refused, naming the live one", func(t *testing.T) {
		_, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("p3.w1", 2)}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "generation 2 is not the live one (1)")
		w.at(sprint.Fleet, "p3.w1", "m1:ready")
	})
	t.Run("another member's card is refused, and nothing of the set moves", func(t *testing.T) {
		_, err := Take(ctx, w.env, TakeReq{As: "m2", Cards: []CardGen{wfCg("p3.w1", 1)}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "dealt to m1")
		w.at(sprint.Fleet, "p3.w1", "m1:ready")
	})
	t.Run("a set of taken and untaken cards takes the untaken", func(t *testing.T) {
		w.cc.Reset()
		res, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("p1.w1", 1), wfCg("p3.w1", 1)}})
		if err != nil || res.Step == nil || w.cc.Trips() != 2 {
			t.Fatalf("err %v step %v trips %d", err, res.Step != nil, w.cc.Trips())
		}
		w.at(sprint.Fleet, "p3.w1", "m1:working")
		if got := w.rec(sprint.Fleet, "p1.w1").Revision; got != rev {
			t.Fatal("the card already taken was written again")
		}
		if !strings.Contains(res.Said, "took 1") || !strings.Contains(res.Said, "1 already taken") {
			t.Fatalf("said %q", res.Said)
		}
	})
	t.Run("a card named without a generation is refused", func(t *testing.T) {
		_, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{{Card: "p1.w1"}}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "names no generation")
	})
}

// wfTakeAndFinishSetup is m1 up with p1..pn of s1 dealt to it and taken.
func wfTakeAndFinishSetup(t *testing.T, n int) (*wf, []CardGen) {
	t.Helper()
	w := newWF(t)
	w.member("m1", sprint.Up)
	var ps []string
	var cards []CardGen
	for i := 1; i <= n; i++ {
		p := "p" + strconv.Itoa(i)
		ps = append(ps, p)
		cards = append(cards, wfCg(sprint.WorkCardID(p, 1), 1))
	}
	w.dealt("m1", "s1", 1, ps...)
	if _, err := Take(context.Background(), w.env, TakeReq{As: "m1", Cards: cards}); err != nil {
		t.Fatalf("take: %v", err)
	}
	w.cc.Reset()
	return w, cards
}

// TestFinishSameGenOtherFactsRefused: finish moves the work card working -> ok
// and its primary working -> review with the result and the head, in one step
// of two round trips, with KNOW "work came back ok"; the same finish again
// writes nothing; the same generation with another head or result is refused,
// naming what was applied ("already finished at gen 1 with head abc"), and
// nothing of its set is written (1.5.5).
func TestFinishSameGenOtherFactsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, cards := wfTakeAndFinishSetup(t, 2)
	res, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[:1], Head: "abc", Report: "done"})
	if err != nil || res.Step == nil || w.cc.Trips() != 2 {
		t.Fatalf("finish: err %v step %v trips %d", err, res.Step != nil, w.cc.Trips())
	}
	wc := w.at(sprint.Fleet, "p1.w1", "m1:ok")
	pr := w.at(sprint.Work, "p1", "s1:review")
	if wkFieldStr(wc, wkfHead) != "abc" || wkFieldStr(wc, wkfResult) != "ok" || wkFieldStr(pr, wkfHead) != "abc" || wkFieldStr(pr, wkfResult) != "ok" {
		t.Fatalf("card head %q result %q; primary head %q result %q", wkFieldStr(wc, wkfHead), wkFieldStr(wc, wkfResult), wkFieldStr(pr, wkfHead), wkFieldStr(pr, wkfResult))
	}
	if _, ok := w.zscore("due", "unfinished:p1.w1"); ok {
		t.Fatal("the unfinished entry outlived the finish")
	}
	if ns := w.notes(wkNoticeWorkOK); len(ns) != 1 || len(ns[0].About) != 1 || ns[0].About[0] != "p1" {
		t.Fatalf("work came back ok notes %+v, want one naming p1", ns)
	}
	rev := pr.Revision

	t.Run("the same finish again writes nothing", func(t *testing.T) {
		w.cc.Reset()
		res, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[:1], Head: "abc", Report: "done"})
		if err != nil || res.Step != nil || !res.Replay || w.cc.Trips() != 1 {
			t.Fatalf("err %v step %v replay %v trips %d", err, res.Step != nil, res.Replay, w.cc.Trips())
		}
		if w.rec(sprint.Work, "p1").Revision != rev || len(w.notes(wkNoticeWorkOK)) != 1 {
			t.Fatal("a repeat wrote")
		}
	})
	t.Run("the same generation with another head is refused", func(t *testing.T) {
		_, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards, Head: "def"})
		wfRefusedWith(t, err, "OPCONFLICT", "already finished at gen 1 with head abc (ok)")
		w.at(sprint.Fleet, "p2.w1", "m1:working") // nothing of the set was written
	})
	t.Run("the same generation with another result is refused", func(t *testing.T) {
		_, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[:1], Head: "abc", Failed: true})
		wfRefusedWith(t, err, "OPCONFLICT", "already finished at gen 1 with head abc (ok)")
	})
	t.Run("a card not taken is refused", func(t *testing.T) {
		w.dealt("m1", "s2", 1, "q1")
		_, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: []CardGen{wfCg("q1.w1", 1)}, Head: "h"})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "not working: take it first")
	})
	t.Run("a failed finish moves to failed with no ok notice", func(t *testing.T) {
		if _, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[1:], Head: "h2", Failed: true, Report: "broke"}); err != nil {
			t.Fatal(err)
		}
		wc := w.at(sprint.Fleet, "p2.w1", "m1:failed")
		if wkFieldStr(wc, wkfReport) != "broke" || wkFieldStr(w.at(sprint.Work, "p2", "s1:review"), wkfResult) != "failed" {
			t.Fatal("the failed finish lost its report or result")
		}
		if len(w.notes(wkNoticeWorkOK)) != 1 {
			t.Fatal("a failed finish said work came back ok")
		}
	})
}

// TestFinishOkRateNoticeOnCrossing: KNOW "a member's ok rate fell below
// OkRateFloor" is said by the finish that takes the rate across the floor,
// once: not below the minimum of reports, not again while it stays below, and
// again after it rises above and falls once more (2.5).
func TestFinishOkRateNoticeOnCrossing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, cards := wfTakeAndFinishSetup(t, 6)
	w.finished("m1", 4, 4) // 8 reports, 50%: fewer than RateMinReports
	finish := func(i int, failed bool) {
		t.Helper()
		if _, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[i : i+1], Head: "h", Failed: failed}); err != nil {
			t.Fatalf("finish %d: %v", i, err)
		}
	}
	count := func() int { return len(w.notes(wkNoticeOkRate)) }
	finish(0, true) // 4 of 9: below 50%, but 9 reports are fewer than 10
	if count() != 0 {
		t.Fatal("said below the minimum of reports")
	}
	finish(1, false) // 5 of 10: 50%, not below
	if count() != 0 {
		t.Fatal("said at the floor")
	}
	finish(2, true) // 5 of 11: below, a crossing
	if n := w.notes(wkNoticeOkRate); len(n) != 1 || len(n[0].About) != 1 || n[0].About[0] != "m1" || !strings.Contains(n[0].Text, "5 ok of 11") {
		t.Fatalf("notes %+v, want one on m1 saying 5 ok of 11", n)
	}
	finish(3, true) // 5 of 12: still below, no crossing
	if count() != 1 {
		t.Fatal("said again while it stayed below")
	}
	finish(4, false) // 6 of 13: still below
	finish(5, false) // 7 of 14: 50%, back at the floor
	if count() != 1 {
		t.Fatal("said while not crossing")
	}
	// Rising above the floor and falling again is a second crossing.
	w.dealt("m1", "s2", 1, "q1")
	if _, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("q1.w1", 1)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: []CardGen{wfCg("q1.w1", 1)}, Head: "h", Failed: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatalf("%d notices, want a second crossing said", count())
	}
}

// wfReadSetup is p1, p2 of s1 in review, each asked of readers r1 and r2 at
// attempt 1 (as R8 asks), with the readers' rows.
func wfReadSetup(t *testing.T, primaries ...string) *wf {
	t.Helper()
	w := newWF(t)
	w.reader("r1")
	w.reader("r2")
	w.raw(tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}})
	pe := tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:review", Set: map[string]string{"kind": "work", "attempt": "1", "head": "h1"}}
	r := w.r()
	for i, p := range primaries {
		pe.IDs, pe.Scores, pe.About = append(pe.IDs, p), append(pe.Scores, strconv.Itoa(i+1)), append(pe.About, p)
	}
	w.raw(pe)
	for _, rd := range []string{"r1", "r2"} {
		e := tset.Entry{Kind: "create", Table: sprint.Readers, To: rd + ":asked",
			Set: map[string]string{"kind": "read", "stream": "s1", "reader": rd, "attempt": "1", "head": "h1",
				"asked_r": wkMsOf(r), "due_unbegun": wkMsOf(r + 30*60*1000)}}
		for i, p := range primaries {
			e.IDs, e.Scores, e.About = append(e.IDs, sprint.ReadCardID(p, 1, rd)), append(e.Scores, strconv.Itoa(i+1)), append(e.About, p)
			e.Each = append(e.Each, map[string]string{"primary": p})
		}
		w.raw(e)
	}
	w.cc.Reset()
	return w
}

// TestReadBeginAndReport: read --begin moves asked -> reading with begun_r and
// the unreported due entry, the unbegun one ended; --ok reports with the
// verdict and summary; a report on a card never begun is the begin and the
// report in one step; a repeat writes nothing; a report with other facts is
// refused; another reader's card and a primary not in review are refused.
func TestReadBeginAndReport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := wfReadSetup(t, "p1", "p2")
	r := w.r()
	c1, c2 := sprint.ReadCardID("p1", 1, "r1"), sprint.ReadCardID("p2", 1, "r1")
	res, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Begin: true, Cards: []CardGen{{Card: c1}}})
	if err != nil || res.Step == nil || w.cc.Trips() != 2 {
		t.Fatalf("begin: err %v step %v trips %d", err, res.Step != nil, w.cc.Trips())
	}
	rec := w.at(sprint.Readers, c1, "r1:reading")
	if wkFieldStr(rec, wkfBegunR) != wkMsOf(r) {
		t.Fatalf("begun_r %q, want %d", wkFieldStr(rec, wkfBegunR), r)
	}
	if _, ok := w.zscore("due", "unbegun:"+c1); ok {
		t.Fatal("the unbegun entry outlived the begin")
	}
	if s, ok := w.zscore("due", "unreported:"+c1); !ok || int64(s) != r+2*3600*1000 {
		t.Fatalf("unreported entry %v %v, want R + 2 h", s, ok)
	}
	t.Run("a begin again writes nothing", func(t *testing.T) {
		w.cc.Reset()
		res, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Begin: true, Cards: []CardGen{{Card: c1, Gen: 1}}})
		if err != nil || res.Step != nil || !res.Replay || w.cc.Trips() != 1 {
			t.Fatalf("err %v step %v replay %v trips %d", err, res.Step != nil, res.Replay, w.cc.Trips())
		}
	})
	w.advance(time.Minute)
	ok := ReadCardReq{As: "r1", Verdict: sprint.OK, Summary: "reads clean", Cards: []CardGen{{Card: c1}, {Card: c2}}}
	w.cc.Reset()
	if res, err := ReadCard(ctx, w.env, ok); err != nil || res.Step == nil || w.cc.Trips() != 2 {
		t.Fatalf("report: err %v trips %d", err, w.cc.Trips())
	}
	for _, c := range []string{c1, c2} {
		rec := w.at(sprint.Readers, c, "r1:ok")
		if wkFieldStr(rec, wkfVerdict) != sprint.OK || wkFieldStr(rec, wkfSummary) != "reads clean" {
			t.Fatalf("%s: verdict %q summary %q", c, wkFieldStr(rec, wkfVerdict), wkFieldStr(rec, wkfSummary))
		}
		for _, k := range []string{"unbegun:", "unreported:"} {
			if _, has := w.zscore("due", k+c); has {
				t.Fatalf("%s: a due entry %s outlived the report", c, k)
			}
		}
	}
	if wkFieldStr(w.rec(sprint.Readers, c2), wkfBegunR) == "" {
		t.Fatal("a report on a card never begun did not stamp its begin")
	}
	t.Run("the same report again writes nothing", func(t *testing.T) {
		w.cc.Reset()
		res, err := ReadCard(ctx, w.env, ok)
		if err != nil || res.Step != nil || !res.Replay || w.cc.Trips() != 1 {
			t.Fatalf("err %v step %v trips %d", err, res.Step != nil, w.cc.Trips())
		}
	})
	t.Run("a report with other facts is refused", func(t *testing.T) {
		_, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Verdict: sprint.Broken, Finding: "x", Cards: []CardGen{{Card: c1}}})
		wfRefusedWith(t, err, "OPCONFLICT", "already reported ok with summary \"reads clean\"")
	})
	t.Run("another reader's card is refused", func(t *testing.T) {
		_, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Begin: true, Cards: []CardGen{{Card: sprint.ReadCardID("p1", 1, "r2")}}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "not r1's to read")
	})
	t.Run("a card whose primary left review is refused", func(t *testing.T) {
		w.raw(tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:review", To: "s1:merging", IDs: []string{"p2"}, About: []string{"p2"}})
		_, err := ReadCard(ctx, w.env, ReadCardReq{As: "r2", Begin: true, Cards: []CardGen{{Card: sprint.ReadCardID("p2", 1, "r2")}}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "its primary p2 is not in review")
	})
	t.Run("--begin with a verdict is refused", func(t *testing.T) {
		_, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Begin: true, Verdict: sprint.OK, Cards: []CardGen{{Card: c1}}})
		wfRefusedWith(t, err, sprintfn.CodeRequest, "not both")
	})
}

// TestReadSummaryNotice: a report says KNOW "a read of p by r: its one-line
// summary", naming the primaries read, with the verdict and the summary; a
// begin says nothing (2.5).
func TestReadSummaryNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := wfReadSetup(t, "p1", "p2")
	cards := []CardGen{{Card: sprint.ReadCardID("p1", 1, "r2")}, {Card: sprint.ReadCardID("p2", 1, "r2")}}
	if _, err := ReadCard(ctx, w.env, ReadCardReq{As: "r2", Begin: true, Cards: cards}); err != nil {
		t.Fatal(err)
	}
	if n := w.notes(wkNoticeReadSummary); len(n) != 0 {
		t.Fatalf("a begin said %+v", n)
	}
	if _, err := ReadCard(ctx, w.env, ReadCardReq{As: "r2", Verdict: sprint.Broken, Summary: "the lock is taken twice", Finding: "take it once", Cards: cards}); err != nil {
		t.Fatal(err)
	}
	n := w.notes(wkNoticeReadSummary)
	if len(n) != 1 || strings.Join(n[0].About, ",") != "p1,p2" || n[0].Text != "read by r2: broken: the lock is taken twice" || n[0].Op != sprintfn.JOpKnow {
		t.Fatalf("notes %+v, want one KNOW naming p1 and p2 with the verdict and summary", n)
	}
	if f := wkFieldStr(w.rec(sprint.Readers, cards[0].Card), wkfFinding); f != "take it once" {
		t.Fatalf("finding %q", f)
	}
}

// TestReadBrokenRateNoticeOnCrossing: KNOW "a reader's broken rate rose above
// BrokenRateCeiling" is said by the report that takes the rate across the
// ceiling, once a crossing (2.5).
func TestReadBrokenRateNoticeOnCrossing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var ps []string
	for i := 1; i <= 16; i++ {
		ps = append(ps, "p"+strconv.Itoa(i))
	}
	w := wfReadSetup(t, ps...)
	report := func(verdict string, from, to int) {
		t.Helper()
		var cards []CardGen
		for i := from; i <= to; i++ {
			cards = append(cards, CardGen{Card: sprint.ReadCardID("p"+strconv.Itoa(i), 1, "r1")})
		}
		if _, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Verdict: verdict, Cards: cards}); err != nil {
			t.Fatalf("report %s %d..%d: %v", verdict, from, to, err)
		}
	}
	count := func() int { return len(w.notes(wkNoticeBrokenRate)) }
	report(sprint.Broken, 1, 6) // 6 of 6: above, but fewer than 10 reads: not judged
	if count() != 0 {
		t.Fatal("said below the minimum of reads")
	}
	report(sprint.OK, 7, 10) // 6 of 10: judged at last, and above: this report is the crossing
	if count() != 1 {
		t.Fatalf("%d notices: the report that makes the rate judged and above is its crossing", count())
	}
	report(sprint.Broken, 11, 11) // 7 of 11: still above, no crossing
	if count() != 1 {
		t.Fatal("said again while it stayed above")
	}
	w = wfReadSetup(t, ps...)
	report(sprint.OK, 1, 5)      // 0 of 5
	report(sprint.Broken, 6, 10) // 5 of 10: at the ceiling, not above
	if count() != 0 {
		t.Fatal("said at the ceiling")
	}
	report(sprint.Broken, 11, 11) // 6 of 11: above, a crossing
	if n := w.notes(wkNoticeBrokenRate); len(n) != 1 || n[0].About[0] != "r1" || !strings.Contains(n[0].Text, "6 broken of 11") {
		t.Fatalf("notes %+v", n)
	}
	report(sprint.Broken, 12, 12) // still above
	report(sprint.OK, 13, 16)     // 7 of 16: below again
	if count() != 1 {
		t.Fatal("said again without a crossing")
	}
}

// TestWorkerVerbsTakeSets: a worker verb over 2,000 cards is one step, two
// round trips, one flush a trip (1.5.3: worker verbs take sets); a finish,
// which changes two members a card, is one step of 1,000 and two of 2,000
// (1.0's chunk), and a read of 2,000 is one step.
func TestWorkerVerbsTakeSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Up)
	var ps []string
	var cards []CardGen
	for i := 1; i <= 2000; i++ {
		p := fmt.Sprintf("p%04d", i)
		ps = append(ps, p)
		cards = append(cards, wfCg(sprint.WorkCardID(p, 1), 1))
	}
	w.dealt("m1", "s1", 1, ps...)
	w.cc.Reset()
	res, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: cards})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if w.cc.Trips() != 2 || w.cc.Steps() != 1 || res.Trips != 2 {
		t.Fatalf("2,000 takes: %d round trips, %d steps (result %d trips); want 2 and 1", w.cc.Trips(), w.cc.Steps(), res.Trips)
	}
	if got := res.Step.Reply.Changed; got != 2000 {
		t.Fatalf("the step changed %d members, want 2,000", got)
	}
	w.at(sprint.Fleet, cards[1999].Card, "m1:working")

	w.cc.Reset()
	if _, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards[:FinishMax], Head: "h"}); err != nil {
		t.Fatalf("finish 1,000: %v", err)
	}
	if w.cc.Trips() != 2 || w.cc.Steps() != 1 {
		t.Fatalf("1,000 finishes: %d round trips, %d steps; want 2 and 1", w.cc.Trips(), w.cc.Steps())
	}
	w.cc.Reset()
	res, err = Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards, Head: "h"})
	if err != nil {
		t.Fatalf("finish 2,000: %v", err)
	}
	// The first 1,000 are finished already: their step writes nothing, and the
	// second 1,000 are one step. Two steps' reads, one step sent.
	if w.cc.Trips() != 3 || w.cc.Steps() != 1 || res.Replay {
		t.Fatalf("2,000 finishes of which 1,000 applied: %d round trips, %d steps", w.cc.Trips(), w.cc.Steps())
	}
	w.at(sprint.Work, ps[1999], "s1:review")

	t.Run("a finish of 2,000 fresh cards is two steps, four round trips", func(t *testing.T) {
		t.Parallel()
		w, cards := wfTakeAndFinishSetup(t, 2000)
		if _, err := Finish(ctx, w.env, FinishReq{As: "m1", Cards: cards, Head: "h"}); err != nil {
			t.Fatal(err)
		}
		if w.cc.Trips() != 4 || w.cc.Steps() != 2 {
			t.Fatalf("%d round trips, %d steps; want 4 and 2", w.cc.Trips(), w.cc.Steps())
		}
	})
	t.Run("a read of 2,000 is one step, two round trips", func(t *testing.T) {
		t.Parallel()
		var ps []string
		for i := 1; i <= 2000; i++ {
			ps = append(ps, fmt.Sprintf("p%04d", i))
		}
		w := wfReadSetup(t, ps...)
		var cards []CardGen
		for _, p := range ps {
			cards = append(cards, CardGen{Card: sprint.ReadCardID(p, 1, "r1")})
		}
		if _, err := ReadCard(ctx, w.env, ReadCardReq{As: "r1", Verdict: sprint.OK, Cards: cards}); err != nil {
			t.Fatal(err)
		}
		if w.cc.Trips() != 2 || w.cc.Steps() != 1 {
			t.Fatalf("%d round trips, %d steps; want 2 and 1", w.cc.Trips(), w.cc.Steps())
		}
	})
}

// TestQueuePages: queue --as m is the member's ready then working cells, one
// round trip a page, with the cursor of the next page; queue --stream s is the
// stream's open cells; a member with no row is refused NOROW, named.
func TestQueuePages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Up)
	w.dealt("m1", "s1", 1, "p1", "p2", "p3", "p4", "p5")
	if _, err := Take(ctx, w.env, TakeReq{As: "m1", Cards: []CardGen{wfCg("p1.w1", 1), wfCg("p2.w1", 1)}}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()
	var got []string
	after := ""
	pages := 0
	for {
		page, _, err := QueueRead(ctx, w.env, QueueReq{As: "m1", Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, c := range page.Cards {
			got = append(got, c.Cell+" "+c.ID+" "+c.Fields[wkfPrimary])
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	want := "m1:ready p3.w1 p3,m1:ready p4.w1 p4,m1:ready p5.w1 p5,m1:working p1.w1 p1,m1:working p2.w1 p2"
	if strings.Join(got, ",") != want || pages != 3 || w.cc.Trips() != 3 {
		t.Fatalf("queue %q in %d pages, %d round trips; want %q in 3 pages, one trip each", strings.Join(got, ","), pages, w.cc.Trips(), want)
	}
	res, err := Queue(ctx, w.env, QueueReq{Stream: "s1"})
	if err != nil || !strings.Contains(res.Said, "s1:working p1") || strings.Contains(res.Said, "more:") {
		t.Fatalf("stream queue: %v\n%s", err, res.Said)
	}
	_, err = Queue(ctx, w.env, QueueReq{As: "ghost"})
	if rf := wfRefusedWith(t, err, "NOROW", ""); !strings.Contains(rf.Hint, "ghost has no row") {
		t.Fatalf("hint %q", rf.Hint)
	}
}

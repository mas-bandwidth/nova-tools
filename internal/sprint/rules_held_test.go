package sprint

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// R16 and the local holder of a card (the upper design, version 2.1, 2.3 R16),
// on snapshots built by hand: one state for every row of the table and every
// holder it lists, the backlog, the state-implied holder, the seeded stalls,
// and a second run of the rule on the state its first run left.

// hworld is a snapshot with one stream, two members up and two readers, and the
// sprint's own keys (HeldFacts) beside it, at a running time of hR.
type hworld struct {
	s   *Snapshot
	f   HeldFacts
	now Now
}

const hR = int64(1_000_000)

func newHWorld() *hworld {
	s := &Snapshot{Now: time.Unix(1_800_000_000, 0), Work: NewTable(Work), Readers: NewTable(Readers),
		Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	w := &hworld{s: s, now: Now{R: hR, Wall: hR, Running: true},
		f: HeldFacts{Dropping: map[string]string{}, Cut: map[string]int64{}, Quarantined: map[string]bool{},
			Lines: map[uint64]HeldLine{}}}
	w.stream("s1", StreamWaiting)
	w.member("m1", Up)
	w.member("m2", Up)
	w.reader("r1")
	w.reader("r2")
	return w
}

// hkv is a field map from name and value pairs.
func hkv(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func (w *hworld) stream(name, state string) {
	w.s.Work.Rows = append(w.s.Work.Rows, name)
	w.s.Merge.Rows = append(w.s.Merge.Rows, name)
	w.s.Merge.Put(&Card{ID: CtlID(name), Row: name, Col: Ctl, Rev: 1, Fields: hkv("state", state)})
}

func (w *hworld) ctl(stream string) *Card { return w.s.StreamCtl(stream) }

func (w *hworld) member(name, status string) {
	w.s.Fleet.Rows = append(w.s.Fleet.Rows, name)
	w.s.Fleet.Put(&Card{ID: CtlID(name), Row: name, Col: Ctl, Rev: 1, Fields: hkv("status", status)})
}

func (w *hworld) reader(name string) { w.s.Readers.Rows = append(w.s.Readers.Rows, name) }

// primary puts a primary of a stream in a column.
func (w *hworld) primary(id, stream, col string, score float64, kv ...string) *Card {
	c := &Card{ID: id, Row: stream, Col: col, Score: score, Rev: 1, Fields: hkv(kv...)}
	w.s.Work.Put(c)
	return c
}

// work puts the primary's live work card in a member's cell and names it.
func (w *hworld) work(p *Card, member, col string, kv ...string) *Card {
	c := &Card{ID: WorkCardID(p.ID, p.Int("attempt")), Row: member, Col: col, Rev: 1, Fields: hkv(kv...)}
	c.Fields["kind"], c.Fields["primary"], c.Fields["gen"] = "work", p.ID, "1"
	w.s.Fleet.Put(c)
	p.Fields["work"] = c.ID
	return c
}

// read puts a read card of the primary at its attempt in a reader's cell and
// lists it in the primary's rcards.
func (w *hworld) read(p *Card, reader, col string, kv ...string) *Card {
	c := &Card{ID: ReadCardID(p.ID, p.Int("attempt"), reader), Row: reader, Col: col, Rev: 1, Fields: hkv(kv...)}
	c.Fields["kind"], c.Fields["primary"], c.Fields["reader"] = "read", p.ID, reader
	w.s.Readers.Put(c)
	p.Fields["rcards"] = strings.Join(append(Split(p.F("rcards")), c.ID), ",")
	return c
}

// judge opens a judgment of the type on the subject.
func (w *hworld) judge(subject, typ string) {
	id := "n" + strconv.Itoa(len(w.s.Open)+1)
	w.s.Open = append(w.s.Open, Open{Key: OpenKey(id, subject), Note: Note{ID: id, Kind: Judgment, Type: typ}})
}

// hold holds a judgment of the type on the subject with a wait.
func (w *hworld) hold(subject, typ string) {
	id := "h" + strconv.Itoa(len(w.f.Held)+1)
	w.f.Held = append(w.f.Held, Open{Key: OpenKey(id, subject), Note: Note{ID: id, Kind: Acknowledged, Type: typ}})
}

func (w *hworld) view() *holdView { return newHoldView(w.s, w.f, w.now) }

func (w *hworld) verdict(id string) verdict { return w.view().verdict(id) }

func (w *hworld) holder(id string) Hold { return w.verdict(id).Hold }

// keys is the held keys of the cards, in order, ordered by their place in it.
func hkeys(ids ...string) []AgendaKey {
	var out []AgendaKey
	for i, id := range ids {
		out = append(out, AgendaKey{Key: ruleHeld + ":" + id, Seq: uint64(i + 1)})
	}
	return out
}

func (w *hworld) plan(keys ...AgendaKey) RulePlan { return planHeld(w.s, w.f, keys, w.now) }

// apply is J's part of a step, as a twin: each note a plan asks is opened on
// each of its subjects, unless one of its type is open there already.
func (w *hworld) apply(rp RulePlan) {
	for _, n := range rp.Notes {
		for _, sub := range n.Subjects {
			if _, ok := w.view().judgment(sub, n.Type); !ok {
				w.judge(sub, n.Type)
			}
		}
	}
}

// writesNothing says a plan changes no table, asks for no note or intent,
// guards nothing and requeues nothing. It may still name the keys it
// finished: removing a key a first run removed is not a write.
func writesNothing(rp RulePlan) bool {
	p := rp.Plan
	return len(p.Rows) == 0 && len(p.Units) == 0 && len(p.Refused) == 0 && len(p.Notes) == 0 && len(p.Closes) == 0 &&
		len(p.Updates) == 0 && len(rp.Intents) == 0 && len(rp.Guards) == 0 && len(rp.Notes) == 0 &&
		len(rp.Requeue) == 0 && len(rp.Quarantine) == 0 && len(rp.HeldBack) == 0
}

// A fixture is the state of one holder of one row: it builds the world and
// names the card judged.
type hfixture func(w *hworld) string

// holdFixtures has a state for each holder of each row of the table, by the
// row's name and the holder's words. The state is built so that no holder the
// row lists before it holds the card.
var holdFixtures = map[string]hfixture{
	"quarantined / an invariant is broken, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.f.Quarantined["p1"] = true
		w.judge("p1", NInvariant)
		return "p1"
	},
	"of a stream being dropped or removed / R11, the op's cut clock": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.f.Dropping["s1"] = "op1"
		w.f.Cut["op1"] = w.now.Wall + 600_000
		return "p1"
	},
	"of a stream being dropped or removed / a verb in parts stopped before its end, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.f.Dropping["s1"] = "op1"
		w.judge("op1", typeVerbStopped)
		return "p1"
	},
	"any with refused / the machine could not move a card, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Waiting, 1, "refused", "R3: no")
		w.judge("p1", typeCouldNotMove)
		return "p1"
	},
	"ready or review with bound / a card reached its bound, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "3", "bound", "redeals")
		w.judge("p1", NBound)
		return "p1"
	},
	"a sentinel, not reached / cards before it, or needs of its own, still open": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		return "g1"
	},
	"a sentinel, reached / sentinel reached, open or held": func(w *hworld) string {
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		w.judge("g1", NSentinelReached)
		return "g1"
	},
	"a sentinel, reached / R3, reach": func(w *hworld) string {
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		return "g1"
	},
	"waiting, open > 0 / each counted need is open on the table, or missing with its judgment": func(w *hworld) string {
		w.primary("n1", "s1", Ready, 1, "attempt", "1")
		w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
		return "p1"
	},
	"waiting, open > 0 / R4, a landed or removed need still counted": func(w *hworld) string {
		w.primary("n1", "s1", Landed, 1)
		w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
		return "p1"
	},
	"waiting, open > 0 / a blocked judgment on it, open or held": func(w *hworld) string {
		w.primary("n1", "", "", 0, "outcome", "dropped")
		w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
		w.judge("p1", NBlocked)
		return "p1"
	},
	"waiting, open = 0, behind the first sentinel / the first sentinel, open": func(w *hworld) string {
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		w.primary("p1", "s1", Waiting, 3)
		return "p1"
	},
	"waiting, open = 0, below the first sentinel or none (in elig) / R3, release": func(w *hworld) string {
		w.primary("p1", "s1", Waiting, 1)
		w.primary("g1", "s1", Waiting, 5, "kind", "sentinel")
		return "p1"
	},
	"ready, in fresh below the first sentinel or in again / R6, room at an up member": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1)
		return "p1"
	},
	"ready, in fresh below the first sentinel or in again / no room: every up member's ready cell is full": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1)
		for _, m := range []string{"m1", "m2"} {
			for i := range MaxReadyPerMember {
				w.s.Fleet.Put(&Card{ID: m + "-w" + strconv.Itoa(i), Row: m, Col: Ready, Rev: 1, Fields: hkv("kind", "work")})
			}
		}
		return "p1"
	},
	"ready, in fresh below the first sentinel or in again / no fleet member is up, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.ctl("s1") // the stream stays
		w.s.Fleet.Card(CtlID("m1")).Fields["status"] = Down
		w.s.Fleet.Card(CtlID("m2")).Fields["status"] = Down
		w.judge("sprint", NNoMember)
		return "p1"
	},
	"ready, in fresh below the first sentinel or in again / R6, no member is up and it says so": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.s.Fleet.Card(CtlID("m1")).Fields["status"] = Down
		w.s.Fleet.Card(CtlID("m2")).Fields["status"] = Held
		return "p1"
	},
	"ready, in fresh behind the first sentinel / R19, the pull back": func(w *hworld) string {
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		w.primary("p1", "s1", Ready, 3)
		return "p1"
	},
	"working / its live work card at an up member, before its due": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m1", Working, fieldDueUnfinished, strconv.FormatInt(hR+1000, 10))
		return "p1"
	},
	"working / R2, its member is down or held with its card": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m2", Ready, fieldDueUntaken, strconv.FormatInt(hR+1000, 10))
		w.s.Fleet.Card(CtlID("m2")).Fields["status"] = Down
		return "p1"
	},
	"working / R11, its due has passed": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m1", Working, fieldDueUnfinished, strconv.FormatInt(hR-1, 10))
		return "p1"
	},
	"working / its lateness judgment, open or held": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		wc := w.work(p, "m1", Working)
		w.hold(wc.ID, NWorkLate)
		return "p1"
	},
	"review / a read outstanding before its due": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		w.read(p, "r1", Asked, fieldDueUnbegun, strconv.FormatInt(hR+1000, 10))
		return "p1"
	},
	"review / R8, never asked at its attempt": func(w *hworld) string {
		w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		return "p1"
	},
	"review / R9, two different readers said ok": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		w.read(p, "r1", OK, "head", "h1")
		w.read(p, "r2", OK, "head", "h1")
		return "p1"
	},
	"review / R10, failed or broken": func(w *hworld) string {
		w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "failed", "head", "h1")
		return "p1"
	},
	"review / a judgment on it, open or held": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1", "ci", "red", "ci_head", "h1")
		w.read(p, "r1", OK, "head", "h1")
		w.read(p, "r2", OK, "head", "h1")
		w.judge("p1", NCIRed)
		return "p1"
	},
	"merging / queued in a stream merging or waiting, before its merge-idle due": func(w *hworld) string {
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = StreamMerging
		w.ctl("s1").Fields[fieldDueMergeIdle] = strconv.FormatInt(hR+1000, 10)
		return "p1"
	},
	"merging / R5, its cross need landed": func(w *hworld) string {
		w.primary("n1", "s1", Landed, 0)
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Stuck, Rev: 1, Fields: hkv("need_card", "n1")})
		w.ctl("s1").Fields["state"], w.ctl("s1").Fields["cause"], w.ctl("s1").Fields["need_card"] = StreamStopped, "cross", "n1"
		return "p1"
	},
	"merging / its stream's stop or merge-idle judgment, open or held": func(w *hworld) string {
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Stuck, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"], w.ctl("s1").Fields["cause"] = StreamStopped, "conflict"
		w.judge(StreamSubject("s1"), NConflict)
		return "p1"
	},
}

// stallFixtures has a state nothing holds for each row that can fail to hold a
// card, by the row's name: the seeded silent states.
var stallFixtures = map[string]hfixture{
	"quarantined": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.f.Quarantined["p1"] = true
		return "p1"
	},
	"of a stream being dropped or removed": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.f.Dropping["s1"] = "op1"
		return "p1"
	},
	"any with refused": func(w *hworld) string {
		w.primary("p1", "s1", Waiting, 1, "refused", "R3: no")
		return "p1"
	},
	"ready or review with bound": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "3", "bound", "redeals")
		return "p1"
	},
	"waiting, open > 0": func(w *hworld) string {
		w.primary("p1", "s1", Waiting, 1, "needs", "n1", "open", "1") // n1 has no record and no judgment names it
		return "p1"
	},
	"working": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m1", Working) // no due, and nothing else names it
		return "p1"
	},
	"review": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		w.read(p, "r1", OK, "head", "h1")
		w.read(p, "r2", OK, "head", "h0") // one ok at its head: the reads are done, and not enough
		return "p1"
	},
	"merging": func(w *hworld) string {
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = StreamMerging
		w.ctl("s1").Fields[fieldDueMergeIdle] = strconv.FormatInt(hR-1, 10) // past its deadline, and nothing has named it
		return "p1"
	},
	"a place no row of the table gives": func(w *hworld) string {
		w.primary("g1", "s1", Ready, 1, "kind", "sentinel") // a sentinel is never ready
		return "g1"
	},
}

func TestHeldEveryRowOfTheTable(t *testing.T) {
	t.Parallel()
	causes := map[string]string{}
	used := map[string]bool{}
	for _, row := range holdRows {
		if row.Cause != "" {
			if other, dup := causes[row.Cause]; dup {
				t.Errorf("rows %q and %q share the cause %q: J opens one judgment a cause", other, row.Name, row.Cause)
			}
			causes[row.Cause] = row.Name
		}
		if (row.Stall == "") != (row.Cause == "") {
			t.Errorf("row %q: a stall's words and its cause go together (stall %q, cause %q)", row.Name, row.Stall, row.Cause)
		}
		for _, h := range row.Holders {
			key := row.Name + " / " + h.What
			fix, ok := holdFixtures[key]
			if !ok {
				t.Errorf("no state for the holder %q", key)
				continue
			}
			used[key] = true
			w := newHWorld()
			id := fix(w)
			vd := w.verdict(id)
			if vd.row == nil || vd.row.Name != row.Name {
				t.Errorf("%s: judged by row %v, want %q", key, vd.row, row.Name)
				continue
			}
			if vd.By != h.By || !strings.HasPrefix(vd.Why, h.What+": ") {
				t.Errorf("%s: held by (%s) %q, want (%s) %q", key, vd.By, vd.Why, h.By, h.What)
			}
			if vd.Stalled() {
				t.Errorf("%s: stalled", key)
			}
			if rp := w.plan(hkeys(id)...); len(rp.Notes) != 0 || len(rp.Plan.Units) != 0 {
				t.Errorf("%s: a card held by (%s) is judged: %+v", key, vd.By, rp.Notes)
			}
		}
		fix, ok := stallFixtures[row.Name]
		switch {
		case row.Stall == "" && ok:
			t.Errorf("row %q cannot fail to hold a card and has a stall state", row.Name)
		case row.Stall != "" && !ok:
			t.Errorf("row %q can fail to hold a card and has no stall state", row.Name)
		case ok:
			used["stall "+row.Name] = true
			w := newHWorld()
			id := fix(w)
			vd := w.verdict(id)
			if vd.row == nil || vd.row.Name != row.Name || !vd.Stalled() || vd.Why != row.Stall {
				t.Errorf("stall of %q: judged by %v, held by (%s) %q", row.Name, vd.row, vd.By, vd.Why)
			}
		}
	}
	for key := range holdFixtures {
		if !used[key] {
			t.Errorf("a state for a holder the table does not list: %q", key)
		}
	}
	for name := range stallFixtures {
		if !used["stall "+name] {
			t.Errorf("a stall state for a row the table does not list: %q", name)
		}
	}
}

func TestHeldStateImpliedHolder(t *testing.T) {
	t.Parallel()
	// A need landed and the waiter still counts it: its line is not ingested,
	// or its key is queued behind others. The state alone says R4 holds it; no
	// key of R4 is anywhere in the plan.
	w := newHWorld()
	w.primary("n1", "s1", Landed, 1)
	w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
	hd := w.holder("p1")
	if hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R4, ") {
		t.Fatalf("a landed need with the waiter still counting it: (%s) %s", hd.By, hd.Why)
	}
	rp := w.plan(hkeys("p1")...)
	if !writesNothing(rp) || len(rp.Done) != 1 {
		t.Fatalf("a card held by R4's condition is judged or kept: %+v", rp)
	}

	// The same for a need that was removed and is still counted.
	w = newHWorld()
	w.primary("n1", "", "", 0, "outcome", "dropped")
	w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
	if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R4, ") {
		t.Fatalf("a removed need still counted: (%s) %s", hd.By, hd.Why)
	}

	// R4 lowered the count: nothing counts n1, and R3 releases the card.
	w = newHWorld()
	w.primary("n1", "s1", Landed, 1)
	w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "0")
	if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R3, release") {
		t.Fatalf("a need landed and counted no more: (%s) %s", hd.By, hd.Why)
	}

	// A need still open on the table is the waiter's (d), not R4's.
	w = newHWorld()
	w.primary("n1", "s1", Working, 1, "attempt", "1")
	w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
	if hd := w.holder("p1"); hd.By != HeldByWaiting {
		t.Fatalf("a need open on the table: (%s) %s", hd.By, hd.Why)
	}

	// A need waived is not counted: open = 1 with n1 waived and n2 open is
	// n2's, and one landed and counted is R4's, told apart by the count.
	w = newHWorld()
	w.primary("n1", "s1", Landed, 1)
	w.primary("n2", "s1", Working, 2, "attempt", "1")
	w.primary("p1", "s1", Waiting, 3, "needs", "n1,n2", "waived", "n1", "open", "1")
	ns := w.view().needSummary(w.s.Work.Card("p1"))
	if ns.pending != 0 || !slices.Equal(ns.open, []string{"n2"}) || len(ns.landed) != 0 {
		t.Fatalf("a waived need is counted: %+v", ns)
	}
	w = newHWorld()
	w.primary("n1", "s1", Landed, 1)
	w.primary("n2", "s1", Working, 2, "attempt", "1")
	w.primary("p1", "s1", Waiting, 3, "needs", "n1,n2", "open", "2")
	if ns := w.view().needSummary(w.s.Work.Card("p1")); ns.pending != 1 {
		t.Fatalf("one landed need still counted beside one open: pending %d", ns.pending)
	}
}

func TestHeldNamesSeededSilent(t *testing.T) {
	t.Parallel()
	// One card of every row that can fail to hold, seeded silent: each is
	// named, by one note of its row's cause, offering its place's decisions and
	// the ones every stall offers, and guarded at its place and revision.
	rows := 0
	for _, row := range holdRows {
		fix, ok := stallFixtures[row.Name]
		if !ok {
			continue
		}
		rows++
		w := newHWorld()
		id := fix(w)
		rp := w.plan(hkeys(id)...)
		if len(rp.Notes) != 1 || len(rp.Plan.Units) != 1 || len(rp.Done) != 1 || len(rp.Requeue) != 0 {
			t.Fatalf("%q: the plan of a stalled card: %+v", row.Name, rp)
		}
		n := rp.Notes[0]
		wantType, wantCause := NStalled, row.Cause
		if row.Name == "review" {
			wantType, wantCause = NReadsExhausted, "exhausted"
		}
		if n.Op != "open" || n.Type != wantType || n.Cause != wantCause || !slices.Equal(n.Subjects, []string{id}) || n.Text == "" {
			t.Errorf("%q: the note asked: %+v", row.Name, n)
		}
		if n.Type == NStalled {
			tail := n.Decisions[len(n.Decisions)-len(stalledTail):]
			if !slices.Equal(tail, stalledTail) || !slices.Equal(n.Decisions[:len(row.Decisions)], row.Decisions) {
				t.Errorf("%q: the decisions offered: %v", row.Name, n.Decisions)
			}
			if n.Text != row.Stall {
				t.Errorf("%q: the words of the note: %q", row.Name, n.Text)
			}
		}
		u := rp.Plan.Units[0]
		if u.Key != id || len(u.Changes) != 1 || u.Changes[0].Table != Work || u.Changes[0].Entry.ID != id ||
			u.Changes[0].Entry.Expect == nil || u.Changes[0].Entry.Expect.Revision != "1" ||
			u.Changes[0].Entry.Move != nil || u.Changes[0].Entry.Create != nil || u.Changes[0].Entry.Remove {
			t.Errorf("%q: the card is not guarded at its place and revision, alone: %+v", row.Name, u)
		}
	}
	if rows != len(stallFixtures) {
		t.Fatalf("judged %d rows of %d stall states", rows, len(stallFixtures))
	}

	// and several silent cards of one sprint in one plan: a note for each
	// cause, each naming its cards once
	w := newHWorld()
	w.stream("s2", StreamMerging)
	p1 := w.primary("p1", "s1", Working, 1, "attempt", "1")
	w.work(p1, "m1", Working)
	p2 := w.primary("p2", "s1", Working, 2, "attempt", "1")
	w.work(p2, "m2", Working)
	p3 := w.primary("p3", "s1", Review, 3, "attempt", "1", "result", "ok", "head", "h1")
	w.read(p3, "r1", OK, "head", "h1")
	w.read(p3, "r2", OK, "head", "h0")
	w.primary("p4", "s2", Merging, 4)
	w.s.Merge.Put(&Card{ID: "p4", Row: "s2", Col: Queued, Rev: 1, Fields: hkv()})
	w.ctl("s2").Fields[fieldDueMergeIdle] = strconv.FormatInt(hR-1, 10)
	w.primary("p5", "s1", Waiting, 5, "needs", "gone", "open", "1")
	w.primary("p6", "s1", Ready, 6, "attempt", "3", "bound", "redeals")
	w.primary("p7", "s1", Waiting, 7, "needs", "gone", "open", "1", "waived", "")
	w.judge("p6", NBound) // p6 is named already: held, and not named again
	rp := w.plan(hkeys("p7", "p6", "p5", "p4", "p3", "p2", "p1")...)
	got := map[string][]string{}
	for _, n := range rp.Notes {
		got[n.Type+" / "+n.Cause] = n.Subjects
	}
	want := map[string][]string{
		NStalled + " / working":          {"p1", "p2"},
		NReadsExhausted + " / exhausted": {"p3"},
		NStalled + " / merging":          {"p4"},
		NStalled + " / waiting":          {"p5", "p7"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the notes of a sprint's silent cards: %v, want %v", got, want)
	}
	if len(rp.Plan.Units) != 6 || len(rp.Done) != 7 {
		t.Fatalf("guards %d, done %d", len(rp.Plan.Units), len(rp.Done))
	}
}

func TestHeldReviewNamesReadsExhaustedNotStalled(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	id := stallFixtures["review"](w)
	rp := w.plan(hkeys(id)...)
	if len(rp.Notes) != 1 || rp.Notes[0].Type != NReadsExhausted || !slices.Equal(rp.Notes[0].Decisions, reviewStalls[NReadsExhausted].Decisions) {
		t.Fatalf("a primary whose reads are done without two oks: %+v", rp.Notes)
	}
	// once named, a judgment holds it (c), and nothing more is asked
	w.apply(rp)
	if hd := w.holder(id); hd.By != HeldByJudgment {
		t.Fatalf("named by its judgment: (%s) %s", hd.By, hd.Why)
	}
	if rp := w.plan(hkeys(id)...); !writesNothing(rp) {
		t.Fatalf("a named primary is named again: %+v", rp)
	}
}

// heldRule is the rule the registry holds for held.
func heldRule(t *testing.T) Rule {
	t.Helper()
	for _, r := range RuleTable() {
		if r.Name == ruleHeld {
			return r
		}
	}
	t.Fatal("the rule table has no held rule")
	return Rule{}
}

func TestHeldRuleShape(t *testing.T) {
	t.Parallel()
	r := heldRule(t)
	if r.Priority != PriorityHeld || r.MaxSteps != 0 || r.Read == nil || r.Plan == nil {
		t.Fatalf("the rule as registered: %+v", r)
	}
	// R16 comes after every rule of the tick in priority (1.4.2)
	for _, o := range RuleTable() {
		if o.Name != ruleHeld && o.Priority >= r.Priority {
			t.Errorf("rule %s has priority %d, not before held's %d", o.Name, o.Priority, r.Priority)
		}
	}
}

func TestHeldOverBacklogRequeues(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	a := stallFixtures["working"](w)
	keys := []AgendaKey{{Key: "held:" + a, Seq: 7}, {Key: "held@9", Seq: 8}}
	w.f.Lines[9] = HeldLine{IDs: []string{a}, Total: 1}

	// above the bound the machine is catching up: every key is put back with
	// its order, and nothing is judged
	w.f.Backlog = HeldBacklogBound + 1
	rp := w.plan(keys...)
	judged := rp
	judged.Requeue = nil
	if !writesNothing(judged) || len(rp.Done) != 0 || !slices.Equal(rp.Requeue, keys) {
		t.Fatalf("over the backlog bound: %+v", rp)
	}

	// at the bound it judges
	w.f.Backlog = HeldBacklogBound
	rp = w.plan(keys...)
	if len(rp.Notes) != 1 || len(rp.Requeue) != 0 || len(rp.Done) != 2 {
		t.Fatalf("at the backlog bound: %+v", rp)
	}
	if HeldBacklogBound != 5000 {
		t.Fatalf("the bound is 5,000 lines (0, row 26): %d", HeldBacklogBound)
	}
}

func TestHeldTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	// every stall state in one sprint's worth of runs: the first run names the
	// card, and a second run of the rule on the same keys, on the state the
	// first left, writes nothing
	for _, row := range holdRows {
		fix, ok := stallFixtures[row.Name]
		if !ok {
			continue
		}
		w := newHWorld()
		id := fix(w)
		keys := hkeys(id)
		first := w.plan(keys...)
		if len(first.Notes) == 0 {
			t.Fatalf("%q: the first run names nothing", row.Name)
		}
		w.apply(first)
		second := w.plan(keys...)
		if !writesNothing(second) {
			t.Errorf("%q: the second run writes: %+v", row.Name, second)
		}
		// and through the rule as the registry holds it
		third := heldRule(t).Plan(w.s, keys, w.now)
		if !writesNothing(third) {
			t.Errorf("%q: the second run of the registered rule writes: %+v", row.Name, third)
		}
	}
	// a held state is quiet at once, and twice
	w := newHWorld()
	id := holdFixtures["merging / R5, its cross need landed"](w)
	for i := range 2 {
		if rp := w.plan(hkeys(id)...); !writesNothing(rp) {
			t.Fatalf("run %d over a held card writes: %+v", i+1, rp)
		}
	}
	// a hold that stands over a judgment keeps the card held, and named
	w = newHWorld()
	id = stallFixtures["working"](w)
	w.hold(w.s.Work.Card(id).F("work"), NWorkLate)
	if hd := w.holder(id); hd.By != HeldByJudgment {
		t.Fatalf("a hold on the lateness judgment: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldLineCutRequeuesAtItsOffset(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		w.primary(id, "s1", Landed, 1)
	}
	key := AgendaKey{Key: "held@9", Seq: 40}
	// the read named the first two ids of a line of five
	w.f.Lines[9] = HeldLine{IDs: []string{"a", "b"}, Total: 5}
	rp := w.plan(key)
	want := AgendaKey{Key: "held@9+2", Seq: 40}
	if !slices.Equal(rp.Done, []AgendaKey{key}) || !slices.Equal(rp.Requeue, []AgendaKey{want}) {
		t.Fatalf("a line cut at two of five: done %+v requeue %+v", rp.Done, rp.Requeue)
	}
	// resumed at the offset, and cut again at four
	w.f.Lines[9] = HeldLine{IDs: []string{"c", "d"}, Total: 5}
	rp = w.plan(want)
	if !slices.Equal(rp.Requeue, []AgendaKey{{Key: "held@9+4", Seq: 40}}) {
		t.Fatalf("resumed at 2: %+v", rp.Requeue)
	}
	// the last id: nothing is left, and the key goes
	w.f.Lines[9] = HeldLine{IDs: []string{"e"}, Total: 5}
	rp = w.plan(AgendaKey{Key: "held@9+4", Seq: 40})
	if len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("the end of the line: %+v", rp)
	}
	// a read that gave no ids ends the key, so that the key never spins
	w.f.Lines[9] = HeldLine{Total: 5}
	rp = w.plan(want)
	if len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("a line that gave nothing: %+v", rp)
	}
	// a line the snapshot does not carry stays queued
	rp = w.plan(AgendaKey{Key: "held@77", Seq: 1})
	if len(rp.Done) != 0 || len(rp.Requeue) != 1 {
		t.Fatalf("a line that was not read: %+v", rp)
	}
	// a key of another rule is not this rule's to finish
	rp = w.plan(AgendaKey{Key: "deal", Seq: 1})
	if len(rp.Done) != 0 || len(rp.Requeue) != 1 {
		t.Fatalf("a key of another rule: %+v", rp)
	}
}

func TestHeldParseKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key  string
		want heldKey
		ok   bool
	}{
		{"held:p17", heldKey{Card: "p17"}, true},
		{"held@48213", heldKey{Line: 48213, ByLine: true}, true},
		{"held@48213+2000", heldKey{Line: 48213, Offset: 2000, ByLine: true}, true},
		{"held:", heldKey{}, false},
		{"held@x", heldKey{}, false},
		{"held@1+x", heldKey{}, false},
		{"held@1+-3", heldKey{}, false},
		{"ask:p17", heldKey{}, false},
		{"held", heldKey{}, false},
	} {
		got, ok := parseHeldKey(AgendaKey{Key: c.key})
		if ok != c.ok || got != c.want {
			t.Errorf("%q: %+v, %v; want %+v, %v", c.key, got, ok, c.want, c.ok)
			continue
		}
		if ok && got.agenda(5).Key != c.key {
			t.Errorf("%q: the key it gives back is %q", c.key, got.agenda(5).Key)
		}
		if ok && RuleOf(c.key) != ruleHeld {
			t.Errorf("%q: RuleOf says %q", c.key, RuleOf(c.key))
		}
	}
}

func TestHeldStoppedMachine(t *testing.T) {
	t.Parallel()
	// (b) is (e) while the machine is STOPPED: the rule waits for the start
	w := newHWorld()
	id := holdFixtures["waiting, open = 0, below the first sentinel or none (in elig) / R3, release"](w)
	w.now.Running = false
	hd := w.holder(id)
	if hd.By != HeldByStopped || !strings.Contains(hd.Why, "R3, release") {
		t.Fatalf("R3's card while STOPPED: (%s) %s", hd.By, hd.Why)
	}
	// (a) and (c) hold as they do, stopped or not
	w = newHWorld()
	id = holdFixtures["working / its live work card at an up member, before its due"](w)
	w.now.Running = false
	if hd := w.holder(id); hd.By != HeldByActor {
		t.Fatalf("an actor's card while STOPPED: (%s) %s", hd.By, hd.Why)
	}
	w = newHWorld()
	id = holdFixtures["any with refused / the machine could not move a card, open or held"](w)
	w.now.Running = false
	if hd := w.holder(id); hd.By != HeldByJudgment {
		t.Fatalf("a named card while STOPPED: (%s) %s", hd.By, hd.Why)
	}
	// a card nothing holds is held by the stop, and R16 names nothing while
	// the machine is STOPPED: the first tick after start does
	w = newHWorld()
	id = stallFixtures["working"](w)
	w.now.Running = false
	if hd := w.holder(id); hd.By != HeldByStopped || !strings.Contains(hd.Why, "R17") {
		t.Fatalf("a stall while STOPPED: (%s) %s", hd.By, hd.Why)
	}
	if rp := w.plan(hkeys(id)...); !writesNothing(rp) {
		t.Fatalf("R16 names a stall while STOPPED: %+v", rp)
	}
	w.now.Running = true
	if rp := w.plan(hkeys(id)...); len(rp.Notes) != 1 {
		t.Fatalf("after the start: %+v", rp)
	}
}

func TestHeldReviewWithBoundIsHeldByItsJudgment(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	w.primary("p1", "s1", Review, 1, "attempt", "3", "result", "failed", "bound", "attempts")
	if vd := w.verdict("p1"); !vd.Stalled() || vd.row.Name != "ready or review with bound" {
		t.Fatalf("a primary at its bound with no judgment: %+v", vd)
	}
	w.judge("p1", NBound)
	if hd := w.holder("p1"); hd.By != HeldByJudgment {
		t.Fatalf("a primary at its bound with its judgment: (%s) %s", hd.By, hd.Why)
	}
	// R10 binds a card that fails at its last attempt: the state is R10's
	// before it has set the bound
	w = newHWorld()
	w.primary("p1", "s1", Review, 1, "attempt", strconv.Itoa(heldAttemptBound), "result", "failed", "head", "h1")
	if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R10, ") || !strings.Contains(hd.Why, "sets its bound") {
		t.Fatalf("failed at the last attempt: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldReadyWithNoMemberIsNeverSilent(t *testing.T) {
	t.Parallel()
	// no member is up: R6 raises "no fleet member is up" once for the sprint;
	// before it has, the state meets R6's condition, and after it, the
	// judgment holds every ready card
	w := newHWorld()
	w.primary("p1", "s1", Ready, 1, "attempt", "1")
	w.primary("p2", "s1", Ready, 2, "attempt", "1")
	w.s.Fleet.Card(CtlID("m1")).Fields["status"] = Down
	w.s.Fleet.Card(CtlID("m2")).Fields["status"] = Down
	for _, id := range []string{"p1", "p2"} {
		if hd := w.holder(id); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R6, no member is up") {
			t.Fatalf("%s before R6 has said so: (%s) %s", id, hd.By, hd.Why)
		}
	}
	w.judge("sprint", NNoMember)
	for _, id := range []string{"p1", "p2"} {
		if hd := w.holder(id); hd.By != HeldByJudgment {
			t.Fatalf("%s after: (%s) %s", id, hd.By, hd.Why)
		}
	}
}

func TestHeldOffTheTableAndNotOnIt(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	w.primary("done", "s1", Landed, 1)
	w.primary("gone", "", "", 0, "outcome", "dropped")
	p := w.primary("p1", "s1", Working, 2, "attempt", "1")
	wc := w.work(p, "m1", Working)
	if hd := w.holder("done"); hd.By != HeldDone {
		t.Errorf("landed: %+v", hd)
	}
	if hd := w.holder("gone"); hd.By != HeldDone || !strings.Contains(hd.Place, "dropped") {
		t.Errorf("dropped: %+v", hd)
	}
	if hd := w.holder("ghost"); !hd.Stalled() || hd.Why != "not on the table" {
		t.Errorf("no card: %+v", hd)
	}
	// a card that is not a primary is judged as its primary
	if a, b := w.holder(wc.ID), w.holder("p1"); a != b {
		t.Errorf("the work card %+v is not its primary %+v", a, b)
	}
	// nothing owed for a card that landed, is off the table, or is not there
	rp := w.plan(hkeys("done", "gone", "ghost")...)
	if !writesNothing(rp) || len(rp.Done) != 3 {
		t.Errorf("cards owed nothing: %+v", rp)
	}
}

func TestHeldOneNotePerCauseForAnyNumberOfCards(t *testing.T) {
	t.Parallel()
	// 2,000 cards stalled for one cause are one note naming them all (1.3.4,
	// T4), and one guard each, in a fixed order
	w := newHWorld()
	var ids []string
	for i := range HeldChunk {
		id := "p" + strconv.Itoa(i)
		p := w.primary(id, "s1", Working, float64(i), "attempt", "1")
		w.work(p, "m1", Working)
		ids = append(ids, id)
	}
	rp := w.plan(hkeys(ids...)...)
	if len(rp.Notes) != 1 || len(rp.Notes[0].Subjects) != HeldChunk || len(rp.Plan.Units) != HeldChunk || len(rp.Done) != HeldChunk {
		t.Fatalf("%d cards of one cause: %d notes, %d subjects, %d guards", HeldChunk, len(rp.Notes), len(rp.Notes[0].Subjects), len(rp.Plan.Units))
	}
	if !slices.IsSorted(rp.Notes[0].Subjects) {
		t.Fatal("the subjects are not in a fixed order")
	}
	again := w.plan(hkeys(ids...)...)
	if !reflect.DeepEqual(rp, again) {
		t.Fatal("the same state and keys give a different plan")
	}
	// two keys naming one card judge it once
	dup := w.plan(append(hkeys("p1"), hkeys("p1")...)...)
	if len(dup.Notes) != 1 || len(dup.Notes[0].Subjects) != 1 {
		t.Fatalf("a card named by two keys: %+v", dup.Notes)
	}
}

func TestHeldHoldsFromTheSnapshotsAckedConditions(t *testing.T) {
	t.Parallel()
	// the public function reads the snapshot alone: the conditions the
	// coordinator acknowledged are the holds it carries
	w := newHWorld()
	id := stallFixtures["working"](w)
	wcID := w.s.Work.Card(id).F("work")
	if hd := LocalHolder(w.s, id, w.now); !hd.Stalled() {
		t.Fatalf("no holder: %+v", hd)
	}
	w.s.Acked = append(w.s.Acked, Open{Key: OpenKey("n1", wcID), Note: Note{ID: "n1", Kind: Acknowledged, Type: NWorkLate}})
	if hd := LocalHolder(w.s, id, w.now); hd.By != HeldByJudgment {
		t.Fatalf("a held lateness judgment: %+v", hd)
	}
}

func heldBounds() ReadBounds {
	return ReadBounds{Queries: 1024, Records: 10_000, RangeIDs: 20_000, Bytes: 8 << 20}
}

func TestHeldReadIsSizedByDeclaredCost(t *testing.T) {
	t.Parallel()
	b := heldBounds()
	var keys []AgendaKey
	for i := range 3000 {
		keys = append(keys, AgendaKey{Key: "held:p" + strconv.Itoa(i), Seq: uint64(i)})
	}
	rp, left := readHeld(keys, b, 0)
	n := heldReadCards(b, 0)
	if c := rp.Cost(); c.Records > b.Records || c.RangeIDs > b.RangeIDs {
		t.Fatalf("the read costs %+v, over the bounds %+v", c, b)
	}
	if len(keys)-len(left) != n || !slices.Equal(left, keys[n:]) {
		t.Fatalf("the read took %d of %d keys, want %d, the rest in order", len(keys)-len(left), len(keys), n)
	}
	// declared: 1 + work, rcards (15), merge, control, needs (64), jopen = 84 an id, and
	// front and fleet at 250 each
	if per := QueryCost(SprintQ{Kind: "related", IDs: []string{"x"}, Follow: heldFollows}).Records; per != 84 {
		t.Fatalf("a card and its follows are declared at %d records", per)
	}
	if n != (10_000-500)/84 {
		t.Fatalf("a read of %d cards at 84 records and 500 fixed", n)
	}
	kinds := map[string]int{}
	for _, q := range rp.Sprint {
		kinds[q.Kind]++
		if q.Kind == "related" && (!slices.Equal(q.Fields, heldFields) || !slices.Equal(q.Follow, heldFollows)) {
			t.Errorf("a related query without R16's fields and follows: %+v", q)
		}
	}
	if kinds["related"] != 1 || kinds["front"] != 1 || kinds["fleet"] != 1 {
		t.Fatalf("the queries: %v", kinds)
	}
	// with few keys the read takes all of them, and with none it asks nothing
	rp, left = readHeld(keys[:5], b, 0)
	if len(left) != 0 || len(rp.Sprint[0].IDs) != 5 {
		t.Fatalf("five keys: %+v, left %d", rp.Sprint, len(left))
	}
	if rp, left = readHeld(nil, b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
		t.Fatalf("no keys: %+v", rp)
	}
}

func TestHeldReadHalvings(t *testing.T) {
	t.Parallel()
	b := heldBounds()
	n := heldReadCards(b, 0)
	for h := 1; h <= 8; h++ {
		want := max(1, n>>h)
		if got := heldReadCards(b, h); got != want {
			t.Errorf("%d halvings: %d cards, want %d", h, got, want)
		}
	}
	// with the chunk the binding limit, and never below one card
	big := ReadBounds{Queries: 1024, Records: 1_000_000}
	if got := heldReadCards(big, 0); got != HeldChunk {
		t.Errorf("the chunk is %d cards, got %d", HeldChunk, got)
	}
	if got := heldReadCards(ReadBounds{}, 0); got != 1 {
		t.Errorf("no room at all still reads one card, got %d", got)
	}
}

func TestHeldReadOfLines(t *testing.T) {
	t.Parallel()
	b := heldBounds()
	keys := []AgendaKey{
		{Key: "held:a", Seq: 1},
		{Key: "held@7+100", Seq: 2},
		{Key: "held:b", Seq: 3},
	}
	rp, left := readHeld(keys, b, 0)
	n := heldReadCards(b, 0)
	var line *SprintQ
	for i := range rp.Sprint {
		if rp.Sprint[i].Line == 7 {
			line = &rp.Sprint[i]
		}
	}
	if line == nil || line.Offset != 100 || line.Limit != n-1 {
		t.Fatalf("the line is read from its offset for the room left: %+v", line)
	}
	// the line takes the room that is left, so the key behind it waits
	if !slices.Equal(left, []AgendaKey{{Key: "held:b", Seq: 3}}) {
		t.Fatalf("left for later: %+v", left)
	}
	// a foreign key stays out of the read and keeps its place among the rest
	rp, left = readHeld([]AgendaKey{{Key: "deal", Seq: 1}, {Key: "held:a", Seq: 2}}, b, 0)
	if !slices.Equal(left, []AgendaKey{{Key: "deal", Seq: 1}}) || len(rp.Sprint[0].IDs) != 1 {
		t.Fatalf("a foreign key: %+v %+v", rp.Sprint, left)
	}
	// and the queries are bounded: with room for no more, a line waits
	rp, left = readHeld(keys, ReadBounds{Queries: heldFixedQueries, Records: 10_000}, 0)
	if !slices.Equal(left, keys[1:]) || len(rp.Sprint[0].IDs) != 1 {
		t.Fatalf("no query to spare for a line: %+v left %+v", rp.Sprint, left)
	}
}

// BenchmarkHeld2000 is 2,000 cards judged in one plan, the held rule's chunk (its
// limit is 15 ms of store time for the read, measured by IT25; this is the Go
// time of the plan over it): cards of every place, most of them held.
func BenchmarkHeld2000(b *testing.B) { benchHeld(b, HeldChunk) }

// BenchmarkHeld8000 is four chunks in one plan, to show the cost of a card does
// not grow with the number of them.
func BenchmarkHeld8000(b *testing.B) { benchHeld(b, 4*HeldChunk) }

func benchHeld(b *testing.B, n int) {
	w := newHWorld()
	w.stream("s2", StreamWaiting)
	streams := []string{"s1", "s2"}
	w.primary("g1", "s1", Waiting, 1e6, "kind", "sentinel")
	var ids []string
	for i := range n {
		id := "p" + strconv.Itoa(i)
		st := streams[i%2]
		score := float64(i + 1)
		switch i % 6 {
		case 0:
			w.primary(id, st, Waiting, score)
		case 1:
			w.primary("n"+id, st, Working, score-0.5, "attempt", "1")
			w.primary(id, st, Waiting, score, "needs", "n"+id, "open", "1")
		case 2:
			w.primary(id, st, Ready, score, "attempt", "1")
		case 3:
			p := w.primary(id, st, Working, score, "attempt", "1")
			w.work(p, "m1", Working, fieldDueUnfinished, strconv.FormatInt(hR+1000, 10))
		case 4:
			p := w.primary(id, st, Review, score, "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", OK, "head", "h1")
			w.read(p, "r2", OK, "head", "h1")
		case 5:
			w.primary(id, st, Merging, score)
			w.s.Merge.Put(&Card{ID: id, Row: st, Col: Queued, Rev: 1, Fields: hkv()})
		}
		ids = append(ids, id)
	}
	for _, st := range streams {
		w.ctl(st).Fields["state"] = StreamMerging
		w.ctl(st).Fields[fieldDueMergeIdle] = strconv.FormatInt(hR+1000, 10)
	}
	keys := hkeys(ids...)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = planHeld(w.s, w.f, keys, w.now)
	}
}

func TestHeldSentinelWithNeedsOfItsOwnIsNotReached(t *testing.T) {
	t.Parallel()
	// nothing is before it, but it waits for a card of another stream: not
	// reached, and held by (d)
	w := newHWorld()
	w.stream("s2", StreamWaiting)
	w.primary("n1", "s2", Ready, 1, "attempt", "1")
	w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1")
	vd := w.verdict("g1")
	if vd.By != HeldByWaiting || vd.row.Name != "a sentinel, not reached" {
		t.Fatalf("a sentinel with a need open: (%s) %s", vd.By, vd.Why)
	}
	// a second sentinel has the first before it whatever else is there
	w = newHWorld()
	w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
	w.primary("g2", "s1", Waiting, 3, "kind", "sentinel")
	if vd := w.verdict("g2"); vd.row.Name != "a sentinel, not reached" {
		t.Fatalf("a later sentinel: %s, %s", vd.row.Name, vd.Why)
	}
	if vd := w.verdict("g1"); vd.row.Name != "a sentinel, reached" {
		t.Fatalf("the first sentinel, nothing before it: %s, %s", vd.row.Name, vd.Why)
	}
}

func TestHeldReadsAreThoseOfTheAttempt(t *testing.T) {
	t.Parallel()
	// a read of the last attempt, still outstanding, does not hold a primary
	// that has been reworked and not yet asked at its new attempt: R8 does
	w := newHWorld()
	p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
	w.read(p, "r1", Asked, fieldDueUnbegun, strconv.FormatInt(hR+1000, 10))
	p.Fields["attempt"], p.Fields["result"] = "2", "ok"
	hd := w.holder("p1")
	if hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R8, ") {
		t.Fatalf("a read of another attempt: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldNeedsAreCountedOnce(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	w.primary("n1", "s1", Working, 1, "attempt", "1")
	w.primary("n2", "s1", Landed, 2)
	w.primary("p1", "s1", Waiting, 3, "needs", "n1,n1,n2", "open", "2")
	ns := w.view().needSummary(w.s.Work.Card("p1"))
	if !slices.Equal(ns.open, []string{"n1"}) || !slices.Equal(ns.landed, []string{"n2"}) || ns.pending != 1 {
		t.Fatalf("a need named twice: %+v", ns)
	}
}

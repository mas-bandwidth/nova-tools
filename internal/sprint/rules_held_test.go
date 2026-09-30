package sprint

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// R16 and the local holder of a card (the upper design, version 2.1, 2.3 R16),
// on snapshots built by hand: one state for every row of the table and every
// holder it lists, the backlog, the state-implied holder, the seeded stalls,
// and a second run of the rule on the state its first run left.
//
// The registered rule is run the way the tick runs it: its Read plans the read
// for the keys, the read is answered from the world by a twin that returns only
// what the plan asked for, LoadPartial loads that answer, and the rule's Plan
// runs on the partial snapshot, where a read of anything the plan did not ask
// for panics (hworld.viaRule). planHeld is run on the whole world with the
// facts the partial snapshot does not carry.

// hworld is a snapshot with one stream, two members up and two readers, and the
// sprint's own keys (HeldFacts) beside it, at a running time of hR.
type hworld struct {
	s     *Snapshot
	f     HeldFacts
	now   Now
	lines map[uint64][]string // the ids of the lines the twin can read by seq
	blind bool                // a verdict is judged on a partial snapshot, whose plan asked no front
	tb    testing.TB          // the test a blind verdict is loaded for
}

const hR = int64(1_000_000)

func newHWorld() *hworld {
	s := &Snapshot{Now: time.Unix(1_800_000_000, 0), Work: NewTable(Work), Readers: NewTable(Readers),
		Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	w := &hworld{s: s, now: Now{R: hR, Wall: hR, Running: true}, lines: map[uint64][]string{},
		f: HeldFacts{Dropping: map[string]string{}, Cut: map[string]int64{}, Quarantined: map[string]bool{},
			Lines: map[HeldLineAt]HeldLine{}}}
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
	w.s.Work.SetRows(append(w.s.Work.Rows(), name))
	w.s.Merge.SetRows(append(w.s.Merge.Rows(), name))
	w.s.Merge.Put(&Card{ID: CtlID(name), Row: name, Col: Ctl, Rev: 1, Fields: hkv("state", state)})
}

func (w *hworld) ctl(stream string) *Card { return w.s.StreamCtl(stream) }

func (w *hworld) member(name, status string) {
	w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), name))
	w.s.Fleet.Put(&Card{ID: CtlID(name), Row: name, Col: Ctl, Rev: 1, Fields: hkv("status", status)})
}

func (w *hworld) reader(name string) { w.s.Readers.SetRows(append(w.s.Readers.Rows(), name)) }

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

// usesFacts says the world has a fact the partial snapshot has no carrier for.
func (w *hworld) usesFacts() bool {
	return len(w.f.Dropping) > 0 || len(w.f.Cut) > 0 || len(w.f.Quarantined) > 0 || w.f.Backlog > 0
}

func (w *hworld) view() *holdView { return newHoldView(w.s, w.f, w.now) }

// verdict is the verdict of the whole world, or, for a blind world, the
// verdict on the partial snapshot the rule's read of the card gives.
func (w *hworld) verdict(id string) verdict {
	if w.blind {
		return w.partialView(w.tb, hkeys(id)...).verdict(id)
	}
	return w.view().verdict(id)
}

func (w *hworld) holder(id string) Hold { return w.verdict(id).Hold }

// keys is the held keys of the cards, in order, ordered by their place in it.
func hkeys(ids ...string) []AgendaKey {
	var out []AgendaKey
	for i, id := range ids {
		out = append(out, AgendaKey{Key: ruleHeld + ":" + id, Seq: uint64(i + 1)})
	}
	return out
}

// plan is planHeld on the whole world, with its facts.
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

// The twin. It answers a read plan from the whole world, and only what the plan
// asked: the records of the ids and lines the `related` queries name, what each
// follow of them reaches, and the members with their ready counts. A plan that
// did not ask a thing does not get it, so that a plan that reads what it did not
// ask for fails where it reads it.
//
// The follows are the design's (1.0): work is the primary's live work card (the
// one its `work` field names), rcards its read cards (the ids of its `rcards`),
// merge its merge card, control its stream's control card, needs the records of
// the needs it names. jopen is the judgments open, or held by a wait, on each
// record the query returned, on the stream of each primary and on the sprint
// (the design's own words for R16's read: "jopen of the card, its stream and the
// sprint"); which the partial snapshot has no field for, so the twin sets them
// on the snapshot as the caller of a plan does (Snapshot.Open, Snapshot.Acked).

// sourceIDs is the ids a related query's source names in the world: the ids it
// lists, or the window of the line it reads.
func (w *hworld) sourceIDs(src IDSource) []string {
	if src.Kind == SourceIDs {
		return src.IDs
	}
	if src.Kind != SourceLine {
		panic("twin: a source of kind " + src.Kind)
	}
	line := w.lines[src.Seq]
	from, n := src.lineWindow()
	from = min(from, len(line))
	return slices.Clone(line[from:min(len(line), from+n)])
}

func (w *hworld) answer(rp ReadPlan) (ReadAnswer, map[string]bool) {
	ans := ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: "1790000000123"}
	subjects := map[string]bool{}
	for _, q := range rp.Sprint {
		a := Answer{Kind: q.Kind}
		added := map[string]bool{}
		add := func(t string, c *Card) {
			if c != nil && !added[t+"/"+c.ID] {
				added[t+"/"+c.ID] = true
				a.Records = append(a.Records, TableCard{Table: t, Card: c})
			}
		}
		switch q.Kind {
		case QueryRelated:
			ids := w.sourceIDs(q.Source)
			if q.Source.Kind == SourceLine {
				a.IDs = ids
			}
			for _, id := range ids {
				p := w.s.Work.Card(id)
				if p == nil {
					continue
				}
				add(Work, p)
				for _, f := range q.Follow {
					switch f {
					case FollowWork:
						wid := p.Fields["work"]
						if wid == "" {
							wid = WorkCardID(p.ID, p.Int("attempt"))
						}
						add(Fleet, w.s.Fleet.Card(wid))
					case FollowRCards:
						for _, rid := range Split(p.Fields["rcards"]) {
							add(Readers, w.s.Readers.Card(rid))
						}
					case FollowMerge:
						add(Merge, w.s.Merge.Card(p.ID))
					case FollowControl:
						if p.Row != "" {
							add(Merge, w.s.Merge.Card(CtlID(p.Row)))
						}
					case FollowNeeds:
						for _, n := range Split(p.Fields["needs"]) {
							add(Work, w.s.Work.Card(n))
						}
					}
				}
				if slices.Contains(q.Follow, FollowJOpen) {
					if p.Row != "" {
						subjects[StreamSubject(p.Row)] = true
					}
					subjects["sprint"], subjects[SprintSubject] = true, true
				}
			}
			if slices.Contains(q.Follow, FollowJOpen) {
				for _, r := range a.Records {
					subjects[r.Card.ID] = true
				}
			}
		case QueryFleet:
			a.Rows = slices.Clone(w.s.Fleet.Rows())
			for _, m := range a.Rows {
				add(Fleet, w.s.Fleet.Card(CtlID(m)))
				a.Counts = append(a.Counts, CellCount{Row: m, Col: Ready, N: w.s.Fleet.Count(m, Ready)})
			}
		default:
			panic("twin: R16 asked a query of kind " + q.Kind)
		}
		ans.Sprint = append(ans.Sprint, a)
	}
	return ans, subjects
}

// load is the snapshot the tick has after the read: the plan's answer, loaded,
// with the judgments the plan's jopen reached set on it by the caller.
func (w *hworld) load(t testing.TB, rp ReadPlan) *Snapshot {
	t.Helper()
	if slots := rp.TsetSlots(); len(slots) != 0 {
		t.Fatalf("R16's read asks Layer 1 or Layer 2 queries: %+v", slots)
	}
	ans, subjects := w.answer(rp)
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatalf("the answer does not answer the plan: %v", err)
	}
	s.Now = w.s.Now
	for _, o := range w.s.Open {
		if subjects[o.Subject()] {
			s.Open = append(s.Open, o)
		}
	}
	for _, o := range w.f.Held {
		if subjects[o.Subject()] {
			s.Acked = append(s.Acked, o)
		}
	}
	return s
}

// heldRule is the rule the registry holds for held.
func heldRule(t testing.TB) Rule {
	t.Helper()
	for _, r := range RuleTable() {
		if r.Name == ruleHeld {
			return r
		}
	}
	t.Fatal("the rule table has no held rule")
	return Rule{}
}

// readOf is the read of the rule for the keys at layer 1's bounds, and the keys
// it took, in the order they came.
func readOf(t testing.TB, keys []AgendaKey, halvings int) (ReadPlan, []AgendaKey) {
	t.Helper()
	rp, left := heldRule(t).Read(keys, L1ReadBounds(), halvings)
	var taken []AgendaKey
	for _, k := range keys {
		if !slices.Contains(left, k) {
			taken = append(taken, k)
		}
	}
	return rp, taken
}

// partialView is the view of the snapshot the rule's read of the keys gives.
func (w *hworld) partialView(t testing.TB, keys ...AgendaKey) *holdView {
	t.Helper()
	rp, _ := readOf(t, keys, 0)
	s := w.load(t, rp)
	return newHoldView(s, heldFactsOf(s), w.now)
}

// viaRule is the plan of the registered rule for the keys, run as the tick runs
// it: read, answer, load, plan.
func (w *hworld) viaRule(t testing.TB, keys ...AgendaKey) RulePlan {
	t.Helper()
	rp, taken := readOf(t, keys, 0)
	s := w.load(t, rp)
	plan := heldRule(t).Plan(s, taken, w.now)
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("the plan read what its read did not ask for: %v", err)
	}
	return plan
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
	"a sentinel, not reached / cards before it are open": func(w *hworld) string {
		w.primary("p1", "s1", Ready, 1, "attempt", "1")
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel")
		return "g1"
	},
	"a sentinel, not reached / each need of its own is open on the table, or missing with its judgment": func(w *hworld) string {
		w.stream("s2", StreamWaiting)
		w.primary("n1", "s2", Ready, 1, "attempt", "1")
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1")
		return "g1"
	},
	"a sentinel, not reached / R4, a landed or removed need still counted": func(w *hworld) string {
		w.stream("s2", StreamWaiting)
		w.primary("n1", "s2", Landed, 1)
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1")
		return "g1"
	},
	"a sentinel, not reached / a blocked judgment on it, open or held": func(w *hworld) string {
		w.primary("n1", "", "", 0, "outcome", "dropped")
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1")
		w.judge("g1", NBlocked)
		return "g1"
	},
	"a sentinel, not reached / its stream's front was not read": func(w *hworld) string {
		w.blind = true
		w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1") // n1 has no record
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
			for i := range heldReadyPerMember {
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
		w.work(p, "m1", Working, heldDueUnfinished, strconv.FormatInt(hR+1000, 10))
		return "p1"
	},
	"working / R2, its member is down or held with its card": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m2", Ready, heldDueUntaken, strconv.FormatInt(hR+1000, 10))
		w.s.Fleet.Card(CtlID("m2")).Fields["status"] = Down
		return "p1"
	},
	"working / R11, its due has passed": func(w *hworld) string {
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m1", Working, heldDueUnfinished, strconv.FormatInt(hR-1, 10))
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
		w.read(p, "r1", Asked, heldDueUnbegun, strconv.FormatInt(hR+1000, 10))
		return "p1"
	},
	"review / R11, a read's due has passed": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		w.read(p, "r1", Reading, heldDueUnreported, strconv.FormatInt(hR-1, 10))
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
	"review / a read's lateness judgment, open or held": func(w *hworld) string {
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
		r := w.read(p, "r1", OK, "head", "h1") // it has reported since; its lateness judgment stands
		w.judge(r.ID, NReadLate)
		return "p1"
	},
	"merging / queued in a stream merging or waiting, before its merge-idle due": func(w *hworld) string {
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = StreamMerging
		w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(hR+1000, 10)
		return "p1"
	},
	"merging / R11, its stream's merge-idle due has passed": func(w *hworld) string {
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = StreamMerging
		w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(hR-1, 10)
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
	"a sentinel, not reached": func(w *hworld) string {
		w.primary("g1", "s1", Waiting, 1, "kind", "sentinel", "needs", "n1", "open", "1") // n1 has no record and no judgment names it
		return "g1"
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
		w.ctl("s1").Fields["state"] = StreamMerging // queued in a stream that merges, and no deadline set: nothing holds it
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
			w.tb = t
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
			rp := w.plan(hkeys(id)...)
			if !w.usesFacts() {
				rp = w.viaRule(t, hkeys(id)...)
			}
			if len(rp.Notes) != 0 || len(rp.Plan.Units) != 0 {
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
	for _, rp := range []RulePlan{w.plan(hkeys("p1")...), w.viaRule(t, hkeys("p1")...)} {
		if !writesNothing(rp) || len(rp.Done) != 1 {
			t.Fatalf("a card held by R4's condition is judged or kept: %+v", rp)
		}
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
	// the ones every stall offers, and guarded at its place and revision. The
	// registered rule names it too, from the snapshot its own read loaded,
	// wherever the state is one the snapshot can carry.
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
			if !slices.Equal(tail, stalledTail) {
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
		if !w.usesFacts() && row.Name != "a sentinel, not reached" {
			if got := w.viaRule(t, hkeys(id)...); !reflect.DeepEqual(got, rp) {
				t.Errorf("%q: the registered rule plans %+v, planHeld %+v", row.Name, got, rp)
			}
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
	w.primary("p5", "s1", Waiting, 5, "needs", "gone", "open", "1")
	w.primary("p6", "s1", Ready, 6, "attempt", "3", "bound", "redeals")
	w.primary("p7", "s1", Waiting, 7, "needs", "gone", "open", "1", "waived", "")
	w.judge("p6", NBound) // p6 is named already: held, and not named again
	for name, rp := range map[string]RulePlan{
		"planHeld": w.plan(hkeys("p7", "p6", "p5", "p4", "p3", "p2", "p1")...),
		"the rule": w.viaRule(t, hkeys("p7", "p6", "p5", "p4", "p3", "p2", "p1")...),
	} {
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
			t.Fatalf("%s: the notes of a sprint's silent cards: %v, want %v", name, got, want)
		}
		if len(rp.Plan.Units) != 6 || len(rp.Done) != 7 {
			t.Fatalf("%s: guards %d, done %d", name, len(rp.Plan.Units), len(rp.Done))
		}
	}
}

func TestHeldRefusedCardInReviewIsNamedByItsRowAndOnceOnly(t *testing.T) {
	t.Parallel()
	// a primary in review that the machine refused to move, whose reads are also
	// done without two oks, is named by the row that takes it first (refused), not
	// by review's judgment: naming it "reads exhausted" would leave it stalled
	// again once J had opened that, and the second run would name it a second time
	w := newHWorld()
	p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1", "refused", "R9: no")
	w.read(p, "r1", OK, "head", "h1")
	w.read(p, "r2", OK, "head", "h0")
	first := w.viaRule(t, hkeys("p1")...)
	if len(first.Notes) != 1 || first.Notes[0].Type != NStalled || first.Notes[0].Cause != "refused" {
		t.Fatalf("the first run: %+v", first.Notes)
	}
	w.apply(first)
	if second := w.viaRule(t, hkeys("p1")...); !writesNothing(second) {
		t.Fatalf("the second run writes: %+v", second)
	}
	// the same for a card at its bound in review
	w = newHWorld()
	p = w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1", "bound", "attempts")
	w.read(p, "r1", OK, "head", "h1")
	first = w.viaRule(t, hkeys("p1")...)
	if len(first.Notes) != 1 || first.Notes[0].Cause != "bound" {
		t.Fatalf("at its bound: %+v", first.Notes)
	}
	w.apply(first)
	if second := w.viaRule(t, hkeys("p1")...); !writesNothing(second) {
		t.Fatalf("at its bound, the second run writes: %+v", second)
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

func TestHeldRuleShape(t *testing.T) {
	t.Parallel()
	r := heldRule(t)
	// 1.4.2 puts R16 last, and IT05's table counts eighteen places
	if p, ok := PriorityOf(ruleHeld); !ok || p != 18 || r.Priority != 18 || r.MaxSteps != 0 || r.Read == nil || r.Plan == nil {
		t.Fatalf("the rule as registered: %+v, PriorityOf %d %v", r, p, ok)
	}
	// R16 comes after every rule of the tick in priority (1.4.2)
	for _, o := range RuleTable() {
		if o.Name != ruleHeld && o.Priority >= r.Priority {
			t.Errorf("rule %s has priority %d, not before held's %d", o.Name, o.Priority, r.Priority)
		}
	}
	// the numbers of the design that R16 and the holder carry
	if HeldChunk != 2000 || HeldBacklogBound != 5000 || heldReadyPerMember != 2 || heldMaxAttempts != 3 {
		t.Fatalf("the design's numbers: chunk %d (2,000), backlog %d (5,000), ready cap %d (2), attempts %d (3)",
			HeldChunk, HeldBacklogBound, heldReadyPerMember, heldMaxAttempts)
	}
}

func TestHeldRegisteredRulePlansAStall(t *testing.T) {
	t.Parallel()
	// The registered Plan, on the snapshot its own read loaded and nothing else
	// (a stall with no fact beside the tables), opens one note of the row's
	// cause and one guard, and its second run, on the state the first left,
	// writes nothing.
	w := newHWorld()
	id := stallFixtures["working"](w)
	keys := hkeys(id)
	first := w.viaRule(t, keys...)
	if len(first.Notes) != 1 || first.Notes[0].Type != NStalled || first.Notes[0].Cause != "working" ||
		!slices.Equal(first.Notes[0].Subjects, []string{id}) || len(first.Plan.Units) != 1 || first.Plan.Units[0].Key != id ||
		len(first.Done) != 1 || len(first.Requeue) != 0 || len(first.HeldBack) != 0 {
		t.Fatalf("the first run of the registered rule on a working stall: %+v", first)
	}
	w.apply(first)
	if second := w.viaRule(t, keys...); !writesNothing(second) || len(second.Done) != 1 {
		t.Fatalf("the second run of the registered rule writes: %+v", second)
	}
}

func TestHeldOverBacklogRequeues(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	a := stallFixtures["working"](w)
	keys := []AgendaKey{{Key: "held:" + a, Seq: 7}, {Key: "held@9", Seq: 8}}
	w.f.Lines[HeldLineAt{Line: 9}] = HeldLine{IDs: []string{a}}

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
	// first left, writes nothing; through planHeld with the facts, and, where
	// the state is one the snapshot carries, through the registered rule read
	// and answered as the tick reads it, both runs
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
		if !w.usesFacts() && row.Name != "a sentinel, not reached" {
			if viaFirst := w.viaRule(t, keys...); len(viaFirst.Notes) == 0 {
				t.Fatalf("%q: the first run of the registered rule names nothing", row.Name)
			}
		}
		w.apply(first)
		second := w.plan(keys...)
		if !writesNothing(second) {
			t.Errorf("%q: the second run writes: %+v", row.Name, second)
		}
		if !w.usesFacts() {
			if third := w.viaRule(t, keys...); !writesNothing(third) {
				t.Errorf("%q: the second run of the registered rule writes: %+v", row.Name, third)
			}
		}
	}
	// a held state is quiet at once, and twice
	w := newHWorld()
	id := holdFixtures["merging / R5, its cross need landed"](w)
	for i := range 2 {
		if rp := w.plan(hkeys(id)...); !writesNothing(rp) {
			t.Fatalf("run %d over a held card writes: %+v", i+1, rp)
		}
		if rp := w.viaRule(t, hkeys(id)...); !writesNothing(rp) {
			t.Fatalf("run %d of the registered rule over a held card writes: %+v", i+1, rp)
		}
	}
	// a hold that stands over a judgment keeps the card held, and named
	w = newHWorld()
	id = stallFixtures["working"](w)
	w.hold(w.s.Work.Card(id).F("work"), NWorkLate)
	if hd := w.holder(id); hd.By != HeldByJudgment {
		t.Fatalf("a hold on the lateness judgment: (%s) %s", hd.By, hd.Why)
	}
	if rp := w.viaRule(t, hkeys(id)...); !writesNothing(rp) {
		t.Fatalf("the registered rule judges a card a hold names: %+v", rp)
	}
}

func TestHeldLineCutRequeuesAtItsOffset(t *testing.T) {
	t.Parallel()
	w := newHWorld()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		w.primary(id, "s1", Landed, 1)
	}
	key := AgendaKey{Key: "held@9", Seq: 40}
	// the read named the first two ids of a line of five, and stopped at its limit
	w.f.Lines[HeldLineAt{Line: 9}] = HeldLine{IDs: []string{"a", "b"}, More: true}
	rp := w.plan(key)
	want := AgendaKey{Key: "held@9+2", Seq: 40}
	if !slices.Equal(rp.Done, []AgendaKey{key}) || !slices.Equal(rp.Requeue, []AgendaKey{want}) {
		t.Fatalf("a line cut at two of five: done %+v requeue %+v", rp.Done, rp.Requeue)
	}
	// resumed at the offset, and cut again at four
	w.f.Lines[HeldLineAt{Line: 9, Offset: 2}] = HeldLine{IDs: []string{"c", "d"}, More: true}
	rp = w.plan(want)
	if !slices.Equal(rp.Requeue, []AgendaKey{{Key: "held@9+4", Seq: 40}}) {
		t.Fatalf("resumed at 2: %+v", rp.Requeue)
	}
	// the last id: nothing is left, and the key goes
	w.f.Lines[HeldLineAt{Line: 9, Offset: 4}] = HeldLine{IDs: []string{"e"}}
	rp = w.plan(AgendaKey{Key: "held@9+4", Seq: 40})
	if len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("the end of the line: %+v", rp)
	}
	// a read that gave no ids ends the key, so that the key never spins
	w.f.Lines[HeldLineAt{Line: 9, Offset: 2}] = HeldLine{More: true}
	rp = w.plan(want)
	if len(rp.Requeue) != 0 || len(rp.Done) != 1 {
		t.Fatalf("a line that gave nothing: %+v", rp)
	}
	// a line the snapshot does not carry is held back, and not put back
	// unchanged to stand at the head of the queue
	rp = w.plan(AgendaKey{Key: "held@77", Seq: 1})
	if len(rp.Done) != 0 || len(rp.Requeue) != 0 || len(rp.HeldBack) != 1 {
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

func TestHeldEveryKeyTheRuleNamesIsServedByHeld(t *testing.T) {
	t.Parallel()
	// (d) a key R16 finishes, puts back or holds back is a key of the rule the
	// registry holds for it, at the priority of the table: ServingRule says
	// held, and the registered rule is the one PriorityOf gives.
	w := newHWorld()
	for _, id := range []string{"a", "b", "c", "d"} {
		w.primary(id, "s1", Landed, 1)
	}
	w.f.Lines[HeldLineAt{Line: 9}] = HeldLine{IDs: []string{"a", "b"}, More: true}
	keys := []AgendaKey{{Key: "held:a", Seq: 1}, {Key: "held@9", Seq: 2}, {Key: "held@77", Seq: 3}}
	rp := w.plan(keys...)
	var named []AgendaKey
	named = append(append(append(named, rp.Done...), rp.Requeue...), rp.HeldBack...)
	if len(named) != 4 {
		t.Fatalf("the keys the plan names: %+v", rp)
	}
	r := heldRule(t)
	for _, k := range named {
		if got := ServingRule(k.Key); got != r.Name {
			t.Errorf("ServingRule(%q) = %q, not %q", k.Key, got, r.Name)
		}
		if p, ok := PriorityOf(ServingRule(k.Key)); !ok || p != r.Priority {
			t.Errorf("%q: priority %d %v, the registered rule's %d", k.Key, p, ok, r.Priority)
		}
	}
	// and every key of the rule ingest makes is one it reads back and gives back
	in := Ingest([]Event{
		EventOf(5, Line{Kind: LineMove, Table: Work, Card: "p1", Primary: "p1", Stream: "s1", From: "s1:waiting", To: "s1:waiting",
			Set: map[string]string{"open": "1"}}),
		EventOf(100, Line{Kind: LineMove, Table: Work, Card: "p1", Cards: []string{"p1", "p2", "p3"}, Stream: "s1", From: "s1:waiting", To: "s1:ready"}),
	})
	held := map[string]bool{}
	for _, k := range in.Keys {
		if RuleOf(k.Key) != ruleHeld {
			continue
		}
		held[k.Key] = true
		hk, ok := parseHeldKey(k)
		if !ok || hk.agenda(k.Seq) != k {
			t.Errorf("ingest made %+v, which the rule reads as %+v and gives back as %+v", k, hk, hk.agenda(k.Seq))
		}
		if got := ServingRule(k.Key); got != r.Name {
			t.Errorf("ServingRule(%q) = %q", k.Key, got)
		}
	}
	if !held["held:p1"] || !held["held@100"] {
		t.Fatalf("ingest made these keys of the held rule from a card's line and a line of cards: %v (%+v)", held, in.Keys)
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
	if rp := w.viaRule(t, hkeys(id)...); !writesNothing(rp) {
		t.Fatalf("R16 names a stall while STOPPED: %+v", rp)
	}
	w.now.Running = true
	if rp := w.viaRule(t, hkeys(id)...); len(rp.Notes) != 1 {
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
	w.primary("p1", "s1", Review, 1, "attempt", strconv.Itoa(heldMaxAttempts), "result", "failed", "head", "h1")
	if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R10, ") || !strings.Contains(hd.Why, "sets its bound") {
		t.Fatalf("failed at the last attempt: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldReworkWordsFollowTheAttemptBound(t *testing.T) {
	t.Parallel()
	// R10 reworks a failed primary below its third attempt and sets its bound
	// at the third; both are R10's, and the words say which. The bound is 3 (2.3 R10).
	for attempt, want := range map[int]string{1: "is reworked", 2: "is reworked", 3: "sets its bound"} {
		w := newHWorld()
		w.primary("p1", "s1", Review, 1, "attempt", strconv.Itoa(attempt), "result", "failed", "head", "h1")
		hd := w.holder("p1")
		if hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R10, failed or broken: attempt "+strconv.Itoa(attempt)+" "+want) {
			t.Errorf("attempt %d: (%s) %s, want R10 and %q", attempt, hd.By, hd.Why, want)
		}
	}
	// a broken read is R10's as well
	w := newHWorld()
	p := w.primary("p1", "s1", Review, 1, "attempt", "2", "result", "ok", "head", "h1")
	w.read(p, "r1", Broken)
	if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R10, ") {
		t.Errorf("a broken read: (%s) %s", hd.By, hd.Why)
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
	// through the rule's read, whose jopen follow reaches the sprint's judgments
	if rp := w.viaRule(t, hkeys("p1", "p2")...); !writesNothing(rp) {
		t.Fatalf("the rule judges a ready card held by R6's judgment: %+v", rp)
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
	rp := w.viaRule(t, hkeys("done", "gone", "ghost")...)
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

// heldCardsPerRead is the cards one read names at layer 1's bounds: the bytes bind.
// Each card is 1 + 4 (work, merge, control, jopen) + 15 (rcards) + 64 (needs) = 84
// records of 128 + 32 x 26 = 960 bytes (80,640), and fleet 250 records of
// 128 + 32 = 160 bytes (40,000), against 10,000 records and 8 MiB: 116 by the
// records, 103 by the bytes.
const heldCardsPerRead = 103

func TestHeldReadIsSizedByDeclaredCost(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	var keys []AgendaKey
	for i := range 3000 {
		keys = append(keys, AgendaKey{Key: "held:p" + strconv.Itoa(i), Seq: uint64(i)})
	}
	rp, left := readHeld(keys, b, 0)
	n := heldReadCards(b, 0)
	if n != heldCardsPerRead {
		t.Fatalf("a read of %d cards at layer 1's bounds, want %d", n, heldCardsPerRead)
	}
	if !within(rp.Queries(), rp.Cost(), b) {
		t.Fatalf("the read costs %+v in %d queries, over the bounds %+v", rp.Cost(), rp.Queries(), b)
	}
	if len(keys)-len(left) != n || !slices.Equal(left, keys[n:]) {
		t.Fatalf("the read took %d of %d keys, want %d, the rest in order", len(keys)-len(left), len(keys), n)
	}
	// declared: 1 + work, rcards (15), merge, control, needs (64), jopen = 84 an id, and
	// fleet at 250
	if per := QueryCost(heldRelatedIDs([]string{"x"})).Records; per != 84 {
		t.Fatalf("a card and its follows are declared at %d records", per)
	}
	if fixed := QueryCost(heldFleet()).Records; fixed != 250 {
		t.Fatalf("the fleet is declared at %d records", fixed)
	}
	kinds := map[string]int{}
	for _, q := range rp.Sprint {
		kinds[q.Kind]++
		if q.Kind == QueryRelated && (!slices.Equal(q.Fields, heldFields) || !slices.Equal(q.Follow, heldFollows) || q.Table != Work) {
			t.Errorf("a related query without R16's fields and follows: %+v", q)
		}
	}
	if kinds[QueryRelated] != 1 || kinds[QueryFleet] != 1 || len(rp.Sprint) != 2 {
		t.Fatalf("the queries: %v", kinds)
	}
	// with few keys the read takes all of them, and with none it asks nothing
	rp, left = readHeld(keys[:5], b, 0)
	if len(left) != 0 || len(rp.Sprint[0].Source.IDs) != 5 {
		t.Fatalf("five keys: %+v, left %d", rp.Sprint, len(left))
	}
	if rp, left = readHeld(nil, b, 0); len(rp.Sprint) != 0 || len(left) != 0 {
		t.Fatalf("no keys: %+v", rp)
	}
	// the records alone would allow 116, and the bytes are what stop it at 103
	if rec := (b.Records - 250) / 84; rec != 116 {
		t.Fatalf("by records %d", rec)
	}
}

func TestHeldReadHalvings(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	n := heldReadCards(b, 0)
	for h := 1; h <= 8; h++ {
		if got, want := heldReadCards(b, h), Halved(n, h); got != want || got < 1 {
			t.Errorf("%d halvings: %d cards, want %d", h, got, want)
		}
	}
	if heldReadCards(b, 8) != 1 || heldReadCards(b, 1) != 52 {
		t.Errorf("halved from %d: 1 halving %d, 8 halvings %d", n, heldReadCards(b, 1), heldReadCards(b, 8))
	}
	// with the chunk the binding limit, and never below one card
	big := ReadBounds{Queries: 1024, Records: 10_000_000, Bytes: 1 << 40}
	if got := heldReadCards(big, 0); got != HeldChunk {
		t.Errorf("the chunk is %d cards, got %d", HeldChunk, got)
	}
	if got := heldReadCards(ReadBounds{Records: 1}, 0); got != 1 {
		t.Errorf("no room at all still reads one card, got %d", got)
	}
	if got := heldReadCards(ReadBounds{}, 0); got != HeldChunk {
		t.Errorf("no bound at all is the chunk (0 is no bound), got %d", got)
	}
	// the read after a halving is half of what it was
	var keys []AgendaKey
	for i := range 500 {
		keys = append(keys, AgendaKey{Key: "held:p" + strconv.Itoa(i), Seq: uint64(i)})
	}
	rp, left := readHeld(keys, b, 2)
	if got := len(rp.Sprint[0].Source.IDs); got != Halved(n, 2) || len(keys)-len(left) != got {
		t.Errorf("two halvings read %d ids, want %d", got, Halved(n, 2))
	}
}

func TestHeldReadOfLines(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	n := heldReadCards(b, 0)
	keys := []AgendaKey{
		{Key: "held:a", Seq: 1},
		{Key: "held@7+100", Seq: 2},
		{Key: "held:b", Seq: 3},
	}
	rp, left := readHeld(keys, b, 0)
	var line *SprintQ
	for i := range rp.Sprint {
		if rp.Sprint[i].Source.Kind == SourceLine {
			line = &rp.Sprint[i]
		}
	}
	// the two id keys take a place each, and the line the rest of the room
	if line == nil || line.Source.Seq != 7 || line.Source.Offset != 100 || line.Source.Limit != n-2 || len(left) != 0 {
		t.Fatalf("the line is read from its offset for the room left: %+v, left %+v", line, left)
	}
	// a foreign key stays out of the read and keeps its place among the rest
	rp, left = readHeld([]AgendaKey{{Key: "deal", Seq: 1}, {Key: "held:a", Seq: 2}}, b, 0)
	if !slices.Equal(left, []AgendaKey{{Key: "deal", Seq: 1}}) || len(rp.Sprint[0].Source.IDs) != 1 {
		t.Fatalf("a foreign key: %+v %+v", rp.Sprint, left)
	}
	// and the queries are bounded: with room for no more, a line waits, and so
	// does every key behind it
	rp, left = readHeld(keys, ReadBounds{Queries: heldFixedQueries, Records: 10_000}, 0)
	if !slices.Equal(left, keys[1:]) || len(rp.Sprint[0].Source.IDs) != 1 {
		t.Fatalf("no query to spare for a line: %+v left %+v", rp.Sprint, left)
	}
}

func TestHeldReadServesManyLineKeysATick(t *testing.T) {
	t.Parallel()
	// ten line keys at the head of the queue, each of a line of 2,000 ids, are
	// all served in one tick, each for its share of the room, with the keys
	// behind them (one card) after; none takes the room the others need
	b := L1ReadBounds()
	room := heldReadCards(b, 0)
	var keys []AgendaKey
	for i := range 10 {
		keys = append(keys, AgendaKey{Key: "held@" + strconv.Itoa(100+i), Seq: uint64(i + 1)})
	}
	keys = append(keys, AgendaKey{Key: "held:tail", Seq: 11})
	rp, left := readHeld(keys, b, 0)
	if len(left) != 0 {
		t.Fatalf("keys left for a later tick: %+v", left)
	}
	if !within(rp.Queries(), rp.Cost(), b) {
		t.Fatalf("the read costs %+v in %d queries, over the bounds", rp.Cost(), rp.Queries())
	}
	total, lines := 0, 0
	seen := map[uint64]bool{}
	for _, q := range rp.Sprint {
		switch q.Source.Kind {
		case SourceLine:
			lines++
			seen[q.Source.Seq] = true
			if q.Source.Limit < 1 {
				t.Errorf("a line key with no share of the room: %+v", q.Source)
			}
			total += q.Source.Limit
		case SourceIDs:
			total += len(q.Source.IDs)
		}
	}
	if lines != 10 || len(seen) != 10 || total != room {
		t.Fatalf("%d lines read (%d distinct), %d ids named of a room of %d", lines, len(seen), total, room)
	}
	// and the shares are level: no line has more than one id above another
	lo, hi := 1<<30, 0
	for _, q := range rp.Sprint {
		if q.Source.Kind == SourceLine {
			lo, hi = min(lo, q.Source.Limit), max(hi, q.Source.Limit)
		}
	}
	if hi-lo > 1 {
		t.Errorf("shares from %d to %d ids", lo, hi)
	}

	// the same through the twin: ten lines, each cut where the read stopped,
	// requeued at its offset in the order it came, and the card behind judged
	w := newHWorld()
	for i := range 10 {
		var ids []string
		for j := range 300 {
			id := fmt.Sprintf("l%dc%d", i, j)
			w.primary(id, "s1", Landed, float64(i*1000+j))
			ids = append(ids, id)
		}
		w.lines[uint64(100+i)] = ids
	}
	w.primary("tail", "s1", Landed, 1)
	plan := w.viaRule(t, keys...)
	if len(plan.Done) != 11 || len(plan.Requeue) != 10 || len(plan.HeldBack) != 0 {
		t.Fatalf("done %d, requeued %d, held back %d of 11 keys", len(plan.Done), len(plan.Requeue), len(plan.HeldBack))
	}
	for i, k := range plan.Requeue {
		want := "held@" + strconv.Itoa(100+i) + "+"
		if !strings.HasPrefix(k.Key, want) || k.Seq != uint64(i+1) {
			t.Errorf("requeue %d is %+v, want %s<offset> at the order %d", i, k, want, i+1)
		}
		hk, _ := parseHeldKey(k)
		if hk.Offset < 1 || hk.Offset > room {
			t.Errorf("requeued at the offset %d", hk.Offset)
		}
	}
	// the next tick resumes each at its offset, with a read of the requeued keys
	next, _ := readOf(t, plan.Requeue, 0)
	for i, q := range next.Sprint {
		if q.Kind != QueryRelated {
			continue
		}
		hk, _ := parseHeldKey(plan.Requeue[i])
		if q.Source.Seq != hk.Line || q.Source.Offset != hk.Offset {
			t.Errorf("the next read of %+v is %+v", plan.Requeue[i], q.Source)
		}
	}
}

func TestHeldReadNamesThePrimaryOfAWorkOrReadCard(t *testing.T) {
	t.Parallel()
	// a key that names a work card or a read card is read as its primary, once
	// however many of its cards are named, and judged as it
	keys := hkeys("p1.w2", "p1.r1.r2", "p1", "p2.r3.r1")
	rp, taken := readOf(t, keys, 0)
	if len(taken) != len(keys) {
		t.Fatalf("the read took %d of %d keys", len(taken), len(keys))
	}
	if ids := rp.Sprint[0].Source.IDs; !slices.Equal(ids, []string{"p1", "p2"}) {
		t.Fatalf("the ids the read names: %v", ids)
	}
	w := newHWorld()
	p := w.primary("p1", "s1", Working, 1, "attempt", "2")
	w.work(p, "m1", Working)
	wc := WorkCardID("p1", 2)
	plan := w.viaRule(t, hkeys(wc, "p1")...)
	if len(plan.Notes) != 1 || !slices.Equal(plan.Notes[0].Subjects, []string{"p1"}) || len(plan.Done) != 2 {
		t.Fatalf("a work card and its primary are one judgment of the primary: %+v", plan)
	}
	// a primary's own id is not read as another's, whatever it looks like
	if heldPrimaryID("p1") != "p1" || heldPrimaryID("p1.w2") != "p1" || heldPrimaryID("p1.r1.r2") != "p1" || heldPrimaryID("stream:s1") != "stream:s1" {
		t.Fatal("the primary of an id by its shape")
	}
}

func TestHeldKeyTheReadDidNotCarryIsHeldBack(t *testing.T) {
	t.Parallel()
	// the plan is for a read of p1 and the line 9; a key of a card or a line
	// that read did not name is held back, and not finished (nothing was read
	// of it) nor put back unchanged (it would stand at the head)
	w := newHWorld()
	w.primary("p1", "s1", Landed, 1)
	w.primary("p2", "s1", Landed, 2)
	w.lines[9] = []string{"p1"}
	rp, _ := readOf(t, []AgendaKey{{Key: "held:p1", Seq: 1}, {Key: "held@9", Seq: 2}}, 0)
	s := w.load(t, rp)
	plan := heldRule(t).Plan(s, []AgendaKey{{Key: "held:p1", Seq: 1}, {Key: "held@9", Seq: 2}, {Key: "held:p2", Seq: 3},
		{Key: "held@10", Seq: 4}, {Key: "held@9+5", Seq: 5}, {Key: "held:p2.w1", Seq: 6}}, w.now)
	if len(plan.Done) != 2 || len(plan.HeldBack) != 4 || len(plan.Requeue) != 0 {
		t.Fatalf("done %+v, held back %+v, requeued %+v", plan.Done, plan.HeldBack, plan.Requeue)
	}
	// a card the read named and the store does not have is off the table: done
	plan = heldRule(t).Plan(s, hkeys("p1"), w.now)
	if len(plan.Done) != 1 || len(plan.HeldBack) != 0 {
		t.Fatalf("a card the read named: %+v", plan)
	}
}

func TestHeldFactsOfALineRead(t *testing.T) {
	t.Parallel()
	// a partial snapshot carries what its lines' reads returned, and whether a
	// read stopped at its limit, and a snapshot built whole carries none
	if f := heldFactsOf(newHWorld().s); len(f.Lines) != 0 {
		t.Fatalf("a whole snapshot has lines: %+v", f.Lines)
	}
	w := newHWorld()
	for i := range 6 {
		w.primary("c"+strconv.Itoa(i), "s1", Landed, float64(i))
		w.lines[9] = append(w.lines[9], "c"+strconv.Itoa(i))
	}
	lim := func(seq uint64, offset, limit int) HeldLine {
		q := heldRelatedLine(heldKey{Line: seq, Offset: offset, ByLine: true}, limit)
		s := w.load(t, ReadPlan{Sprint: []SprintQ{q}})
		return heldFactsOf(s).Lines[HeldLineAt{Line: seq, Offset: offset}]
	}
	if l := lim(9, 0, 4); !slices.Equal(l.IDs, []string{"c0", "c1", "c2", "c3"}) || !l.More {
		t.Errorf("a read cut at its limit: %+v", l)
	}
	if l := lim(9, 4, 4); !slices.Equal(l.IDs, []string{"c4", "c5"}) || l.More {
		t.Errorf("a read that reached the end of its line: %+v", l)
	}
	if l := lim(9, 0, 6); !slices.Equal(l.IDs, []string{"c0", "c1", "c2", "c3", "c4", "c5"}) || !l.More {
		t.Errorf("a read that ended where its limit did may have more: %+v", l)
	}
	if l := lim(9, 6, 4); len(l.IDs) != 0 || l.More {
		t.Errorf("a read past the end: %+v", l)
	}
	if l := lim(9, MaxLineIDs-2, 2); l.More {
		t.Errorf("a read to the last id a line may hold has no more: %+v", l)
	}
}

func TestHeldReadsAreThoseOfTheAttempt(t *testing.T) {
	t.Parallel()
	// a read of the last attempt, still outstanding, does not hold a primary
	// that has been reworked and not yet asked at its new attempt: R8 does
	w := newHWorld()
	p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
	w.read(p, "r1", Asked, heldDueUnbegun, strconv.FormatInt(hR+1000, 10))
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

func TestHeldSentinelWithNothingBeforeItAndNeedsNothingHoldsIsAStall(t *testing.T) {
	t.Parallel()
	// (d) is what it waits on, itself held: a sentinel with nothing before it
	// and a need of its own that no judgment names is not held by its count
	// alone, as a primary in the same state is not
	w := newHWorld()
	w.primary("g1", "s1", Waiting, 1, "kind", "sentinel", "needs", "n1", "open", "1")
	w.primary("p1", "s1", Waiting, 2, "needs", "n1", "open", "1")
	g, p := w.verdict("g1"), w.verdict("p1")
	if !g.Stalled() || !p.Stalled() || g.row.Name != "a sentinel, not reached" || p.row.Name != "waiting, open > 0" {
		t.Fatalf("the sentinel %+v (%v), the primary %+v (%v)", g.Hold, g.row, p.Hold, p.row)
	}
	rp := w.plan(hkeys("g1")...)
	if len(rp.Notes) != 1 || rp.Notes[0].Type != NStalled || rp.Notes[0].Cause != "sentinel" {
		t.Fatalf("the sentinel's stall: %+v", rp.Notes)
	}
	// its blocked judgment names it, as it names the primary
	w.judge("g1", NMissingNeed)
	if vd := w.verdict("g1"); vd.Stalled() {
		t.Fatalf("a sentinel named by its blocked judgment: %+v", vd.Hold)
	}
	// with cards before it the sentinel is held whatever its needs
	w = newHWorld()
	w.primary("p0", "s1", Ready, 1, "attempt", "1")
	w.primary("g1", "s1", Waiting, 2, "kind", "sentinel", "needs", "n1", "open", "1")
	if vd := w.verdict("g1"); vd.Stalled() || vd.By != HeldByWaiting || !strings.Contains(vd.Why, "n_before 1") {
		t.Fatalf("a sentinel with a card before it: %+v", vd.Hold)
	}
	// on a snapshot with no front of its stream it cannot be told from that one,
	// and is held: never judged for a position it does not know
	w = newHWorld()
	w.primary("g1", "s1", Waiting, 1, "kind", "sentinel", "needs", "n1", "open", "1")
	if vd := w.partialView(t, hkeys("g1")...).verdict("g1"); vd.Stalled() || !strings.Contains(vd.Why, "front was not read") {
		t.Fatalf("a sentinel on a snapshot with no front: %+v", vd.Hold)
	}
	if rp := w.viaRule(t, hkeys("g1")...); !writesNothing(rp) {
		t.Fatalf("the registered rule judges a sentinel whose front it did not read: %+v", rp)
	}
}

// The eleven states that a mutation pass over the two files found no test for
// (each a one-line change of a condition or a number, run against these tests
// before it was added, and killed by the test named).

func TestHeldCrossNeedThatHasNotLandedHoldsNothing(t *testing.T) {
	t.Parallel()
	// R5's condition is a cross need that has landed: while it has not, or the
	// stream names none, the stopped stream's card is held by nothing but its
	// stop judgment. The read names the need beside the card (the read has no
	// follow that reaches the card a control card names).
	for _, field := range []string{"need_card", "other"} {
		w := newHWorld()
		w.primary("n1", "s1", Ready, 0, "attempt", "1")
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Stuck, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"], w.ctl("s1").Fields["cause"], w.ctl("s1").Fields[field] = StreamStopped, "cross", "n1"
		if vd := w.verdict("p1"); !vd.Stalled() || vd.row.Name != "merging" {
			t.Errorf("a cross need not landed, named by %s: %+v", field, vd.Hold)
		}
		if rp := w.viaRule(t, hkeys("p1", "n1")...); len(rp.Notes) != 1 || !slices.Equal(rp.Notes[0].Subjects, []string{"p1"}) {
			t.Errorf("a cross need not landed, named by %s: the registered rule names %+v", field, rp.Notes)
		}
		// landed, by either field, and R5 holds it
		w.s.Work.Card("n1").Col = Landed
		w.s.Work.Put(w.s.Work.Card("n1"))
		if vd := w.verdict("p1"); vd.By != HeldByTick || !strings.HasPrefix(vd.Why, "R5, ") {
			t.Errorf("a cross need landed (%s): (%s) %s", field, vd.By, vd.Why)
		}
		if rp := w.viaRule(t, hkeys("p1", "n1")...); !writesNothing(rp) {
			t.Errorf("a cross need landed (%s): the registered rule names %+v", field, rp.Notes)
		}
	}
	// a stream stopped for another cause has no cross need to wait for
	w := newHWorld()
	w.primary("n1", "s1", Landed, 0)
	w.primary("p1", "s1", Merging, 1)
	w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Stuck, Rev: 1, Fields: hkv()})
	w.ctl("s1").Fields["state"], w.ctl("s1").Fields["cause"], w.ctl("s1").Fields["need_card"] = StreamStopped, "conflict", "n1"
	if vd := w.verdict("p1"); !vd.Stalled() {
		t.Errorf("a landed card named by a stream stopped for another cause: %+v", vd.Hold)
	}
}

func TestHeldWorkAtAMemberThatIsHeldIsR2s(t *testing.T) {
	t.Parallel()
	// R2's condition is a member down or held: a held member keeps its cards
	// held, and only `fleet up` releases it, so its cards are named by R2
	for _, status := range []string{Down, Held} {
		w := newHWorld()
		p := w.primary("p1", "s1", Working, 1, "attempt", "1")
		w.work(p, "m2", Working, heldDueUnfinished, strconv.FormatInt(hR+1000, 10))
		w.s.Fleet.Card(CtlID("m2")).Fields["status"] = status
		hd := w.holder("p1")
		if hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R2, ") || !strings.Contains(hd.Why, "member m2 is "+status) {
			t.Errorf("work at a member that is %s: (%s) %s", status, hd.By, hd.Why)
		}
		if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
			t.Errorf("the rule judges a card at a member that is %s: %+v", status, rp)
		}
	}
	// at an up member, before its due, it is (a) and not R2
	w := newHWorld()
	p := w.primary("p1", "s1", Working, 1, "attempt", "1")
	w.work(p, "m2", Working, heldDueUnfinished, strconv.FormatInt(hR+1000, 10))
	if hd := w.holder("p1"); hd.By != HeldByActor {
		t.Errorf("work at an up member: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldMergeCardQueuedInAWaitingStreamIsHeld(t *testing.T) {
	t.Parallel()
	// (a) is a merge card queued in a stream merging or waiting before its
	// merge-idle deadline; in a stream stopped it is not
	for state, want := range map[string]string{StreamMerging: HeldByActor, StreamWaiting: HeldByActor, StreamStopped: ""} {
		w := newHWorld()
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = state
		w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(hR+1000, 10)
		hd := w.holder("p1")
		if want != "" && (hd.By != want || !strings.HasPrefix(hd.Why, "queued in a stream merging or waiting")) {
			t.Errorf("a merge card queued in a stream %s: (%s) %s", state, hd.By, hd.Why)
		}
		if want == "" && !hd.Stalled() {
			t.Errorf("a merge card queued in a stream %s: (%s) %s", state, hd.By, hd.Why)
		}
	}
}

func TestHeldMergeIdleDeadlineIsR11sOnlyWhileTheStreamMergesOrWaits(t *testing.T) {
	t.Parallel()
	// R11 raises the merge-idle judgment for a stream merging, or waiting with
	// queued cards; a stream stopped or with no deadline set is not its to name,
	// and a card of it that nothing else holds is a stall
	for state, want := range map[string]string{StreamMerging: HeldByTick, StreamWaiting: HeldByTick, StreamStopped: ""} {
		w := newHWorld()
		w.primary("p1", "s1", Merging, 1)
		w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
		w.ctl("s1").Fields["state"] = state
		w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(hR-1, 10)
		hd := w.holder("p1")
		if want != "" && (hd.By != want || !strings.HasPrefix(hd.Why, "R11, its stream's merge-idle due has passed")) {
			t.Errorf("a stream %s past its merge-idle deadline: (%s) %s", state, hd.By, hd.Why)
		}
		if want == "" && !hd.Stalled() {
			t.Errorf("a stream %s past its merge-idle deadline: (%s) %s", state, hd.By, hd.Why)
		}
	}
}

func TestHeldDeadlinesAreHeldByTheActorBeforeThemAndByR11AtThem(t *testing.T) {
	t.Parallel()
	// at R = due - 1 the outside actor holds; at R = due (a card is late when R
	// is at least its due, 2.3 R11) R11 does: never neither, never both
	type state struct {
		name  string
		build func(w *hworld, due int64)
		actor string // the words of (a)
		late  string // the words of R11's holder
	}
	for _, st := range []state{
		{"work", func(w *hworld, due int64) {
			p := w.primary("p1", "s1", Working, 1, "attempt", "1")
			w.work(p, "m1", Working, heldDueUnfinished, strconv.FormatInt(due, 10))
		}, "its live work card at an up member", "R11, its due has passed"},
		{"work not taken", func(w *hworld, due int64) {
			p := w.primary("p1", "s1", Working, 1, "attempt", "1")
			w.work(p, "m1", Ready, heldDueUntaken, strconv.FormatInt(due, 10))
		}, "its live work card at an up member", "R11, its due has passed"},
		{"read asked", func(w *hworld, due int64) {
			p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", Asked, heldDueUnbegun, strconv.FormatInt(due, 10))
		}, "a read outstanding before its due", "R11, a read's due has passed"},
		{"read reading", func(w *hworld, due int64) {
			p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", Reading, heldDueUnreported, strconv.FormatInt(due, 10))
		}, "a read outstanding before its due", "R11, a read's due has passed"},
		{"merge idle", func(w *hworld, due int64) {
			w.primary("p1", "s1", Merging, 1)
			w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
			w.ctl("s1").Fields["state"] = StreamMerging
			w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(due, 10)
		}, "queued in a stream merging or waiting", "R11, its stream's merge-idle due has passed"},
	} {
		for _, c := range []struct {
			due  int64
			want string
			by   string
		}{{hR + 1, st.actor, HeldByActor}, {hR, st.late, HeldByTick}, {hR - 1, st.late, HeldByTick}} {
			w := newHWorld()
			st.build(w, c.due)
			hd := w.holder("p1")
			if hd.By != c.by || !strings.HasPrefix(hd.Why, c.want) {
				t.Errorf("%s due at R %d, now R %d: (%s) %s, want (%s) %s", st.name, c.due, hR, hd.By, hd.Why, c.by, c.want)
			}
			if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
				t.Errorf("%s: the rule judges a card held by its deadline: %+v", st.name, rp)
			}
		}
	}
}

func TestHeldRedCIAtAnOlderHeadDoesNotStopR9(t *testing.T) {
	t.Parallel()
	// R9 skips a primary whose CI is red at its head; a red CI at an older head
	// is a report about work that is gone, and two oks at the new head are R9's
	build := func(ciHead string) *hworld {
		w := newHWorld()
		p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h2", "ci", "red", "ci_head", ciHead)
		w.read(p, "r1", OK, "head", "h2")
		w.read(p, "r2", OK, "head", "h2")
		return w
	}
	if hd := build("h1").holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R9, ") {
		t.Errorf("red at an older head: (%s) %s", hd.By, hd.Why)
	}
	if vd := build("h2").verdict("p1"); !vd.Stalled() || vd.row.Name != "review" {
		t.Errorf("red at its head, with nothing naming it: %+v", vd.Hold)
	}
	if hd := build("h2").holder("p1"); hd.By != "" {
		t.Errorf("red at its head is R9's no more: (%s) %s", hd.By, hd.Why)
	}
	w := build("h1")
	if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
		t.Errorf("the rule judges a card R9 accepts: %+v", rp)
	}
	// "returned to review" is the coordinator's to decide, and holds it by (c)
	w = build("h1")
	w.judge("p1", NReturned)
	if hd := w.holder("p1"); hd.By != HeldByJudgment {
		t.Errorf("returned to review: (%s) %s", hd.By, hd.Why)
	}
}

func TestHeldADeadReadIsNamedByR11AndItsJudgment(t *testing.T) {
	t.Parallel()
	// a read past its due is R11's; once R11 has judged it the judgment on the
	// read card holds the primary, and R16 raises nothing beside it; the same for
	// a stream past its merge-idle deadline
	w := newHWorld()
	p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
	r := w.read(p, "r1", Asked, heldDueUnbegun, strconv.FormatInt(hR-1, 10))
	for _, when := range []string{"before R11", "after its judgment"} {
		if when != "before R11" {
			w.judge(r.ID, NReadLate)
		}
		hd := w.holder("p1")
		if hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R11, ") {
			t.Errorf("a late read %s: (%s) %s", when, hd.By, hd.Why)
		}
		if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
			t.Errorf("a late read %s: R16 raises %+v", when, rp.Notes)
		}
	}
	// the judgment alone holds it when the read has moved on
	w = newHWorld()
	p = w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1")
	r = w.read(p, "r1", OK, "head", "h1")
	if vd := w.verdict("p1"); !vd.Stalled() {
		t.Errorf("a reported read and nothing naming the primary: %+v", vd.Hold)
	}
	w.hold(r.ID, NReadLate)
	if hd := w.holder("p1"); hd.By != HeldByJudgment || !strings.Contains(hd.Why, NReadLate) {
		t.Errorf("a read's lateness judgment held by a wait: (%s) %s", hd.By, hd.Why)
	}
	if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
		t.Errorf("the rule judges a primary a held read judgment names: %+v", rp)
	}
	// a merge card in a stream past its merge-idle deadline, judged by R11
	w = newHWorld()
	w.primary("p1", "s1", Merging, 1)
	w.s.Merge.Put(&Card{ID: "p1", Row: "s1", Col: Queued, Rev: 1, Fields: hkv()})
	w.ctl("s1").Fields["state"] = StreamMerging
	w.ctl("s1").Fields[heldDueMergeIdle] = strconv.FormatInt(hR-1, 10)
	for _, when := range []string{"before R11", "after its judgment"} {
		if when != "before R11" {
			w.judge(StreamSubject("s1"), NMergeLate)
		}
		if hd := w.holder("p1"); hd.By != HeldByTick || !strings.HasPrefix(hd.Why, "R11, ") {
			t.Errorf("a merge card in a stream past its deadline %s: (%s) %s", when, hd.By, hd.Why)
		}
		if rp := w.viaRule(t, hkeys("p1")...); !writesNothing(rp) {
			t.Errorf("a stream past its merge-idle deadline %s: R16 raises %+v", when, rp.Notes)
		}
	}
}

func TestHeldDecisionsAreThoseOfTheDesign(t *testing.T) {
	t.Parallel()
	// the decisions of a stalled card by its place, as literals (2.2: the
	// decisions its place allows, today's held.decisions, then card, drop, wait),
	// and the two judgments of a review (2.2: reads exhausted, stranded)
	want := map[string][]string{
		"quarantined":                          {"card", "drop", "wait"},
		"of a stream being dropped or removed": {"card", "drop", "wait"},
		"any with refused":                     {"card", "drop", "wait"},
		"ready or review with bound":           {"card", "drop", "wait"},
		"a sentinel, not reached":              {"card", "drop", "wait"},
		"waiting, open > 0":                    {"card", "drop", "wait"},
		"working":                              {"fleet down m1", "card", "drop", "wait"},
		"review":                               {"rework --fix", "card", "drop", "wait"},
		"merging":                              {"return", "card", "drop", "wait"},
		"a place no row of the table gives":    {"card", "drop", "wait"},
	}
	for _, row := range holdRows {
		fix, ok := stallFixtures[row.Name]
		if !ok {
			continue
		}
		w := newHWorld()
		id := fix(w)
		if row.Name == "review" { // the review row's own stall is one R16 keeps no judgment for: CI red at its head
			w = newHWorld()
			p := w.primary("p1", "s1", Review, 1, "attempt", "1", "result", "ok", "head", "h1", "ci", "red", "ci_head", "h1")
			w.read(p, "r1", OK, "head", "h1")
			w.read(p, "r2", OK, "head", "h1")
			id = "p1"
		}
		rp := w.plan(hkeys(id)...)
		if len(rp.Notes) != 1 || rp.Notes[0].Type != NStalled || !slices.Equal(rp.Notes[0].Decisions, want[row.Name]) {
			t.Errorf("%q: the decisions offered %+v, want %v", row.Name, rp.Notes, want[row.Name])
		}
	}
	// a working card's decision names the member its work card sits at, and
	// a note of cards at two members offers one for each, in name order
	w := newHWorld()
	for i, m := range []string{"m2", "m1", "m2"} {
		p := w.primary("p"+strconv.Itoa(i), "s1", Working, float64(i), "attempt", "1")
		w.work(p, m, Working)
	}
	rp := w.plan(hkeys("p0", "p1", "p2")...)
	if len(rp.Notes) != 1 || !slices.Equal(rp.Notes[0].Decisions, []string{"fleet down m1", "fleet down m2", "card", "drop", "wait"}) {
		t.Errorf("cards at two members: %+v", rp.Notes)
	}
	for _, d := range rp.Notes[0].Decisions {
		if strings.Contains(d, "<") {
			t.Errorf("the decision %q is a placeholder", d)
		}
	}
	// no work card is found: the placeholder goes, and the rest stays
	w = newHWorld()
	w.primary("p1", "s1", Working, 1, "attempt", "1")
	rp = w.plan(hkeys("p1")...)
	if len(rp.Notes) != 1 || !slices.Equal(rp.Notes[0].Decisions, []string{"card", "drop", "wait"}) {
		t.Errorf("a working card with no work card: %+v", rp.Notes)
	}
	// the two judgments of review: the words and the decisions of 2.2
	if got := reviewStalls[NReadsExhausted]; !slices.Equal(got.Decisions, []string{"ask --another", "rework --fix", "drop"}) || got.Cause != "exhausted" {
		t.Errorf("reads exhausted: %+v", got)
	}
	if got := reviewStalls[NStranded]; !slices.Equal(got.Decisions, []string{"ask", "rework --fix", "drop"}) || got.Cause != "stranded" {
		t.Errorf("stranded in review: %+v", got)
	}
	if len(reviewStalls) != 2 {
		t.Errorf("R16 keeps the two of reviewJudgment: %v", reviewStalls)
	}
}

func TestHeldReviewStallAgreesWithReviewJudgment(t *testing.T) {
	t.Parallel()
	// the judgment R16 decides from its view (the judgments open on the card by
	// subject, its own read cards) is the one today's reviewJudgment gives, over
	// every combination of the two readers' cards, the primary's result, its
	// judgments and its CI: a state where they differ is a fault of the view
	readStates := []string{"", Asked, Reading, OK, "okOld", Broken}
	results := []string{"", "ok", "failed"}
	judgments := []struct {
		typ         string
		held, level bool
		accept      bool
	}{
		{}, {typ: NCIRed}, {typ: NStalled}, {typ: NReadsExhausted, held: true}, {typ: NReadyToAccept, accept: true},
		{typ: NReturned}, {typ: NBound, level: true},
	}
	checked := 0
	for _, res := range results {
		for _, r1 := range readStates {
			for _, r2 := range readStates {
				for _, j := range judgments {
					for _, attempt := range []string{"1", "2"} {
						w := newHWorld()
						p := w.primary("p1", "s1", Review, 1, "attempt", attempt, "result", res, "head", "h1")
						for i, rs := range []string{r1, r2} {
							reader := "r" + strconv.Itoa(i+1)
							switch rs {
							case "":
							case "okOld":
								w.read(p, reader, OK, "head", "h0")
							case OK:
								w.read(p, reader, OK, "head", "h1")
							default:
								w.read(p, reader, rs)
							}
						}
						// a read of another attempt is not this attempt's
						other := &Card{ID: ReadCardID("p1", 9, "r2"), Row: "r2", Col: Asked, Rev: 1,
							Fields: hkv("kind", "read", "primary", "p1", "reader", "r2")}
						w.s.Readers.Put(other)
						if j.typ != "" {
							note := Note{ID: "n1", Kind: Judgment, Type: j.typ, StreamLevel: j.level}
							if j.accept {
								note.Decisions = []string{"accept", "drop"}
							}
							if j.held {
								w.f.Held = append(w.f.Held, Open{Key: OpenKey("h1", "p1"), Note: note})
							} else {
								w.s.Open = append(w.s.Open, Open{Key: OpenKey("n1", "p1"), Note: note})
							}
						}
						want, wantOK := reviewJudgment(w.s, p, reviewStep{who: MachineActor})
						got, gotOK := w.view().reviewStallType(p)
						if gotOK != wantOK || (gotOK && got != want.Type) {
							t.Fatalf("result %q, reads %q %q, judgment %+v, attempt %s: R16 says %q %v, reviewJudgment %q %v",
								res, r1, r2, j, attempt, got, gotOK, want.Type, wantOK)
						}
						checked++
					}
				}
			}
		}
	}
	if checked != len(results)*len(readStates)*len(readStates)*len(judgments)*2 {
		t.Fatalf("checked %d states", checked)
	}
}

func TestHeldReviewStallsCostTheirOwnJudgments(t *testing.T) {
	t.Parallel()
	// 2,000 primaries stalled in review, beside 8,000 judgments of the sprint on
	// other subjects: what a card costs is the judgments on itself, so the
	// judgments looked at do not grow with the ones beside it (a scan of every
	// open judgment for each card was O(cards x judgments): 282 ms at 8,000)
	probes := func(others int) (int, RulePlan) {
		w := newHWorld()
		var ids []string
		for i := range HeldChunk {
			id := "p" + strconv.Itoa(i)
			p := w.primary(id, "s1", Review, float64(i), "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", OK, "head", "h1")
			w.read(p, "r2", OK, "head", "h0")
			ids = append(ids, id)
		}
		for i := range others {
			w.judge("other"+strconv.Itoa(i), NCIRed)
		}
		v := w.view()
		var stalls int
		for _, id := range ids {
			if vd := v.verdict(id); vd.Stalled() {
				stalls++
				stallOf(v, vd)
			}
		}
		if stalls != HeldChunk {
			t.Fatalf("%d of %d review cards stalled", stalls, HeldChunk)
		}
		return v.probes, w.plan(hkeys(ids...)...)
	}
	base, rp := probes(0)
	if len(rp.Notes) != 1 || rp.Notes[0].Type != NReadsExhausted || len(rp.Notes[0].Subjects) != HeldChunk {
		t.Fatalf("the plan of 2,000 review stalls: %d notes", len(rp.Notes))
	}
	for _, others := range []int{2000, 8000} {
		if got, _ := probes(others); got != base {
			t.Errorf("beside %d judgments on other subjects the plan looked at %d judgments, at none %d", others, got, base)
		}
	}
	if base > 4*HeldChunk {
		t.Errorf("a card looks at %d judgments on average", base/HeldChunk)
	}
}

func TestHeldPlanDoesNotGrowWithTheJudgmentsBesideIt(t *testing.T) {
	t.Parallel()
	// the same 2,000 review stalls beside 8,000 judgments of the sprint on other
	// subjects cost about what they cost beside none: the plan is O(c + j), the
	// index of the judgments, and not O(c x j) (a scan of every open judgment for
	// each card took 282 ms at 8,000, seventy times its time at none). The time
	// beside 8,000 is held to twenty times the time beside none, best of three,
	// which is far from linear growth and far under the quadratic one.
	best := func(others int) time.Duration {
		w := newHWorld()
		var ids []string
		for i := range HeldChunk {
			id := "p" + strconv.Itoa(i)
			p := w.primary(id, "s1", Review, float64(i), "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", OK, "head", "h1")
			w.read(p, "r2", OK, "head", "h0")
			ids = append(ids, id)
		}
		for i := range others {
			w.judge("other"+strconv.Itoa(i), NCIRed)
		}
		keys := hkeys(ids...)
		d := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			if rp := w.plan(keys...); len(rp.Notes) != 1 {
				t.Fatalf("the plan of 2,000 review stalls: %d notes", len(rp.Notes))
			}
			d = min(d, time.Since(start))
		}
		return d
	}
	none, many := best(0), best(8000)
	if many > 20*none+20*time.Millisecond {
		t.Errorf("2,000 review stalls take %v beside no other judgment and %v beside 8,000", none, many)
	}
}

// randomHWorld is a sprint of two streams, three members and two readers with
// forty primaries in every state the table has a row for, at random: needs,
// work cards, reads, merge cards, sentinels and the judgments open on them.
func randomHWorld(rng *rand.Rand) *hworld {
	w := newHWorld()
	pick := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	w.stream("s2", pick(StreamWaiting, StreamMerging, StreamStopped))
	w.member("m3", pick(Up, Down, Held))
	for _, m := range []string{"m1", "m2", "m3"} {
		w.s.Fleet.Card(CtlID(m)).Fields["status"] = pick(Up, Up, Down, Held)
	}
	for _, st := range []string{"s1", "s2"} {
		f := w.ctl(st).Fields
		f["state"] = pick(StreamWaiting, StreamMerging, StreamStopped)
		if rng.Intn(3) > 0 {
			f[heldDueMergeIdle] = strconv.FormatInt(hR+int64(rng.Intn(3)-1), 10)
		}
		f["cause"] = pick("cross", "conflict", "red")
		f["need_card"] = "p" + strconv.Itoa(rng.Intn(40))
	}
	for i, m := range []string{"m1", "m2", "m3"} {
		for j := range rng.Intn(3) {
			w.s.Fleet.Put(&Card{ID: "fill" + strconv.Itoa(i) + strconv.Itoa(j), Row: m, Col: Ready, Rev: 1, Fields: hkv("kind", "work")})
		}
	}
	score := 0.0
	next := func() float64 { score++; return score }
	for _, st := range []string{"s1", "s2"} {
		if rng.Intn(2) == 0 {
			need := pick("p"+strconv.Itoa(rng.Intn(40)), "gone")
			w.primary("g"+st, st, Waiting, next(), "kind", "sentinel", "needs", need, "open", strconv.Itoa(rng.Intn(2)))
		}
	}
	due := func() string { return strconv.FormatInt(hR+int64(rng.Intn(3)-1), 10) }
	for i := range 40 {
		id, st := "p"+strconv.Itoa(i), pick("s1", "s2")
		kv := []string{}
		if rng.Intn(6) == 0 {
			kv = append(kv, "refused", "R3: no")
		}
		switch col := pick(Waiting, Waiting, Ready, Working, Working, Review, Review, Merging, Landed, ""); col {
		case Waiting:
			needs := []string{}
			for range rng.Intn(3) {
				needs = append(needs, "p"+strconv.Itoa(rng.Intn(40)))
			}
			if rng.Intn(5) == 0 {
				needs = append(needs, "gone")
			}
			p := w.primary(id, st, Waiting, next(), append(kv, "needs", strings.Join(needs, ","), "open", strconv.Itoa(rng.Intn(3)))...)
			if rng.Intn(4) == 0 && len(needs) > 0 {
				p.Fields["waived"] = needs[0]
			}
		case Ready:
			kv = append(kv, "attempt", strconv.Itoa(rng.Intn(3)))
			if rng.Intn(6) == 0 {
				kv = append(kv, "bound", "redeals")
			}
			w.primary(id, st, Ready, next(), kv...)
		case Working:
			p := w.primary(id, st, Working, next(), append(kv, "attempt", "1")...)
			switch rng.Intn(4) {
			case 0:
			case 1:
				w.work(p, pick("m1", "m2", "m3"), Ready, heldDueUntaken, due())
			default:
				w.work(p, pick("m1", "m2", "m3"), Working, heldDueUnfinished, due())
			}
		case Review:
			p := w.primary(id, st, Review, next(), append(kv, "attempt", strconv.Itoa(1+rng.Intn(3)), "result", pick("ok", "ok", "failed", ""), "head", "h1")...)
			if rng.Intn(3) == 0 {
				p.Fields["ci"], p.Fields["ci_head"] = pick("red", "green"), pick("h1", "h0")
			}
			if rng.Intn(6) == 0 {
				p.Fields["bound"] = "attempts"
			}
			for _, r := range []string{"r1", "r2"} {
				switch rng.Intn(6) {
				case 0:
				case 1:
					w.read(p, r, Asked, heldDueUnbegun, due())
				case 2:
					w.read(p, r, Reading, heldDueUnreported, due())
				case 3:
					w.read(p, r, OK, "head", "h1")
				case 4:
					w.read(p, r, OK, "head", "h0")
				default:
					w.read(p, r, Broken)
				}
			}
		case Merging:
			w.primary(id, st, Merging, next(), kv...)
			switch rng.Intn(3) {
			case 0:
				w.s.Merge.Put(&Card{ID: id, Row: st, Col: Queued, Rev: 1, Fields: hkv()})
			case 1:
				w.s.Merge.Put(&Card{ID: id, Row: st, Col: Stuck, Rev: 1, Fields: hkv()})
			}
		case Landed:
			w.primary(id, st, Landed, next())
		default:
			w.primary(id, "", "", 0, "outcome", "dropped")
		}
	}
	// judgments, on the subjects the design's jopen reaches: a card, its work
	// card and its read cards, its stream, the sprint
	subjects := []string{"sprint", StreamSubject("s1"), StreamSubject("s2")}
	for i := range 40 {
		id := "p" + strconv.Itoa(i)
		subjects = append(subjects, id)
		if p := w.s.Work.Card(id); p != nil {
			if wc := p.F("work"); wc != "" {
				subjects = append(subjects, wc)
			}
			subjects = append(subjects, Split(p.F("rcards"))...)
		}
	}
	types := []string{NInvariant, typeCouldNotMove, NBound, NSentinelReached, NBlocked, NMissingNeed, NNoMember, NWorkLate, NReadLate,
		NCannotAsk, NCIRed, NReturned, NReadsExhausted, NStranded, NConflict, NRed, NCross, NRejected, NMergeLate, NStalled}
	for range rng.Intn(25) {
		sub, typ := subjects[rng.Intn(len(subjects))], types[rng.Intn(len(types))]
		if rng.Intn(4) == 0 {
			w.hold(sub, typ)
		} else {
			w.judge(sub, typ)
		}
	}
	return w
}

func TestHeldReadWithoutFrontsAgreesWithWhole(t *testing.T) {
	t.Parallel()
	// The registered rule reads no front (a read plan names its streams in
	// advance and R16's cards are found by id or line: IT05 question 3). Over
	// 300 random sprints, each primary judged on the snapshot its own read
	// loaded, with no front and only what its plan asked, gives the verdict of
	// the whole world, whole: the same holder in the same words for every card
	// whose row front does not tell apart, and for the rest (waiting or ready
	// below the first sentinel or behind it, a sentinel) "held" or "stalled" is
	// the same; the one state whose answer is not the same is a sentinel with
	// nothing before it whose own needs nothing holds, which the whole world
	// judges and the partial one holds, and the partial one never judges a card
	// the whole world holds.
	frontRows := map[string]bool{
		"a sentinel, not reached": true, "a sentinel, reached": true,
		"waiting, open = 0, behind the first sentinel":                  true,
		"waiting, open = 0, below the first sentinel or none (in elig)": true,
		"ready, in fresh below the first sentinel or in again":          true,
		"ready, in fresh behind the first sentinel":                     true,
	}
	rows := map[string]int{}
	blindStalls := 0
	for seed := int64(1); seed <= 300; seed++ {
		w := randomHWorld(rand.New(rand.NewSource(seed)))
		var ids []string
		for _, c := range w.s.Work.Cards() {
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		keys := hkeys(ids...)
		rp, taken := readOf(t, keys, 0)
		if len(taken) != len(keys) {
			t.Fatalf("seed %d: the read took %d of %d keys", seed, len(taken), len(keys))
		}
		s := w.load(t, rp)
		part := newHoldView(s, heldFactsOf(s), w.now)
		whole := w.view()
		for _, id := range ids {
			a, b := whole.verdict(id), part.verdict(id)
			if a.row != nil {
				rows[a.row.Name]++
			}
			switch {
			case a.row == nil || !frontRows[a.row.Name]:
				if a.Hold != b.Hold || (a.row == nil) != (b.row == nil) || a.row != nil && a.row.Name != b.row.Name {
					t.Fatalf("seed %d, %s: whole %+v (%v), partial %+v (%v)", seed, id, a.Hold, a.row, b.Hold, b.row)
				}
			case a.Stalled() == b.Stalled():
			case b.Stalled():
				t.Fatalf("seed %d, %s: the partial snapshot judges a card the whole world holds: %+v against %+v", seed, id, b.Hold, a.Hold)
			case a.row.Name == "a sentinel, not reached":
				blindStalls++
			default:
				t.Fatalf("seed %d, %s: whole %+v, partial %+v", seed, id, a.Hold, b.Hold)
			}
		}
		if err := s.UnloadedErr(); err != nil {
			t.Fatalf("seed %d: the judgment read what the plan did not ask for: %v", seed, err)
		}
	}
	// the sprints reach every row of the table, or the agreement proves little
	for _, row := range holdRows {
		if rows[row.Name] == 0 && !slices.Contains([]string{"quarantined", "of a stream being dropped or removed", "a place no row of the table gives"}, row.Name) {
			t.Errorf("no random sprint had a card of the row %q (%v)", row.Name, rows)
		}
	}
	if blindStalls == 0 {
		t.Errorf("no random sprint had a sentinel the partial snapshot cannot tell (%v)", rows)
	}
}

func TestHeldRegisteredRuleAgreesWithPlanHeldOnRandomSprints(t *testing.T) {
	t.Parallel()
	// the whole plan, not only the verdict: notes, guards and keys, for every
	// primary of 100 random sprints named by a key, are the same through the
	// registered rule as through planHeld on the whole world, but for the notes
	// that name a sentinel the partial snapshot cannot judge
	for seed := int64(1); seed <= 100; seed++ {
		w := randomHWorld(rand.New(rand.NewSource(1000 + seed)))
		var ids []string
		for _, c := range w.s.Work.Cards() {
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		keys := hkeys(ids...)
		want, got := w.plan(keys...), w.viaRule(t, keys...)
		if !reflect.DeepEqual(want.Done, got.Done) || len(got.Requeue) != 0 || len(got.HeldBack) != 0 {
			t.Fatalf("seed %d: done %v, %v", seed, want.Done, got.Done)
		}
		strip := func(rp RulePlan) map[string][]string {
			out := map[string][]string{}
			for _, n := range rp.Notes {
				for _, id := range n.Subjects {
					if strings.HasPrefix(id, "g") && n.Cause == "sentinel" {
						continue
					}
					out[n.Type+" / "+n.Cause] = append(out[n.Type+" / "+n.Cause], id)
				}
			}
			return out
		}
		if !reflect.DeepEqual(strip(want), strip(got)) {
			t.Fatalf("seed %d: planHeld %v, the rule %v", seed, strip(want), strip(got))
		}
	}
}

func TestHeldRegisteredRuleTwiceOnRandomSprintsSecondEmpty(t *testing.T) {
	t.Parallel()
	// (b) idempotence over 150 random sprints, through the registered rule read
	// and answered as the tick reads it: the first run names what nothing holds,
	// J opens what it asked, and the second run over the same keys and the state
	// the first left writes nothing (it may still finish the keys)
	named := 0
	for seed := int64(1); seed <= 150; seed++ {
		w := randomHWorld(rand.New(rand.NewSource(5000 + seed)))
		var ids []string
		for _, c := range w.s.Work.Cards() {
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		keys := hkeys(ids...)
		first := w.viaRule(t, keys...)
		named += len(first.Notes)
		w.apply(first)
		if second := w.viaRule(t, keys...); !writesNothing(second) {
			t.Fatalf("seed %d: the second run writes: %+v (the first named %+v)", seed, second, first.Notes)
		}
	}
	if named == 0 {
		t.Fatal("no random sprint had a stall: the second run proves nothing")
	}
}

// BenchmarkHeld2000 is 2,000 cards judged in one plan, the held rule's chunk (its
// limit is 15 ms of store time for the read, measured by IT25; this is the Go
// time of the plan over it, held to the same 15 ms): cards of every place, most
// of them held.
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
			w.work(p, "m1", Working, heldDueUnfinished, strconv.FormatInt(hR+1000, 10))
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
		w.ctl(st).Fields[heldDueMergeIdle] = strconv.FormatInt(hR+1000, 10)
	}
	keys := hkeys(ids...)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = planHeld(w.s, w.f, keys, w.now)
	}
	// the limit is held over a run of fifty or more, not over the single cold
	// op the harness times first
	if per := b.Elapsed() / time.Duration(b.N); b.N >= 50 && per > time.Duration(n/HeldChunk)*15*time.Millisecond {
		b.Fatalf("a plan of %d cards took %v, over %d ms", n, per, 15*n/HeldChunk)
	}
}

// BenchmarkHeldReviewStalls2000 is 2,000 primaries stalled in review beside 0,
// 2,000 and 8,000 open judgments of the sprint on other subjects: the plan does
// not grow with the judgments beside the cards (O(c + j), the index of them).
func BenchmarkHeldReviewStalls2000(b *testing.B) {
	for _, others := range []int{0, 2000, 8000} {
		w := newHWorld()
		var ids []string
		for i := range HeldChunk {
			id := "p" + strconv.Itoa(i)
			p := w.primary(id, "s1", Review, float64(i), "attempt", "1", "result", "ok", "head", "h1")
			w.read(p, "r1", OK, "head", "h1")
			w.read(p, "r2", OK, "head", "h0")
			ids = append(ids, id)
		}
		for i := range others {
			w.judge("other"+strconv.Itoa(i), NCIRed)
		}
		keys := hkeys(ids...)
		b.Run(fmt.Sprintf("judgments%d", others), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = planHeld(w.s, w.f, keys, w.now)
			}
			if per := b.Elapsed() / time.Duration(b.N); b.N >= 50 && per > 15*time.Millisecond {
				b.Fatalf("2,000 review stalls beside %d judgments took %v, over 15 ms", others, per)
			}
		})
	}
}

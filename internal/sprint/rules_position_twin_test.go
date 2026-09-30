package sprint

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The twin the position rules are tested against. Layer 1's in-memory twin is
// not in the tree, so the rules run against a twin of their own, posTwin: the
// work and merge tables' records, the indexes the rules read by their
// definitions (sent, elig, fresh from the records; wait and missing kept as the
// intents keep them), the composite queries the read plans ask, and an apply
// that does what layer 1 and X do with a plan: refuse the whole plan when a
// place, a revision, a count guard or the stream-set counter is not as read, and
// otherwise write it, decide the intents on the state as it is, and keep the
// judgments the notes open and close.
//
// The twin answers a read plan as the store does: a ReadAnswer built from the
// plan alone, holding what the plan asked for and nothing else (the heads and
// the sprint keys it names, and only the fields it names), loaded with IT05's
// LoadPartial, so that a plan that reads what its read did not ask for panics in
// the test. It is not a snapshot of the whole twin.

var posBounds = ReadBounds{Queries: 1024, Records: 10000, RangeIDs: 20000, Bytes: 8 << 20}

// posMaxHalvings is the halvings after which a read is a chunk of one: 2,000
// halved eleven times (Halved) is 1.
const posMaxHalvings = 11

// posRec is a record of the twin: a card of the work table or of the merge
// table, at a place (col empty when it is kept off the table).
type posRec struct {
	id, row, col string
	score        float64
	rev          uint64
	f            map[string]string
}

func (r *posRec) card() *Card {
	f := make(map[string]string, len(r.f))
	for k, v := range r.f {
		f[k] = v
	}
	return &Card{ID: r.id, Row: r.row, Col: r.col, Score: r.score, Rev: r.rev, Fields: f}
}

type posTwin struct {
	work, merge map[string]*posRec
	rows        []string
	wait        map[string]map[string]bool // wait:n -> its waiters
	missing     map[string]bool            // {p}missing@e
	dropping    map[string]bool
	quar        map[string]bool
	judged      map[string]bool // type, cause and subject of each open judgment
	held        map[string]bool // the same, of each judgment the coordinator holds
	know        []string
	streams     uint64 // {p}next@e.streams
	lines       map[uint64][]string
	agenda      map[string]uint64
	heldKeys    map[string]bool // the keys a plan held back, that the loop does not plan again
	// inject are ids that the head of elig (or of fresh above σ) names whatever
	// their record says, by stream: a fault the planner must refuse, never move.
	inject map[string][]string
	now    Now
}

func newPosTwin(streams ...string) *posTwin {
	return &posTwin{work: map[string]*posRec{}, merge: map[string]*posRec{}, rows: streams,
		wait: map[string]map[string]bool{}, missing: map[string]bool{}, dropping: map[string]bool{}, quar: map[string]bool{},
		judged: map[string]bool{}, held: map[string]bool{}, lines: map[uint64][]string{}, agenda: map[string]uint64{},
		heldKeys: map[string]bool{}, inject: map[string][]string{}, streams: uint64(len(streams)),
		now: Now{R: 5_000_000, Wall: 1_790_000_000_000, Running: true}}
}

func posKV(kv []string) map[string]string {
	f := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		f[kv[i]] = kv[i+1]
	}
	return f
}

// card puts a primary of the work table at a place, with fields.
func (tw *posTwin) card(id, stream, col string, score float64, kv ...string) *posRec {
	f := map[string]string{"kind": "primary", "attempt": "0", "open": "0"}
	for k, v := range posKV(kv) {
		f[k] = v
	}
	r := &posRec{id: id, row: stream, col: col, score: score, rev: 1, f: f}
	tw.work[id] = r
	return r
}

// sentinel puts a sentinel waiting in the stream.
func (tw *posTwin) sentinel(id, stream string, score float64, kv ...string) *posRec {
	return tw.card(id, stream, Waiting, score, append([]string{"kind", "sentinel"}, kv...)...)
}

// ctl puts a stream's control card.
func (tw *posTwin) ctl(stream string, kv ...string) *posRec {
	f := map[string]string{"state": StreamWaiting}
	for k, v := range posKV(kv) {
		f[k] = v
	}
	r := &posRec{id: CtlID(stream), row: stream, col: Ctl, rev: 1, f: f}
	tw.merge[r.id] = r
	return r
}

// stuck puts a card of the merge table in the stream's stuck cell.
func (tw *posTwin) stuck(stream, id string, score float64) *posRec {
	r := &posRec{id: id, row: stream, col: Stuck, score: score, rev: 1, f: map[string]string{"need_card": "x", "need_stream": "s2"}}
	tw.merge[id] = r
	return r
}

func (tw *posTwin) waiter(need string, ids ...string) {
	if tw.wait[need] == nil {
		tw.wait[need] = map[string]bool{}
	}
	for _, id := range ids {
		tw.wait[need][id] = true
	}
}

func posJKey(typ, cause, subject string) string { return typ + "\x00" + cause + "\x00" + subject }

func (tw *posTwin) judge(typ, cause, subject string) { tw.judged[posJKey(typ, cause, subject)] = true }

// hold puts a judgment the coordinator holds (acknowledged, or waited on): it is
// not open, and no rule raises it again while it is held (1.3.4).
func (tw *posTwin) hold(typ, cause, subject string) { tw.held[posJKey(typ, cause, subject)] = true }

func (tw *posTwin) opened(typ, cause, subject string) bool {
	return tw.judged[posJKey(typ, cause, subject)]
}

// jtypes are the types of the judgments of a set that are on the subject.
func jtypes(set map[string]bool, subject string) []string {
	var out []string
	for k := range set {
		if parts := strings.Split(k, "\x00"); parts[2] == subject {
			out = append(out, parts[0])
		}
	}
	sort.Strings(out)
	return out
}

func (tw *posTwin) rec(table, id string) *posRec {
	if table == Merge {
		return tw.merge[id]
	}
	return tw.work[id]
}

// The indexes, by their definitions (1.3.1), in work order.

func (tw *posTwin) sorted(keep func(*posRec) bool) []*posRec {
	var out []*posRec
	for _, r := range tw.work {
		if keep(r) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].id < out[j].id
	})
	return out
}

func (tw *posTwin) sentinels(stream string) []*posRec {
	return tw.sorted(func(r *posRec) bool { return r.row == stream && r.col == Waiting && r.f["kind"] == "sentinel" })
}

func (tw *posTwin) elig(stream string) []*posRec {
	return tw.sorted(func(r *posRec) bool {
		return r.row == stream && r.col == Waiting && r.f["kind"] != "sentinel" && r.f["open"] == "0" && r.f["refused"] == "" && !tw.quar[r.id]
	})
}

func (tw *posTwin) fresh(stream string) []*posRec {
	return tw.sorted(func(r *posRec) bool {
		return r.row == stream && r.col == Ready && r.f["kind"] != "sentinel" && r.f["attempt"] == "0" && r.f["refused"] == "" && !tw.quar[r.id]
	})
}

func (tw *posTwin) count(stream, col string) int {
	n := 0
	for _, r := range tw.work {
		if r.row == stream && r.col == col {
			n++
		}
	}
	return n
}

func posIsOpen(col string) bool {
	for _, c := range posOpenColumns {
		if string(c) == col {
			return true
		}
	}
	return false
}

// Answering a read plan, as the store does.

// answer is what the read plan asked, answered from the twin's state now. It
// answers composite queries only, and only what each names.
func (tw *posTwin) answer(rp ReadPlan) ReadAnswer {
	if len(rp.IDs)+len(rp.Ranges)+len(rp.Counts)+len(rp.RCounts)+len(rp.Lines) != 0 {
		panic("the twin answers the composite queries of the position rules only")
	}
	ans := ReadAnswer{Epoch: "1", ActiveEpoch: "1", TimeMS: Decimal(strconv.FormatInt(tw.now.Wall, 10))}
	for _, q := range rp.Sprint {
		switch q.Kind {
		case QueryFront:
			ans.Sprint = append(ans.Sprint, tw.answerFront(q))
		case QueryWaiters:
			ans.Sprint = append(ans.Sprint, tw.answerWaiters(q))
		case QueryStreams:
			ans.Sprint = append(ans.Sprint, tw.answerStreams(q))
		default:
			panic("the twin does not answer " + q.Kind)
		}
	}
	return ans
}

// recorder collects the records of an answer, once each.
type recorder struct {
	a    *Answer
	seen map[string]bool
}

func newRecorder(a *Answer) *recorder { return &recorder{a: a, seen: map[string]bool{}} }

func (rc *recorder) add(table string, r *posRec) {
	if r != nil && !rc.seen[table+"/"+r.id] {
		rc.seen[table+"/"+r.id] = true
		rc.a.Records = append(rc.a.Records, TableCard{Table: table, Card: r.card()})
	}
}

func (tw *posTwin) answerFront(q SprintQ) Answer {
	stream := q.Stream
	a := Answer{Kind: QueryFront}
	rc := newRecorder(&a)
	f := FrontAnswer{Stream: stream}
	sigma := math.Inf(1)
	if ss := tw.sentinels(stream); len(ss) > 0 {
		g := ss[0]
		sigma = g.score
		f.G, f.Sigma = g.id, g.score
		for _, r := range tw.work {
			if r.row == stream && r.score < sigma && posIsOpen(r.col) {
				f.NBefore++
			}
		}
		if tw.quar[g.id] {
			f.GQuarantined = true // its record is not returned
		} else {
			rc.add(Work, g)
		}
	}
	a.Front = &f
	for _, h := range q.Heads {
		var rs []*posRec
		switch h.Index {
		case HeadEligBelow:
			for _, r := range tw.elig(stream) {
				if r.score < sigma {
					rs = append(rs, r)
				}
			}
		case HeadFreshAbove:
			if f.G != "" {
				for _, r := range tw.fresh(stream) {
					if r.score > sigma {
						rs = append(rs, r)
					}
				}
			}
		default:
			panic("the twin does not answer the head " + h.Index)
		}
		for _, id := range tw.inject[stream] {
			if r := tw.work[id]; r != nil && !slicesHas(rs, r) {
				rs = append(rs, r)
			}
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].score != rs[j].score {
				return rs[i].score < rs[j].score
			}
			return rs[i].id < rs[j].id
		})
		ha := HeadAnswer{Index: h.Index, More: len(rs) > h.Limit}
		if ha.More {
			rs = rs[:max(h.Limit, 0)]
		}
		for _, r := range rs {
			ha.IDs = append(ha.IDs, r.id)
			rc.add(Work, r)
		}
		a.Heads = append(a.Heads, ha)
	}
	for _, k := range q.Keys {
		switch k {
		case KeyJOpenG:
			a.Keys = append(a.Keys, KeyAnswer{Key: k, Subject: f.G, Open: jtypes(tw.judged, f.G), Held: jtypes(tw.held, f.G)})
		case KeyDropping:
			ka := KeyAnswer{Key: k}
			if tw.dropping[stream] {
				ka.Streams = []string{stream}
			}
			a.Keys = append(a.Keys, ka)
		default:
			panic("the twin does not read the key " + k + " with front")
		}
	}
	return a
}

func slicesHas(rs []*posRec, r *posRec) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

func (tw *posTwin) answerWaiters(q SprintQ) Answer {
	a := Answer{Kind: QueryWaiters}
	rc := newRecorder(&a)
	var ids []string
	if q.Source.Kind == SourceLine {
		all := tw.lines[q.Source.Seq]
		offset, n := q.Source.lineWindow()
		offset = min(offset, len(all))
		end := min(len(all), offset+n)
		ids = all[offset:end]
		a.IDs = append([]string(nil), ids...)
		a.MoreIDs = end < len(all)
	} else {
		ids = q.Source.IDs
	}
	dropStreams := map[string]bool{}
	for _, n := range ids {
		na := NeedAnswer{ID: n, Missing: tw.missing[n]}
		if r := tw.work[n]; r != nil {
			na.Place = r.col
		}
		if !q.Missing || na.Missing {
			// wait:n is read in the order of its members' ids, after the cursor: a
			// place in that order, which a waiter leaving wait:n does not move
			var ws []*posRec
			for w := range tw.wait[n] {
				if r := tw.work[w]; r != nil && !tw.quar[w] && w > q.WaiterAfter {
					ws = append(ws, r)
				}
			}
			sort.Slice(ws, func(i, j int) bool { return ws[i].id < ws[j].id })
			na.More = len(ws) > q.Limit
			if na.More {
				ws = ws[:q.Limit]
			}
			for _, r := range ws {
				na.Waiters = append(na.Waiters, r.id)
				rc.add(Work, r)
				if tw.dropping[r.row] {
					dropStreams[r.row] = true
				}
			}
		}
		a.Needs = append(a.Needs, na)
	}
	for _, k := range q.Keys {
		switch k {
		case KeyDropping:
			ka := KeyAnswer{Key: k}
			for st := range dropStreams {
				ka.Streams = append(ka.Streams, st)
			}
			sort.Strings(ka.Streams)
			a.Keys = append(a.Keys, ka)
		default:
			panic("the twin does not read the key " + k + " with waiters")
		}
	}
	return a
}

func (tw *posTwin) answerStreams(q SprintQ) Answer {
	a := Answer{Kind: QueryStreams, Rows: append([]string(nil), tw.rows...)}
	rc := newRecorder(&a)
	stuck := map[string][]*posRec{}
	for _, r := range tw.merge {
		if r.col == Stuck {
			stuck[r.row] = append(stuck[r.row], r)
		}
	}
	for _, stream := range tw.rows {
		ctl := tw.merge[CtlID(stream)]
		rc.add(Merge, ctl)
		if ctl == nil || ctl.f["state"] != StreamStopped || ctl.f["cause"] != "cross" {
			continue
		}
		rc.add(Work, tw.work[ctl.f[posCrossNeedField]]) // the card it needs, for its place
		if q.Limit > 0 {
			cells := stuck[stream]
			sort.Slice(cells, func(i, j int) bool { return cells[i].score < cells[j].score })
			sa := StuckAnswer{Stream: stream, More: len(cells) > q.Limit}
			for i, r := range cells {
				if i < q.Limit {
					sa.IDs = append(sa.IDs, r.id)
				}
			}
			a.Stuck = append(a.Stuck, sa)
		}
	}
	for _, stream := range tw.rows {
		for _, col := range q.Counts {
			a.Counts = append(a.Counts, CellCount{Row: stream, Col: col, N: tw.count(stream, col)})
		}
	}
	for _, k := range q.Keys {
		switch k {
		case KeyNextStreams:
			a.Keys = append(a.Keys, KeyAnswer{Key: k, N: tw.streams})
		case KeyJOpenSprint:
			a.Keys = append(a.Keys, KeyAnswer{Key: k, Subject: SprintSubject, Open: jtypes(tw.judged, SprintSubject), Held: jtypes(tw.held, SprintSubject)})
		case KeyDropping:
			ka := KeyAnswer{Key: k}
			for _, st := range tw.rows {
				if tw.dropping[st] {
					ka.Streams = append(ka.Streams, st)
				}
			}
			a.Keys = append(a.Keys, ka)
		default:
			panic("the twin does not read the key " + k + " with streams")
		}
	}
	return a
}

// load is the snapshot of the read plan answered from the twin as it is now:
// IT05's LoadPartial, which in a test refuses (panics at) any read of what the
// plan did not ask for.
func (tw *posTwin) load(t testing.TB, rp ReadPlan) *Snapshot {
	t.Helper()
	s, err := LoadPartial(rp, tw.answer(rp))
	if err != nil {
		t.Fatalf("the twin's answer does not load: %v", err)
	}
	return s
}

func posRule(t testing.TB, name string) Rule {
	t.Helper()
	for _, r := range RuleTable() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no rule %q registered", name)
	return Rule{}
}

func posKeyOf(key string) AgendaKey { return AgendaKey{Key: key, Seq: 100} }

// plan reads and plans a rule on the twin as it is now, keys as given, through
// the read the rule itself plans.
func (tw *posTwin) plan(t *testing.T, name string, halvings int, keys ...AgendaKey) RulePlan {
	t.Helper()
	r := posRule(t, name)
	rp, left := r.Read(keys, posBounds, halvings)
	if len(left) > 0 {
		t.Fatalf("the read of %s left keys for later: %v", name, left)
	}
	s := tw.load(t, rp)
	p := r.Plan(s, keys, tw.now)
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("the plan of %s read what its read did not load: %v", name, err)
	}
	return p
}

// run plans a rule and applies its plan, and keeps the keys as the tick does.
func (tw *posTwin) run(t *testing.T, name string, halvings int, keys ...AgendaKey) (RulePlan, posOutcome) {
	t.Helper()
	p := tw.plan(t, name, halvings, keys...)
	out := tw.apply(p)
	if out.refused == "" {
		for _, k := range p.Done {
			delete(tw.agenda, k.Key)
		}
		for _, k := range p.Requeue {
			if _, ok := tw.agenda[k.Key]; !ok {
				tw.agenda[k.Key] = k.Seq
			}
		}
		for _, k := range p.HeldBack {
			tw.heldKeys[k.Key] = true
		}
	}
	return p, out
}

// drain runs a rule on every key of its own that is in the agenda and not held
// back, all in one plan as the tick does, until none is left, and returns the
// runs it took: a rule that never ends is a fault (maxRuns).
func (tw *posTwin) drain(t *testing.T, name string, halvings, maxRuns int) int {
	t.Helper()
	for run := 0; ; run++ {
		var keys []AgendaKey
		for k, seq := range tw.agenda {
			if ServingRule(k) == name && !tw.heldKeys[k] {
				keys = append(keys, AgendaKey{Key: k, Seq: seq})
			}
		}
		if len(keys) == 0 {
			return run
		}
		if run >= maxRuns {
			t.Fatalf("%s did not finish in %d runs; the agenda is %v", name, maxRuns, tw.agenda)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Seq != keys[j].Seq {
				return keys[i].Seq < keys[j].Seq
			}
			return keys[i].Key < keys[j].Key
		})
		if _, out := tw.run(t, name, halvings, keys...); out.refused != "" {
			t.Fatalf("%s was refused: %+v", name, out)
		}
	}
}

// posQuiet says a plan writes nothing and raises nothing: it may still remove
// the key it was given (the second delivery of a key, E7).
func posQuiet(p RulePlan) bool {
	return len(p.Plan.Units) == 0 && len(p.Plan.Refused) == 0 && len(p.Plan.Notes) == 0 && len(p.Plan.Closes) == 0 &&
		len(p.Plan.Updates) == 0 && len(p.Plan.Rows) == 0 && len(p.Intents) == 0 && len(p.Guards) == 0 &&
		len(p.Notes) == 0 && len(p.Requeue) == 0 && len(p.Quarantine) == 0 && len(p.HeldBack) == 0
}

// posOutcome is what applying a plan did: the code it was refused with, or
// whether it wrote anything.
type posOutcome struct {
	refused string
	wrote   bool
}

// apply does what layer 1 and X do with a plan: check every guard against the
// state as it is and refuse the whole plan on the first that fails, then write.
func (tw *posTwin) apply(p RulePlan) posOutcome {
	for _, u := range p.Plan.Units {
		for _, c := range u.Changes {
			r := tw.rec(c.Table, c.Entry.ID)
			e := c.Entry.Expect
			switch {
			case e == nil:
			case r == nil:
				return posOutcome{refused: "MISSING " + c.Entry.ID}
			case e.Place != nil && (r.row != e.Place.Row || r.col != e.Place.Col):
				return posOutcome{refused: "PLACE " + c.Entry.ID}
			case e.Revision != "" && e.Revision != strconv.FormatUint(r.rev, 10):
				return posOutcome{refused: "REVISION " + c.Entry.ID}
			}
		}
	}
	gs, err := SetGuardsOf(p)
	if err != nil {
		panic(err)
	}
	for _, g := range gs {
		if !tw.holds(g) {
			return posOutcome{refused: "GUARD " + g.Kind + " " + g.Key}
		}
	}
	for _, x := range p.Guards {
		if x.Kind == posCounter && x.Key == "streams" && uint64(x.Score) != tw.streams {
			return posOutcome{refused: "COUNTER"}
		}
	}
	wrote := false
	for _, u := range p.Plan.Units {
		for _, c := range u.Changes {
			wrote = tw.change(c) || wrote
		}
	}
	for _, in := range p.Intents {
		wrote = tw.intent(in) || wrote
	}
	for _, n := range p.Notes {
		wrote = tw.note(n) || wrote
	}
	return posOutcome{wrote: wrote}
}

// holds is a set guard on the state as it is.
func (tw *posTwin) holds(g SetGuard) bool {
	lo, loOpen := posBound(g.Min)
	hi, hiOpen := posBound(g.Max)
	in := func(score float64) bool {
		return (score > lo || score == lo && !loOpen) && (score < hi || score == hi && !hiOpen)
	}
	n := 0
	switch g.Kind {
	case GuardRCount:
		cells := map[string]bool{}
		for _, c := range g.Cells {
			cells[c] = true
		}
		for _, r := range tw.work {
			if cells[r.row+":"+r.col] && in(r.score) {
				n++
			}
		}
	case GuardZGuard:
		for _, r := range tw.sentinels(strings.TrimPrefix(g.Key, "sent:")) {
			if in(r.score) {
				n++
			}
		}
	}
	return (g.AtLeast == nil || n >= *g.AtLeast) && (g.AtMost == nil || n <= *g.AtMost)
}

// posBound reads a score bound of the store's grammar: the number, and whether
// it is an open bound.
func posBound(s string) (float64, bool) {
	open := strings.HasPrefix(s, "(")
	s = strings.TrimPrefix(s, "(")
	switch s {
	case "-inf":
		return math.Inf(-1), false
	case "+inf", "inf":
		return math.Inf(1), false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic("bad score bound " + s)
	}
	return f, open
}

func (tw *posTwin) change(c Change) bool {
	e := c.Entry
	r := tw.rec(c.Table, e.ID)
	changed := false
	if m := e.Move; m != nil {
		if r.row != m.Row || r.col != m.Col {
			r.row, r.col, changed = m.Row, m.Col, true
		}
		if m.Score != nil && *m.Score != r.score {
			r.score, changed = *m.Score, true
		}
	}
	for k, v := range e.Set {
		if r.f[k] != v {
			r.f[k], changed = v, true
		}
	}
	for _, k := range e.Unset {
		if _, ok := r.f[k]; ok {
			delete(r.f, k)
			changed = true
		}
	}
	if changed {
		r.rev++
	}
	return changed
}

// intent decides an intent on the state as it is, as Lua does (1.3.3): a
// waiter still in wait:n, not quarantined, is served; the others are not.
func (tw *posTwin) intent(in Intent) bool {
	wrote := false
	switch in.Kind {
	case posNeedmet, posNeedgone:
		for _, w := range in.Waiters {
			r := tw.work[w]
			if !tw.wait[in.Need][w] || r == nil || tw.quar[w] {
				continue
			}
			delete(tw.wait[in.Need], w)
			if in.Kind == posNeedmet {
				n, _ := strconv.Atoi(r.f["open"])
				r.f["open"] = strconv.Itoa(n - 1)
				r.rev++
				delete(tw.judged, posJKey(NMissingNeed, in.Need, w))
			} else {
				tw.judge(NBlocked, in.Need, w)
			}
			wrote = true
		}
	case posMade:
		if tw.missing[in.Need] {
			delete(tw.missing, in.Need)
			wrote = true
		}
	}
	return wrote
}

func (tw *posTwin) note(n NoteReq) bool {
	wrote := false
	switch n.Op {
	case posOpen:
		for _, sub := range n.Subjects {
			if k := posJKey(n.Type, n.Cause, sub); !tw.judged[k] {
				tw.judged[k], wrote = true, true
			}
		}
	case posClose:
		for _, sub := range n.Subjects {
			k := posJKey(n.Type, n.Cause, sub)
			if tw.judged[k] {
				delete(tw.judged, k)
				wrote = true
			}
			if tw.held[k] {
				delete(tw.held, k)
				wrote = true
			}
		}
	case posKnow:
		tw.know = append(tw.know, n.Type+": "+n.Text)
		wrote = true
	}
	return wrote
}

// posPositionHolds is the model's PositionHolds for one stream (5): no card
// sorting after an unlanded sentinel is in review or merging, and none is ready
// that was not dealt (attempt 0) before the sentinel; a reached judgment is
// open only while no open card sorts before its sentinel.
func (tw *posTwin) posPositionHolds(stream string) string {
	ss := tw.sentinels(stream)
	if len(ss) == 0 {
		return ""
	}
	sigma := ss[0].score
	for _, r := range tw.work {
		if r.row == stream && r.score > sigma && r.f["kind"] != "sentinel" && r.col == string(Ready) && r.f["attempt"] == "0" {
			return fmt.Sprintf("%s is ready behind the sentinel %s", r.id, ss[0].id)
		}
	}
	if tw.opened(NSentinelReached, "", ss[0].id) {
		for _, r := range tw.work {
			if r.row == stream && r.score < sigma && posIsOpen(r.col) {
				return fmt.Sprintf("%s is reached with %s open before it", ss[0].id, r.id)
			}
		}
	}
	return ""
}

func posUnitIDs(p RulePlan) []string {
	var out []string
	for _, u := range p.Plan.Units {
		out = append(out, u.Key)
	}
	return out
}

func posGuardsOf(t *testing.T, p RulePlan) []SetGuard {
	t.Helper()
	gs, err := SetGuardsOf(p)
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

func posHas(keys []AgendaKey, key string) bool {
	for _, k := range keys {
		if k.Key == key {
			return true
		}
	}
	return false
}

func posSameStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

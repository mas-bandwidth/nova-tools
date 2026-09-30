package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The review verbs of section 3 (item IT21): ask, accept, rework, return and
// ci, the coordinator's decisions on a primary in review or merging. Each
// reads a declared plan in one snapshot (1.5.1), plans on the partial snapshot
// it loads (1.5.2), and writes through the driver: a verb of one step through
// Env.Do (two round trips), a verb in parts through Env.Parts (n + 1).
//
// This file also holds what the three files of IT21 share: the read of a plan
// with the clock beside it (verbRead), the entries a plan writes (stepOps), and
// the conversion of a planner's sprint.Plan into Layer 1 entries (planOps).
// They are local to IT21 until the driver (IT18) or the tick (IT17) names
// their home.

// ---- the read: a plan, the clock and the sprint keys beside it (1.5.1)

// verbRead is one atomic read of a verb: the read plan its snapshot is loaded
// from (sprint.LoadPartial), the sprint-key reads after the plan's composite
// queries (the clock is always the first of them, so every verb plans at the
// store's time and R, 1.0 "The clock"), and Layer 1 queries after the plan's
// own (a done query, a range read raw).
type verbRead struct {
	plan  sprint.ReadPlan
	keys  []sprintfn.KeyQ
	extra []tset.ReadQuery
}

// request is the read at an epoch (IT17's alignment of a plan and its
// ReadRequest: the plan's Layer 1 and Layer 2 queries in ReadPlan.TsetSlots'
// order, then the extra ones; the plan's composite queries, then the keys).
func (vr verbRead) request(names sprint.Names, epoch tset.Decimal) (*sprintfn.ReadRequest, error) {
	if err := vr.plan.Validate(); err != nil {
		return nil, err
	}
	rr := &sprintfn.ReadRequest{Epoch: epoch}
	for _, sl := range vr.plan.TsetSlots() {
		var q tset.ReadQuery
		switch sl.Kind {
		case sprint.AnswerIDs:
			q = tset.ReadQuery{Kind: "ids", Table: sl.Table, IDs: vr.plan.IDs[sl.Table]}
		case sprint.AnswerRange:
			r := vr.plan.Ranges[sl.Index]
			q = tset.ReadQuery{Kind: "range", Table: r.Table, Cell: r.Cell, Min: orBound(r.Min, "-inf"), Max: orBound(r.Max, "+inf"),
				Limit: r.Limit, Desc: r.Desc, Records: r.Records, Fields: r.Fields}
			if r.Key != "" {
				q.Key = names.Key(r.Key) + "@" + string(epoch)
			}
		case sprint.AnswerCount:
			c := vr.plan.Counts[sl.Index]
			q = tset.ReadQuery{Kind: "count", Table: c.Table, Cells: c.Cells}
		case sprint.AnswerRCount:
			c := vr.plan.RCounts[sl.Index]
			q = tset.ReadQuery{Kind: "rcount", Table: c.Table, Cells: c.Cells, Min: orBound(c.Min, "-inf"), Max: orBound(c.Max, "+inf")}
		default:
			return nil, fmt.Errorf("verbs: a read plan's query of the kind %q", sl.Kind)
		}
		rr.Tset = append(rr.Tset, q)
	}
	rr.Tset = append(rr.Tset, vr.extra...)
	for _, q := range vr.plan.Sprint {
		w, ref := sprintfn.EncodeSprintQ(q)
		if ref != nil {
			return nil, ref
		}
		rr.Sprint = append(rr.Sprint, w)
	}
	for _, k := range vr.allKeys() {
		w, ref := sprintfn.EncodeKeyQ(k)
		if ref != nil {
			return nil, ref
		}
		rr.Sprint = append(rr.Sprint, w)
	}
	return rr, nil
}

// allKeys is the clock, then the verb's own key reads.
func (vr verbRead) allKeys() []sprintfn.KeyQ {
	return append([]sprintfn.KeyQ{{Kind: sprintfn.KeyClock}}, vr.keys...)
}

func orBound(b, otherwise string) string {
	if b == "" {
		return otherwise
	}
	return b
}

// readAt is the verb's read as the driver takes it: a read that cannot be
// encoded (a name outside the sprint's alphabet) is kept in *failed and read
// as the clock alone, and the plan returns it before anything is written.
func (vr verbRead) readAt(names sprint.Names, epoch tset.Decimal, failed *error) *sprintfn.ReadRequest {
	rr, err := vr.request(names, epoch)
	if err != nil {
		*failed = err
		return clockRead(epoch)
	}
	*failed = nil
	return rr
}

// verbAnswer is a verb's read loaded: the partial snapshot of its plan, the
// store's clocks, the answers of its key reads (the clock's first) and of its
// extra Layer 1 queries.
type verbAnswer struct {
	snap  *sprint.Snapshot
	now   sprint.Now
	keys  []sprintfn.QueryResult
	extra []tset.ReadAnswer
	raw   []json.RawMessage // the plan's composite answers as the store gave them
}

// load is the read's answer: LoadPartial over the plan's slots (1.5.2), and
// the keys and extras decoded. A read that does not answer its plan is not
// planned on (1.5.1).
func (vr verbRead) load(rd *sprintfn.ReadReply) (*verbAnswer, error) {
	if rd == nil {
		return nil, errors.New("verbs: no read to plan on")
	}
	slots := vr.plan.TsetSlots()
	keys := vr.allKeys()
	if len(rd.Tset) != len(slots)+len(vr.extra) || len(rd.Sprint) != len(vr.plan.Sprint)+len(keys) {
		return nil, fmt.Errorf("verbs: %d and %d queries read, %d and %d answered", len(slots)+len(vr.extra),
			len(vr.plan.Sprint)+len(keys), len(rd.Tset), len(rd.Sprint))
	}
	ans := sprint.ReadAnswer{Epoch: sprint.Decimal(rd.Epoch), ActiveEpoch: sprint.Decimal(rd.ActiveEpoch), TimeMS: sprint.Decimal(rd.TimeMS)}
	for i, sl := range slots {
		a := rd.Tset[i]
		out := sprint.TsetAnswer{Kind: sl.Kind, IDs: a.IDs, HasMore: a.HasMore, Sum: int(a.Sum)}
		for _, s := range a.Scores {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("verbs: query %d: the score %q", i, s)
			}
			out.Scores = append(out.Scores, f)
		}
		for _, c := range a.Counts {
			out.Counts = append(out.Counts, int(c))
		}
		for _, r := range a.Records {
			if !r.Exists {
				out.Records = append(out.Records, nil)
				continue
			}
			out.Records = append(out.Records, cardOfRecord(r))
		}
		ans.Tset = append(ans.Tset, out)
	}
	va := &verbAnswer{extra: rd.Tset[len(slots):]}
	for i, q := range vr.plan.Sprint {
		res, err := sprintfn.DecodeResult(q.Kind, rd.Sprint[i])
		if err != nil {
			return nil, err
		}
		ans.Sprint = append(ans.Sprint, res.Project(q))
		va.raw = append(va.raw, rd.Sprint[i])
	}
	for i, k := range keys {
		res, err := sprintfn.DecodeResult(k.Kind, rd.Sprint[len(vr.plan.Sprint)+i])
		if err != nil {
			return nil, err
		}
		va.keys = append(va.keys, res)
	}
	snap, err := sprint.LoadPartial(vr.plan, ans)
	if err != nil {
		return nil, err
	}
	va.snap = snap
	c, ok := va.keys[0].(sprintfn.ClockResult)
	if !ok {
		return nil, errors.New("verbs: the clock answer is of another kind")
	}
	r, err1 := strconv.ParseInt(c.R, 10, 64)
	wall, err2 := strconv.ParseInt(c.WallMS, 10, 64)
	if err1 != nil || err2 != nil {
		// A sprint with no clock (not initialised): R is the store's time.
		ms, _ := strconv.ParseInt(string(rd.TimeMS), 10, 64)
		r, wall = ms, ms
	}
	va.now = sprint.Now{R: r, Wall: wall, Running: running(c)}
	snap.Now = time.UnixMilli(wall).UTC()
	return va, nil
}

// cardOfRecord is a Layer 1 record as the sprint's card (IT17's reading).
func cardOfRecord(r tset.MemberRecord) *sprint.Card {
	c := &sprint.Card{ID: r.ID, Fields: map[string]string{}}
	if r.Place != nil {
		c.Row, c.Col = r.Place.Row, r.Place.Col
	}
	if r.Score != "" {
		c.Score, _ = strconv.ParseFloat(r.Score, 64)
	}
	if r.Revision != "" {
		c.Rev, _ = strconv.ParseUint(string(r.Revision), 10, 64)
	}
	for name, v := range r.Fields {
		if v.Present {
			c.Fields[name] = v.Value
		}
	}
	return c
}

// wallStamp is a wall time as the stamps for the reader are written (1.2:
// wall stamps stay for the reader; no rule reads them).
func wallStamp(now sprint.Now) string { return time.UnixMilli(now.Wall).UTC().Format(time.RFC3339) }

// dueAt is a due field's value: running milliseconds, R plus the span (1.2).
func dueAt(now sprint.Now, span time.Duration) string {
	return strconv.FormatInt(now.R+span.Milliseconds(), 10)
}

// related is the `related` query of 1.0 over named work ids.
func related(ids, fields, follow []string) sprint.SprintQ {
	return sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: ids},
		Fields: fields, Follow: follow}
}

// ---- the entries of a step (1.3.6; L1 3)

// stepOp is one card's change in a step, before the entries are made.
type stepOp struct {
	kind, table, from, to string
	id, rev, score        string
	set                   map[string]string
	unset                 []string
}

// stepOps collects a step's changes, one a card, and makes Layer 1 entries of
// them: the changes of one kind, table, source and destination cell, with the
// same unset list, are one entry whose ids share it and whose fields ride in
// Each; a card is guarded or changed once (a guard of a card the step changes
// is left out, for the change guards it by its revision too: L1 3 refuses a
// card named twice, TWICE). Guards and removes come first, creates last (1.0:
// revision-guarded entries before creates). Layer 1's own guards (count,
// rcount) are kept as given, before them all.
type stepOps struct {
	ops   []stepOp
	at    map[string]int // table \x00 id -> index into ops
	raw   []tset.Entry
	fault error
}

func cellOfCard(c *sprint.Card) string { return c.Row + ":" + c.Col }

func scoreText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func (b *stepOps) add(op stepOp) {
	if b.at == nil {
		b.at = map[string]int{}
	}
	k := op.table + "\x00" + op.id
	if i, ok := b.at[k]; ok {
		switch {
		case op.kind == "guard":
			return // the change guards it already
		case b.ops[i].kind == "guard":
			b.ops[i] = op
			return
		default:
			if b.fault == nil {
				b.fault = fmt.Errorf("verbs: the plan changes %s card %s twice in one step", op.table, op.id)
			}
			return
		}
	}
	b.at[k] = len(b.ops)
	b.ops = append(b.ops, op)
}

// guard checks a card at its place and revision, and changes nothing.
func (b *stepOps) guard(table string, c *sprint.Card) {
	b.add(stepOp{kind: "guard", table: table, from: cellOfCard(c), id: c.ID, rev: strconv.FormatUint(c.Rev, 10)})
}

// move moves a card at its place and revision to col of its row (col "" keeps
// it where it is), with fields set and unset.
func (b *stepOps) move(table string, c *sprint.Card, col string, set map[string]string, unset ...string) {
	to := ""
	if col != "" && col != c.Col {
		to = c.Row + ":" + col
	}
	b.add(stepOp{kind: "move", table: table, from: cellOfCard(c), to: to, id: c.ID, rev: strconv.FormatUint(c.Rev, 10),
		set: set, unset: presentOf(c, unset)})
}

// moveScored is move with a new score.
func (b *stepOps) moveScored(table string, c *sprint.Card, col string, score float64, set map[string]string, unset ...string) {
	b.move(table, c, col, set, unset...)
	if i, ok := b.at[table+"\x00"+c.ID]; ok && b.ops[i].kind == "move" {
		b.ops[i].score = scoreText(score)
	}
}

// remove takes a card at its place and revision off the table, its record
// kept, with fields set.
func (b *stepOps) remove(table string, c *sprint.Card, set map[string]string) {
	b.add(stepOp{kind: "remove", table: table, from: cellOfCard(c), id: c.ID, rev: strconv.FormatUint(c.Rev, 10), set: set})
}

// create places a new card (Layer 1 refuses EXISTS when the id has a record).
func (b *stepOps) create(table, row, col, id string, score float64, set map[string]string) {
	b.add(stepOp{kind: "create", table: table, to: row + ":" + col, id: id, score: scoreText(score), set: set})
}

// entry adds a Layer 1 guard entry (count, rcount) as it is.
func (b *stepOps) entry(e tset.Entry) { b.raw = append(b.raw, e) }

// unsetRefused unsets refused on every primary the step moves that holds it,
// and returns their ids (1.3.5, 1.5.3: the builder unsets refused on every
// card the verb changes; the model's rework, VEff "rework", sets refused
// FALSE). The one builder (IT04, IT18) owns this for every verb; until it
// does, rework and return call it here.
func (b *stepOps) unsetRefused(s *sprint.Snapshot) []string {
	var out []string
	for i := range b.ops {
		op := &b.ops[i]
		if op.table != sprint.Work || op.kind != "move" || contains(op.unset, "refused") {
			continue
		}
		if c := s.Work.Card(op.id); c != nil && c.Has("refused") {
			op.unset = append(append([]string(nil), op.unset...), "refused")
			out = append(out, op.id)
		}
	}
	return out
}

// presentOf is the unset names a card holds (unsetting an absent field
// changes nothing, and would name a field the read did not load).
func presentOf(c *sprint.Card, names []string) []string {
	var out []string
	for _, n := range names {
		if c.Has(n) {
			out = append(out, n)
		}
	}
	return out
}

var opRank = map[string]int{"guard": 0, "remove": 1, "move": 2, "create": 3}

// entries is the step's Layer 1 entries.
func (b *stepOps) entries() ([]tset.Entry, error) {
	if b.fault != nil {
		return nil, b.fault
	}
	type group struct {
		e     tset.Entry
		order int
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, op := range b.ops {
		unset := append([]string(nil), op.unset...)
		sort.Strings(unset)
		key := strings.Join([]string{op.kind, op.table, op.from, op.to, strconv.FormatBool(op.score != ""), strconv.FormatBool(op.rev != ""),
			strings.Join(unset, ",")}, "\x00")
		g := byKey[key]
		if g == nil {
			g = &group{e: tset.Entry{Kind: op.kind, Table: op.table, From: op.from, To: op.to}, order: len(groups)}
			if len(unset) != 0 {
				g.e.Unset = unset
			}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.e.IDs = append(g.e.IDs, op.id)
		if op.kind != "guard" {
			g.e.About = append(g.e.About, op.id)
			each := map[string]string{}
			for k, v := range op.set {
				each[k] = v
			}
			g.e.Each = append(g.e.Each, each)
		}
		if op.rev != "" {
			g.e.Revs = append(g.e.Revs, tset.Decimal(op.rev))
		}
		if op.score != "" {
			g.e.Scores = append(g.e.Scores, op.score)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return opRank[groups[i].e.Kind] < opRank[groups[j].e.Kind] })
	out := append([]tset.Entry(nil), b.raw...)
	for _, g := range groups {
		empty := true
		for _, m := range g.e.Each {
			if len(m) != 0 {
				empty = false
			}
		}
		if empty {
			g.e.Each = nil
		}
		out = append(out, g.e)
	}
	return out, nil
}

// planOps are the changes of a planner's sprint.Plan (the pure planners of
// package sprint, today's ntable entries) as a step's changes: a create, a
// move (with or without a new place or score), a remove, or a guard of a card
// at its place and revision.
func planOps(b *stepOps, p sprint.Plan) error {
	for _, u := range p.Units {
		if len(u.Bumps) != 0 {
			return fmt.Errorf("verbs: the plan of %s bumps a counter, which the write path does not carry", u.Key)
		}
		for _, ch := range u.Changes {
			if err := changeOp(b, ch.Table, ch.Entry); err != nil {
				return err
			}
		}
	}
	for _, pw := range p.Props {
		guard := tset.Entry{Kind: "propguard", Table: pw.Table, Name: pw.Name}
		if !pw.WasAbsent {
			was := pw.Was
			guard.Value = &was
		}
		value := pw.Value
		b.entry(guard)
		b.entry(tset.Entry{Kind: "prop", Table: pw.Table, Name: pw.Name, Value: &value})
	}
	return nil
}

func changeOp(b *stepOps, table string, e ntable.BatchMemberEntry) error {
	op := stepOp{table: table, id: e.ID, set: e.Set, unset: e.Unset}
	if x := e.Expect; x != nil {
		op.rev = x.Revision
		if x.Place != nil {
			op.from = x.Place.Row + ":" + x.Place.Col
		}
		if len(x.Fields) != 0 {
			return fmt.Errorf("verbs: the plan guards fields of %s card %s, which Layer 1 does not", table, e.ID)
		}
	}
	switch {
	case e.Create != nil:
		op.kind, op.to, op.score, op.rev = "create", e.Create.Row+":"+e.Create.Col, scoreText(e.Create.Score), ""
		if len(op.unset) != 0 {
			return fmt.Errorf("verbs: the plan creates %s card %s with fields unset", table, e.ID)
		}
	case e.Remove:
		op.kind = "remove"
	case e.Move != nil:
		op.kind = "move"
		if to := e.Move.Row + ":" + e.Move.Col; to != op.from {
			op.to = to
		}
		if e.Move.Score != nil {
			op.score = scoreText(*e.Move.Score)
		}
	case len(e.Set) != 0 || len(e.Unset) != 0:
		op.kind = "move" // a change in place
	default:
		op.kind = "guard"
	}
	if op.kind != "create" && op.from == "" {
		return fmt.Errorf("verbs: the plan changes %s card %s without its place", table, e.ID)
	}
	b.add(op)
	return nil
}

// ---- notes (1.3.4, 2.2, 2.5)

// The judgment and notice types these verbs raise, as tables 2.2 and 2.5 name
// them (sprint.Judgments, sprint.Notices), and the causes they are raised
// under (1.3.4: one judgment a type, cause and subject).
const (
	typeReturned     = sprint.NReturned // "returned to review"
	typeCIRed        = sprint.TypeCIRed // 2.2's "ci red on a primary"
	typeCIGreen      = sprint.NCIGreen  // "ci green"
	typeStarted      = sprint.NStartedMerging
	typeBatchLanded  = sprint.NBatchLanded
	typeStreamLanded = sprint.NStreamLanded
	causeReturn      = sprint.CauseReturn
	causeCI          = sprint.CauseCI
	// typeUnfrozen is the request line a drop's last part, or its abort,
	// writes naming the streams it unfreezes (1.5.4; 2.1: it queues resolve:s,
	// pullback:s and deal). The design names the line and no type: this is
	// IT21's, and ingest's row for it (IT01) is owed.
	typeUnfrozen = "the streams were unfrozen"
)

// stopTypes is the judgment a stream stopped by merge's facts raises, by the
// stop's cause (2.2's four "stream stopped" rows), raised and closed under
// the cause itself.
var stopTypes = map[string]string{
	"conflict": sprint.NConflict,
	"cross":    sprint.NCross,
	"red":      sprint.NRed,
	"rejected": sprint.NRejected,
}

func note(op, typ, cause, text string, subjects []string, decisions ...string) sprintfn.NoteReq {
	return sprintfn.NoteReq{Op: op, Type: typ, Cause: cause, Subjects: subjects, Text: text, Decisions: decisions}
}

// decisionsOf are the decisions 2.2's row of a judgment type prints.
func decisionsOf(typ string) []string {
	var out []string
	for _, d := range sprint.Judgments[typ].Decisions {
		out = append(out, d.String())
	}
	return out
}

func streamSubjects(streams []string) []string {
	out := make([]string, len(streams))
	for i, s := range streams {
		out[i] = sprint.StreamSubject(s)
	}
	return out
}

// ---- judgments on cards (1.3.4)

// closer collects close requests: one a judgment field, naming its subjects
// in the order added, each (type, cause, subject) once (J refuses one named
// by two requests of a step). A close of a field a subject does not hold
// writes nothing (1.3.4: "J closes a subject only when the field is
// present"), so a verb may close a field it did not read.
type closer struct {
	order []sprint.JudgmentField
	subs  map[sprint.JudgmentField][]string
	seen  map[[3]string]bool
}

func (k *closer) add(f sprint.JudgmentField, subjects ...string) {
	if k.subs == nil {
		k.subs, k.seen = map[sprint.JudgmentField][]string{}, map[[3]string]bool{}
	}
	for _, s := range subjects {
		key := [3]string{f.Type, f.Cause, s}
		if k.seen[key] {
			continue
		}
		k.seen[key] = true
		if _, ok := k.subs[f]; !ok {
			k.order = append(k.order, f)
		}
		k.subs[f] = append(k.subs[f], s)
	}
}

// notes are the close requests, in the order their fields were added.
func (k *closer) notes(text string) []sprintfn.NoteReq {
	var out []sprintfn.NoteReq
	for _, f := range k.order {
		out = append(out, note(sprintfn.JOpClose, f.Type, f.Cause, text, k.subs[f]))
	}
	return out
}

// everyOpen is every judgment field a card can hold (but the blocked ones,
// sprint.CardJudgmentFields) as open on each of the ids, as the present
// planners read Snapshot.Open (their words, sprint.PlannerWord), and the field
// each stands for, by its Open key. A planner given it returns in Unit.Closes
// the fields its own list answers (IT09's ReworkAt: reviewReworkResolves), and
// the verb closes them: J closes a field only where the subject holds it at
// apply (1.3.4), so no read of jopen is needed, and none races the step.
func everyOpen(ids []string) ([]sprint.Open, map[string]sprint.JudgmentField) {
	all := sprint.CardJudgmentFields()
	opens := make([]sprint.Open, 0, len(ids)*len(all))
	fields := make(map[string]sprint.JudgmentField, len(ids)*len(all))
	for _, id := range ids {
		for i, f := range all {
			o := sprint.Open{Key: sprint.OpenKey("j"+strconv.Itoa(i), id), Note: sprint.Note{Type: sprint.PlannerWord(f.Type)}}
			opens = append(opens, o)
			fields[o.Key] = f
		}
	}
	return opens, fields
}

// resolvesOf are the fields a card can hold whose type is one of a planner's
// list (its words): the judgments a decision answers.
func resolvesOf(types []string) []sprint.JudgmentField {
	var out []sprint.JudgmentField
	for _, f := range sprint.CardJudgmentFields() {
		if contains(types, sprint.PlannerWord(f.Type)) {
			out = append(out, f)
		}
	}
	return out
}

// couldNotMove closes "the machine could not move a card" on the cards, of
// every rule that raises it (1.3.5, 1.5.3; the model's rework, VEff
// "rework", closes it with refused), beside the unset of refused
// (stepOps.unsetRefused).
func couldNotMove(k *closer, ids []string) {
	for _, f := range sprint.CouldNotMoveFields() {
		k.add(f, ids...)
	}
}

// ---- common checks

// refusedIDs is a local refusal naming each id the plan refused, and why:
// nothing of the step (or of the part) is written (1.5.3).
func refusedIDs(verb string, refused []sprint.Refusal) *Refused {
	var parts []string
	for _, r := range refused {
		parts = append(parts, r.Key+": "+r.Why)
	}
	rf := refuseLocal(verb, sprintfn.CodeRequest, "%s", strings.Join(parts, "; "))
	for _, r := range refused {
		rf.Refusal.Detail.IDs = append(rf.Refusal.Detail.IDs, r.Key)
	}
	return rf
}

// sortedIDs checks named ids (each a word of the sprint's alphabet, none
// twice) and sorts them (1.5.3: named ids are sorted, then cut into parts).
func sortedIDs(verb string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, refuseLocal(verb, sprintfn.CodeRequest, "names no card")
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	for i, id := range out {
		if !sprint.ValidID(id) {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a card's id", id)
		}
		if i > 0 && out[i-1] == id {
			return nil, refuseLocal(verb, sprintfn.CodeRequest, "%s is named twice", id)
		}
	}
	return out, nil
}

// idsArgs is a list of named ids as an intent carries it (1.5.4).
func idsArgs(sorted []string) string { return IDsDigest(sorted) }

// named is the part of a sorted list a part of n ids from a continuation
// takes: the index of the next id (1.5.4, "Named ids in parts").
func named(sorted []string, cont string, n int) (ids []string, next int, err error) {
	at := 0
	if cont != "" {
		if at, err = strconv.Atoi(cont); err != nil || at < 0 || at >= len(sorted) {
			return nil, 0, fmt.Errorf("verbs: the continuation %q is not an index of the %d ids", cont, len(sorted))
		}
	}
	end := min(at+max(n, 1), len(sorted))
	return sorted[at:end], end, nil
}

// perPart is how many primaries of a part's chunk a verb takes, when each
// changes at most each members (1.0, "The chunk": the chunk counts physical
// members).
func perPart(chunk, each int) int { return max(1, chunk/each) }

// primaryOf is the primary a named id loaded, nil when it has no record.
func primaryOf(s *sprint.Snapshot, id string) *sprint.Card { return s.Work.Card(id) }

// cardPlace is where a card is, in words.
func cardPlace(c *sprint.Card) string {
	switch {
	case c == nil:
		return "no such card"
	case !c.Placed():
		return "off the table"
	}
	return c.Col
}

// readsAt is the primary's read cards at its attempt that its rcards lists
// and the read loaded (1.3.1: rcards is every read card ever made for it).
func readsAt(s *sprint.Snapshot, c *sprint.Card) []*sprint.Card {
	var out []*sprint.Card
	attempt := c.Int("attempt")
	for _, id := range sprint.Split(c.F("rcards")) {
		if rc := s.Readers.Card(id); rc != nil && rc.Int("attempt") == attempt {
			out = append(out, rc)
		}
	}
	return out
}

// okPair is two ok read cards of two different readers at the primary's
// attempt and head, in rcards order (R9's guard; ReadCardAgrees).
func okPair(s *sprint.Snapshot, c *sprint.Card) []*sprint.Card {
	var oks []*sprint.Card
	seen := map[string]bool{}
	for _, rc := range readsAt(s, c) {
		rd := rc.F("reader")
		if rc.Placed() && rc.Col == sprint.OK && rc.F("head") == c.F("head") && sprint.ReadCardAgrees(rc) && !seen[rd] {
			seen[rd] = true
			if oks = append(oks, rc); len(oks) == sprint.AcceptReaders {
				break
			}
		}
	}
	return oks
}

// ---- ask (section 3; 2.3 R8)

// AskReq is ask's request: the primaries, and Another for `ask --another`
// (one more reader for a primary already asked at its attempt).
type AskReq struct {
	Op      string
	IDs     []string
	Another bool
}

// askFields is what ask reads of a primary and its read cards.
var askFields = []string{sprint.PrimaryField, "attempt", "result", "asked", "refused", "rcards", "head", "reader"}

// askDeadline is an asked read card's due (1.2: 30 minutes).
const askDeadline = 30 * time.Minute

// Ask asks readers for primaries in review (section 3, `ask --another p...`;
// 2.2's "stranded in review" prints `ask` for one never asked at its attempt).
// Without Another it is R8's ask by the coordinator: two read cards to the two
// readers with the shortest asked queues, those the primary names first. With
// Another, one more read card to a reader not yet asked at the attempt. A
// primary that already has 15 read cards is refused (rcards is at most 15,
// 1.3.1), as are one not in review, one whose work came back failed, one
// asked already (without Another) or never asked (with it), and one with no
// free reader. One step, all or nothing: any refused primary refuses the
// verb, naming each. Guard: each primary at review with its revision, each
// read card absent (its create). Two round trips.
func Ask(ctx context.Context, e *Env, req AskReq) (Result, error) {
	verb := "ask"
	if req.Another {
		verb = "ask --another"
	}
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	vr := verbRead{plan: sprint.ReadPlan{Sprint: []sprint.SprintQ{
		related(ids, askFields, []string{sprint.FollowRCards}),
		{Kind: sprint.QueryReaders, Fields: []string{}},
	}}}
	var failed error
	return e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"ids": idsArgs(ids), "another": req.Another},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest { return vr.readAt(e.Names, epoch, &failed) },
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			req, err := planAsk(verb, va, ids, req.Another)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: req}, nil
		}})
}

func planAsk(verb string, va *verbAnswer, ids []string, another bool) (*sprintfn.Request, error) {
	s, now := va.snap, va.now
	readers := s.Readers.Rows()
	queue := map[string]int{}
	for _, rd := range readers {
		queue[rd] = s.Readers.Count(rd, sprint.Asked)
	}
	var b stepOps
	var refused []sprint.Refusal
	var asked []string
	for _, id := range ids {
		c := primaryOf(s, id)
		if c == nil || !c.Placed() || c.Col != sprint.Review {
			refused = append(refused, sprint.Refusal{Key: id, Why: "not in review (" + cardPlace(c) + ")"})
			continue
		}
		rcards := sprint.Split(c.F("rcards"))
		if len(rcards) >= sprint.MaxRCards {
			refused = append(refused, sprint.Refusal{Key: id, Why: fmt.Sprintf("it already has %d read cards, the most a primary has (rcards at most %d)", len(rcards), sprint.MaxRCards)})
			continue
		}
		if c.F("result") == "failed" {
			refused = append(refused, sprint.Refusal{Key: id, Why: "its work came back failed: rework or drop it"})
			continue
		}
		attempt := c.Int("attempt")
		have := map[string]bool{}
		placed := 0
		for _, rc := range readsAt(s, c) {
			have[rc.F("reader")] = true
			if rc.Placed() {
				placed++
			}
		}
		if another && len(have) == 0 {
			refused = append(refused, sprint.Refusal{Key: id, Why: fmt.Sprintf("not asked yet at attempt %d: --another adds a reader to one already asked", attempt)})
			continue
		}
		if !another && placed > 0 {
			refused = append(refused, sprint.Refusal{Key: id, Why: "asked already at its attempt: --another adds a reader"})
			continue
		}
		want := sprint.AskReaders
		if another {
			want = 1
		}
		want = min(want, sprint.MaxRCards-len(rcards))
		var free []string
		for _, rd := range readers {
			if !have[rd] {
				free = append(free, rd)
			}
		}
		var chosen []string
		if !another {
			for _, rd := range sprint.Split(c.F("asked")) {
				if contains(free, rd) && !contains(chosen, rd) && len(chosen) < want {
					chosen = append(chosen, rd)
				}
			}
		}
		for len(chosen) < want {
			best := ""
			for _, rd := range free {
				if !contains(chosen, rd) && (best == "" || queue[rd] < queue[best]) {
					best = rd
				}
			}
			if best == "" {
				break
			}
			chosen = append(chosen, best)
		}
		if len(chosen) < want {
			refused = append(refused, sprint.Refusal{Key: id, Why: fmt.Sprintf("needs %d different readers and %d is free who has not read attempt %d; add one: nova-sprint reader add <name>", want, len(chosen), attempt)})
			continue
		}
		for _, rd := range chosen {
			queue[rd]++
			rid := sprint.ReadCardID(c.ID, attempt, rd)
			b.create(sprint.Readers, rd, sprint.Asked, rid, c.Score, map[string]string{"kind": "read", sprint.PrimaryField: c.ID, "stream": c.Row,
				"reader": rd, "attempt": strconv.Itoa(attempt), "head": c.F("head"), "asked": wallStamp(now), "due_unbegun": dueAt(now, askDeadline)})
			rcards = append(rcards, rid)
		}
		set := map[string]string{"rcards": strings.Join(rcards, ",")}
		if !another {
			set["asked"] = strings.Join(chosen, ",")
		}
		b.move(sprint.Work, c, "", set)
		asked = append(asked, c.ID)
	}
	if len(refused) != 0 {
		return nil, refusedIDs(verb, refused)
	}
	entries, err := b.entries()
	if err != nil {
		return nil, err
	}
	// The judgments the ask answers on each primary: IT09's lists
	// (sprint.AskResolves, AskAnotherResolves: R16's stranded and stalled,
	// and for --another the exhausted and broken reads).
	resolves := sprint.AskResolves
	if another {
		resolves = sprint.AskAnotherResolves
	}
	var k closer
	for _, f := range resolvesOf(resolves) {
		k.add(f, asked...)
	}
	return &sprintfn.Request{Meta: sprintfn.Meta{Verb: "ask"}, Body: sprintfn.Body{Entries: entries, Notes: k.notes("asked by the coordinator")}}, nil
}

// ---- accept (section 3; 2.3 R9 by the coordinator)

// AcceptReq is accept's request: named primaries, or the streams whose review
// cells it walks (--stream), and the chunk its parts run at (0 is StepChunk).
type AcceptReq struct {
	Op      string
	IDs     []string
	Streams []string
	Chunk   int
}

// acceptFields is what accept reads of a primary and what its follows reach:
// the read cards (reader, attempt, head), the merge card (its place), the
// stream's control card (state).
var acceptFields = []string{sprint.PrimaryField, "attempt", "head", "ci", "ci_head", "refused", "rcards", "reader", "state"}

var acceptFollow = []string{sprint.FollowRCards, sprint.FollowMerge, sprint.FollowControl}

// acceptEach is the most members one primary's accept changes: the primary,
// its merge card, and the outstanding read cards it retires (at most 15 less
// the two ok ones), beside its stream's control card.
const acceptEach = 2 + sprint.MaxRCards - sprint.AcceptReaders + 1

// mergeIdleSpan is a merging stream's deadline for its next merge step (1.2:
// last merge step or state change + 30 min).
const mergeIdleSpan = 30 * time.Minute

// Accept accepts primaries for merging, as R9 by the coordinator (section 3):
// review -> merging, the merge card queued at the primary's score (created, or
// moved from returned), the stream merging with since and due_mergeidle when it
// was waiting, and every outstanding read card of the attempt retired. The
// coordinator may accept a red CI and a primary returned to review; accept
// closes both judgments. Guard: the primary at review with its revision; its
// two ok read cards of two different readers at ok with their revisions; the
// merge card absent (its create) or at returned with its revision; the
// stream's control card with its revision. Named ids go in parts of the
// sorted list, the continuation the next id's index; a primary a part cannot
// accept refuses the part, naming it, and the parts before stay applied
// (1.5.3). With Streams, the parts walk each stream's review cell from a
// cursor to its boundary (1.5.4): the eligible are accepted, the others left
// in review. n + 1 round trips.
func Accept(ctx context.Context, e *Env, req AcceptReq) (Result, error) {
	const verb = "accept"
	if len(req.Streams) != 0 {
		if len(req.IDs) != 0 {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "names cards and --stream both")
		}
		return walkReview(ctx, e, acceptWalk(req.Streams), req.Op, req.Streams, req.Chunk)
	}
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	return namedParts(ctx, e, verb, req.Op, ids, req.Chunk, acceptEach, nil,
		func(part []string) verbRead {
			return verbRead{plan: sprint.ReadPlan{Sprint: []sprint.SprintQ{related(part, acceptFields, acceptFollow)}}}
		},
		func(va *verbAnswer, part []string) (*sprintfn.Request, error) {
			var b stepOps
			acc := newAcceptance(va)
			var refused []sprint.Refusal
			for _, id := range part {
				if why := acc.accept(&b, primaryOf(va.snap, id), id); why != "" {
					refused = append(refused, sprint.Refusal{Key: id, Why: why})
				}
			}
			if len(refused) != 0 {
				return nil, refusedIDs(verb, refused)
			}
			return acc.request(&b)
		})
}

// acceptance is one part's accept: the streams it started merging, the
// primaries it accepted, and the control cards it has changed or guarded.
type acceptance struct {
	va       *verbAnswer
	accepted []string
	started  []string
	ctl      map[string]bool
}

func newAcceptance(va *verbAnswer) *acceptance { return &acceptance{va: va, ctl: map[string]bool{}} }

// accept plans one primary's accept, or says why it cannot.
func (a *acceptance) accept(b *stepOps, c *sprint.Card, id string) string {
	s, now := a.va.snap, a.va.now
	if c == nil || !c.Placed() || c.Col != sprint.Review {
		return "not in review (" + cardPlace(c) + ")"
	}
	oks := okPair(s, c)
	if len(oks) < sprint.AcceptReaders {
		return fmt.Sprintf("needs ok reads from %d different readers at its head, and has %d", sprint.AcceptReaders, len(oks))
	}
	m := s.Merge.Card(c.ID)
	if m != nil && (!m.Placed() || m.Col != sprint.Returned) {
		return "its merge card is " + cardPlace(m)
	}
	ctl := s.StreamCtl(c.Row)
	if ctl == nil {
		return "stream " + c.Row + " has no control card"
	}
	for _, o := range oks {
		b.guard(sprint.Readers, o)
	}
	for _, rc := range readsAt(s, c) {
		if rc.Placed() && (rc.Col == sprint.Asked || rc.Col == sprint.Reading) {
			b.remove(sprint.Readers, rc, map[string]string{"retired": wallStamp(now), "retired_by": "accept"})
		}
	}
	if m != nil {
		b.moveScored(sprint.Merge, m, sprint.Queued, c.Score, nil)
	} else {
		b.create(sprint.Merge, c.Row, sprint.Queued, c.ID, c.Score, map[string]string{"kind": "merge", sprint.PrimaryField: c.ID, "stream": c.Row})
	}
	readers := oks[0].F("reader") + "," + oks[1].F("reader")
	b.move(sprint.Work, c, sprint.Merging, map[string]string{"readers": readers, "accepted": wallStamp(now)})
	if !a.ctl[c.Row] {
		a.ctl[c.Row] = true
		if ctl.F("state") == sprint.StreamWaiting {
			b.move(sprint.Merge, ctl, "", map[string]string{"state": sprint.StreamMerging, "since": wallStamp(now), "due_mergeidle": dueAt(now, mergeIdleSpan)})
			a.started = append(a.started, c.Row)
		} else {
			b.guard(sprint.Merge, ctl)
		}
	}
	a.accepted = append(a.accepted, c.ID)
	return ""
}

// request is the part's step: its entries, and its notes: the returned and
// the ci red judgments closed on each primary accepted (the decision answers
// them, 2.2), and KNOW "stream started merging" for each stream it started.
func (a *acceptance) request(b *stepOps) (*sprintfn.Request, error) {
	entries, err := b.entries()
	if err != nil {
		return nil, err
	}
	req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "accept"}, Body: sprintfn.Body{Entries: entries}}
	if len(a.accepted) != 0 {
		req.Body.Notes = append(req.Body.Notes,
			note(sprintfn.JOpClose, typeReturned, causeReturn, "accepted by the coordinator", a.accepted),
			note(sprintfn.JOpClose, typeCIRed, causeCI, "accepted by the coordinator", a.accepted))
	}
	for _, st := range a.started {
		req.Body.Notes = append(req.Body.Notes, note(sprintfn.JOpKnow, typeStarted, "", "", []string{sprint.StreamSubject(st)}))
	}
	return req, nil
}

// namedParts runs a verb over named ids in parts (1.5.4, "Named ids in
// parts"): each part reads its slice of the sorted ids (n at a time for the
// chunk, each changing at most each members) and plans its step; the
// continuation is the index of the next id. extra are the verb's arguments
// beside the ids.
func namedParts(ctx context.Context, e *Env, verb, op string, ids []string, chunk, each int, extra map[string]any,
	readOf func(part []string) verbRead, planOf func(va *verbAnswer, part []string) (*sprintfn.Request, error)) (Result, error) {
	args := map[string]any{"ids": idsArgs(ids), "n": len(ids)}
	for k, v := range extra {
		args[k] = v
	}
	var failed error
	return e.Parts(ctx, op, PartsPlan{Verb: verb, Args: args, Chunk: chunk,
		Read: func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
			part, _, err := named(ids, cont, perPart(chunk, each))
			if err != nil {
				failed = err
				return clockRead(epoch)
			}
			return readOf(part).readAt(e.Names, epoch, &failed)
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			part, next, err := named(ids, cont, perPart(chunk, each))
			if err != nil {
				return Part{}, err
			}
			va, err := readOf(part).load(rd)
			if err != nil {
				return Part{}, err
			}
			req, err := planOf(va, part)
			if err != nil {
				return Part{}, err
			}
			return Part{Req: req, Next: strconv.Itoa(next), Last: next >= len(ids)}, nil
		}})
}

// ---- the walk of review cells (1.5.4, "Accept, rework --stream")

// walkCont is a walk's continuation: the stream index, the cursor (the last
// score examined, "" before the first), the boundary recorded when the walk
// began the stream ("" before), and how many cards it left in review below
// the cursor, which its head read reads past.
type walkCont struct {
	Stream   int    `json:"s"`
	Cursor   string `json:"c,omitempty"`
	Boundary string `json:"b,omitempty"`
	Passed   int    `json:"p,omitempty"`
	Started  bool   `json:"st,omitempty"`
}

// walkHeadMax is the most ids a walk's head read names: each costs its
// record and its follows (at most 18), inside Layer 1's 10,000 records a read.
const walkHeadMax = 256

// walkWindow is the most cards one part of a walk examines.
const walkWindow = 64

// walk is one verb's walk of review cells: what its head read reads of each
// card and beside it, the members one card's change costs, and the plan of the
// cards one part examines, which returns the part's step and how many of them
// it left in review.
type walk struct {
	verb   string
	args   map[string]any
	fields []string
	follow []string
	beside []sprint.SprintQ // composite queries read beside the head (rework: the fleet)
	each   int
	plan   func(va *verbAnswer, ctl *sprint.Card, cards []*sprint.Card) (*sprintfn.Request, int, error)
	// advance moves the cursor to the score of the card examined; a test
	// freezes it to hold the progress bound to a walk that would not end.
	advance func(wc *walkCont, score float64)
}

func advanceCursor(wc *walkCont, score float64) { wc.Cursor = scoreText(score) }

// acceptWalk is accept --stream's walk: each card examined accepted as R9 by
// the coordinator, or left in review.
func acceptWalk(streams []string) walk {
	return walk{verb: "accept", args: map[string]any{"streams": append([]string(nil), streams...)},
		fields: acceptFields, follow: acceptFollow, each: acceptEach, advance: advanceCursor,
		plan: func(va *verbAnswer, ctl *sprint.Card, cards []*sprint.Card) (*sprintfn.Request, int, error) {
			var b stepOps
			acc := newAcceptance(va)
			left := 0
			for _, c := range cards {
				if why := acc.accept(&b, c, c.ID); why != "" {
					left++
				}
			}
			if len(b.ops) == 0 {
				b.guard(sprint.Merge, ctl) // a part that accepts nothing still writes its receipt
			}
			req, err := acc.request(&b)
			return req, left, err
		}}
}

// walkReview walks the review cells of streams in parts (1.5.4): from a
// cursor (the last score examined) up to the boundary recorded when the walk
// began the stream (the highest score in its review cell then), examining at
// most walkWindow cards a part and planning them through the walk's plan. The
// ineligible stay in review and the head does not shrink, so each part reads
// the head of the cell past those it left (Passed) and examines only the
// cards above the cursor: U1 keeps scores unique, so the cursor is exact. A
// card that enters review below the cursor after it passed is not in this
// op's selection. The design records every stream's boundary at part 1; a
// continuation holds at most 4 KiB, so a walk records each stream's when it
// begins it (a card that enters a later stream's review cell before the walk
// reaches it is in the selection). A part that does not end its stream must
// move the cursor up, or it is refused NOPROGRESS before anything is written:
// every part examines a card above the cursor, so a walk ends. The model's
// action is PartApply (a part applies once, its receipt holding its
// continuation), tla/SprintEvents.tla.
func walkReview(ctx context.Context, e *Env, w walk, op string, streams []string, chunk int) (Result, error) {
	verb := w.verb
	for _, s := range streams {
		if !sprint.ValidID(s) {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%q is not a stream", s)
		}
	}
	readOf := func(wc walkCont) verbRead {
		s := streams[wc.Stream]
		head := min(walkHeadMax, wc.Passed+walkWindow)
		vr := verbRead{plan: sprint.ReadPlan{
			IDs: map[string][]string{sprint.Merge: {sprint.CtlID(s)}},
			Sprint: append([]sprint.SprintQ{{Kind: sprint.QueryRelated, Table: sprint.Work,
				Source: sprint.IDSource{Kind: sprint.SourceHead, Key: s + ":" + sprint.Review, Limit: head},
				Fields: w.fields, Follow: w.follow}}, w.beside...)}}
		if !wc.Started {
			vr.extra = []tset.ReadQuery{{Kind: "range", Table: sprint.Work, Cell: s + ":" + sprint.Review, Min: "-inf", Max: "+inf", Limit: 1, Desc: true}}
		}
		return vr
	}
	decode := func(cont string) (walkCont, error) {
		var wc walkCont
		if cont == "" {
			return wc, nil
		}
		if err := json.Unmarshal([]byte(cont), &wc); err != nil || wc.Stream < 0 || wc.Stream >= len(streams) {
			return wc, fmt.Errorf("verbs: the continuation %q is not a walk's", cont)
		}
		return wc, nil
	}
	var failed error
	return e.Parts(ctx, op, PartsPlan{Verb: verb, Args: w.args, Chunk: chunk,
		Read: func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
			wc, err := decode(cont)
			if err != nil {
				failed = err
				return clockRead(epoch)
			}
			return readOf(wc).readAt(e.Names, epoch, &failed)
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			wc, err := decode(cont)
			if err != nil {
				return Part{}, err
			}
			vr := readOf(wc)
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			s := streams[wc.Stream]
			if !wc.Started {
				wc.Started = true
				if top := va.extra[0]; len(top.Scores) != 0 {
					wc.Boundary = top.Scores[0]
				} else {
					wc.Boundary = "-inf" // an empty review cell: nothing to walk
				}
			}
			ctl := va.snap.StreamCtl(s)
			if ctl == nil {
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "stream %s has no control card", s)
			}
			from := wc.Cursor
			boundary, cursor := parseScore(wc.Boundary, negInf), parseScore(wc.Cursor, negInf)
			head := min(walkHeadMax, wc.Passed+walkWindow) // what this part's read asked
			ans := va.snap.Partial.Answer.Sprint[0]
			window := min(walkWindow, perPart(chunk, w.each))
			var examined []*sprint.Card
			stopped := ""
			for _, id := range ans.IDs {
				c := va.snap.Work.Card(id)
				if c == nil || c.Score <= cursor {
					continue
				}
				if c.Score > boundary {
					stopped = "boundary"
					break
				}
				if len(examined) == window {
					stopped = "window"
					break
				}
				examined = append(examined, c)
				w.advance(&wc, c.Score)
			}
			var done bool
			switch {
			case stopped == "boundary":
				done = true
			case stopped == "window":
				done = false
			case len(ans.IDs) < head:
				done = true // the head held the whole cell
			case len(examined) == 0:
				return Part{}, refuseLocal(verb, sprintfn.CodeLimit, "stream %s holds more than %d cards the walk left in review; name them instead", s, walkHeadMax-walkWindow)
			default:
				done = parseScore(wc.Cursor, negInf) >= boundary // the head was full: read again past the cursor
			}
			if !done && parseScore(wc.Cursor, negInf) <= parseScore(from, negInf) {
				return Part{}, refuseLocal(verb, "NOPROGRESS", "the walk of stream %s did not move its cursor past %s: a part that does not end the stream moves it up", s, orText(from, "the start"))
			}
			req, left, err := w.plan(va, ctl, examined)
			if err != nil {
				return Part{}, err
			}
			wc.Passed += left
			req.Meta.Verb = verb
			last := false
			if done {
				if wc.Stream == len(streams)-1 {
					last = true
				}
				wc = walkCont{Stream: wc.Stream + 1}
			}
			next := ""
			if !last {
				nb, _ := json.Marshal(wc)
				next = string(nb)
			}
			return Part{Req: req, Next: next, Last: last}, nil
		}})
}

const negInf = -1e308

func parseScore(s string, otherwise float64) float64 {
	if s == "" || s == "-inf" {
		return otherwise
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return otherwise
	}
	return f
}

// ---- rework (section 3; 2.3 R10 by the coordinator; IT09's ReworkV21)

// ReworkReq is rework's request: the primaries, or the streams whose review
// cells it walks (--stream), the fix (empty: each one's own), and the chunk
// its parts run at.
type ReworkReq struct {
	Op      string
	IDs     []string
	Streams []string
	Fix     string
	Chunk   int
}

// reworkFields is what IT09's ReworkAt reads of the primaries, their read
// cards, their work cards and the fleet (its documentation names the read).
var reworkFields = []string{sprint.PrimaryField, "attempt", "result", "asked", "refused", "rcards", "work", "bound", "reworks",
	"broken_reads", "readers", "avoid", "reader", "finding", "member", "report", "redeals"}

var reworkFollow = []string{sprint.FollowRCards, sprint.FollowWork, sprint.FollowWithdrawn}

// reworkBeside are the listings ReworkAt reads beside the primaries: the
// fleet's rows, counts and members' status, which it deals the next attempt
// from, and the readers' rows, which the judgment of a primary it refuses
// reads.
var reworkBeside = []sprint.SprintQ{{Kind: sprint.QueryFleet, Fields: []string{"status"}, Props: []string{sprint.PropDealIndex}}, {Kind: sprint.QueryReaders, Fields: []string{}}}

// reworkEach is the most members one primary's rework changes: the primary,
// its read cards (at most 15), its withdrawn work card and the new one.
const reworkEach = 1 + sprint.MaxRCards + 2

// Rework sends primaries back with a fix, as R10 by the coordinator (section
// 3; IT09's ReworkV21, planned here by ReworkAt at the store's R): accepted
// for a primary in review at any attempt and for one in ready at its redeal
// bound (whose withdrawn work card it retires), bound and refused unset, the
// attempt's read cards retired, and the next attempt dealt at once to the up
// member with the shortest ready queue other than avoid (XGUARD memberup on
// it), or the primary to ready when none has room. It closes what ReworkAt
// says it answers (Unit.Closes) and "the machine could not move a card" (the
// model's VEff "rework"). Named ids go in parts of the sorted list; a primary
// the planner refuses refuses its part, naming it (1.5.3). With Streams it
// walks each stream's review cell (walkReview): the reworked leave review,
// the refused stay; a primary in ready at its redeal bound is not in a review
// cell, and is named instead. n + 1 round trips.
func Rework(ctx context.Context, e *Env, req ReworkReq) (Result, error) {
	const verb = "rework"
	if len(req.Streams) != 0 {
		if len(req.IDs) != 0 {
			return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "names cards and --stream both")
		}
		return walkReview(ctx, e, reworkWalk(e, req), req.Op, req.Streams, req.Chunk)
	}
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	return namedParts(ctx, e, verb, req.Op, ids, req.Chunk, reworkEach, map[string]any{"fix": req.Fix},
		func(part []string) verbRead {
			return verbRead{plan: sprint.ReadPlan{Sprint: append([]sprint.SprintQ{related(part, reworkFields, reworkFollow)}, reworkBeside...)}}
		},
		func(va *verbAnswer, part []string) (*sprintfn.Request, error) {
			r, p, err := planRework(va, part, req.Fix, e.Actor)
			if err != nil {
				return nil, err
			}
			if len(p.Refused) != 0 {
				return nil, refusedIDs(verb, p.Refused)
			}
			return r, nil
		})
}

// reworkWalk is rework --stream's walk: the cards examined reworked by
// ReworkAt, those it refuses left in review.
func reworkWalk(e *Env, req ReworkReq) walk {
	return walk{verb: "rework", args: map[string]any{"streams": append([]string(nil), req.Streams...), "fix": req.Fix},
		fields: reworkFields, follow: reworkFollow, beside: reworkBeside, each: reworkEach, advance: advanceCursor,
		plan: func(va *verbAnswer, ctl *sprint.Card, cards []*sprint.Card) (*sprintfn.Request, int, error) {
			ids := make([]string, len(cards))
			for i, c := range cards {
				ids[i] = c.ID
			}
			r, p, err := planRework(va, ids, req.Fix, e.Actor)
			if err != nil {
				return nil, 0, err
			}
			if r == nil {
				var b stepOps
				b.guard(sprint.Merge, ctl) // a part that reworks nothing still writes its receipt
				entries, err := b.entries()
				if err != nil {
					return nil, 0, err
				}
				r = &sprintfn.Request{Meta: sprintfn.Meta{Verb: "rework"}, Body: sprintfn.Body{Entries: entries}}
			}
			return r, len(cards) - len(p.Units), nil
		}}
}

// planRework is the rework of ids on a verb's read: IT09's ReworkAt at the
// store's R, planned on a snapshot whose Open holds every field a card can hold
// (everyOpen), so its Unit.Closes are the fields its own list answers; the
// step closes them with "the machine could not move a card", unsets refused
// on each primary it moves (stepOps.unsetRefused), and guards memberup on each
// member it deals to. The request is nil when nothing is reworked; the plan
// carries the refusals.
func planRework(va *verbAnswer, ids []string, fix, who string) (*sprintfn.Request, sprint.Plan, error) {
	opens, fields := everyOpen(ids)
	va.snap.Open = opens
	p := sprint.ReworkAt(va.snap, sprint.ReworkReq{Sel: sprint.Sel{IDs: ids}, Fix: fix, Who: who}, va.now)
	va.snap.Open = nil
	if len(p.Units) == 0 {
		return nil, p, nil
	}
	var b stepOps
	if err := planOps(&b, p); err != nil {
		return nil, p, err
	}
	b.unsetRefused(va.snap)
	entries, err := b.entries()
	if err != nil {
		return nil, p, err
	}
	r := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "rework"}, Body: sprintfn.Body{Entries: entries}}
	var k closer
	var reworked []string
	for _, u := range p.Units {
		reworked = append(reworked, u.Key)
		for _, o := range u.Closes {
			if f, ok := fields[o.Key]; ok {
				k.add(f, o.Subject())
			}
		}
		for _, ch := range u.Changes {
			if ch.Table == sprint.Fleet && ch.Entry.Create != nil {
				r.Body.Guards = append(r.Body.Guards, sprintfn.XGuard{Kind: "memberup", Member: ch.Entry.Create.Row})
			}
		}
	}
	couldNotMove(&k, reworked)
	r.Body.Notes = k.notes("reworked by the coordinator")
	return r, p, nil
}

// ---- return (section 3)

// ReturnReq is return's request.
type ReturnReq struct {
	Op  string
	IDs []string
}

// Return sends merging primaries back to review (section 3): merging ->
// review, the merge card queued (or stuck) -> returned, and "returned to
// review" opened on each: the coordinator decides it again; refused unset
// and "the machine could not move a card" closed on each (1.3.5, 1.5.3;
// stepOps.unsetRefused). One step, all or nothing. Guard: each primary at merging and its merge card at its cell, each
// with its revision. Two round trips.
func Return(ctx context.Context, e *Env, req ReturnReq) (Result, error) {
	const verb = "return"
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	vr := verbRead{plan: sprint.ReadPlan{Sprint: []sprint.SprintQ{related(ids, []string{sprint.PrimaryField, "need_card", "need_stream", "refused"}, []string{sprint.FollowMerge})}}}
	var failed error
	return e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"ids": idsArgs(ids)},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest { return vr.readAt(e.Names, epoch, &failed) },
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			var b stepOps
			var refused []sprint.Refusal
			for _, id := range ids {
				if why := retreat(&b, va, primaryOf(va.snap, id), "returned"); why != "" {
					refused = append(refused, sprint.Refusal{Key: id, Why: why})
				}
			}
			if len(refused) != 0 {
				return Part{}, refusedIDs(verb, refused)
			}
			b.unsetRefused(va.snap)
			entries, err := b.entries()
			if err != nil {
				return Part{}, err
			}
			var k closer
			couldNotMove(&k, ids)
			return Part{Req: &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: entries,
				Notes: append([]sprintfn.NoteReq{note(sprintfn.JOpOpen, typeReturned, causeReturn,
					"returned to review from merging: the coordinator decides it again", ids, decisionsOf(typeReturned)...)},
					k.notes("returned by the coordinator")...)}}}, nil
		}})
}

// retreat plans a merging primary's return to review, and its merge card's to
// returned, stamping the primary's field why; it says why it cannot.
func retreat(b *stepOps, va *verbAnswer, c *sprint.Card, why string) string {
	if c == nil || !c.Placed() || c.Col != sprint.Merging {
		return "not merging (" + cardPlace(c) + ")"
	}
	m := va.snap.Merge.Card(c.ID)
	if m == nil || !m.Placed() || (m.Col != sprint.Queued && m.Col != sprint.Stuck) {
		return "its merge card is " + cardPlace(m) + ", not queued or stuck"
	}
	b.move(sprint.Merge, m, sprint.Returned, nil, "need_card", "need_stream")
	b.move(sprint.Work, c, sprint.Review, map[string]string{why: wallStamp(va.now)})
	return ""
}

// ---- ci (section 3)

// CIReq is ci's request: the primaries, the result (Red, or green), and the
// head it is of ("" is each primary's own head).
type CIReq struct {
	Op   string
	IDs  []string
	Red  bool
	Head string
}

// CI records a CI result on primaries (section 3): their CI fields (ci,
// ci_head, ci_at). Red on a merging primary: merging -> review and its merge
// card queued -> returned in the same step, then "ci red on a primary" opened
// on it; red elsewhere: opened. Green: KNOW "ci green", and "ci red on a
// primary" closed on each primary whose red was at this head. One step, all
// or nothing. Guard: each primary with its revision (and its merge card, when
// it retreats). Two round trips.
func CI(ctx context.Context, e *Env, req CIReq) (Result, error) {
	const verb = "ci"
	ids, err := sortedIDs(verb, req.IDs)
	if err != nil {
		return Result{Verb: verb}, err
	}
	vr := verbRead{plan: sprint.ReadPlan{Sprint: []sprint.SprintQ{related(ids, []string{sprint.PrimaryField, "head", "ci", "ci_head", "returned", "need_card", "need_stream"},
		[]string{sprint.FollowMerge})}}}
	result := "green"
	if req.Red {
		result = "red"
	}
	var failed error
	return e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"ids": idsArgs(ids), "result": result, "head": req.Head},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest { return vr.readAt(e.Names, epoch, &failed) },
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if failed != nil {
				return Part{}, failed
			}
			va, err := vr.load(rd)
			if err != nil {
				return Part{}, err
			}
			var b stepOps
			var refused []sprint.Refusal
			var closing []string
			for _, id := range ids {
				c := primaryOf(va.snap, id)
				if c == nil || !c.Placed() || c.Col == sprint.Landed {
					refused = append(refused, sprint.Refusal{Key: id, Why: "not open on the table (" + cardPlace(c) + ")"})
					continue
				}
				head := req.Head
				if head == "" {
					head = c.F("head")
				}
				set := map[string]string{"ci": result, "ci_head": head, "ci_at": wallStamp(va.now)}
				switch {
				case req.Red && c.Col == sprint.Merging:
					if why := retreat(&b, va, c, "returned"); why != "" {
						refused = append(refused, sprint.Refusal{Key: id, Why: why})
						continue
					}
					i := b.at[sprint.Work+"\x00"+c.ID]
					for k, v := range set {
						b.ops[i].set[k] = v
					}
				default:
					if !req.Red && c.F("ci") == "red" && (c.F("ci_head") == "" || c.F("ci_head") == head) {
						closing = append(closing, id)
					}
					b.move(sprint.Work, c, "", set)
				}
			}
			if len(refused) != 0 {
				return Part{}, refusedIDs(verb, refused)
			}
			entries, err := b.entries()
			if err != nil {
				return Part{}, err
			}
			r := &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb}, Body: sprintfn.Body{Entries: entries}}
			if req.Red {
				r.Body.Notes = append(r.Body.Notes, note(sprintfn.JOpOpen, typeCIRed, causeCI, "CI is red at its head", ids, decisionsOf(typeCIRed)...))
			} else {
				r.Body.Notes = append(r.Body.Notes, note(sprintfn.JOpKnow, typeCIGreen, "", "", ids))
				if len(closing) != 0 {
					r.Body.Notes = append(r.Body.Notes, note(sprintfn.JOpClose, typeCIRed, causeCI, "CI green at the same head", closing))
				}
			}
			return Part{Req: r}, nil
		}})
}

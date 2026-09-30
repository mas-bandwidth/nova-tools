package sprintfn

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// LogTwin is the smallest surface of Layer 2's Go log twin that the composed
// twin needs (errata E3, E7.2). Layer 2 names and builds the real one (item
// J9); LogStub stands in until then.
type LogTwin interface {
	// Plan plans one step's lines, writing nothing: one line per emitting
	// entry of the table plan, in its order, then one per note, with seqs
	// after the epoch's last (L2 1, 2). A refusal is the step's.
	Plan(in LogInput) (LogPlan, LogApply, *Refusal)
	// Read serves Layer 2's queries (last, lines, cardlines) of one plan: a
	// page, or one atomic query.
	Read(prefix string, plan tset.ReadPlan) (tset.ReadReply, *Refusal)
}

// LogInput is what the log plans a step from.
type LogInput struct {
	Prefix string
	Epoch  tset.Decimal // the write epoch: the successor on an advance (L2 0)
	NowMS  tset.Decimal // the call's one TIME, every line's at_ms (L2 5)
	Table  TablePlan
	Notes  []tset.Note // the step's notes, in order; NoteSeqs aligns with them
}

// LogApply appends what one Plan planned. The twin calls it only at commit,
// and it cannot fail.
type LogApply func()

// Phases are the sprint's own phases of one step on the twin, which later
// items fill: X (IT13), derive (IT14) and J (IT15). Each of those items adds
// its file to this package and sets its fields of defaultPhases from its
// init; the Lua core takes the same functions through NS.SP.phase. A phase
// left nil refuses CONFIG when the step needs it (X always; derive when the
// step carries intents; J when it carries note requests), so a twin assembled
// without a phase never writes as if the phase had run. A phase runs inside
// the twin's call, as it runs inside the store's function: it must not call
// the twin.
type Phases struct {
	// Before names the ids and fields X, derive and J read in the pre stage,
	// beyond the ids and before_fields of the request's own entries.
	Before  func(st *State, req *Request) []BeforeAsk
	XPre    func(st *State, req *Request, obs *Before) *Refusal
	Derive  func(st *State, in []Intent, obs *Before) ([]tset.Entry, []NoteReq, *Refusal)
	JDecide func(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal)
	XCmds   func(st *State, tp TablePlan, lp LogPlan) []Cmd
	JCmds   func(st *State, jp JPlan, lp LogPlan) []Cmd
	// JBefore names what J reads of the cards a step moves or removes, beside
	// Before's, so that composing X's asks with J's clobbers neither.
	JBefore func(st *State, req *Request) []BeforeAsk
	// JOnEntries says JDecide also runs for a step that carries entries and no
	// note request, since J ends the timed states the entries end (1.3.4); it
	// reads them from st.Entries. A phase set that leaves it false calls JDecide
	// for a step's note requests only.
	JOnEntries bool
	// QueryCheck is the static phase of one sprint query (IT30; errata E6's
	// validate): pure, run for every sprint query of a read before TIME and before
	// any Layer 1 or Layer 2 query, as the store's S.validate runs every validate
	// before it reads anything. A refusal is the read's, at the query's index.
	// Nil: no static check of sprint queries.
	QueryCheck func(q SprintQuery) *Refusal
	// QueryStart begins the sprint queries of one read over what its Layer 1
	// and Layer 2 queries charged (one budget a read, L1 6, 7). Nil: each
	// sprint query is charged alone.
	QueryStart func(st *State, charged QueryCharge)
	// Query answers one sprint query in the read's snapshot (IT30). Nil: a
	// sprint query is a kind the twin does not know, REQUEST.
	Query func(st *State, q SprintQuery) (json.RawMessage, *Refusal)
}

// defaultPhases are the write path's phases, filled by the inits of IT13's
// (X), IT14's (Derive) and IT15's (J) files. A Twin takes a copy when it is
// made. IT30's queries are not among them: a twin answers sprint queries once
// UseQueries installs them, and plans the intents' X commands once UseIntents
// does. IT16's parts are in defaultParts.
var defaultPhases Phases

// beforeRecordsMax is the most distinct records one write observes across
// S.before and S.plan (L1 6: 6,000 (table, stored ID)).
const beforeRecordsMax = 6000

// DivergedError is a twin that can no longer stand for the store. Since
// Layer 1's S15 (Mem.Plan, then Mem.Commit) a refusal of any phase after plan
// leaves the twin as it was, so one case is left: prepare's shared bound
// (L1 6) holds Layer 1's planned commands and argv bytes together with the
// log's and the sprint's, and tset.MemPlan does not carry Layer 1's counts;
// they arrive in the reply of Mem.Commit. The twin checks the log's and the
// sprint's commands before the commit and the total after it, and a total
// over the bound that only Layer 1's counts push over is this error: the store
// would have refused the step and written nothing, the twin has written the
// tables. Every later call returns it, so no test goes on over a state the
// store could not reach. It goes when MemPlan carries Layer 1's counts.
type DivergedError struct {
	Phase string
	// Reason is the refusal the store gives the step. It is not unwrapped: the
	// twin has written, so no caller may read it as "nothing was changed".
	Reason *Refusal
}

// Error names the phase and the store's refusal.
func (e *DivergedError) Error() string {
	return fmt.Sprintf("sprintfn twin: the store refuses this step in %s (%s, budget %s), and Layer 1's share of the bound was known only after Mem.Commit had written the tables; the twin no longer stands for the store",
		e.Phase, e.Reason.Code, e.Reason.Detail.Budget)
}

// Twin is the composed in-memory write path: Layer 1's twin (tset.Mem), Layer
// 2's log twin and the sprint's own keys, driven through the phases of 1.0 in
// their order (errata E3). It samples one time a call and sets it on the Mem,
// so every line of a call carries one at_ms. It is the Mem's one writer: the
// caller defines the four tables on the Mem (errata E1) before NewTwin, and
// steps it only through the twin afterwards.
type Twin struct {
	mu     sync.Mutex
	tab    *tset.Mem
	log    LogTwin
	names  sprint.Names
	prefix string
	keys   *keyspace
	active tset.Decimal
	broken error
	clock  func() time.Time
	phases Phases
	parts  *PartRegistry
	trace  func(phase string)
	// seqs keeps each op's log seqs: tset.Mem's receipt holds "0" for both,
	// having no log, and a replay or a done slot must say the seqs the step
	// wrote (L1 1.4).
	seqs map[string][2]tset.Decimal
	// sprintRead is the budget of the read in flight, set by QueryStart.
	sprintRead *sprintRead
}

// NewTwin composes a twin over a Mem whose four tables are defined, a log
// twin, and one deployment's names (8.0; errata E3).
func NewTwin(tab *tset.Mem, log LogTwin, names sprint.Names) *Twin {
	t := &Twin{tab: tab, log: log, names: names, prefix: names.Prefix, keys: newKeyspace(),
		clock: time.Now, phases: defaultPhases, parts: defaultParts, seqs: map[string][2]tset.Decimal{}}
	if tab == nil || log == nil {
		t.broken = errors.New("sprintfn twin: no table twin or no log twin")
		return t
	}
	snap, err := tab.Snapshot(names.Prefix)
	if err != nil {
		t.broken = fmt.Errorf("sprintfn twin: the tables are not defined: %w", err)
		return t
	}
	t.active = snap.ActiveEpoch
	return t
}

// SetClock sets the time source of the twin's calls; nil is the wall clock.
func (t *Twin) SetClock(now func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	t.clock = now
}

// SprintKeys is a copy of the sprint's own keys as the twin holds them.
func (t *Twin) SprintKeys() map[string]KeyValue {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keys.dump()
}

// Pipeline runs every item in order, each atomic alone, and returns one
// result for each. A malformed item refuses the whole pipeline before any
// item runs, as the store's client refuses it before sending.
func (t *Twin) Pipeline(ctx context.Context, items []Item) ([]Result, error) {
	encoded := make([]encodedStep, len(items))
	for i, it := range items {
		ref := checkItem(it)
		switch {
		case ref != nil:
		case it.Step != nil:
			encoded[i], ref = encodeStep(t.prefix, it.Step)
		case it.Read != nil:
			_, ref = encodeRead(t.prefix, it.Read)
		default:
			_, ref = checkPage(t.prefix, it.Page)
		}
		if ref != nil {
			return nil, &ItemError{Index: i, Refusal: ref}
		}
	}
	results := make([]Result, len(items))
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			for j := i; j < len(items); j++ {
				results[j].Err = err
			}
			break
		}
		switch {
		case it.Step != nil:
			results[i] = t.step(ctx, it.Step, encoded[i])
		case it.Read != nil:
			results[i] = t.read(ctx, it.Read)
		default:
			results[i] = t.page(it.Page)
		}
	}
	return results, nil
}

func (t *Twin) enter(phase string) {
	if t.trace != nil {
		t.trace(phase)
	}
}

// begin samples the call's one time and sets it on the Mem.
func (t *Twin) begin() (time.Time, tset.Decimal) {
	now := t.clock()
	t.tab.SetClock(func() time.Time { return now })
	return now, tset.Decimal(strconv.FormatInt(now.UnixMilli(), 10))
}

// partPlan is one part the request carries and what its pre decided.
type partPlan struct {
	name string
	part Part
	plan any
}

// step runs one request through 1.0's phases.
func (t *Twin) step(ctx context.Context, req *Request, enc encodedStep) Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken != nil {
		return Result{Err: t.broken}
	}
	_, nowMS := t.begin()
	st := &State{Prefix: t.prefix, Epoch: req.Epoch, NowMS: nowMS, Names: t.names, Keys: &Keys{ks: t.keys}}

	// open: the composed profile's static rule, a fence, a receipt, the epoch.
	t.enter(PhaseOpen)
	if ref := checkAbout(PhaseOpen, enc.step.Entries); ref != nil {
		return Result{Refusal: ref}
	}
	if req.Fence {
		// E5: a fence returns at open. Layer 1 writes its receipt alone.
		t.enter(PhasePrepare)
		t.enter(PhaseCommit)
		return t.layerOneOnly(enc.step)
	}
	if req.Body.Op != nil {
		found, ref := t.receiptFound(ctx, enc.step)
		if ref != nil {
			return Result{Refusal: ref}
		}
		if found { // a replay, or OPCONFLICT: no phase runs (L1 1.4)
			return t.layerOneOnly(enc.step)
		}
	}
	if c := compareDecimal(req.Epoch, t.active); c != 0 {
		code := CodeStale
		if c > 0 {
			code = CodeEpochAhead
		}
		ref := refuse(PhaseOpen, code, RefusalDetail{RefusalDetail: tset.RefusalDetail{ActiveEpoch: t.active}})
		ref.Message = fmt.Sprintf("%s (active_epoch=%s); nothing was changed", code, t.active)
		return Result{Refusal: ref}
	}

	// the pre stage: every phase reads and decides; nothing writes.
	t.enter(PhaseBefore)
	obs, ref := t.before(ctx, st, req)
	if ref != nil {
		return Result{Refusal: ref}
	}
	t.enter(PhaseXPre)
	if t.phases.XPre == nil {
		return Result{Refusal: refuse(PhaseXPre, CodeConfig, RefusalDetail{})}
	}
	if ref := t.phases.XPre(st, req, obs); ref != nil {
		return Result{Refusal: withPhase(ref, PhaseXPre)}
	}
	t.enter(PhaseDerive)
	var derived []tset.Entry
	noteReqs := append([]NoteReq(nil), req.Body.Notes...)
	if len(req.Body.Intents) != 0 {
		if t.phases.Derive == nil {
			return Result{Refusal: refuse(PhaseDerive, CodeConfig, RefusalDetail{})}
		}
		entries, notes, ref := t.phases.Derive(st, req.Body.Intents, obs)
		if ref != nil {
			return Result{Refusal: withPhase(ref, PhaseDerive)}
		}
		derived, noteReqs = entries, append(noteReqs, notes...)
	}
	t.enter(PhaseJ)
	var notes []tset.Note
	var jp JPlan
	ranJ := false
	// J sees the step's combined entries, the caller's and the derived ones
	// (S.plan takes the same list), for the timed states they end (1.3.4): it
	// runs for any step with entries as well as for one with note requests.
	st.Entries = append(append([]tset.Entry{}, enc.step.Entries...), derived...)
	if len(noteReqs) != 0 || (t.phases.JOnEntries && t.phases.JDecide != nil && len(st.Entries) != 0) {
		if t.phases.JDecide == nil {
			return Result{Refusal: refuse(PhaseJ, CodeConfig, RefusalDetail{})}
		}
		var ref *Refusal
		notes, jp, ref = t.phases.JDecide(st, noteReqs, obs)
		if ref != nil {
			return Result{Refusal: withPhase(ref, PhaseJ)}
		}
		ranJ = true
	}
	t.enter(PhaseParts)
	var plans []partPlan
	for _, name := range PartOrder {
		if !requestPart(req, name) {
			continue
		}
		p, ok := t.parts.Lookup(name)
		if !ok {
			return Result{Refusal: refuse(PhaseParts, CodeConfig, RefusalDetail{})}
		}
		plan, ref := p.Pre(st, req, obs)
		if ref != nil {
			return Result{Refusal: withPhase(ref, PhaseParts)}
		}
		plans = append(plans, partPlan{name: name, part: p, plan: plan})
	}

	// plan: Layer 1 over the combined entries, planned and not written
	// (Mem.Plan, S15). J's notes stay out of the Mem, whose encoder asks an op
	// of any note-bearing step, which a preplan's notes do not need (errata 2,
	// item 9; S15 keeps this route); their bounds are checked here.
	t.enter(PhasePlan)
	if ref := checkAbout(PhasePlan, derived); ref != nil {
		return Result{Refusal: ref}
	}
	combined := enc.step
	combined.Entries = append(append([]tset.Entry{}, enc.step.Entries...), derived...)
	if ref := checkNotes(combined.Entries, notes); ref != nil {
		return Result{Refusal: ref}
	}
	mp, lref := t.tab.Plan(combined)
	if lref != nil {
		return Result{Refusal: fromTset(PhasePlan, lref)}
	}
	if mp.Replay {
		// A captured replay commits at once, and no later planner runs (S15).
		// Open found no receipt, so under the twin's lock this does not happen.
		return t.commitAtOnce(mp, combined)
	}
	tp := tablePlanOf(combined, mp)
	writeEpoch, err := writeEpochOf(combined)
	if err != nil {
		return Result{Refusal: refuse(PhasePlan, "OVERFLOW", RefusalDetail{})}
	}

	// Nothing is written before commit: a refusal from here on leaves the Mem,
	// the log and the sprint's keys as they were.
	t.enter(PhaseLog)
	lp, appendLog, ref := t.log.Plan(LogInput{Prefix: t.prefix, Epoch: writeEpoch, NowMS: nowMS, Table: tp, Notes: notes})
	if ref != nil {
		return Result{Refusal: withPhase(ref, PhaseLog)}
	}
	t.enter(PhaseXPlan)
	if t.phases.XCmds == nil {
		return Result{Refusal: refuse(PhaseXPlan, CodeConfig, RefusalDetail{})}
	}
	cmds := t.phases.XCmds(st, tp, lp)
	if ranJ {
		if t.phases.JCmds == nil {
			return Result{Refusal: refuse(PhaseXPlan, CodeConfig, RefusalDetail{})}
		}
		cmds = append(cmds, t.phases.JCmds(st, jp, lp)...)
	}
	parts := map[string]json.RawMessage{}
	for _, p := range plans {
		pc, ref := p.part.Cmds(st, p.plan, lp)
		if ref != nil {
			return Result{Refusal: withPhase(ref, PhaseXPlan)}
		}
		cmds = append(cmds, pc...)
		b, err := json.Marshal(p.plan)
		if err != nil {
			return Result{Refusal: refuse(PhaseXPlan, CodeRequest, RefusalDetail{})}
		}
		parts[p.name] = b
	}
	t.enter(PhasePrepare)
	cost, ref := t.keys.check(t.prefix, cmds)
	if ref != nil {
		return Result{Refusal: ref}
	}
	// The shared bound over what the twin can see before the commit: the log's
	// commands and the sprint's. Layer 1's share arrives with Mem.Commit.
	if ref := sharedBounds(0, 0, lp, cost); ref != nil {
		return Result{Refusal: ref}
	}
	t.enter(PhaseCommit)
	reply, lref := t.tab.Commit(mp)
	if lref != nil {
		// Commit refuses before it writes: a state moved since the plan
		// (REVISION), which the twin's lock rules out, or a plan used twice.
		return Result{Refusal: fromTset(PhaseCommit, lref)}
	}
	l1Commands, l1Bytes, ok := layerOneCounts(reply)
	if !ok {
		return t.diverge(PhaseCommit, refuse(PhaseCommit, CodeConfig, RefusalDetail{}))
	}
	if ref := sharedBounds(l1Commands, l1Bytes, lp, cost); ref != nil {
		return t.diverge(PhasePrepare, ref)
	}
	t.active = reply.EpochAfter
	appendLog()
	t.keys.apply(cmds)
	reply.FirstSeq, reply.LastSeq, reply.Lines = lp.FirstSeq, lp.LastSeq, lp.LineCount
	reply.MemPlan = nil
	if op := req.Body.Op; op != nil {
		t.seqs[opKey(req.Epoch, op.ID)] = [2]tset.Decimal{lp.FirstSeq, lp.LastSeq}
	}
	return Result{Step: &StepReply{Reply: reply, Parts: parts}}
}

// layerOneOnly is a step Layer 1 settles alone at open: a fence, a replay, or
// an op whose receipt has another intent. It is planned (Mem.Plan) and, for a
// fence or a replay, committed at once (S15): no log, sprint or other planner
// runs. Open found a receipt for an op that is not a fence, so a plan that is
// neither a replay nor a refusal means the done read and the plan disagree,
// and nothing is committed.
func (t *Twin) layerOneOnly(st tset.Step) Result {
	mp, lref := t.tab.Plan(st)
	if lref != nil {
		return Result{Refusal: fromTset(PhaseOpen, lref)}
	}
	if !mp.Replay && !st.Fence {
		return Result{Err: errors.New("sprintfn twin: the done read found a receipt that Layer 1's plan did not; nothing was committed")}
	}
	return t.commitAtOnce(mp, st)
}

// commitAtOnce commits a plan that runs no later phase: a fence, or a replay
// (S15). A replay's seqs are the ones its step wrote, which the twin keeps.
func (t *Twin) commitAtOnce(mp *tset.MemPlan, st tset.Step) Result {
	reply, lref := t.tab.Commit(mp)
	if lref != nil {
		return Result{Refusal: fromTset(PhaseCommit, lref)}
	}
	if !reply.Replay {
		t.active = reply.EpochAfter
	}
	if st.Op != nil {
		if s, ok := t.seqs[opKey(st.Epoch, *st.Op)]; ok && reply.Replay {
			reply.FirstSeq, reply.LastSeq = s[0], s[1]
		}
	}
	reply.MemPlan = nil
	return Result{Step: &StepReply{Reply: reply, Parts: map[string]json.RawMessage{}}}
}

// receiptFound asks Layer 1's done record whether the op already has a
// receipt at the request epoch: a match (a replay) or a conflict.
func (t *Twin) receiptFound(ctx context.Context, st tset.Step) (bool, *Refusal) {
	if compareDecimal(st.Epoch, t.active) > 0 {
		return false, nil // no receipt lies in an epoch not yet reached
	}
	sum := sha1.Sum([]byte(*st.Intent))
	q := tset.ReadQuery{Kind: "done", Ops: []tset.DoneIdentity{{Epoch: st.Epoch, Op: *st.Op, IntentDigest: hex.EncodeToString(sum[:])}}}
	rep, err := t.tab.Read(ctx, newTSetReadPlan(t.prefix, st.Epoch, "atomic", []tset.ReadQuery{q}))
	if err != nil {
		var lref *tset.Refusal
		if errors.As(err, &lref) {
			return false, nil // the epoch holds no receipt the read can see
		}
		return false, refuse(PhaseOpen, CodeConfig, RefusalDetail{})
	}
	if len(rep.Answers) != 1 || len(rep.Answers[0].Done) != 1 {
		return false, refuse(PhaseOpen, CodeConfig, RefusalDetail{})
	}
	return rep.Answers[0].Done[0].Status != "absent", nil
}

// before is S.before: every id the request's entries and intents name, and
// every id a phase asks for, with the fields asked, read from the Mem in one
// snapshot (L1 1.1, 1.2).
func (t *Twin) before(ctx context.Context, st *State, req *Request) (*Before, *Refusal) {
	type ask struct {
		ids    []string
		seen   map[string]bool
		fields map[string]bool
	}
	asks := map[string]*ask{}
	add := func(table string, ids, fields []string) {
		a := asks[table]
		if a == nil {
			a = &ask{seen: map[string]bool{}, fields: map[string]bool{}}
			asks[table] = a
		}
		for _, id := range ids {
			if !a.seen[id] {
				a.seen[id] = true
				a.ids = append(a.ids, id)
			}
		}
		for _, f := range fields {
			a.fields[f] = true
		}
	}
	for _, e := range req.Body.Entries {
		switch e.Kind {
		case "create", "move", "remove", "guard":
			add(e.Table, e.IDs, e.BeforeFields)
		}
	}
	for _, in := range req.Body.Intents {
		var ids []string
		// 8.0 gives a needmet and a needgone a Need and its Waiters and no Card:
		// the empty Card is not an id to read (Layer 1 refuses it REQUEST), and
		// the need, read below, is the card they are about (IT14).
		if in.Card != "" || (in.Kind != IntentNeedMet && in.Kind != IntentNeedGone) {
			ids = append(ids, in.Card)
		}
		ids = append(ids, in.Needs...)
		if in.Need != "" {
			ids = append(ids, in.Need)
		}
		add(sprint.Work, append(ids, in.Waiters...), nil)
	}
	if t.phases.Before != nil {
		for _, a := range t.phases.Before(st, req) {
			add(a.Table, a.IDs, a.Fields)
		}
	}
	if t.phases.JBefore != nil {
		for _, a := range t.phases.JBefore(st, req) {
			add(a.Table, a.IDs, a.Fields)
		}
	}
	obs := &Before{Records: map[string]map[string]tset.MemberRecord{}}
	total := 0
	tables := make([]string, 0, len(asks))
	for table, a := range asks {
		total += len(a.ids)
		tables = append(tables, table)
	}
	if total > beforeRecordsMax {
		return nil, refuse(PhaseBefore, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "before_records"}})
	}
	if total == 0 {
		return obs, nil
	}
	sort.Strings(tables)
	var queries []tset.ReadQuery
	for _, table := range tables {
		a := asks[table]
		if len(a.ids) == 0 {
			continue
		}
		fields := make([]string, 0, len(a.fields))
		for f := range a.fields {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		// A query projects at most tset.MaxFieldsPerMember fields: more fields
		// read the same ids again, and the answers merge.
		for first := 0; first == 0 || first < len(fields); first += tset.MaxFieldsPerMember {
			chunk := fields[first:min(first+tset.MaxFieldsPerMember, len(fields))]
			queries = append(queries, tset.ReadQuery{Kind: "ids", Table: table, IDs: a.ids, Fields: append([]string{}, chunk...)})
		}
	}
	rep, err := t.tab.Read(ctx, newTSetReadPlan(t.prefix, req.Epoch, "atomic", queries))
	if err != nil {
		var lref *tset.Refusal
		if errors.As(err, &lref) {
			if lref.Code == "BUDGET" { // a write plan that exhausts a bound is LIMIT (L1 1.2)
				return nil, refuse(PhaseBefore, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: lref.Detail.Budget}})
			}
			return nil, fromTset(PhaseBefore, lref)
		}
		return nil, refuse(PhaseBefore, CodeConfig, RefusalDetail{})
	}
	for i, q := range queries {
		byID := obs.Records[q.Table]
		if byID == nil {
			byID = map[string]tset.MemberRecord{}
			obs.Records[q.Table] = byID
		}
		for _, r := range rep.Answers[i].Records {
			prior, ok := byID[r.ID]
			if ok {
				for f, v := range r.Fields {
					if prior.Fields == nil {
						prior.Fields = map[string]tset.FieldValue{}
					}
					prior.Fields[f] = v
				}
				r = prior
			}
			byID[r.ID] = r
		}
	}
	return obs, nil
}

// diverge marks the twin as no longer standing for the store (DivergedError):
// only after Mem.Commit, when Layer 1's own counts put the step over prepare's
// shared bound.
func (t *Twin) diverge(phase string, ref *Refusal) Result {
	err := &DivergedError{Phase: phase, Reason: withPhase(ref, phase)}
	t.broken = err
	return Result{Err: err}
}

// read answers an atomic read from one snapshot, under one time: Layer 1's
// queries from the Mem in one call, Layer 2's from the log twin, the
// sprint's from the query phase, each answer in its query's place.
func (t *Twin) read(ctx context.Context, rr *ReadRequest) Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken != nil {
		return Result{Err: t.broken}
	}
	// The static phase, before TIME and before any query is read (IT30).
	if t.phases.QueryCheck != nil {
		for i, q := range rr.Sprint {
			if ref := t.phases.QueryCheck(q); ref != nil {
				return Result{Refusal: atQuery(ref, len(rr.Tset)+i)}
			}
		}
	}
	_, nowMS := t.begin()
	if compareDecimal(rr.Epoch, t.active) > 0 {
		return Result{Refusal: refuse(PhaseOpen, CodeEpochAhead, RefusalDetail{RefusalDetail: tset.RefusalDetail{ActiveEpoch: t.active}})}
	}
	out := &ReadReply{Epoch: rr.Epoch, ActiveEpoch: t.active, TimeMS: nowMS,
		Tset: make([]tset.ReadAnswer, len(rr.Tset)), Sprint: make([]json.RawMessage, len(rr.Sprint))}
	// The store answers the queries in input order and reports the first one
	// that refuses (L1 8). Layer 2's queries are answered one at a time here and
	// Layer 1's in one Mem call, whose refusal names its index within that
	// call, so each source finds its own first refusal and the lower index is
	// the read's. A refusal that names no query is the plan's, which the store
	// finds before it answers any.
	var l1 []int
	var refused *Refusal
	var charged QueryCharge // what the read's Layer 1 and Layer 2 queries charged, for the sprint queries' budget
	refusedAt := -1
	refuseAt := func(ref *Refusal, i int) {
		if refusedAt < 0 || i < refusedAt {
			refused, refusedAt = atQuery(ref, i), i
		}
	}
	for i, q := range rr.Tset {
		switch {
		case q.Kind == "range" && strings.HasPrefix(q.Key, t.prefix+"sprint:"):
			// Layer 1's range in its raw key form reads any sorted set of the
			// space (L1 7), the sprint's own keys among them: the agenda's and
			// the held queue's heads of RT1 (1.4.2). The twin holds those keys
			// itself, and tset.Mem serves the raw form from its fixtures only.
			if refusedAt >= 0 {
				continue
			}
			ans, ref := t.keyRange(q)
			if ref != nil {
				refuseAt(ref, i)
				continue
			}
			out.Tset[i] = ans
			charged.RangeIDs += len(ans.IDs)
		case q.Kind == "last" || q.Kind == "lines" || q.Kind == "cardlines":
			if refusedAt >= 0 {
				continue // a lower index already refused; this one cannot outrank it
			}
			rep, ref := t.log.Read(t.prefix, newTSetReadPlan(t.prefix, rr.Epoch, "atomic", []tset.ReadQuery{q}))
			if ref != nil {
				refuseAt(ref, i)
				continue
			}
			if len(rep.Answers) != 1 {
				refuseAt(refuse(PhaseOpen, CodeConfig, RefusalDetail{}), i)
				continue
			}
			out.Tset[i] = rep.Answers[0]
			charged.addCounters(rep.Counters)
		default:
			l1 = append(l1, i)
		}
	}
	if len(l1) != 0 {
		qs := make([]tset.ReadQuery, len(l1))
		for j, i := range l1 {
			qs[j] = rr.Tset[i]
		}
		rep, err := t.tab.Read(ctx, newTSetReadPlan(t.prefix, rr.Epoch, "atomic", qs))
		if err != nil {
			var lref *tset.Refusal
			if !errors.As(err, &lref) {
				return Result{Err: err}
			}
			ref := fromTset(PhaseOpen, lref)
			if lref.Detail.QueryIndex == nil || *lref.Detail.QueryIndex >= len(l1) {
				return Result{Refusal: ref}
			}
			refuseAt(ref, l1[*lref.Detail.QueryIndex])
		} else {
			for j, i := range l1 {
				out.Tset[i] = t.withDoneSeqs(rr.Tset[i], rep.Answers[j])
			}
			charged.addCounters(rep.Counters)
		}
	}
	if refused != nil {
		return Result{Refusal: refused}
	}
	st := &State{Prefix: t.prefix, Epoch: rr.Epoch, NowMS: nowMS, Names: t.names, Keys: &Keys{ks: t.keys}}
	if len(rr.Sprint) != 0 && t.phases.QueryStart != nil {
		t.phases.QueryStart(st, charged)
	}
	for i, q := range rr.Sprint {
		if t.phases.Query == nil {
			return Result{Refusal: atQuery(refuse(PhaseOpen, CodeRequest, RefusalDetail{}), len(rr.Tset)+i)}
		}
		ans, ref := t.phases.Query(st, q)
		if ref != nil {
			return Result{Refusal: atQuery(ref, len(rr.Tset)+i)}
		}
		out.Sprint[i] = ans
	}
	return Result{Read: out}
}

// keyRange is Layer 1's range in its raw key form over one of the sprint's own
// sorted sets (L1 7): the first Limit members in [Min, Max] by score, then by
// member (Desc from the highest), with their scores and whether more match. The
// key form takes no records and no fields; a limit below 1, or a bound outside
// the ZRANGE grammar, is REQUEST, a limit over 2,000 LIMIT, and a key of
// another type WRONGTYPE.
func (t *Twin) keyRange(q tset.ReadQuery) (tset.ReadAnswer, *Refusal) {
	bad := func(code string) (tset.ReadAnswer, *Refusal) {
		return tset.ReadAnswer{}, refuse(PhaseOpen, code, RefusalDetail{})
	}
	if q.Table != "" || q.Cell != "" || q.Records || q.Fields != nil || q.Limit < 1 {
		return bad(CodeRequest)
	}
	if q.Limit > sprint.MaxRangeLimit {
		return bad(CodeLimit) // as Layer 1's static check refuses it
	}
	if _, _, ok := parseBound(q.Min); !ok {
		return bad(CodeRequest)
	}
	if _, _, ok := parseBound(q.Max); !ok {
		return bad(CodeRequest)
	}
	if kind := t.keys.typeOf(q.Key); kind != kindNone && kind != kindZSet {
		return bad(CodeWrongType)
	}
	pairs := t.keys.zpairs(q.Key)
	if q.Desc {
		for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
			pairs[i], pairs[j] = pairs[j], pairs[i]
		}
	}
	ans := tset.ReadAnswer{Kind: "range", IDs: []string{}, Scores: []string{}}
	for _, p := range pairs {
		if !inBounds(p.score, q.Min, q.Max) {
			continue
		}
		if len(ans.IDs) == q.Limit {
			ans.HasMore = true
			break
		}
		ans.IDs, ans.Scores = append(ans.IDs, p.member), append(ans.Scores, formatScore(p.score))
	}
	return ans, nil
}

// page answers a page of lines from the log twin.
func (t *Twin) page(p *tset.ReadPlan) Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.broken != nil {
		return Result{Err: t.broken}
	}
	_, nowMS := t.begin()
	plan, ref := checkPage(t.prefix, p)
	if ref != nil {
		return Result{Refusal: ref}
	}
	if compareDecimal(plan.Epoch, t.active) > 0 {
		return Result{Refusal: refuse(PhaseOpen, CodeEpochAhead, RefusalDetail{RefusalDetail: tset.RefusalDetail{ActiveEpoch: t.active}})}
	}
	rep, ref := t.log.Read(t.prefix, plan)
	if ref != nil {
		return Result{Refusal: ref}
	}
	rep.ActiveEpoch, rep.TimeMS = t.active, nowMS
	return Result{Page: &rep}
}

// withDoneSeqs puts each matched receipt's log seqs, which the twin keeps,
// into a done answer.
func (t *Twin) withDoneSeqs(q tset.ReadQuery, a tset.ReadAnswer) tset.ReadAnswer {
	if q.Kind != "done" {
		return a
	}
	for i, slot := range a.Done {
		if slot.Receipt == nil || i >= len(q.Ops) {
			continue
		}
		if s, ok := t.seqs[opKey(q.Ops[i].Epoch, q.Ops[i].Op)]; ok {
			r := *slot.Receipt
			r.FirstSeq, r.LastSeq = s[0], s[1]
			a.Done[i].Receipt = &r
		}
	}
	return a
}

func opKey(epoch tset.Decimal, op string) string { return string(epoch) + "\x00" + op }

func withPhase(ref *Refusal, phase string) *Refusal {
	if ref.Phase == "" {
		ref.Phase = phase
	}
	return ref
}

func atQuery(ref *Refusal, index int) *Refusal {
	ref.Detail.QueryIndex = &index
	return ref
}

// checkAbout is the composed profile's rule: every member-changing entry
// supplies about, aligned with its ids (L1 3).
func checkAbout(phase string, entries []tset.Entry) *Refusal {
	for i, e := range entries {
		switch e.Kind {
		case "create", "move", "remove":
			if len(e.About) != len(e.IDs) {
				index := i
				return refuse(phase, CodeRequest, RefusalDetail{RefusalDetail: tset.RefusalDetail{EntryIndex: &index, Table: e.Table}})
			}
		}
	}
	return nil
}

// checkNotes holds J's notes to the step's bounds (L1 6): at most
// tset.MaxNotes notes, each a note line with its about ids, and at most
// tset.MaxAboutBeforeDedup about ids over the entries and the notes together.
func checkNotes(entries []tset.Entry, notes []tset.Note) *Refusal {
	if len(notes) > tset.MaxNotes {
		return refuse(PhasePlan, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "notes"}})
	}
	abouts := 0
	for _, e := range entries {
		abouts += len(e.About)
	}
	for _, n := range notes {
		if n.Line.Kind != "note" || n.About == nil {
			return refuse(PhasePlan, CodeRequest, RefusalDetail{})
		}
		abouts += len(n.About)
	}
	if abouts > tset.MaxAboutBeforeDedup {
		return refuse(PhasePlan, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "about"}})
	}
	return nil
}

// sharedBounds holds Layer 1's planned commands and argv bytes, the log's and
// the sprint's together to the step's bounds (L1 6), as S.prepare does, with
// its refusal: LIMIT, the budget "commands" or "argv_bytes", the actual count
// and the bound. Before the commit the twin passes zero for Layer 1's share,
// which tset.MemPlan does not carry (see DivergedError).
func sharedBounds(l1Commands, l1Bytes int, lp LogPlan, cost cmdCost) *Refusal {
	over := func(budget string, actual, bound int) *Refusal {
		a, b := int64(actual), int64(bound)
		return refuse(PhasePrepare, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: budget, Actual: &a, Limit: &b}})
	}
	if commands := l1Commands + lp.Commands + cost.commands; commands > tset.MaxPlannedCommands {
		return over("commands", commands, tset.MaxPlannedCommands)
	}
	if bytes := l1Bytes + lp.ArgvBytes + cost.argvBytes; bytes > tset.MaxPlannedArgvBytes {
		return over("argv_bytes", bytes, tset.MaxPlannedArgvBytes)
	}
	return nil
}

// layerOneCounts are Layer 1's planned commands and argv bytes, from the
// counters of a committed reply.
func layerOneCounts(reply tset.Reply) (commands, bytes int, ok bool) {
	var counters struct {
		PlannedCommands *int `json:"planned_commands"`
		PlannedBytes    *int `json:"planned_argv_bytes"`
	}
	if json.Unmarshal(reply.Counters, &counters) != nil || counters.PlannedCommands == nil || counters.PlannedBytes == nil {
		return 0, 0, false
	}
	return *counters.PlannedCommands, *counters.PlannedBytes, true
}

// tablePlanOf is the Go table plan of a Mem plan (S15): the plan's entries,
// aligned with the step's, and the counts the reply will carry, which the
// plan does not: a create, move or remove entry changed each id it keeps (the
// plan keeps the effective ids only), and a guard guarded each of its ids.
func tablePlanOf(st tset.Step, mp *tset.MemPlan) TablePlan {
	tp := TablePlan{Before: mp.Before, ChangedPerEntry: make([]int, len(mp.Entries))}
	for i, e := range mp.Entries {
		tp.Entries = append(tp.Entries, PlannedEntry{Entry: e.Entry, Before: e.Before, After: e.After,
			FieldChanges: e.FieldChanges, Added: e.Added})
		switch e.Entry.Kind {
		case "create", "move", "remove":
			tp.ChangedPerEntry[i] = len(e.Entry.IDs)
			tp.Changed += len(e.Entry.IDs)
		}
	}
	for _, e := range st.Entries {
		if e.Kind == "guard" {
			tp.Guarded += len(e.IDs)
		}
	}
	return tp
}

// writeEpochOf is the epoch a planned step writes at: its successor when the
// step advances (the plan has checked where the advance stands), else its own
// (L1 3; L2 0).
func writeEpochOf(st tset.Step) (tset.Decimal, error) {
	for _, e := range st.Entries {
		if e.Kind == "advance" {
			return tset.NextDecimal(st.Epoch)
		}
	}
	return st.Epoch, nil
}

// compareDecimal orders two canonical decimals: -1, 0 or 1.
func compareDecimal(a, b tset.Decimal) int {
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

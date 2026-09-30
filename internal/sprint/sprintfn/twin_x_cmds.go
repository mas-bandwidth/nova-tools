package sprintfn

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The second half of X (1.0, "X.plan"; 1.3.2): commands only, once the table
// plan and the log plan are known. Nothing here reads a key or refuses: every
// read X needs was made in XPre and every field the derivation reads was
// validated there, so a step cannot fail here for a reason X.pre could have
// found (the store's prepare still validates the whole list, L1 1.4).

// zmscore is the scores of several members of one sorted set, in commands of at
// most xPiece members: one probe each.
func (r *xRead) zmscore(key string, members []string) (map[string]float64, *Refusal) {
	r.probes += xProbesFor(len(members))
	if ref := r.wrongType(key, kindZSet); ref != nil {
		return nil, ref
	}
	out := make(map[string]float64, len(members))
	for _, m := range members {
		if s, ok := r.k.ZScore(key, m); ok {
			out[m] = s
		}
	}
	return out, nil
}

// xScore spells an index score for ZADD: the shortest decimal that reads back
// as the same number, with no exponent.
func xScore(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// xIndexKey is the stored key of an index or of the due set at an epoch:
// {p}<index>:<arg>@e, "@0" included (0, change 1). IT02's IndexKey.Stored spells
// epoch zero without the suffix, as the present build does; this is the design's
// (listed as an open question).
func xIndexKey(prefix string, k sprint.IndexKey, epoch tset.Decimal) string {
	return prefix + "sprint:" + k.String() + "@" + string(epoch)
}

// xPoison is the one command X.plan returns when it cannot derive: an empty
// descriptor, which prepare refuses REQUEST in the store and on the twin, so
// that nothing is written. X.plan has no refusal channel (errata 1, E3: commands
// only), and dropping the commands it could not make would leave an index
// disagreeing with its definition. It cannot happen after XPre's validation
// unless a derived entry is malformed.
var xPoison = Cmd{}

// xFieldsOf is the fields a card had, from every observation of it: the
// pre stage's (the derivation's fields) and the table plan's (the step's own
// projection), and the fields of sprint.IndexFields neither observed.
func xFieldsOf(recs ...tset.MemberRecord) (fields map[string]string, unobserved []string) {
	fields = map[string]string{}
	seen := map[string]bool{}
	for _, rec := range recs {
		for name, v := range rec.Fields {
			seen[name] = true
			if v.Present {
				fields[name] = v.Value
			}
		}
	}
	for _, name := range sprint.IndexFields() {
		if !seen[name] {
			unobserved = append(unobserved, name)
		}
	}
	return fields, unobserved
}

// xCardOf is a card as the derivation sees it, from one observation.
func xCardOf(table, id string, rec tset.MemberRecord, fields map[string]string) (*sprint.IndexCard, error) {
	c := &sprint.IndexCard{Table: table, ID: id, Fields: fields}
	if rec.Place != nil {
		score, err := strconv.ParseFloat(rec.Score, 64)
		if err != nil {
			return nil, fmt.Errorf("%s card %s is scored %q, not a number", table, id, rec.Score)
		}
		c.Row, c.Col, c.Score = rec.Place.Row, rec.Place.Col, score
	}
	return c, nil
}

// xProspectiveCard is the card an entry leaves for one of its ids, from the
// card as it was: the fields the entry sets and unsets, the cell it moves to,
// the score it gives. It is what the table plan will say after the step, before
// the step is planned, so X.pre can validate what the derivation will read.
func xProspectiveCard(e tset.Entry, i int, before *sprint.IndexCard) (*sprint.IndexCard, error) {
	after := &sprint.IndexCard{Table: e.Table, ID: e.IDs[i], Fields: map[string]string{}}
	if before != nil {
		after.Row, after.Col, after.Score = before.Row, before.Col, before.Score
		for k, v := range before.Fields {
			after.Fields[k] = v
		}
	}
	for k, v := range e.Set {
		after.Fields[k] = v
	}
	if i < len(e.Each) {
		for k, v := range e.Each[i] {
			after.Fields[k] = v
		}
	}
	for _, k := range e.Unset {
		delete(after.Fields, k)
	}
	if e.Kind == "remove" {
		after.Row, after.Col = "", ""
		return after, nil
	}
	cell := e.To
	if cell == "" {
		cell = e.From
	}
	if cell != "" {
		row, col, err := tset.ParseCellRef(cell)
		if err != nil {
			return nil, err
		}
		after.Row, after.Col = row, col
	}
	if i < len(e.Scores) {
		score, err := strconv.ParseFloat(e.Scores[i], 64)
		if err != nil {
			return nil, fmt.Errorf("%s card %s is given the score %q, not a number", e.Table, e.IDs[i], e.Scores[i])
		}
		after.Score = score
	}
	return after, nil
}

// xWriteEpoch is the epoch the step's keys are written at: the request epoch,
// or its successor when an entry advances it (L1 1.1; 1.0's "@e").
func xWriteEpoch(st *State, req *Request) (tset.Decimal, *Refusal) {
	for _, e := range req.Body.Entries {
		if e.Kind == "advance" {
			next, err := tset.NextDecimal(st.Epoch)
			if err != nil {
				return "", xRefuse("OVERFLOW", RefusalDetail{}, "the epoch has no successor")
			}
			return next, nil
		}
	}
	return st.Epoch, nil
}

// xQueueOf is the sorted set a rule key lives in: the held rule's keys have a
// queue of their own (1.1), every other key is in the agenda.
func xQueueOf(key string) string {
	if sprint.RuleOf(key) == "held" {
		return xKeyHeldQ
	}
	return xKeyAgenda
}

// xLineOf is the seq a key names, when it names a line: <rule>@<seq> or
// <rule>@<seq>+<offset> (1.1, E6). A key a rule requeues with an offset is
// scored by the line's seq, the order the key it continues was queued at.
func xLineOf(key string) (uint64, bool) {
	i := strings.IndexByte(key, '@')
	if i < 0 {
		return 0, false
	}
	rest := key[i+1:]
	if j := strings.IndexByte(rest, '+'); j >= 0 {
		rest = rest[:j]
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
}

// xChangedIDs is the ids a request changes: the cards the derivation reads.
func xChangedIDs(req *Request) []string {
	var ids []string
	for _, e := range req.Body.Entries {
		if xChanged(e) {
			ids = append(ids, e.IDs...)
		}
	}
	for _, in := range req.Body.Intents {
		for _, id := range append(append([]string{in.Card, in.Need}, in.Needs...), in.Waiters...) {
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	return xDistinct(ids)
}

// xPrepareDerivation reads what X.plan will need and validates what it will
// read: the write epoch; the ids of the step already in {p}quarantine@e (one
// probe); the orders of the requeued keys (one probe a queue); and every indexed
// field of every card the derivation will read, the before-state as observed
// (DRIFT, naming the card: a record the machine never wrote) and the state an
// entry leaves (REQUEST: the caller's).
func xPrepareDerivation(r *xRead, st *State, req *Request, obs *Before, carry *xCarry) *Refusal {
	var ref *Refusal
	if carry.writeEpoch, ref = xWriteEpoch(st, req); ref != nil {
		return ref
	}
	carry.quarantined = map[string]bool{}
	if ids := xChangedIDs(req); len(ids) != 0 {
		marks, ref := r.hmget(r.at(xKeyQuarantine), ids)
		if ref != nil {
			return ref
		}
		for id := range marks {
			carry.quarantined[id] = true
		}
	}
	carry.requeue = map[string]float64{}
	byQueue := map[string][]string{}
	for _, k := range req.Body.Requeue {
		byQueue[xQueueOf(k)] = append(byQueue[xQueueOf(k)], k)
	}
	for _, queue := range []string{xKeyAgenda, xKeyHeldQ} {
		keys := xDistinct(byQueue[queue])
		if len(keys) == 0 {
			continue
		}
		scores, ref := r.zmscore(r.at(queue), keys)
		if ref != nil {
			return ref
		}
		for _, k := range keys {
			if _, ok := scores[k]; ok {
				continue // a key already queued keeps its order: nothing is written for it
			}
			line, ok := xLineOf(k)
			if !ok {
				return xRefuse(CodeRequest, RefusalDetail{}, "the key %s is requeued, is not queued and names no line, so it has no order", k)
			}
			carry.requeue[k] = float64(line)
		}
	}
	// The derivation's fields, well formed.
	for _, e := range req.Body.Entries {
		if !xChanged(e) {
			continue
		}
		for i, id := range e.IDs {
			var before *sprint.IndexCard
			if e.Kind != "create" {
				rec, ok := obs.Record(e.Table, id)
				if !ok {
					return xRefuse(CodeConfig, RefusalDetail{}, "X.pre was not given the before-state of %s card %s", e.Table, id)
				}
				fields, unobserved := xFieldsOf(rec)
				if rec.Exists && len(unobserved) != 0 {
					return xRefuse(CodeConfig, RefusalDetail{}, "X.pre was not given the fields %v of %s card %s", unobserved, e.Table, id)
				}
				var err error
				if rec.Exists {
					if before, err = xCardOf(e.Table, id, rec, fields); err == nil {
						_, err = sprint.IndexOps(before, before)
					}
					if err != nil {
						return xRefuse("DRIFT", RefusalDetail{RefusalDetail: tset.RefusalDetail{Table: e.Table, IDs: []string{id}}}, "DRIFT: %v", err)
					}
				}
			}
			after, err := xProspectiveCard(e, i, before)
			if err == nil {
				_, err = sprint.IndexOps(after, after)
			}
			if err != nil {
				return xRefuse(CodeRequest, RefusalDetail{RefusalDetail: tset.RefusalDetail{Table: e.Table, IDs: []string{id}}}, "%v", err)
			}
		}
	}
	for _, in := range req.Body.Intents {
		for _, id := range append(append([]string{in.Card, in.Need}, in.Needs...), in.Waiters...) {
			if id == "" {
				continue
			}
			rec, ok := obs.Record(sprint.Work, id)
			if !ok || !rec.Exists {
				continue // a need may have no record: it is admitted (1.3.3)
			}
			fields, unobserved := xFieldsOf(rec)
			if len(unobserved) != 0 {
				return xRefuse(CodeConfig, RefusalDetail{}, "X.pre was not given the fields %v of card %s", unobserved, id)
			}
			c, err := xCardOf(sprint.Work, id, rec, fields)
			if err == nil {
				_, err = sprint.IndexOps(c, c)
			}
			if err != nil {
				return xRefuse("DRIFT", RefusalDetail{RefusalDetail: tset.RefusalDetail{Table: sprint.Work, IDs: []string{id}}}, "DRIFT: %v", err)
			}
		}
	}
	return nil
}

// XCmds is X's second half (1.0, "X.plan"): the commands, in A1's order (the
// writes that record owed work before the writes that forget their trigger):
//
//  1. the derivation of the indexes and the card kinds of the due set, from
//     each changed card's before and after (1.3.2), one ZREM and one ZADD a key
//     in pieces of at most 1,000 members;
//  2. the removal of each id a quarantine names from elig, fresh and again of
//     its stream and from askwait (1.3.5: "X takes the id out"; a sentinel stays in
//     sent);
//  3. the agenda's edits: the requeued keys are added, with their orders kept,
//     and then the done keys are removed.
//
// It takes what XPre stored for the call; called for a call XPre did not pass,
// or unable to derive, it returns the one command prepare refuses (xPoison).
func XCmds(st *State, tp TablePlan, lp LogPlan) []Cmd {
	carry := xStash.take(st)
	if carry == nil {
		return []Cmd{xPoison}
	}
	cmds, err := xCommands(st.Prefix, carry, tp)
	if err != nil {
		return []Cmd{xPoison}
	}
	return cmds
}

// xCommands is X.plan's command list for a table plan.
func xCommands(prefix string, carry *xCarry, tp TablePlan) ([]Cmd, error) {
	var changes, quarantined []sprint.IndexChange
	for _, pe := range tp.Entries {
		e := pe.Entry
		if !xChanged(e) {
			continue
		}
		if len(pe.Before) != len(e.IDs) || len(pe.After) != len(e.IDs) || len(pe.FieldChanges) != len(e.IDs) {
			return nil, fmt.Errorf("the plan's entry for %s is not aligned with its ids", e.Table)
		}
		for j, id := range e.IDs {
			var before *sprint.IndexCard
			if pe.Before[j].Exists {
				obsRec, _ := carry.obs.Record(e.Table, id)
				fields, unobserved := xFieldsOf(obsRec, pe.Before[j])
				if len(unobserved) != 0 {
					return nil, fmt.Errorf("the fields %v of %s card %s were not observed", unobserved, e.Table, id)
				}
				var err error
				if before, err = xCardOf(e.Table, id, pe.Before[j], fields); err != nil {
					return nil, err
				}
			}
			afterFields := map[string]string{}
			if before != nil {
				for k, v := range before.Fields {
					afterFields[k] = v
				}
			}
			for k, v := range pe.FieldChanges[j].Set {
				afterFields[k] = v
			}
			for _, k := range pe.FieldChanges[j].Unset {
				delete(afterFields, k)
			}
			after, err := xCardOf(e.Table, id, pe.After[j], afterFields)
			if err != nil {
				return nil, err
			}
			ch := sprint.IndexChange{Before: before, After: after}
			if carry.quarantined[id] {
				quarantined = append(quarantined, ch)
			} else {
				changes = append(changes, ch)
			}
		}
	}
	ops, err := sprint.StepIndexOps(changes)
	if err != nil {
		return nil, err
	}
	// A quarantined card is given no index membership but sent (1.3.2; I1): its
	// other memberships, and its due entries and waits, are left as they are.
	for _, ch := range quarantined {
		qops, err := sprint.StepIndexOps([]sprint.IndexChange{ch})
		if err != nil {
			return nil, err
		}
		for _, o := range qops {
			if o.Key.Index == sprint.IndexSent {
				ops = append(ops, o)
			}
		}
	}
	var cmds []Cmd
	for _, o := range ops {
		key := xIndexKey(prefix, o.Key, carry.writeEpoch)
		if len(o.Rem) != 0 {
			cmds = append(cmds, Command("ZREM", key, kindZSet, o.Rem...))
		}
		if len(o.Add) != 0 {
			args := make([]string, 0, 2*len(o.Add))
			for _, a := range o.Add {
				args = append(args, xScore(a.Score), a.Member)
			}
			cmds = append(cmds, Command("ZADD", key, kindZSet, args...))
		}
	}
	cmds = append(cmds, xQuarantineCmds(prefix, carry)...)
	cmds = append(cmds, xAgendaCmds(prefix, carry)...)
	return cmds, nil
}

// xZRemPieces is ZREM of the members of a key, in commands of at most xPiece.
func xZRemPieces(key string, members []string) []Cmd {
	var out []Cmd
	for i := 0; i < len(members); i += xPiece {
		out = append(out, Command("ZREM", key, kindZSet, members[i:min(i+xPiece, len(members))]...))
	}
	return out
}

// xQuarantineCmds takes each quarantined id out of elig, fresh and again of
// its stream, and out of askwait (1.3.5). An id whose stream is not named is
// taken out of askwait alone: no key names the others without it.
func xQuarantineCmds(prefix string, carry *xCarry) []Cmd {
	q := carry.req.Body.Quarantine
	if len(q) == 0 {
		return nil
	}
	byStream := map[string][]string{}
	var all []string
	for _, c := range q {
		all = append(all, c.ID)
		if c.Stream != "" {
			byStream[c.Stream] = append(byStream[c.Stream], c.ID)
		}
	}
	streams := make([]string, 0, len(byStream))
	for s := range byStream {
		streams = append(streams, s)
	}
	sort.Strings(streams)
	var out []Cmd
	for _, s := range streams {
		ids := xDistinct(byStream[s])
		for _, idx := range []string{sprint.IndexElig, sprint.IndexFresh, sprint.IndexAgain} {
			out = append(out, xZRemPieces(xIndexKey(prefix, sprint.IndexKey{Index: idx, Arg: s}, carry.writeEpoch), ids)...)
		}
	}
	key := prefix + "sprint:" + xKeyAskwait + "@" + string(carry.writeEpoch)
	return append(out, xZRemPieces(key, xDistinct(all))...)
}

// xAgendaCmds is the agenda's edits of a step, in A1's order: a key is added
// before the key it continues is removed (1.0; 1.1). A requeued key already
// queued keeps its order and is not written; one that is not (a continuation
// with an offset) is added at its line's seq. A done key the sprint part parks
// is the part's to remove, after its park is written (1.3.5), so X leaves it.
func xAgendaCmds(prefix string, carry *xCarry) []Cmd {
	req := carry.req
	at := func(queue string) string { return prefix + "sprint:" + queue + "@" + string(carry.writeEpoch) }
	var out []Cmd
	for _, queue := range []string{xKeyAgenda, xKeyHeldQ} {
		var keys []string
		for _, k := range xDistinct(req.Body.Requeue) {
			if _, ok := carry.requeue[k]; ok && xQueueOf(k) == queue {
				keys = append(keys, k)
			}
		}
		for i := 0; i < len(keys); i += xPiece {
			args := make([]string, 0, 2*xPiece)
			for _, k := range keys[i:min(i+xPiece, len(keys))] {
				args = append(args, xScore(carry.requeue[k]), k)
			}
			out = append(out, Command("ZADD", at(queue), kindZSet, args...))
		}
	}
	parked := map[string]bool{}
	if req.Sprint != nil {
		for k := range req.Sprint.Park {
			parked[k] = true
		}
	}
	byQueue := map[string][]string{}
	for _, k := range xDistinct(req.Body.Done) {
		if !parked[k] {
			byQueue[xQueueOf(k)] = append(byQueue[xQueueOf(k)], k)
		}
	}
	for _, queue := range []string{xKeyAgenda, xKeyHeldQ} {
		out = append(out, xZRemPieces(at(queue), byQueue[queue])...)
	}
	return out
}

// XCost is X's share of a body's cost, in the units of the step builder's
// Coster (8.0: step.Cost{Commands, ArgvBytes, Probes, Notes}): the commands X.plan
// writes, their argv bytes, the keys X.pre probes, and the notes it adds (none:
// J's). The builder counts X's share from the read by running the twin's XCmds on
// the plan, with 25% headroom (1.3.6); IT04's package does not export the
// Coster of 8.0 yet, so this is its words and this package's types.
type XCost struct{ Commands, ArgvBytes, Probes, Notes int }

// CostX counts what X adds to a request, from the read (obs) and the sprint's
// keys (st.Keys): it runs X.pre (so a request X would refuse is refused here,
// with X's refusal) and X.plan on the plan the entries make before Layer 1 sees
// them. The counts are exact for the caller's entries: X's commands depend on
// each card's state before and after, not on whether Layer 1 found the entry
// effective. A derived entry (an intent's) is not in the body and is not counted:
// the headroom is for it.
func CostX(st *State, req *Request, obs *Before) (XCost, *Refusal) {
	carry, ref := xPre(st, req, obs)
	if ref != nil {
		return XCost{}, ref
	}
	tp, err := xDryPlan(req, obs)
	if err != nil {
		return XCost{}, xRefuse(CodeRequest, RefusalDetail{}, "%v", err)
	}
	cmds, err := xCommands(st.Prefix, carry, tp)
	if err != nil {
		return XCost{}, xRefuse(CodeRequest, RefusalDetail{}, "%v", err)
	}
	cost := XCost{Commands: len(cmds), Probes: carry.probes}
	for _, c := range cmds {
		for _, a := range c.Argv {
			cost.ArgvBytes += len(a)
		}
	}
	return cost, nil
}

// xDryPlan is the table plan the request's entries would make if Layer 1
// accepted every one as written: each id changed, its after-state the entry's.
func xDryPlan(req *Request, obs *Before) (TablePlan, error) {
	var tp TablePlan
	for _, e := range req.Body.Entries {
		if !xChanged(e) {
			continue
		}
		pe := PlannedEntry{Entry: e}
		for i, id := range e.IDs {
			var rec tset.MemberRecord
			if e.Kind != "create" {
				rec, _ = obs.Record(e.Table, id)
			}
			fields, _ := xFieldsOf(rec)
			var before *sprint.IndexCard
			if rec.Exists {
				var err error
				if before, err = xCardOf(e.Table, id, rec, fields); err != nil {
					return TablePlan{}, err
				}
			}
			after, err := xProspectiveCard(e, i, before)
			if err != nil {
				return TablePlan{}, err
			}
			rec.ID = id
			pe.Before = append(pe.Before, rec)
			aRec := tset.MemberRecord{ID: id, Exists: true}
			if after.Col != "" {
				aRec.Place, aRec.Score = &tset.CellPlace{Row: after.Row, Col: after.Col}, xScore(after.Score)
			}
			pe.After = append(pe.After, aRec)
			change := tset.MemFieldChange{Set: map[string]string{}}
			for k, v := range after.Fields {
				if before == nil || before.Fields[k] != v {
					change.Set[k] = v
				}
			}
			if before != nil {
				for k := range before.Fields {
					if _, still := after.Fields[k]; !still {
						change.Unset = append(change.Unset, k)
					}
				}
			}
			pe.FieldChanges = append(pe.FieldChanges, change)
		}
		tp.Entries = append(tp.Entries, pe)
	}
	return tp, nil
}

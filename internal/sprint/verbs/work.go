package verbs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The worker verbs (section 3's table: take, finish, read, queue; 1.5.5; item
// IT20). take, finish and read name each card at its generation and take a set
// (1.5.3, "every verb takes a set"): one read of every card named and of its
// primary, then one step, two round trips whatever the size of the set up to
// what one step holds; a larger set is k steps in k + 1 round trips (each step
// pipelined with the next read, 1.5.3). They write no receipt, even with an op: the generation
// is the idempotency key (1.5.5, Q2). A report already applied at that
// generation, by that actor, with the same facts, writes nothing; the same
// generation with other facts is refused, naming what was applied. queue reads
// and writes nothing.
//
// A card is named by the id the table holds it under (the id queue prints).
// Each step is all or nothing: one card of the set that cannot move refuses
// the whole step before anything is sent, naming every such card.

// The provisional constants of section 2.5's two rate notices: the owner's to
// set (section 7). A rate is judged only over at least RateMinReports reports.
const (
	// OkRateFloor is the percent of a member's finished work that came back ok
	// below which "a member's ok rate fell below OkRateFloor" is said.
	OkRateFloor = 50
	// BrokenRateCeiling is the percent of a reader's reads found broken above
	// which "a reader's broken rate rose above BrokenRateCeiling" is said.
	BrokenRateCeiling = 50
	// RateMinReports is the fewest reports a rate is judged over.
	RateMinReports = 10
)

// How many cards one step of a worker verb holds. Two bounds meet (1.0, "The
// chunk"; L1 6): 2,000 changed members a step, and 4,000 about ids over the
// entries and the notes together (tset.MaxAboutBeforeDedup). A take moves one
// fleet card a card, and a begin one read card, so 2,000; a finish moves the
// work card and its primary, two members a card, so 1,000, and its ok notice
// names a card too: three abouts a card, well under the cap. A report of read
// cards names each card in its entry and again in its one summary note, two
// abouts a card, and the broken-rate notice adds one: 2 x n + 1 at most 4,000,
// so 1,999. A larger set goes in successive steps of these sizes, each step
// pipelined with the next step's read (1.5.3, the parts rule: n steps cost
// n + 1 round trips); by generation, a set run again after a refused step
// repeats nothing that applied.
const (
	TakeMax       = StepChunk
	ReadBeginMax  = StepChunk
	ReadReportMax = (tset.MaxAboutBeforeDedup - 1) / 2
	FinishMax     = StepChunk / 2
)

// The spans of the due times the worker verbs stamp (1.2): a work card taken
// is due unfinished at first_taken_r + 2 h, a read card begun is due
// unreported at begun_r + 2 h.
const (
	wkUnfinishedSpan = 2 * time.Hour
	wkUnreportedSpan = 2 * time.Hour
)

// The card fields the worker verbs read and write (1.2, 1.3.1). Stamps in R
// are the rules' clocks; the wall stamps (taken, finished, begun, read) are for
// the reader, and no rule reads them.
const (
	wkfGen          = "gen"
	wkfMember       = "member"
	wkfPrimary      = "primary"
	wkfReader       = "reader"
	wkfWork         = "work"
	wkfResult       = "result"
	wkfHead         = "head"
	wkfReport       = "report"
	wkfBranch       = "branch"
	wkfBase         = "base"
	wkfRefused      = "refused"
	wkfFirstTakenR  = "first_taken_r"
	wkfDueUnfinish  = "due_unfinished"
	wkfUntakenR     = "untaken_r"
	wkfUntakenRepl  = "untaken_replaced"
	wkfDueUntaken   = "due_untaken"
	wkfUntakenSince = "untaken_since"
	wkfTaken        = "taken"
	wkfFinished     = "finished"
	wkfBegunR       = "begun_r"
	wkfBegun        = "begun"
	wkfDueUnbegun   = "due_unbegun"
	wkfDueUnreport  = "due_unreported"
	wkfVerdict      = "verdict"
	wkfSummary      = "summary"
	wkfFinding      = "finding"
	wkfReadAt       = "read"
)

// The projections of the worker verbs' reads: every read names its fields
// (1.0, "Bytes"), and each names every field its plan reads.
var (
	wkWorkCardReadFields = []string{wkfGen, wkfMember, wkfPrimary, wkfFirstTakenR, wkfHead, wkfResult}
	wkPrimaryReadFields  = []string{wkfWork}
	wkReadCardReadFields = []string{wkfGen, wkfReader, wkfPrimary, wkfVerdict, wkfSummary, wkfFinding}
	wkListingFields      = []string{}
)

// CardGen is a card as a worker names it: <card>@<gen>. Gen 0 is a card named
// without a generation, which only read takes (a read card is asked of one
// reader once, so its generation is 1 unless the card says otherwise).
type CardGen struct {
	Card string
	Gen  int
}

// String is the card as a worker names it.
func (c CardGen) String() string {
	if c.Gen == 0 {
		return c.Card
	}
	return c.Card + "@" + strconv.Itoa(c.Gen)
}

// ParseCardGen reads <card>@<gen>, or <card> alone (generation 0).
func ParseCardGen(s string) (CardGen, error) {
	card, gen, found := strings.Cut(s, "@")
	if card == "" || strings.ContainsAny(card, " \t\r\n\x00") || len(card) > 256 {
		return CardGen{}, fmt.Errorf("%q is not a card", s)
	}
	if !found {
		return CardGen{Card: card}, nil
	}
	n, err := strconv.Atoi(gen)
	if err != nil || n < 1 || strconv.Itoa(n) != gen {
		return CardGen{}, fmt.Errorf("%q names no generation: <card>@<gen>, gen a whole number from 1", s)
	}
	return CardGen{Card: card, Gen: n}, nil
}

// wkWorkRefuse is a refusal a worker verb makes from its arguments or its read,
// before anything is sent: nothing was written.
func wkWorkRefuse(verb, code, format string, args ...any) *Refused {
	return wkWorkRefuseIDs(verb, code, nil, format, args...)
}

// wkWorkRefuseIDs is wkWorkRefuse naming the cards (or members) it refuses in
// Detail.IDs, as a refused step names its ids (1.5.3).
func wkWorkRefuseIDs(verb, code string, ids []string, format string, args ...any) *Refused {
	return &Refused{Verb: verb, Local: true, Refusal: &sprintfn.Refusal{Code: code,
		Message: fmt.Sprintf(format, args...) + "; nothing was written",
		Detail:  sprintfn.RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: append([]string{}, ids...), Cells: []string{}, Rows: []string{}}}}}
}

// wkFaults collects what a plan refuses from a set: each card (or member) it
// found wrong, with the reason. A card of another generation than the live
// one, or finished or reported with other facts, is a conflict of state and
// refuses OPCONFLICT; the rest of the faults refuse REQUEST, malformed or not
// theirs to change. Detail.IDs names the cards, in the order found.
type wkFaults struct {
	bad, clash []string
	ids        []string
}

func (f *wkFaults) add(id, format string, args ...any) {
	f.bad = append(f.bad, fmt.Sprintf(format, args...))
	f.ids = append(f.ids, id)
}

func (f *wkFaults) conflict(id, format string, args ...any) {
	f.clash = append(f.clash, fmt.Sprintf(format, args...))
	f.ids = append(f.ids, id)
}

// live records why a work card is not the member's live card (wkLiveCard): a
// stale generation is a conflict, the rest are faults.
func (f *wkFaults) live(id, why string, stale bool) {
	if stale {
		f.conflict(id, "%s", why)
		return
	}
	f.add(id, "%s", why)
}

// err is the refusal of the faults found, nil when none.
func (f *wkFaults) err(verb string) error {
	switch {
	case len(f.clash) > 0:
		return wkWorkRefuseIDs(verb, "OPCONFLICT", f.ids, "%s", strings.Join(append(append([]string{}, f.clash...), f.bad...), "; "))
	case len(f.bad) > 0:
		return wkWorkRefuseIDs(verb, sprintfn.CodeRequest, f.ids, "%s", strings.Join(f.bad, "; "))
	}
	return nil
}

// wkFieldOf is a record's field, and whether it is present.
func wkFieldOf(r tset.MemberRecord, name string) (string, bool) {
	f, ok := r.Fields[name]
	if !ok || !f.Present {
		return "", false
	}
	return f.Value, true
}

// wkFieldStr is a record's field, "" when absent.
func wkFieldStr(r tset.MemberRecord, name string) string {
	v, _ := wkFieldOf(r, name)
	return v
}

// wkPlaceOf is a record's place as "row:col", or the word for a record with none.
func wkPlaceOf(r tset.MemberRecord) string {
	switch {
	case !r.Exists:
		return "no record"
	case r.Place == nil:
		return "off the table"
	}
	return r.Place.Row + ":" + r.Place.Col
}

// wkColOf is the column a record is placed in, "" when it has no place.
func wkColOf(r tset.MemberRecord) string {
	if !r.Exists || r.Place == nil {
		return ""
	}
	return r.Place.Col
}

// wkClockAt decodes the clock read at a sprint slot: R and the wall time, in ms
// (1.0, "The clock": a verb's stamps come from its read, never from the
// client's clock).
func wkClockAt(rd *sprintfn.ReadReply, slot int) (r, wall int64, err error) {
	if rd == nil || slot >= len(rd.Sprint) {
		return 0, 0, errors.New("verbs: the read has no clock answer")
	}
	qr, err := sprintfn.DecodeResult(sprintfn.KeyClock, rd.Sprint[slot])
	if err != nil {
		return 0, 0, err
	}
	c, ok := qr.(sprintfn.ClockResult)
	if !ok {
		return 0, 0, errors.New("verbs: the clock answer is of another kind")
	}
	if r, err = strconv.ParseInt(c.R, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("verbs: R %q is not a number", c.R)
	}
	if wall, err = strconv.ParseInt(c.WallMS, 10, 64); err != nil {
		return 0, 0, fmt.Errorf("verbs: the wall time %q is not a number", c.WallMS)
	}
	return r, wall, nil
}

// wkClockQuery is the sprint-key read of the clock and R.
func wkClockQuery() sprintfn.SprintQuery {
	q, ref := sprintfn.EncodeKeyQ(sprintfn.KeyQ{Kind: sprintfn.KeyClock})
	if ref != nil {
		panic(fmt.Sprintf("verbs: the clock query refused: %v", ref)) // a fixed, valid query
	}
	return q
}

// wkListingQuery is the fleet or readers query (1.0): every row's control card
// and the counts of its cells, which a finish and a read judge a rate from.
func wkListingQuery(kind string) sprintfn.SprintQuery {
	q, ref := sprintfn.EncodeSprintQ(sprint.SprintQ{Kind: kind, Fields: wkListingFields})
	if ref != nil {
		panic(fmt.Sprintf("verbs: the %s query refused: %v", kind, ref))
	}
	return q
}

// wkCellCounts decodes a listing at a sprint slot: whether row is one of its
// rows, and the count of each of its cells.
func wkCellCounts(rd *sprintfn.ReadReply, slot int, kind, row string) (bool, map[string]int, error) {
	if rd == nil || slot >= len(rd.Sprint) {
		return false, nil, fmt.Errorf("verbs: the read has no %s answer", kind)
	}
	qr, err := sprintfn.DecodeResult(kind, rd.Sprint[slot])
	if err != nil {
		return false, nil, err
	}
	l, ok := qr.(sprintfn.ListingResult)
	if !ok {
		return false, nil, fmt.Errorf("verbs: the %s answer is of another kind", kind)
	}
	for _, it := range l.Items {
		if it.Row != row {
			continue
		}
		counts := map[string]int{}
		for _, c := range it.Counts {
			counts[c.Col] = c.N
		}
		return true, counts, nil
	}
	return false, nil, nil
}

// wkWallStamp is a wall time in ms as the stamps for the reader are written.
func wkWallStamp(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// wkMsOf is a time in ms as a field's value.
func wkMsOf(v int64) string { return strconv.FormatInt(v, 10) }

// wkNameList is ids for a line of text: the first few, and how many more.
func wkNameList(ids []string) string {
	const most = 10
	if len(ids) <= most {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:most], ", ") + fmt.Sprintf(" and %d more", len(ids)-most)
}

// wkUniqueSorted is the ids sorted, each once.
func wkUniqueSorted(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	n := 0
	for i, id := range out {
		if i == 0 || id != out[n-1] {
			out[n] = id
			n++
		}
	}
	return out[:n]
}

// wkNamedCards checks a set of named cards: at least one, each once, and sorted
// by card (1.5.3: "the ids are sorted").
func wkNamedCards(verb string, cards []CardGen, needGen bool) ([]CardGen, error) {
	if len(cards) == 0 {
		return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s names no card", verb)
	}
	out := append([]CardGen(nil), cards...)
	sort.Slice(out, func(i, j int) bool { return out[i].Card < out[j].Card })
	for i, c := range out {
		if needGen && c.Gen < 1 {
			return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s names no generation: %s <card>@<gen>", c.Card, verb)
		}
		if c.Gen < 0 {
			return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s names a negative generation", c.Card)
		}
		if i > 0 && out[i-1].Card == c.Card {
			return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is named twice", c.Card)
		}
	}
	return out, nil
}

// wkActorOf is the worker a verb runs as: --as, else the Env's actor.
func wkActorOf(verb string, e *Env, as string) (string, error) {
	if as == "" {
		as = e.Actor
	}
	if !sprint.ValidID(as) {
		return "", wkWorkRefuse(verb, sprintfn.CodeRequest, "--as %q is not a member's or reader's name", as)
	}
	return as, nil
}

// wkStepRun is one step of a worker verb in its set: its result, and what it
// said and replayed (the cards already so at their generation).
type wkStepRun func(ctx context.Context, lo, hi int) (Result, error)

// wkInSteps runs a set of n cards in steps of at most per cards, each its own
// read and step (two round trips), in order. The results add up: the round
// trips, the retries and the lines said; Replay holds only when no step wrote.
// A step refused after others applied names how to go on: by generation, the
// same command repeats nothing that applied.
func wkInSteps(ctx context.Context, verb string, n, per int, run wkStepRun) (Result, error) {
	var total Result
	total.Verb = verb
	var said []string
	replay := true
	for lo := 0; lo < n; lo += per {
		hi := min(lo+per, n)
		res, err := run(ctx, lo, hi)
		total.Trips += res.Trips
		total.Retries += res.Retries
		total.Epoch, total.EpochAfter = res.Epoch, res.EpochAfter
		total.Read = res.Read
		if err != nil {
			var rf *Refused
			if lo > 0 && errors.As(err, &rf) && rf.Hint == "" {
				rf.Hint = fmt.Sprintf("the first %d cards are applied; fix the rest and run the same command again: by generation it repeats nothing", lo)
			}
			total.Said = strings.Join(said, "; ")
			total.Replay = false
			return total, err
		}
		if res.Step != nil {
			total.Step = res.Step
			replay = false
		}
		if res.Said != "" {
			said = append(said, res.Said)
		}
	}
	total.Replay = replay && total.Step == nil
	total.Said = strings.Join(said, "; ")
	return total, nil
}

// wkChunk is a worker verb's step over one chunk of its cards: the read and the
// plan of one step (a Planned with no op), and done, which turns the step's
// result into the chunk's own (its line, its replay) once the step applied or
// planned nothing.
type wkChunk struct {
	Planned
	done func(Result) Result
}

// wkPipedSteps runs a set of n cards in steps of at most per cards, in order,
// each step pipelined with the next step's read in one flush (1.5.3, the parts
// rule; SprintEvents.tla runs each step as its own atomic action): the first
// read, then a flush a step, so k steps cost k + 1 round trips, and a set that
// fits one step costs two. The next read runs after the step in the flush, so
// it sees the counts the step left. A step that plans nothing sends no flush,
// and the next step's read goes alone. The results add up as wkInSteps' do,
// and a step refused after others applied names how to go on: by generation,
// the same command repeats nothing that applied.
func wkPipedSteps(ctx context.Context, e *Env, verb string, n, per int, chunkOf func(lo, hi int) wkChunk) (Result, error) {
	var total Result
	total.Verb = verb
	var said []string
	replay := true
	var rd *sprintfn.ReadReply // the read of the chunk at lo, when the step before it brought it
	var ahead *wkChunk         // the chunk at lo, made when the step before it was flushed
	for lo := 0; lo < n; lo += per {
		hi := min(lo+per, n)
		var cur wkChunk
		if ahead != nil {
			cur, ahead = *ahead, nil
		} else {
			cur = chunkOf(lo, hi)
		}
		var nxt *wkChunk
		if hi < n {
			c := chunkOf(hi, min(hi+per, n))
			nxt = &c
		}
		res, nextRd, err := e.wkChunkDo(ctx, cur, nxt, rd)
		rd, ahead = nextRd, nxt
		total.Trips += res.Trips
		total.Retries += res.Retries
		total.Epoch, total.EpochAfter = res.Epoch, res.EpochAfter
		total.Read = res.Read
		if err != nil {
			var rf *Refused
			if lo > 0 && errors.As(err, &rf) && rf.Hint == "" {
				rf.Hint = fmt.Sprintf("the first %d cards are applied; fix the rest and run the same command again: by generation it repeats nothing", lo)
			}
			total.Said = strings.Join(said, "; ")
			total.Replay = false
			return total, err
		}
		if res.Step != nil {
			total.Step = res.Step
			replay = false
		}
		if res.Said != "" {
			said = append(said, res.Said)
		}
	}
	total.Replay = replay && total.Step == nil
	total.Said = strings.Join(said, "; ")
	return total, nil
}

// wkChunkDo runs one chunk as Env.Do runs a verb of one step (1.5.3): read (or
// take the read the step before it brought), plan, build, send, and plan again
// on a race at most Retries times, the epoch reloaded first after STALE or
// EPOCHAHEAD, any other refusal returned at once. The step goes in one flush
// with the next chunk's read when there is one, and the answer to that read is
// returned for the next chunk to plan on; a refused or failed read there is
// not an error here, the next chunk reads again alone and meets it.
func (e *Env) wkChunkDo(ctx context.Context, cur wkChunk, nxt *wkChunk, rd *sprintfn.ReadReply) (Result, *sprintfn.ReadReply, error) {
	verb := cur.Verb
	res := Result{Verb: verb}
	for {
		epoch := dec(e.epoch())
		res.Epoch = e.epoch()
		if rd == nil {
			rr := cur.Read(epoch)
			rr.Epoch = epoch
			r, err := sprintfn.Read(ctx, e.C, rr)
			res.Trips++
			if err != nil {
				return res, nil, err
			}
			if r.Err != nil {
				return res, nil, r.Err
			}
			if ref := r.Refusal; ref != nil {
				if epochMoved(ref.Code) && res.Retries < Retries && e.reload(ref) {
					res.Retries++
					continue
				}
				return res, nil, &Refused{Verb: verb, Refusal: ref, Retries: res.Retries}
			}
			rd = r.Read
		}
		// AL2: the read names the active epoch; a verb plans at it.
		if active, ok := undec(rd.ActiveEpoch); ok && active != e.epoch() {
			if res.Retries >= Retries {
				return res, nil, &Refused{Verb: verb, Retries: res.Retries,
					Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale, Message: "the epoch kept moving under the verb"}}
			}
			res.Retries++
			e.setEpoch(active)
			rd = nil
			continue
		}
		res.Read = rd
		req, err := cur.Plan(rd)
		if err != nil {
			var rf *Refused
			if errors.As(err, &rf) && rf.Verb == "" {
				rf.Verb = verb
			}
			return res, nil, err
		}
		if req == nil {
			return cur.done(res), nil, nil
		}
		e.fill(req, verb, epoch)
		if rf := build(verb, req); rf != nil {
			return res, nil, rf
		}
		items := []sprintfn.Item{{Step: req}}
		if nxt != nil {
			nr := nxt.Read(epoch)
			nr.Epoch = epoch
			items = append(items, sprintfn.Item{Read: nr})
		}
		out, err := e.C.Pipeline(ctx, items)
		res.Trips++
		if err != nil {
			return res, nil, e.unknown(verb, "", 0, err)
		}
		if len(out) != len(items) {
			return res, nil, &Unknown{Verb: verb, Err: fmt.Errorf("a pipeline of %d returned %d results", len(items), len(out))}
		}
		step := out[0]
		if step.Err != nil {
			return res, nil, e.unknown(verb, "", 0, step.Err)
		}
		if ref := step.Refusal; ref != nil {
			if IsRace(ref.Code) && res.Retries < Retries {
				res.Retries++
				if epochMoved(ref.Code) {
					e.reload(ref)
				}
				if err := e.wait(ctx, res.Retries); err != nil {
					return res, nil, err
				}
				rd = nil // planned again on a fresh read
				continue
			}
			rf := &Refused{Verb: verb, Refusal: ref, Retries: res.Retries}
			if IsRace(ref.Code) {
				rf.Hint = fmt.Sprintf("the sprint kept moving under the verb for %d retries; run it again", Retries)
			}
			return res, nil, rf
		}
		res.Step = step.Step
		res.Replay = step.Step.Reply.Replay
		res.Recorded = step.Step.Reply.Result
		if after, ok := undec(step.Step.Reply.EpochAfter); ok {
			res.EpochAfter = after
			if after > e.epoch() {
				e.setEpoch(after)
			}
		}
		var nextRd *sprintfn.ReadReply
		if nxt != nil && out[1].Err == nil && out[1].Refusal == nil {
			nextRd = out[1].Read
		}
		return cur.done(res), nextRd, nil
	}
}

// wkIdsRead is Layer 1's read of named records of one table with a projection.
func wkIdsRead(table string, ids, fields []string) tset.ReadQuery {
	return tset.ReadQuery{Kind: "ids", Table: table, IDs: ids, Fields: fields}
}

// wkRecordsAt are the records of the read's Layer 1 slot, aligned with the ids
// asked.
func wkRecordsAt(rd *sprintfn.ReadReply, slot, want int) ([]tset.MemberRecord, error) {
	if rd == nil || slot >= len(rd.Tset) {
		return nil, fmt.Errorf("verbs: the read has no answer at slot %d", slot)
	}
	recs := rd.Tset[slot].Records
	if len(recs) != want {
		return nil, fmt.Errorf("verbs: the read answered %d records for %d ids", len(recs), want)
	}
	return recs, nil
}

// wkPrimaryOfWork is the primary a work card belongs to (its id, p.w<attempt>).
func wkPrimaryOfWork(card string) (string, bool) {
	p, _, ok := sprint.ParseWorkCard(card)
	return p, ok
}

// wkStreamGroups groups ids by the row their record is placed in, in the order
// of the rows' first ids (which are sorted), for one entry a stream.
func wkStreamGroups(ids []string, rows map[string]string) (order []string, by map[string][]string) {
	by = map[string][]string{}
	for _, id := range ids {
		r := rows[id]
		if _, ok := by[r]; !ok {
			order = append(order, r)
		}
		by[r] = append(by[r], id)
	}
	return order, by
}

// ---- take

// TakeReq is take <card>@<gen>... --as m: the member's work cards it takes,
// each at the generation it was dealt at.
type TakeReq struct {
	Cards []CardGen
	As    string
}

// Take moves the member's work cards fleet ready -> working (section 3, take;
// 1.2; 1.5.5), a set in one step: two round trips for up to TakeMax cards. It
// reads the work cards and their primaries. A card is taken when it is in the
// member's ready cell at the named generation and its primary is working on
// it; its first take stamps first_taken_r = R and due_unfinished = R + 2 h
// (the unfinished entry by derivation, 1.3.2), and the untaken clock ends
// (untaken_r, untaken_replaced and due_untaken unset, 1.2). A card already
// working at that generation for that member was taken: it writes nothing
// (1.5.5). A card at another generation was dealt again, and one dealt to
// another member is not this member's: either refuses the step, naming the
// live generation and where the card is. The step is guarded by each card's
// revision as read (V2), so a card dealt again between the read and the step
// refuses it REVISION, and the verb plans again (1.5.3). The model's take
// (SprintEvents.tla, VEff "take", VGuard: the card's record as read and not
// frozen) is this step; X refuses DROPPING for a frozen stream.
func Take(ctx context.Context, e *Env, req TakeReq) (Result, error) {
	const verb = "take"
	as, err := wkActorOf(verb, e, req.As)
	if err != nil {
		return Result{Verb: verb}, err
	}
	cards, err := wkNamedCards(verb, req.Cards, true)
	if err != nil {
		return Result{Verb: verb}, err
	}
	for _, c := range cards {
		if _, ok := wkPrimaryOfWork(c.Card); !ok {
			return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is not a work card (<primary>.w<attempt>)", c.Card)
		}
	}
	return wkPipedSteps(ctx, e, verb, len(cards), TakeMax, func(lo, hi int) wkChunk {
		return wkTakeChunk(as, cards[lo:hi])
	})
}

// wkWorkAndPrimaries is the read of named work cards and their primaries (the
// worker verbs' "the work cards and their primaries"), with the clock and R.
func wkWorkAndPrimaries(cards []CardGen, extra ...sprintfn.SprintQuery) (ids, prims []string, read func(tset.Decimal) *sprintfn.ReadRequest) {
	ids = make([]string, len(cards))
	var ps []string
	for i, c := range cards {
		ids[i] = c.Card
		p, _ := wkPrimaryOfWork(c.Card)
		ps = append(ps, p)
	}
	prims = wkUniqueSorted(ps)
	read = func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch,
			Tset:   []tset.ReadQuery{wkIdsRead(sprint.Fleet, ids, wkWorkCardReadFields), wkIdsRead(sprint.Work, prims, wkPrimaryReadFields)},
			Sprint: append([]sprintfn.SprintQuery{wkClockQuery()}, extra...)}
	}
	return ids, prims, read
}

// wkLiveCard is why a work card named at a generation is not the member's live
// card at it, "" when it is: no record, another generation (stale: the card was
// dealt again or withdrawn), another member.
func wkLiveCard(c CardGen, rec tset.MemberRecord, as string) (why string, stale bool) {
	if !rec.Exists || rec.Place == nil {
		return fmt.Sprintf("%s: no work card is placed (%s)", c.Card, wkPlaceOf(rec)), false
	}
	gen, _ := strconv.Atoi(wkFieldStr(rec, wkfGen))
	member := wkFieldStr(rec, wkfMember)
	if gen != c.Gen {
		where := "withdrawn"
		if member != "" && rec.Place.Col != sprint.Withdrawn {
			where = "dealt again to " + member
		}
		return fmt.Sprintf("%s: stale: generation %d is not the live one (%d): the card was %s", c.Card, c.Gen, gen, where), true
	}
	if member != as || rec.Place.Row != as {
		return fmt.Sprintf("%s@%d: dealt to %s (at %s), not %s", c.Card, c.Gen, member, wkPlaceOf(rec), as), false
	}
	return "", false
}

// wkWorkingOn says the primary's record is working on the card: placed in
// working, naming the card as its work.
func wkWorkingOn(pr tset.MemberRecord, card string) bool {
	return wkColOf(pr) == string(sprint.Working) && sprint.CardID(wkFieldStr(pr, wkfWork)) == sprint.CardID(card)
}

func wkTakeChunk(as string, cards []CardGen) wkChunk {
	const verb = "take"
	ids, prims, read := wkWorkAndPrimaries(cards)
	var already []string
	pl := Planned{Verb: verb, Read: read,
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			already = already[:0]
			works, err := wkRecordsAt(rd, 0, len(ids))
			if err != nil {
				return nil, err
			}
			precs, err := wkRecordsAt(rd, 1, len(prims))
			if err != nil {
				return nil, err
			}
			byPrim := map[string]tset.MemberRecord{}
			for i, p := range prims {
				byPrim[p] = precs[i]
			}
			r, wall, err := wkClockAt(rd, 0)
			if err != nil {
				return nil, err
			}
			var bad wkFaults
			en := tset.Entry{Kind: "move", Table: sprint.Fleet, From: as + ":" + string(sprint.Ready), To: as + ":" + string(sprint.Working),
				Set:   map[string]string{wkfTaken: wkWallStamp(wall)},
				Unset: []string{wkfUntakenR, wkfUntakenRepl, wkfDueUntaken, wkfUntakenSince, wkfRefused}}
			for i, c := range cards {
				rec := works[i]
				if why, stale := wkLiveCard(c, rec, as); why != "" {
					bad.live(c.Card, why, stale)
					continue
				}
				p, _ := wkPrimaryOfWork(c.Card)
				switch rec.Place.Col {
				case string(sprint.Working):
					already = append(already, c.String())
					continue
				case string(sprint.Ready):
				default:
					bad.add(c.Card, "%s@%d: it is %s, not ready", c.Card, c.Gen, wkPlaceOf(rec))
					continue
				}
				if pr := byPrim[p]; !wkWorkingOn(pr, c.Card) {
					bad.add(c.Card, "%s@%d: its primary %s is not working on it (it is %s, work %q)", c.Card, c.Gen, p, wkPlaceOf(pr), wkFieldStr(pr, wkfWork))
					continue
				}
				first, err := strconv.ParseInt(wkFieldStr(rec, wkfFirstTakenR), 10, 64)
				each := map[string]string{}
				if _, has := wkFieldOf(rec, wkfFirstTakenR); !has || err != nil {
					first = r
					each[wkfFirstTakenR] = wkMsOf(r)
				}
				each[wkfDueUnfinish] = wkMsOf(first + wkUnfinishedSpan.Milliseconds())
				en.IDs = append(en.IDs, c.Card)
				en.Revs = append(en.Revs, rec.Revision)
				en.About = append(en.About, p)
				en.Each = append(en.Each, each)
			}
			if err := bad.err(verb); err != nil {
				return nil, err
			}
			if len(en.IDs) == 0 {
				return nil, nil // every card was taken at its generation already (1.5.5)
			}
			return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb, Actor: as}, Body: sprintfn.Body{Entries: []tset.Entry{en}}}, nil
		}}
	return wkChunk{Planned: pl, done: func(res Result) Result {
		taken := len(cards) - len(already)
		if res.Step == nil {
			taken = 0
		}
		res.Replay = res.Step == nil
		res.Said = fmt.Sprintf("take: %s took %d", as, taken)
		if len(already) > 0 {
			res.Said += fmt.Sprintf("; %d already taken at their generation (%s), nothing written for them", len(already), wkNameList(already))
		}
		return res
	}}
}

// ---- finish

// FinishReq is finish <card>@<gen>... --as m, ok or --failed: the facts are
// the result, the head each card's work is at (Heads by card, else Head; the
// card's own id when neither names one) and, for the reader, the report and the
// branch the work is on and the one it started from.
type FinishReq struct {
	Cards        []CardGen
	As           string
	Failed       bool
	Head         string
	Heads        map[string]string
	Report       string
	Branch, Base string
}

// headOf is the head a finish reports for a card.
func (r FinishReq) headOf(card string) string {
	if h := r.Heads[card]; h != "" {
		return h
	}
	if r.Head != "" {
		return r.Head
	}
	return card
}

// Finish moves work cards fleet working -> ok or failed, and their primaries
// work working -> review with the result and the head (section 3, finish;
// 1.5.5), a set in one step: two round trips for up to FinishMax cards (two
// changed members a card). It reads the work cards and their primaries, the
// clock, and the fleet (the members' ok and failed counts). A card is finished
// when it is in the member's working cell at the named generation and its
// primary is working on it. A card already finished at that generation by that
// member with the same result and head writes nothing; with another result or
// head it is refused ("already finished at gen 3 with head abc"), and nothing
// of the step is written. KNOW "work came back ok" names the primaries that
// came back ok; KNOW "a member's ok rate fell below OkRateFloor" is said when
// this step takes the member's rate across the floor: from the counts as read
// to the counts after the step, which a count guard on the member's ok and
// failed cells holds to what was read, so the notice is said once a crossing
// and needs no mark (2.5). The model's finishok and finishfail
// (SprintEvents.tla, VEff) are this step; what follows (ask, rework) is the
// tick's (V5).
func Finish(ctx context.Context, e *Env, req FinishReq) (Result, error) {
	const verb = "finish"
	as, err := wkActorOf(verb, e, req.As)
	if err != nil {
		return Result{Verb: verb}, err
	}
	cards, err := wkNamedCards(verb, req.Cards, true)
	if err != nil {
		return Result{Verb: verb}, err
	}
	for _, c := range cards {
		if _, ok := wkPrimaryOfWork(c.Card); !ok {
			return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is not a work card (<primary>.w<attempt>)", c.Card)
		}
		if len(req.headOf(c.Card)) > 256 {
			return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "the head of %s is over 256 bytes", c.Card)
		}
	}
	return wkPipedSteps(ctx, e, verb, len(cards), FinishMax, func(lo, hi int) wkChunk {
		return wkFinishChunk(as, cards[lo:hi], req)
	})
}

// wkFinishedAs is how a work card finished: ok or failed, from its column.
func wkFinishedAs(rec tset.MemberRecord) string {
	switch wkColOf(rec) {
	case sprint.DoneOK:
		return "ok"
	case sprint.DoneFailed:
		return "failed"
	}
	return ""
}

// wkOkBelow says a member's ok rate is below the floor: at least RateMinReports
// reports, and ok / (ok + failed) under OkRateFloor percent.
func wkOkBelow(ok, failed int) bool {
	n := ok + failed
	return n >= RateMinReports && ok*100 < OkRateFloor*n
}

// wkBrokenAbove says a reader's broken rate is above the ceiling: at least
// RateMinReports reads, and broken / (ok + broken) over BrokenRateCeiling
// percent.
func wkBrokenAbove(ok, broken int) bool {
	n := ok + broken
	return n >= RateMinReports && broken*100 > BrokenRateCeiling*n
}

// wkKnowNote is a KNOW note of section 2.5.
func wkKnowNote(typ, text string, subjects []string) sprintfn.NoteReq {
	return sprintfn.NoteReq{Op: sprintfn.JOpKnow, Type: typ, Subjects: subjects, Text: text}
}

// The notice types of section 2.5 the worker verbs raise, as IT06's table
// names them.
const (
	wkNoticeWorkOK      = sprint.NWorkOK
	wkNoticeOkRate      = "a member's ok rate fell below OkRateFloor"
	wkNoticeBrokenRate  = "a reader's broken rate rose above BrokenRateCeiling"
	wkNoticeReadSummary = "a read of p by r: its one-line summary"
)

func wkFinishChunk(as string, cards []CardGen, req FinishReq) wkChunk {
	const verb = "finish"
	result, into := "ok", sprint.DoneOK
	if req.Failed {
		result, into = "failed", sprint.DoneFailed
	}
	ids, prims, read := wkWorkAndPrimaries(cards, wkListingQuery(sprint.QueryFleet))
	var already []string
	var cameOK []string
	crossed := false
	pl := Planned{Verb: verb, Read: read,
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			already, cameOK, crossed = already[:0], cameOK[:0], false
			works, err := wkRecordsAt(rd, 0, len(ids))
			if err != nil {
				return nil, err
			}
			precs, err := wkRecordsAt(rd, 1, len(prims))
			if err != nil {
				return nil, err
			}
			byPrim := map[string]tset.MemberRecord{}
			for i, p := range prims {
				byPrim[p] = precs[i]
			}
			_, wall, err := wkClockAt(rd, 0)
			if err != nil {
				return nil, err
			}
			isRow, counts, err := wkCellCounts(rd, 1, sprint.QueryFleet, as)
			if err != nil {
				return nil, err
			}
			if !isRow {
				return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is no fleet member", as)
			}
			var bad wkFaults
			set := map[string]string{wkfResult: result, wkfFinished: wkWallStamp(wall)}
			for k, v := range map[string]string{wkfReport: req.Report, wkfBranch: req.Branch, wkfBase: req.Base} {
				if v != "" {
					set[k] = v
				}
			}
			fleet := tset.Entry{Kind: "move", Table: sprint.Fleet, From: as + ":" + string(sprint.Working), To: as + ":" + into,
				Set: set, Unset: []string{wkfDueUnfinish, wkfRefused}}
			var moved []string
			prow := map[string]string{}
			pRev := map[string]tset.Decimal{}
			pHead := map[string]string{}
			for i, c := range cards {
				rec := works[i]
				if why, stale := wkLiveCard(c, rec, as); why != "" {
					bad.live(c.Card, why, stale)
					continue
				}
				p, _ := wkPrimaryOfWork(c.Card)
				head := req.headOf(c.Card)
				switch rec.Place.Col {
				case sprint.DoneOK, sprint.DoneFailed:
					was, wasHead := wkFinishedAs(rec), wkFieldStr(rec, wkfHead)
					if was == result && wasHead == head {
						already = append(already, c.String())
					} else {
						bad.conflict(c.Card, "%s: already finished at gen %d with head %s (%s)", c.Card, c.Gen, wasHead, was)
					}
					continue
				case string(sprint.Working):
				default:
					bad.add(c.Card, "%s@%d: it is %s, not working: take it first", c.Card, c.Gen, wkPlaceOf(rec))
					continue
				}
				pr := byPrim[p]
				if !wkWorkingOn(pr, c.Card) {
					bad.add(c.Card, "%s@%d: its primary %s is not working on it (it is %s, work %q)", c.Card, c.Gen, p, wkPlaceOf(pr), wkFieldStr(pr, wkfWork))
					continue
				}
				fleet.IDs = append(fleet.IDs, c.Card)
				fleet.Revs = append(fleet.Revs, rec.Revision)
				fleet.About = append(fleet.About, p)
				fleet.Each = append(fleet.Each, map[string]string{wkfHead: head})
				moved = append(moved, p)
				prow[p], pRev[p], pHead[p] = pr.Place.Row, pr.Revision, head
			}
			if err := bad.err(verb); err != nil {
				return nil, err
			}
			if len(moved) == 0 {
				return nil, nil // every card finished at its generation with these facts already (1.5.5)
			}
			entries := []tset.Entry{fleet}
			order, by := wkStreamGroups(moved, prow)
			for _, s := range order {
				w := tset.Entry{Kind: "move", Table: sprint.Work, From: s + ":" + string(sprint.Working), To: s + ":" + string(sprint.Review),
					Set: map[string]string{wkfResult: result}, Unset: []string{wkfRefused}}
				for _, p := range by[s] {
					w.IDs = append(w.IDs, p)
					w.Revs = append(w.Revs, pRev[p])
					w.About = append(w.About, p)
					w.Each = append(w.Each, map[string]string{wkfHead: pHead[p]})
				}
				entries = append(entries, w)
			}
			// The rate is judged from the counts as read, so the count guard
			// holds the member's finished cells to them (2.5: said once a
			// crossing, with no mark).
			ok, failed := counts[sprint.DoneOK], counts[sprint.DoneFailed]
			entries = append(entries, tset.Entry{Kind: "count", Table: sprint.Fleet,
				Cells: []string{as + ":" + sprint.DoneOK, as + ":" + sprint.DoneFailed}, CountMax: []uint64{uint64(ok), uint64(failed)}})
			var notes []sprintfn.NoteReq
			afterOK, afterFailed := ok, failed
			if req.Failed {
				afterFailed += len(moved)
			} else {
				afterOK += len(moved)
				cameOK = append(cameOK, moved...)
				notes = append(notes, wkKnowNote(wkNoticeWorkOK, fmt.Sprintf("work came back ok from %s: %s", as, wkNameList(moved)), moved))
			}
			if !wkOkBelow(ok, failed) && wkOkBelow(afterOK, afterFailed) {
				crossed = true
				notes = append(notes, wkKnowNote(wkNoticeOkRate, fmt.Sprintf("%s's ok rate fell below %d%%: %d ok of %d finished", as, OkRateFloor, afterOK, afterOK+afterFailed), []string{as}))
			}
			return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb, Actor: as},
				Body: sprintfn.Body{Entries: entries, Notes: notes}}, nil
		}}
	return wkChunk{Planned: pl, done: func(res Result) Result {
		n := len(cards) - len(already)
		if res.Step == nil {
			n = 0
		}
		res.Replay = res.Step == nil
		res.Said = fmt.Sprintf("finish: %s finished %d %s", as, n, result)
		if len(already) > 0 {
			res.Said += fmt.Sprintf("; %d already finished at their generation with these facts (%s), nothing written for them", len(already), wkNameList(already))
		}
		if crossed {
			res.Said += fmt.Sprintf("; %s's ok rate fell below %d%%", as, OkRateFloor)
		}
		return res
	}}
}

// ---- read

// ReadCardReq is read <card>... --as r with --begin, or with --ok or --broken
// and the report: its one-line summary (said in the inbox) and, for a broken
// read, the finding (the fix a rework carries, R10). A read card may be named
// at its generation (<card>@1); a read card is asked of one reader once.
type ReadCardReq struct {
	Cards   []CardGen
	As      string
	Begin   bool
	Verdict string // "ok" or "broken"; "" with Begin
	Summary string
	Finding string
}

// ReadCard moves a reader's read cards (section 3, read; 1.5.5), a set in one
// step: two round trips for up to ReadBeginMax cards of a begin, ReadReportMax of a report. --begin moves asked ->
// reading, stamping begun_r = R and due_unreported = R + 2 h (the unbegun entry
// ends and the unreported one begins by derivation, 1.2). --ok and --broken
// move asked or reading -> ok or broken with the verdict, the summary and the
// finding (a report on a card never begun is the begin and the report in one
// step). It reads the read cards and their primaries, the clock, and the
// readers (the reader's ok and broken counts). A read card must be the
// reader's (its row and its reader field) and its primary in review, which the
// step guards at the primary's place (the model's VGuard: the card's state as
// read, its primary in review, not frozen). A begin of a card already begun,
// or a report already made with the same verdict, summary and finding, writes
// nothing; a report with other facts is refused, naming what was reported.
// KNOW "a read of p by r: its one-line summary" names the primaries read; KNOW
// "a reader's broken rate rose above BrokenRateCeiling" is said when this step
// takes the reader's rate across the ceiling, from the counts as read, which a
// count guard holds (2.5). The design names read --broken as its raiser; a
// report of ok is also judged, since the report that brings the reads to
// RateMinReports can be the one that makes a rate above the ceiling judged,
// and the notice is said once a crossing, never missed.
func ReadCard(ctx context.Context, e *Env, req ReadCardReq) (Result, error) {
	const verb = "read"
	as, err := wkActorOf(verb, e, req.As)
	if err != nil {
		return Result{Verb: verb}, err
	}
	switch {
	case req.Begin && req.Verdict != "":
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "read takes --begin, or --ok or --broken, not both")
	case !req.Begin && req.Verdict != sprint.OK && req.Verdict != sprint.Broken:
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "read takes --begin, --ok or --broken")
	case req.Begin && (req.Summary != "" || req.Finding != ""):
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "a begin carries no summary or finding")
	case strings.ContainsAny(req.Summary, "\r\n"):
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "a summary is one line")
	case len(req.Summary) > 4096 || len(req.Finding) > 64<<10:
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "a summary is at most 4 KiB and a finding at most 64 KiB")
	}
	cards, err := wkNamedCards(verb, req.Cards, false)
	if err != nil {
		return Result{Verb: verb}, err
	}
	for _, c := range cards {
		if _, _, _, ok := sprint.ParseReadCard(c.Card); !ok {
			return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is not a read card (<primary>.r<attempt>.<reader>)", c.Card)
		}
	}
	per := ReadReportMax
	if req.Begin {
		per = ReadBeginMax
	}
	return wkPipedSteps(ctx, e, verb, len(cards), per, func(lo, hi int) wkChunk {
		return wkReadChunk(as, cards[lo:hi], req)
	})
}

// wkSameReport says a read card already reported carries this report.
func wkSameReport(rec tset.MemberRecord, req ReadCardReq) bool {
	return wkColOf(rec) == req.Verdict && wkFieldStr(rec, wkfSummary) == req.Summary && wkFieldStr(rec, wkfFinding) == req.Finding
}

func wkReadChunk(as string, cards []CardGen, req ReadCardReq) wkChunk {
	const verb = "read"
	ids := make([]string, len(cards))
	var ps []string
	for i, c := range cards {
		ids[i] = c.Card
		p, _, _, _ := sprint.ParseReadCard(c.Card)
		ps = append(ps, p)
	}
	prims := wkUniqueSorted(ps)
	read := func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch,
			Tset:   []tset.ReadQuery{wkIdsRead(sprint.Readers, ids, wkReadCardReadFields), wkIdsRead(sprint.Work, prims, wkPrimaryReadFields)},
			Sprint: []sprintfn.SprintQuery{wkClockQuery(), wkListingQuery(sprint.QueryReaders)}}
	}
	var already []string
	var reported []string
	crossed := false
	pl := Planned{Verb: verb, Read: read,
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			already, reported, crossed = already[:0], reported[:0], false
			recs, err := wkRecordsAt(rd, 0, len(ids))
			if err != nil {
				return nil, err
			}
			precs, err := wkRecordsAt(rd, 1, len(prims))
			if err != nil {
				return nil, err
			}
			byPrim := map[string]tset.MemberRecord{}
			for i, p := range prims {
				byPrim[p] = precs[i]
			}
			r, wall, err := wkClockAt(rd, 0)
			if err != nil {
				return nil, err
			}
			isRow, counts, err := wkCellCounts(rd, 1, sprint.QueryReaders, as)
			if err != nil {
				return nil, err
			}
			if !isRow {
				return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s is no reader", as)
			}
			var bad wkFaults
			from := map[string]*tset.Entry{} // asked, reading -> the entry that moves them
			var fromOrder []string
			prow := map[string]string{}
			for i, c := range cards {
				rec := recs[i]
				p, _, reader, _ := sprint.ParseReadCard(c.Card)
				if !rec.Exists || rec.Place == nil {
					bad.add(c.Card, "%s: no read card is placed (%s): a retired read is read again on its primary's next attempt", c.Card, wkPlaceOf(rec))
					continue
				}
				gen := 1
				if g, err := strconv.Atoi(wkFieldStr(rec, wkfGen)); err == nil && g > 0 {
					gen = g
				}
				if c.Gen != 0 && c.Gen != gen {
					bad.conflict(c.Card, "%s: stale: generation %d is not the live one (%d)", c.Card, c.Gen, gen)
					continue
				}
				if reader != as || rec.Place.Row != as || wkFieldStr(rec, wkfReader) != as {
					bad.add(c.Card, "%s: not %s's to read (it is %s, reader %q)", c.Card, as, wkPlaceOf(rec), wkFieldStr(rec, wkfReader))
					continue
				}
				col := rec.Place.Col
				switch {
				case req.Begin && col != sprint.Asked:
					if col == sprint.Reading || col == sprint.OK || col == sprint.Broken {
						already = append(already, c.Card)
					} else {
						bad.add(c.Card, "%s: it is %s, not asked", c.Card, wkPlaceOf(rec))
					}
					continue
				case !req.Begin && (col == sprint.OK || col == sprint.Broken):
					if wkSameReport(rec, req) {
						already = append(already, c.Card)
					} else {
						bad.conflict(c.Card, "%s: already reported %s with summary %q", c.Card, col, wkFieldStr(rec, wkfSummary))
					}
					continue
				case !req.Begin && col != sprint.Asked && col != sprint.Reading:
					bad.add(c.Card, "%s: it is %s, not asked or reading", c.Card, wkPlaceOf(rec))
					continue
				}
				pr := byPrim[p]
				if wkColOf(pr) != string(sprint.Review) {
					bad.add(c.Card, "%s: its primary %s is not in review (it is %s)", c.Card, p, wkPlaceOf(pr))
					continue
				}
				en := from[col]
				if en == nil {
					en = wkReadEntry(as, col, req, r, wall)
					from[col] = en
					fromOrder = append(fromOrder, col)
				}
				en.IDs = append(en.IDs, c.Card)
				en.Revs = append(en.Revs, rec.Revision)
				en.About = append(en.About, p)
				reported = append(reported, p)
				prow[p] = pr.Place.Row
			}
			if err := bad.err(verb); err != nil {
				return nil, err
			}
			if len(fromOrder) == 0 {
				return nil, nil // every card already begun, or reported with these facts (1.5.5)
			}
			sort.Strings(fromOrder)
			var entries []tset.Entry
			for _, col := range fromOrder {
				entries = append(entries, *from[col])
			}
			// Each primary guarded at review, at its place alone: a read does not
			// race the primary's own fields (the model guards col = review).
			reads := wkUniqueSorted(reported)
			order, by := wkStreamGroups(reads, prow)
			for _, s := range order {
				entries = append(entries, tset.Entry{Kind: "guard", Table: sprint.Work, From: s + ":" + string(sprint.Review), IDs: by[s]})
			}
			if req.Begin {
				return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb, Actor: as}, Body: sprintfn.Body{Entries: entries}}, nil
			}
			ok, broken := counts[sprint.OK], counts[sprint.Broken]
			entries = append(entries, tset.Entry{Kind: "count", Table: sprint.Readers,
				Cells: []string{as + ":" + sprint.OK, as + ":" + sprint.Broken}, CountMax: []uint64{uint64(ok), uint64(broken)}})
			line := req.Verdict
			if req.Summary != "" {
				line += ": " + req.Summary
			}
			notes := []sprintfn.NoteReq{wkKnowNote(wkNoticeReadSummary, fmt.Sprintf("read by %s: %s", as, line), reads)}
			afterOK, afterBroken := ok, broken
			if req.Verdict == sprint.OK {
				afterOK += len(reported)
			} else {
				afterBroken += len(reported)
			}
			if !wkBrokenAbove(ok, broken) && wkBrokenAbove(afterOK, afterBroken) {
				crossed = true
				notes = append(notes, wkKnowNote(wkNoticeBrokenRate, fmt.Sprintf("%s's broken rate rose above %d%%: %d broken of %d read", as, BrokenRateCeiling, afterBroken, afterOK+afterBroken), []string{as}))
			}
			return &sprintfn.Request{Meta: sprintfn.Meta{Verb: verb, Actor: as}, Body: sprintfn.Body{Entries: entries, Notes: notes}}, nil
		}}
	return wkChunk{Planned: pl, done: func(res Result) Result {
		n := len(reported)
		if res.Step == nil {
			n = 0
		}
		res.Replay = res.Step == nil
		if req.Begin {
			res.Said = fmt.Sprintf("read: %s began %d", as, n)
		} else {
			res.Said = fmt.Sprintf("read: %s reported %d %s", as, n, req.Verdict)
		}
		if len(already) > 0 {
			res.Said += fmt.Sprintf("; %d already so (%s), nothing written for them", len(already), wkNameList(already))
		}
		if crossed {
			res.Said += fmt.Sprintf("; %s's broken rate rose above %d%%", as, BrokenRateCeiling)
		}
		return res
	}}
}

// wkReadEntry is the move of a reader's read cards out of one column: to reading
// for a begin, to the verdict for a report (with the begin's stamps for a card
// never begun).
func wkReadEntry(as, col string, req ReadCardReq, r, wall int64) *tset.Entry {
	begin := map[string]string{wkfBegun: wkWallStamp(wall), wkfBegunR: wkMsOf(r)}
	if req.Begin {
		begin[wkfDueUnreport] = wkMsOf(r + wkUnreportedSpan.Milliseconds())
		return &tset.Entry{Kind: "move", Table: sprint.Readers, From: as + ":" + col, To: as + ":" + sprint.Reading,
			Set: begin, Unset: []string{wkfDueUnbegun, wkfRefused}}
	}
	set := map[string]string{wkfVerdict: req.Verdict, wkfReadAt: wkWallStamp(wall)}
	if req.Summary != "" {
		set[wkfSummary] = req.Summary
	}
	if req.Finding != "" {
		set[wkfFinding] = req.Finding
	}
	if col == sprint.Asked {
		set[wkfBegun], set[wkfBegunR] = begin[wkfBegun], begin[wkfBegunR]
	}
	return &tset.Entry{Kind: "move", Table: sprint.Readers, From: as + ":" + col, To: as + ":" + req.Verdict,
		Set: set, Unset: []string{wkfDueUnbegun, wkfDueUnreport, wkfRefused}}
}

// ---- queue

// QueueLimit is a queue page's cards when the caller names no limit, and
// QueueLimitMax the most a page holds (Layer 1's range: 2,000 ids).
const (
	QueueLimit    = 50
	QueueLimitMax = 2000
)

// QueueReq is queue --as m (the member's ready and working cells in the fleet)
// or queue --stream s (the stream's five open cells in the work table), one
// page of at most Limit cards from After, the cursor the page before it gave.
type QueueReq struct {
	As     string
	Stream string
	Limit  int
	After  string
}

// QueueCard is one card of a queue page: its cell, its score and its fields.
type QueueCard struct {
	ID     string
	Cell   string
	Score  string
	Fields map[string]string
	cell   int // the index of its cell in the queue's cells
}

// QueuePage is one page of a queue, and Next the cursor of the page after it, ""
// when this is the last.
type QueuePage struct {
	Cards []QueueCard
	Next  string
}

// The fields a queue page shows of a card: the packet a worker needs of a work
// card, and what the coordinator reads of a primary.
var (
	wkQueueWorkCardFields = []string{"kind", wkfPrimary, "stream", "attempt", wkfGen, wkfMember, "fix", wkfFirstTakenR, wkfUntakenR, wkfHead}
	wkQueuePrimaryFields  = []string{"kind", "attempt", wkfResult, wkfHead, "needs", "open", wkfRefused, "bound", wkfWork}
)

// wkQueueCursor is a page's cursor: the index of the cell it stopped in, and the
// score and id of the last card it gave there.
type wkQueueCursor struct {
	cell  int
	score string
	id    string
}

func (c wkQueueCursor) String() string { return fmt.Sprintf("%d/%s/%s", c.cell, c.score, c.id) }

func wkParseQueueCursor(s string) (wkQueueCursor, bool) {
	if s == "" {
		return wkQueueCursor{}, true
	}
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return wkQueueCursor{}, false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 0 {
		return wkQueueCursor{}, false
	}
	return wkQueueCursor{cell: n, score: parts[1], id: parts[2]}, true
}

// Queue reads one page of a queue (section 3, queue: "the cells, with a limit,
// and the packets' records"; pages, one round trip a page) and writes
// nothing. Result.Said is the page, one line a card, and the cursor of the next
// page; QueueRead returns it as data.
func Queue(ctx context.Context, e *Env, req QueueReq) (Result, error) {
	page, res, err := QueueRead(ctx, e, req)
	if err != nil {
		return res, err
	}
	var b strings.Builder
	for _, c := range page.Cards {
		fmt.Fprintf(&b, "%s %s", c.Cell, c.ID)
		keys := make([]string, 0, len(c.Fields))
		for k := range c.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%s", k, c.Fields[k])
		}
		b.WriteString("\n")
	}
	if page.Next != "" {
		fmt.Fprintf(&b, "more: queue --after %s\n", page.Next)
	}
	res.Said = strings.TrimSuffix(b.String(), "\n")
	return res, nil
}

// QueueRead is Queue's page as data: one atomic read of each cell from the
// cursor, with the cards' records, cut to the limit in cell order and score
// order. A member's queue is its ready and working cells; a stream's, its
// waiting, ready, working, review and merging cells. One round trip.
func QueueRead(ctx context.Context, e *Env, req QueueReq) (QueuePage, Result, error) {
	const verb = "queue"
	var page QueuePage
	if (req.As == "") == (req.Stream == "") {
		return page, Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "queue takes --as <member> or --stream <stream>")
	}
	limit := req.Limit
	if limit == 0 {
		limit = QueueLimit
	}
	if limit < 1 || limit > QueueLimitMax {
		return page, Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "--limit is 1 to %d", QueueLimitMax)
	}
	cur, ok := wkParseQueueCursor(req.After)
	if !ok {
		return page, Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "--after %q is not a queue cursor", req.After)
	}
	table, row, fields := sprint.Fleet, req.As, wkQueueWorkCardFields
	cols := []string{string(sprint.Ready), string(sprint.Working)}
	if req.Stream != "" {
		table, row, fields = sprint.Work, req.Stream, wkQueuePrimaryFields
		cols = []string{string(sprint.Waiting), string(sprint.Ready), string(sprint.Working), string(sprint.Review), string(sprint.Merging)}
	}
	if !sprint.ValidID(row) {
		return page, Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "%q is not a member's or a stream's name", row)
	}
	if cur.cell >= len(cols) {
		return page, Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "--after %q is past the queue's cells", req.After)
	}
	read := func(epoch tset.Decimal) *sprintfn.ReadRequest {
		rr := &sprintfn.ReadRequest{Epoch: epoch}
		for i := cur.cell; i < len(cols); i++ {
			from := "-inf"
			if i == cur.cell && cur.score != "" {
				from = cur.score // inclusive: the ids at the cursor's own score are cut by id below
			}
			// One more than the page, so the cut by id at the cursor's score
			// still leaves a whole page when there is one.
			rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "range", Table: table, Cell: row + ":" + cols[i], Min: from, Max: "+inf",
				Limit: min(limit+1, QueueLimitMax), Records: true, Fields: fields})
		}
		return rr
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Read: read})
	if err != nil {
		var rf *Refused
		if errors.As(err, &rf) && rf.Code() == "NOROW" {
			rf.Hint = fmt.Sprintf("%s has no row in the %s table", row, table)
		}
		return page, res, err
	}
	rd := res.Read
	if rd == nil || len(rd.Tset) != len(cols)-cur.cell {
		return page, res, fmt.Errorf("queue: the read answered %d cells, asked %d", len(rd.Tset), len(cols)-cur.cell)
	}
	// Cells in order, each in score order (a cell's own order), from the
	// cursor; the page stops when it is full, or at a cell whose read had more
	// than it held, since nothing after that cell's last card read is known.
	var next *wkQueueCursor
	for i := cur.cell; i < len(cols) && next == nil; i++ {
		ans := rd.Tset[i-cur.cell]
		given := 0
		for j, id := range ans.IDs {
			score := ans.Scores[j]
			if i == cur.cell && cur.score != "" && score == cur.score && id <= cur.id {
				continue // given on the page before
			}
			if len(page.Cards) == limit {
				last := page.Cards[len(page.Cards)-1]
				next = &wkQueueCursor{cell: last.cell, score: last.Score, id: last.ID}
				break
			}
			qc := QueueCard{ID: id, Cell: row + ":" + cols[i], Score: score, Fields: map[string]string{}, cell: i}
			if j < len(ans.Records) {
				for k, v := range ans.Records[j].Fields {
					if v.Present {
						qc.Fields[k] = v.Value
					}
				}
			}
			page.Cards = append(page.Cards, qc)
			given++
		}
		if next == nil && ans.HasMore {
			if given == 0 {
				return page, res, fmt.Errorf("queue: more than %d cards of %s:%s share the score %s; read it with a larger --limit", limit, row, cols[i], cur.score)
			}
			last := page.Cards[len(page.Cards)-1]
			next = &wkQueueCursor{cell: i, score: last.Score, id: last.ID}
		}
	}
	if next != nil {
		page.Next = next.String()
	}
	res.Said = fmt.Sprintf("queue: %d cards", len(page.Cards))
	return page, res, nil
}

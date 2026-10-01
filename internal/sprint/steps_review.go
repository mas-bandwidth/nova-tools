package sprint

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The steps of review: ask and read (mechanical, and the readers' own), and
// the coordinator's verbs accept, rework, return, drop, rank and ci.

// AskReq deals primaries in review to readers.
type AskReq struct {
	Sel
	Another bool // one more reader for a primary already asked
	Answers []string
	Who     string
}

// readsAt is the primary's placed read cards at an attempt, in reader row order.
func readsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, r := range s.Readers.Rows() {
		c := s.Readers.Placed(ReadCardID(pr.ID, attempt, r))
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// Ask deals every primary in review that lacks reads to TWO DIFFERENT readers
// up (readers.go), in work order, the readers it names first and then the next readers round
// the readers (round.go, errata 3 amendment 5: from the rolling index,
// wrapping, each the first that has no read card at the attempt, the index
// moved past it: the readers table's ask_index, written with the ask); a
// primary reworked after a read is asked of the same readers again. With Another, a primary already asked is dealt to
// one more reader, the next round the readers.
func Ask(s *Snapshot, r AskReq) Plan {
	var p Plan
	// in stream turns from the ask's stream index on the work table
	// (streamTurns), so a limit asks of every stream alike, and the index moves
	// past the stream of the last primary asked
	srr := askStreamRound(s)
	eligible := func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if c.F("result") == "failed" {
			return "its work came back failed: rework or drop it"
		}
		asked := len(readsAt(s, c, c.Int("attempt")))
		have := len(liveReadsAt(s, c, c.Int("attempt")))
		if r.Another && asked == 0 {
			return "not asked yet at attempt " + itoa(c.Int("attempt")) + ": the machine's tick asks it, or run: nova-sprint ask " + c.ID + "; --another adds a reader to one already asked"
		}
		if !r.Another && have >= 2 {
			return "asked already"
		}
		return ""
	}
	chosen := pick(&p, r.Sel, eligibleTurns(s.Work.Column(Review), eligible, srr), rowOf, eligible, s.primaryCard)
	rr := askRound(s)
	moves := roundMoves{}
	for _, c := range chosen {
		attempt := c.Int("attempt")
		have := map[string]bool{}
		var all []string
		var takenBack []Change
		var away []string
		for _, rc := range readsAt(s, c, attempt) {
			if awayRead(s, rc) {
				// asked of a reader that is not up: taken back, and asked again below
				takenBack = append(takenBack, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "away"})))
				away = append(away, rc.F("reader"))
				continue
			}
			have[rc.F("reader")] = true
			all = append(all, rc.F("reader"))
		}
		var free []string
		for _, rd := range s.Readers.Rows() {
			// a reader with a card at this attempt, even retired, has read it
			// and a reader not up is not asked (reader away, reader up)
			if !have[rd] && s.Readers.Card(ReadCardID(c.ID, attempt, rd)) == nil && s.ReaderIsUp(rd) {
				free = append(free, rd)
			}
		}
		want := 2 - len(all) // a read taken back from a reader away leaves one to ask
		if r.Another {
			want = 1
		}
		var chosenReaders []string
		if !r.Another {
			for _, rd := range Split(c.F("asked")) {
				if contains(free, rd) && len(chosenReaders) < want {
					chosenReaders = append(chosenReaders, rd)
				}
			}
		}
		rotated := rr.picks(want-len(chosenReaders), chosenReaders, func(x string) bool { return contains(free, x) })
		chosenReaders = append(chosenReaders, rotated...)
		if len(chosenReaders) < want {
			p.refuse(c.ID, fmt.Sprintf("needs %d different readers and %d is free who has not already read attempt %d of %s; a reader away or down is not asked (readers: %s); run: nova-sprint reader add <name>, or nova-sprint reader up <name>", want, len(chosenReaders), attempt, c.ID, readersText(s)))
			continue
		}
		u := Unit{Key: c.ID, Stream: c.Row, Changes: takenBack}
		for _, rd := range rotated {
			rr.moved(rd)
			moves[c.ID] = joinMoves(moves[c.ID], rd)
		}
		for _, rd := range chosenReaders {
			u.Changes = append(u.Changes, change(Readers, createEntry(ReadCardID(c.ID, attempt, rd), rd, Asked, c.Score,
				map[string]string{"kind": "read", "primary": c.ID, "stream": c.Row, "reader": rd, "attempt": itoa(attempt), "head": c.F("head"), "asked": stamp(s.Now)})))
		}
		all = append(all, chosenReaders...)
		if !r.Another {
			// the readers kept on the primary are the pair; --another's reader
			// is for this attempt only
			u.Changes = append(u.Changes, change(Work, setEntry(c, map[string]string{"asked": strings.Join(all, ",")})))
		}
		u.Moved = c.ID + " asked of " + strings.Join(chosenReaders, ", ")
		if len(away) > 0 {
			u.Moved += "; its read taken back from " + strings.Join(away, ", ") + " (not up)"
		}
		if r.Another {
			u.Closes = closesFor(s.Open, []string{NReadBroken, NReadsExhausted, NStranded, NStalled}, c.ID)
		} else {
			u.Closes = closesFor(s.Open, []string{NStranded, NStalled}, c.ID)
		}
		asked := map[string]string{}
		for _, rd := range chosenReaders {
			asked[ReadCardID(c.ID, attempt, rd)] = Asked
		}
		if j, ok := reviewJudgment(s, c, reviewStep{moved: asked, closing: noteIDs(u.Closes), who: r.Who}); ok {
			u.Notes = append(u.Notes, j)
		}
		p.Units = append(p.Units, u)
	}
	roundWrites(&p, rr, moves)
	if !r.Another {
		// one more reader of a primary named is not a turn round the streams:
		// the ask's stream index moves with the asks of the streams' cards
		// (errata 3 amendment 10; the reference model's AskAnother moves no
		// stream index)
		streamIndexWrite(&p, srr, s.Work.Placed)
	}
	answered(&p, s, r.Answers, r.Who)
	return p
}

// NamedExtras is the named cards a step must read as records when they are not
// on the table: a report against a retired card is refused naming the retirement.
func NamedExtras(table string, ids []string) func(*Snapshot) map[string][]string {
	return func(s *Snapshot) map[string][]string {
		var out []string
		for _, id := range ids {
			if s.T(table).Placed(id) == nil {
				out = append(out, id)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return map[string][]string{table: out}
	}
}

// ReadReq is a reader moving its own read cards.
type ReadReq struct {
	Sel
	As      string
	Begin   bool   // asked -> reading
	Verdict string // ok or broken
	Finding string
	Who     string
	// Return hands the named read back, with the reason: no verdict, no
	// finding against the work; the tick asks it of another reader.
	Return bool   `json:",omitempty"`
	Reason string `json:",omitempty"`
}

// Read moves a reader's read cards: asked -> reading, or asked|reading -> ok|broken
// with the finding (a report on a card still asked is the begin and the report
// in one step, begun stamped with it), or, with Return, hands a read it holds
// back: retired, the reason in one happened note, and the next tick's ask
// asks it of another reader up at the same attempt (tla/DirtyTick.tla,
// ReadReturn). A broken read is a judgment; the second different reader's
// ok at the primary's head is the judgment ready to accept, which accept,
// rework and drop close. As may name several readers, comma separated: every
// card named is one of theirs, each read as its own reader's, in the one plan
// (errata 3 amendment 10); a read by selection names one reader.
func Read(s *Snapshot, r ReadReq) Plan {
	var p Plan
	sel := r.Sel
	if len(sel.IDs) == 0 && sel.Only == nil && sel.Limit == 0 {
		sel.Limit = 1
	}
	readers := Split(r.As)
	if r.Return && (!named(sel) || r.Reason == "") {
		p.refuse("read", "a return names its card and the reason: read --as <reader> --return <card> --reason <text>")
		return p
	}
	if len(readers) > 1 && !named(sel) {
		p.refuse("read", "a read by selection names one reader: --as <reader>; several readers name their cards")
		return p
	}
	from := []string{Asked, Reading}
	if r.Begin {
		from = []string{Asked}
	}
	var all []*Card
	for _, rd := range readers {
		for _, col := range from {
			all = append(all, s.Readers.Cell(rd, col)...)
		}
	}
	SortCards(all)
	chosen := pick(&p, sel, all, fieldStream, func(c *Card) string {
		if !c.Placed() && c.F("retired") != "" {
			if c.F("retired_by") == "away" {
				return "retired at " + c.F("retired") + ": the reader was away; the read was asked of another reader"
			}
			if c.F("retired_by") == "returned" {
				return "retired at " + c.F("retired") + ": the read was returned; it was asked of another reader"
			}
			return "retired at " + c.F("retired") + " by " + orDash(c.F("retired_by")) + ": the primary was sent back; its next attempt is read on a new card"
		}
		if !c.Placed() || !contains(readers, c.Row) {
			return "not " + r.As + "'s to read (it is " + placeWord(c) + ")"
		}
		if !contains(from, c.Col) {
			return "not " + strings.Join(from, " or ") + " (it is " + c.Col + ")"
		}
		return ""
	}, s.Readers.Card)
	col := OK
	if r.Verdict == "broken" {
		col = Broken
	}
	// every report of the plan, by primary: two readers' reports of one
	// primary in the one step (several readers, errata 3 amendment 10) are
	// judged together, the primary's judgment after the last of them
	moved := map[string]map[string]string{}
	last := map[string]int{}
	lastReader := map[string]string{}
	written := map[string][]Note{}
	broken := map[string]int{}
	for _, c := range chosen {
		pr := s.Work.Card(c.F("primary"))
		if r.Begin {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Readers, moveEntry(c, c.Row, Reading, map[string]string{"begun": stamp(s.Now)}))},
				Moved: c.ID + " asked -> reading"})
			continue
		}
		if r.Return {
			// retired as the away take-back retires: the reader keeps its card
			// at the attempt, so the ask never asks it of this reader again
			n := happened(NReadReturned, c.F("stream"), s.Now, c.F("primary"))
			n.What = c.Row + " returned " + c.ID + ": " + r.Reason
			n.Who = r.Who
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
				Changes: []Change{change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": "returned"}))},
				Moved:   c.ID + " " + c.Col + " -> returned", Notes: []Note{n}})
			continue
		}
		set := map[string]string{"verdict": r.Verdict, "read": stamp(s.Now)}
		if c.Col == Asked { // a report on a card never begun is the begin and the report in one step
			set["begun"] = stamp(s.Now)
		}
		if r.Finding != "" {
			set["finding"] = r.Finding
		}
		u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Readers, moveEntry(c, c.Row, col, set))},
			Moved: fmt.Sprintf("%s %s -> %s", c.ID, c.Col, col)}
		if pr != nil {
			if col == Broken {
				before := pr.Int("broken_reads") + broken[pr.ID]
				for _, o := range s.Readers.Of(pr.ID) {
					if o.Col == Broken && o.ID != c.ID {
						before++
					}
				}
				broken[pr.ID]++
				n := judgment(NReadBroken, pr.Row, s.Now, before, pr.ID)
				n.Who, n.Attempt, n.What = c.Row, c.Int("attempt"), r.Finding
				u.Notes = append(u.Notes, n)
			}
			if moved[pr.ID] == nil {
				moved[pr.ID] = map[string]string{}
			}
			moved[pr.ID][c.ID] = col
			last[pr.ID], lastReader[pr.ID] = len(p.Units), c.Row
			written[pr.ID] = append(written[pr.ID], u.Notes...)
		}
		p.Units = append(p.Units, u)
	}
	for id, i := range last {
		pr := s.Work.Card(id)
		if j, ok := reviewJudgment(s, pr, reviewStep{moved: moved[id], writes: written[id], who: lastReader[id]}); ok {
			p.Units[i].Notes = append(p.Units[i].Notes, j)
		}
	}
	return p
}

// reviewStep is what a step does around a primary it leaves in review, for
// the judgment the primary needs after it.
type reviewStep struct {
	moved   map[string]string // read card id -> its column after the step; a card the step creates is asked
	closing map[string]bool   // note ids the step closes
	writes  []Note            // the notes the step writes
	acked   []string          // judgment types the step acknowledges on it: not written again by the same step
	who     string
}

// noteIDs is the ids of the open judgments, as a set.
func noteIDs(open []Open) map[string]bool {
	out := map[string]bool{}
	for _, o := range open {
		out[o.Note.ID] = true
	}
	return out
}

// inReview is the primary as a step that moves it into review leaves it:
// placed in review, with the fields the step sets.
func inReview(pr *Card, set map[string]string) *Card {
	after := *pr
	after.Col = Review
	after.Fields = map[string]string{}
	for k, v := range pr.Fields {
		after.Fields[k] = v
	}
	for k, v := range set {
		after.Fields[k] = v
	}
	return &after
}

// reviewJudgment is the judgment a primary the step leaves in review (pr, as
// the step leaves it) needs now, so that no primary in review is silent:
//   - ready to accept, when ok reads from two different readers stand at its
//     head, no judgment open on it after the step offers accept (ready to
//     accept, or returned to review with its reads standing), and the
//     machine is STOPPED or its pump holds it (AcceptHeld: its CI red at its
//     head, or returned at its attempt): a RUNNING machine's pump accepts the
//     rest (TickAccept);
//   - else, when nothing is open on it after the step and no read is
//     outstanding: stranded in review when its work came back failed, or when
//     it was never asked at its attempt and the step closes the last judgment
//     on it (a primary that only arrived in review is asked by the machine);
//     reads exhausted when its reads are done without two different oks.
//
// Every step that can leave a primary in review calls it: finish, read,
// return, ack, ask, ci, and a refused rework. A judgment the step itself
// acknowledges on the primary is not written again by that step.
func reviewJudgment(s *Snapshot, pr *Card, st reviewStep) (Note, bool) {
	if !pr.Placed() || pr.Col != Review || s.Readers == nil {
		return Note{}, false
	}
	attempt := pr.Int("attempt")
	oks := map[string]bool{}
	outstanding, reads := false, 0
	for _, r := range s.Readers.Rows() {
		id := ReadCardID(pr.ID, attempt, r)
		c := s.Readers.Placed(id)
		col, moved := st.moved[id]
		switch {
		case c == nil && !moved:
			continue
		case c == nil:
			col = Asked
		case !moved:
			col = c.Col
		}
		reads++
		switch {
		case col == Asked || col == Reading:
			outstanding = true
		case col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c):
			oks[r] = true
		}
	}
	open := map[string]bool{} // the judgment types open on it after the step
	offers := false           // one of them offers accept
	before := closesFor(s.Open, nil, pr.ID)
	for _, o := range before {
		if !st.closing[o.Note.ID] {
			open[o.Note.Type] = true
			offers = offers || contains(o.Note.Decisions, "accept")
		}
	}
	for _, n := range st.writes {
		if n.Kind == Judgment && contains(n.Primaries, pr.ID) {
			open[n.Type] = true
			offers = offers || contains(n.Decisions, "accept")
		}
	}
	var typ, why string
	switch {
	case len(oks) >= 2:
		if offers || s.Running && AcceptHeld(pr) == "" {
			// a RUNNING machine's pump accepts it: "accept is mechanical"
			return Note{}, false
		}
		typ = NReadyToAccept
	case len(open) > 0 || outstanding:
		return Note{}, false
	case pr.F("result") == "failed":
		typ, why = NStranded, "its work came back failed and nothing is open on it"
	case reads == 0 && len(before) == 0:
		return Note{}, false
	case reads == 0:
		typ, why = NStranded, "never asked at attempt "+itoa(attempt)+" and nothing is open on it"
	default:
		typ, why = NReadsExhausted, "no read is outstanding and two different readers have not said ok at "+orDash(pr.F("head"))
	}
	if contains(st.acked, typ) {
		return Note{}, false
	}
	if typ == NReadyToAccept {
		n := judgment(NReadyToAccept, pr.Row, s.Now, 0, pr.ID)
		n.Who, n.Attempt = st.who, attempt
		return n, true
	}
	return strandedNote(s, pr, typ, why, st.who), true
}

// AcceptReq is the coordinator accepting primaries in review.
type AcceptReq struct {
	Sel
	Answers []string
	Who     string
}

// okReaders is two ok read cards from different readers at the primary's
// current attempt and head, in reader row order; fewer when it has fewer.
func okReaders(s *Snapshot, pr *Card) []*Card {
	var out []*Card
	seen := map[string]bool{}
	for _, c := range readsAt(s, pr, pr.Int("attempt")) {
		r := c.F("reader")
		if c.Col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c) && !seen[r] && len(out) < 2 {
			seen[r] = true
			out = append(out, c)
		}
	}
	return out
}

// ReadCardAgrees says a read card is where its identity says: the row it
// occupies, the reader its id names and its reader field are one reader. A
// card that disagrees counts for no reader; check reports it.
func ReadCardAgrees(c *Card) bool {
	_, _, idReader, ok := ParseReadCard(c.ID)
	return ok && c.Row == idReader && c.F("reader") == idReader
}

// Accept moves review -> merging and places the primary in merge queued with
// its score. It is refused without ok reads from two different readers at the
// primary's head, whoever the readers. The primary's read cards still asked
// or reading are retired in the same step, marked retired by accept, so no
// read is outstanding on a primary that is not in review. Named ids are all or nothing under one
// pre-state; a selection (a stream, the primaries with two ok reads, an inbox
// group) moves the eligible and lists the rest with the reason.
func Accept(s *Snapshot, r AcceptReq) Plan {
	var p Plan
	// in stream turns from the accept's stream index on the work table
	// (streamTurns, as the deal's), so a limit accepts of every stream alike
	// and the merge queues fill together; the index moves past the stream of
	// the last accepted (errata 3 amendment 10)
	srr := streamRound(s, PropAcceptStreamIndex)
	eligible := func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if oks := okReaders(s, c); len(oks) < 2 {
			var names []string
			for _, o := range oks {
				names = append(names, o.F("reader"))
			}
			return fmt.Sprintf("needs ok from two different readers at head %s; has ok from %d (%s)", orDash(c.F("head")), len(oks), orDash(strings.Join(names, ",")))
		}
		if m := s.Merge.Card(c.ID); m != nil && (!m.Placed() || m.Col != Returned) {
			return "its merge record is " + placeWord(m)
		}
		if s.StreamCtl(c.Row) == nil {
			return "stream " + c.Row + " has no merge row"
		}
		return ""
	}
	chosen := pick(&p, r.Sel, eligibleTurns(s.Work.Column(Review), eligible, srr), rowOf, eligible, s.primaryCard)
	if len(r.IDs) > 0 && len(p.Refused) > 0 {
		for _, c := range chosen {
			p.refuse(c.ID, "eligible, not moved: the named set is all or nothing and another card of it was refused")
		}
		return p
	}
	for _, c := range chosen {
		oks := okReaders(s, c)
		u := Unit{Key: c.ID, Stream: c.Row}
		for _, o := range oks {
			u.Changes = append(u.Changes, change(Readers, guardEntry(o)))
		}
		retired := 0
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Asked || rc.Col == Reading {
				u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "accept"})))
				retired++
			}
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			e := moveEntry(m, c.Row, Queued, nil)
			sc := c.Score
			e.Move.Score = &sc
			u.Changes = append(u.Changes, change(Merge, e))
		} else {
			u.Changes = append(u.Changes, change(Merge, createEntry(c.ID, c.Row, Queued, c.Score,
				map[string]string{"kind": "merge", "primary": c.ID, "stream": c.Row})))
		}
		readers := oks[0].F("reader") + "," + oks[1].F("reader")
		u.Changes = append(u.Changes, change(Work, moveEntry(c, c.Row, Merging, map[string]string{"readers": readers, "accepted": stamp(s.Now)})))
		u.Moved = fmt.Sprintf("%s review -> merging queued (ok from %s)", c.ID, strings.ReplaceAll(readers, ",", ", "))
		if retired > 0 {
			u.Moved += fmt.Sprintf("; %d outstanding read cards retired", retired)
		}
		u.Closes = closesFor(s.Open, nil, c.ID)
		p.Units = append(p.Units, u)
	}
	// A waiting stream with something queued is merging.
	for _, st := range unitStreams(p) {
		if ctl := s.StreamCtl(st); ctl.F("state") == StreamWaiting {
			n := happened(NStartedMerging, st, s.Now)
			n.Who = r.Who
			setStream(&p, s, st, map[string]string{"state": StreamMerging, "since": stamp(s.Now)}, n)
		}
	}
	answered(&p, s, r.Answers, r.Who)
	p = Lawful(p)
	streamIndexWrite(&p, srr, s.Work.Placed)
	return p
}

// unitStreams is the streams of the plan's units, in order.
func unitStreams(p Plan) []string {
	var out []string
	for _, u := range p.Units {
		if u.Stream != "" && !contains(out, u.Stream) {
			out = append(out, u.Stream)
		}
	}
	return out
}

// settle keeps a stream's state true after cards leave its merge queue
// (offQueue) or the table (offTable): landed when every primary of the stream
// left on the table has landed, else waiting when a merging stream has nothing
// queued or stuck. A stopped stream stays stopped until it resumes.
func settle(p *Plan, s *Snapshot, who string, offQueue, offTable map[string]bool, landing ...map[string]bool) {
	lands := map[string]bool{} // the cards this step lands: landed after it
	for _, l := range landing {
		for id := range l {
			lands[id] = true
		}
	}
	for _, st := range unitStreams(*p) {
		ctl := s.StreamCtl(st)
		state := ctl.F("state")
		if ctl == nil || state == StreamStopped || state == StreamLanded {
			continue
		}
		left := 0
		for _, c := range append(s.Merge.Cell(st, Queued), s.Merge.Cell(st, Stuck)...) {
			if !offQueue[c.ID] && !offTable[c.ID] {
				left++
			}
		}
		open, landed := 0, s.Work.Count(st, Landed)
		for _, x := range []State{Waiting, Ready, Working, Review, Merging} {
			for _, c := range s.Work.Cell(st, x) {
				switch {
				case lands[c.ID]:
					landed++
				case !offTable[c.ID]:
					open++
				}
			}
		}
		switch {
		case open == 0 && landed > 0:
			n := happened(NStreamLanded, st, s.Now)
			n.Who = who
			setStream(p, s, st, map[string]string{"state": StreamLanded, "since": stamp(s.Now)}, n)
		case open == 0 && landed == 0 && (state != StreamWaiting || ctl.F("since") != ""):
			// empty: nothing is on the table for it; waiting, with no since
			setStream(p, s, st, map[string]string{"state": StreamWaiting, "since": ""})
		case left == 0 && state == StreamMerging:
			setStream(p, s, st, map[string]string{"state": StreamWaiting, "since": stamp(s.Now)})
		}
	}
}

// ReworkReq is the coordinator sending primaries back with a fix. With no
// fix, each primary's fix is its own: the finding of its broken read, or the
// report of its failed work; a primary with neither is refused by name.
type ReworkReq struct {
	Sel
	Fix     string
	Answers []string
	Who     string
}

// ReworkResolves is the judgments a rework discharges on its primary.
var ReworkResolves = []string{NWorkFailed, NReadBroken, NCIRed, NRepairSkipped, NReadyToAccept, NReturned, NReadsExhausted, NStranded, NStalled, NBound}

// Rework delegates at once: the next work card attempt, carrying the fix, is
// cut into the next member round the fleet (round.go, errata 3 amendment 5:
// from the deal's rolling index, the first up with room other than the member
// of the attempt's work card, that member only when no other has room; the
// index moved past it and written with the step) and the primary moves
// review -> working in the same step; its read cards are retired and the
// readers' identities kept, so the fixed work is asked of them again when it
// returns. With no member up, or none below its width (tla/DirtyTick.tla,
// WidthRespected), the primary moves review -> ready with the fix and the
// tick's deal cuts its card when a member has room.
func Rework(s *Snapshot, r ReworkReq) Plan {
	var p Plan
	// a primary in review, or one at its redeal bound (ready, its work card
	// withdrawn and dealt no more): rework is its next attempt
	pool := s.Work.Column(Review)
	for _, c := range s.Work.Column(Ready) {
		if AtRedealBound(s, c) != nil {
			pool = append(pool, c)
		}
	}
	chosen := pick(&p, r.Sel, pool, rowOf, func(c *Card) string {
		if c.Placed() && c.Col == Merging {
			return "merging: return it first: nova-sprint return " + c.ID
		}
		if AtRedealBound(s, c) != nil {
			return ""
		}
		return inState(c, Review)
	}, s.primaryCard)
	up := s.UpMembers()
	// the room of each member is its width (width.go, errata 3 amendment 9)
	q, room := memberLoads(s, up), memberWidths(s, up)
	rr := dealRound(s)
	moves := roundMoves{}
	orphans := map[string]bool{}
	for _, c := range chosen {
		// A primary the rework refuses stays in review: the judgment it needs
		// is written, if it has none.
		stays := func() {
			if j, ok := reviewJudgment(s, c, reviewStep{who: r.Who}); ok {
				p.Notes = append(p.Notes, j)
			}
		}
		fix := r.Fix
		if fix == "" {
			if fix = cutText(ownFix(s, c), MaxCardTextBytes); fix == "" {
				p.refuse(c.ID, "no --fix, and no finding of a broken read or report of failed work to take as its fix; give --fix <text>")
				stays()
				continue
			}
		}
		attempt := c.Int("attempt")
		var asked []string
		broken := 0
		var retire []Change
		if wc := AtRedealBound(s, c); wc != nil {
			retire = append(retire, change(Fleet, removeEntry(wc, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Int("attempt") == attempt && !contains(asked, rc.F("reader")) {
				asked = append(asked, rc.F("reader"))
			}
			if rc.Col == Broken {
				broken++
			}
			retire = append(retire, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		// the readers kept are the pair the primary was asked of: an extra
		// reader of ask --another is for its attempt only
		askedField := c.F("asked")
		if askedField == "" && len(asked) > 0 {
			askedField = strings.Join(orderLike(s.Readers.Rows(), asked, ""), ",")
		}
		// the finding and why ride on the primary too: a rework with no member up deals later
		// (start), from the primary, and its child is told all the same
		given := reworkGiven(s, c)
		set := map[string]string{"fix": fix, "finding": given["finding"], "why": given["why"],
			"reworks": itoa(c.Int("reworks") + 1), "broken_reads": itoa(c.Int("broken_reads") + broken)}
		if askedField != "" {
			set["asked"] = askedField
		}
		var u Unit
		// the next member round the fleet with room (width.go; tla/DirtyTick.tla, WidthRespected):
		// none up, or none below its width, and the primary waits ready for the tick's deal
		m := ""
		if len(up) > 0 {
			m = rr.next(up, q, room, reworkAvoid(s, c), false)
		}
		if m != "" {
			var why string
			u, why = deal(s, c, fix, m, q, set, given, "readers")
			if why != "" {
				p.refuse(c.ID, why)
				stays()
				continue
			}
			rr.moved(m)
			moves[c.ID] = m
			u.Changes = append(retire, u.Changes...)
			u.Moved = strings.Replace(u.Moved, " review -> working", " review -> working (rework)", 1)
		} else {
			later := "no fleet member is up: start delegates it"
			if len(up) > 0 {
				later = "no fleet member has room: the tick deals it when one has"
			}
			u = Unit{Key: c.ID, Stream: c.Row, Changes: append(retire, change(Work, moveEntry(c, c.Row, Ready, set, "result", "readers"))),
				Moved: c.ID + " review -> ready (rework; " + later + ")"}
		}
		u.Moved += fmt.Sprintf("; %d read cards retired", len(retire))
		if m := orphanMerge(s, c); m != nil {
			u.Changes = append(u.Changes, change(Merge, moveEntry(m, c.Row, Returned, nil, "need_card", "need_stream")))
			u.Moved += "; its orphan merge card off " + m.Col
			orphans[c.ID] = true
		}
		u.Closes = closesFor(s.Open, ReworkResolves, c.ID)
		p.Units = append(p.Units, u)
	}
	settle(&p, s, r.Who, orphans, nil)
	answered(&p, s, r.Answers, r.Who)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	return p
}

// reworkAvoid is the member a rework's next attempt avoids: the member of the
// work card of the attempt it sends back (the one that failed it, or held it
// when a read found it broken), "" when there is none.
func reworkAvoid(s *Snapshot, c *Card) string {
	wc := s.Fleet.Card(WorkCardID(c.ID, c.Int("attempt")))
	if wc == nil {
		return ""
	}
	if m := wc.F("member"); m != "" {
		return m
	}
	return wc.Row
}

// orphanMerge is the merge card of a primary in review that is still queued
// or stuck (a repair skipped accept's work entry): rework and return take it
// off in the same step, into returned, so an accept later moves it back.
func orphanMerge(s *Snapshot, c *Card) *Card {
	if s.Merge == nil || !c.Placed() || c.Col != Review {
		return nil
	}
	if m := s.Merge.Placed(c.ID); m != nil && (m.Col == Queued || m.Col == Stuck) {
		return m
	}
	return nil
}

// ownFix is a primary's own fix, for a rework given none: the findings of its
// broken reads at its attempt, else the report of its failed work; "" when it
// has neither.
func ownFix(s *Snapshot, c *Card) string {
	if found := brokenFindings(s, c); found != "" {
		return found
	}
	if c.F("result") == "failed" {
		if wc := s.Fleet.Card(c.F("work")); wc != nil {
			return wc.F("report")
		}
	}
	return ""
}

// brokenFindings is the findings of the primary's broken reads at its attempt,
// each once, joined; "" when no read of it is broken.
func brokenFindings(s *Snapshot, c *Card) string {
	var found []string
	for _, rc := range s.Readers.Of(c.ID) {
		if rc.Col == Broken && rc.Int("attempt") == c.Int("attempt") && rc.F("finding") != "" && !contains(found, rc.F("finding")) {
			found = append(found, rc.F("finding"))
		}
	}
	return strings.Join(found, "; ")
}

// MaxCardTextBytes bounds each text field a card carries, the brief excepted (the store
// refuses a step that would write a longer one); a rework's own derived words (the finding,
// why and the fix it takes from a finding) are cut to it, never refused for it.
const MaxCardTextBytes = 8 << 10

// cutText is s cut to at most n bytes, at a rune boundary, ending "..." when it was cut.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("...")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// reworkGiven is what a rework writes on the next attempt's work card besides
// the fix, so the child learns why it exists (docs/SPEC-SPRINT.md, rework):
// finding, the broken reads' words, and why, how the attempt before ended.
func reworkGiven(s *Snapshot, c *Card) map[string]string {
	attempt := c.Int("attempt")
	finding := cutText(brokenFindings(s, c), MaxCardTextBytes)
	why := fmt.Sprintf("attempt %d was sent back by the coordinator", attempt)
	switch wc := s.Fleet.Card(c.F("work")); {
	case c.F("result") == "failed" && wc != nil && strings.TrimSpace(wc.F("report")) != "":
		why = fmt.Sprintf("attempt %d failed: %s", attempt, strings.TrimSpace(wc.F("report")))
	case c.F("result") == "failed":
		why = fmt.Sprintf("attempt %d failed", attempt)
	case finding != "":
		why = fmt.Sprintf("attempt %d finished and a reader found it broken", attempt)
	}
	return map[string]string{"finding": finding, "why": cutText(why, MaxCardTextBytes)}
}

// ReturnReq is the coordinator sending merging primaries back to review.
type ReturnReq struct {
	Sel
	Reason  string
	Answers []string
	Who     string
}

// ReturnResolves is the judgments a return is a decision for: a red CI on the
// primary is discharged; a stream's red or rejected batch is answered and stays
// open while the stream is stopped.
var ReturnResolves = []string{NCIRed, NRed, NRejected, NRepairSkipped, NStalled}

func answeredIn(notes []Note, id string) bool {
	for _, n := range notes {
		if n.Kind == Decided && n.Answers == id {
			return true
		}
	}
	return false
}

// Return moves merging -> review: off the merge queue (or stuck), into the
// merge table's hidden returned column, so a later accept moves it back. The
// primary is marked returned at its attempt (FieldReturnedAttempt): the
// coordinator decides it, by accept, rework or drop, and the pump accepts it
// again only at a new attempt with its own two reads.
func Return(s *Snapshot, r ReturnReq) Plan {
	var p Plan
	chosen := pick(&p, r.Sel, s.Work.Column(Merging), rowOf, func(c *Card) string {
		if orphanMerge(s, c) != nil {
			return ""
		}
		if why := inState(c, Merging); why != "" {
			return why
		}
		if m := s.Merge.Placed(c.ID); m != nil && m.Col != Queued && m.Col != Stuck {
			return "not queued or stuck in merge (it is " + placeWord(m) + ")"
		}
		return "" // queued, stuck, or no merge card at all: return takes it back
	}, s.primaryCard)
	leaving := map[string]bool{}
	for _, c := range chosen {
		m := s.Merge.Placed(c.ID)
		if orphanMerge(s, c) != nil {
			leaving[c.ID] = true
			// returned at its attempt, as any return: the tick does not take it
			// back on its standing reads (AcceptHeld)
			set := map[string]string{FieldReturnedAttempt: itoa(c.Int("attempt"))}
			u := Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Merge, moveEntry(m, c.Row, Returned, nil, "need_card", "need_stream")),
				change(Work, setEntry(c, set))},
				Closes: closesFor(s.Open, ReturnResolves, c.ID), Moved: fmt.Sprintf("%s review: its orphan merge card off %s", c.ID, m.Col)}
			if j, ok := reviewJudgment(s, inReview(c, set), reviewStep{closing: noteIDs(u.Closes), who: r.Who}); ok {
				u.Notes = append(u.Notes, j)
			}
			p.Units = append(p.Units, u)
			continue
		}
		// marked returned at its attempt: the pump does not accept it again on
		// the reads that stand (AcceptHeld); the returned judgment decides it
		set := map[string]string{"returns": itoa(c.Int("returns") + 1), FieldReturnedAttempt: itoa(c.Int("attempt"))}
		if r.Reason != "" {
			set["return_reason"] = r.Reason
		}
		u := Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, moveEntry(c, c.Row, Review, set))},
			Closes: closesFor(s.Open, ReturnResolves, c.ID)}
		if m != nil {
			u.Changes = append([]Change{change(Merge, moveEntry(m, c.Row, Returned, nil, "need_card", "need_stream"))}, u.Changes...)
			u.Moved = fmt.Sprintf("%s merging -> review (off merge %s)", c.ID, m.Col)
		} else {
			// merging with no merge card (a repair skipped its create): back to
			// review, where the coordinator decides again
			u.Moved = fmt.Sprintf("%s merging -> review (it had no merge card)", c.ID)
		}
		// The stream's red or rejected judgment names return as a decision: the
		// answer is recorded; the judgment stays open while the stream is stopped.
		answerListed(&u, s.Open, r.Answers, "return", c.Row, strings.TrimSpace("returned "+c.ID+"; "+r.Reason), r.Who, s.Now, c.ID)
		// Back in review, the coordinator decides again.
		j := judgment(NReturned, c.Row, s.Now, c.Int("returns"), c.ID)
		j.Who, j.Attempt, j.What = r.Who, c.Int("attempt"), r.Reason
		if len(okReaders(s, c)) < 2 {
			j.Decisions = removeDecision(j.Decisions, "accept")
		}
		u.Notes = append(u.Notes, j)
		// Back in review with ok reads from two different readers at its
		// head, the returned judgment offers accept: the one judgment it
		// needs; the call below writes nothing more then.
		if ra, ok := reviewJudgment(s, inReview(c, set), reviewStep{closing: noteIDs(u.Closes), writes: u.Notes, who: r.Who}); ok {
			u.Notes = append(u.Notes, ra)
		}
		leaving[c.ID] = true
		p.Units = append(p.Units, u)
	}
	settle(&p, s, r.Who, leaving, nil)
	answered(&p, s, r.Answers, r.Who)
	return Lawful(p)
}

func removeDecision(ds []string, d string) []string {
	var out []string
	for _, x := range ds {
		if x != d {
			out = append(out, x)
		}
	}
	return out
}

func orEmpty(c *Card, id string) *Card {
	if c == nil {
		return &Card{ID: id, Fields: map[string]string{"outcome": "none"}}
	}
	return c
}

// DropReq is the coordinator taking primaries off the table.
type DropReq struct {
	Sel
	Reason  string
	Answers []string
	Who     string
}

// Drop takes open primaries off the table with the reason: their record,
// outcome and reason are kept; their live work card, unread read cards and
// merge place go with them. Waiting primaries that need one are blocked, and
// the coordinator is told.
func Drop(s *Snapshot, r DropReq) Plan {
	var p Plan
	var all []*Card
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		all = append(all, s.Work.Column(st)...)
	}
	chosen := pick(&p, r.Sel, all, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table (" + orDash(c.F("outcome")) + ")"
		}
		if !IsOpen(c.Col) {
			return "landed; landed is final"
		}
		return ""
	}, s.primaryCard)
	dropping, blocked := map[string]bool{}, map[string]bool{}
	for _, c := range chosen {
		dropping[c.ID] = true
	}
	for _, c := range chosen {
		u := Unit{Key: c.ID, Stream: c.Row}
		for _, fc := range s.Fleet.Of(c.ID) {
			if fc.Col == Ready || fc.Col == Working || fc.Col == Withdrawn {
				u.Changes = append(u.Changes, change(Fleet, removeEntry(fc, map[string]string{"dropped": stamp(s.Now)})))
			}
		}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Asked || rc.Col == Reading {
				u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"dropped": stamp(s.Now)})))
			}
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			u.Changes = append(u.Changes, change(Merge, removeEntry(m, map[string]string{"dropped": stamp(s.Now)})))
		}
		u.Changes = append(u.Changes, change(Work, removeEntry(c, map[string]string{
			"outcome": "dropped", "reason": r.Reason, "dropped_from": c.Col, "dropped_at": stamp(s.Now)})))
		for _, w := range s.Work.Column(Waiting) {
			if dropping[w.ID] || blocked[w.ID] || !contains(Split(w.F("needs")), c.ID) {
				continue
			}
			// One note per waiting primary, naming every need this step drops
			// that no blocked judgment open on it names yet.
			blocked[w.ID] = true
			var gone []string
			for _, need := range Split(w.F("needs")) {
				if dropping[need] {
					gone = append(gone, need)
				}
			}
			if gone = unblocked(s.Open, w.ID, gone, NBlocked); len(gone) > 0 {
				u.Notes = append(u.Notes, blockedNote(s, w.Row, w.ID, r.Who, gone))
			}
		}
		u.Closes = closesFor(s.Open, nil, c.ID)
		answerListed(&u, s.Open, r.Answers, "drop", c.Row, "dropped "+c.ID+"; "+r.Reason, r.Who, s.Now, c.ID)
		u.Moved = fmt.Sprintf("%s %s -> off the table (%s)", c.ID, c.Col, r.Reason)
		p.Units = append(p.Units, u)
	}
	settle(&p, s, r.Who, dropping, dropping)
	// Each stream counts its dropped primaries on its control card.
	for _, st := range unitStreams(p) {
		k := 0
		for _, u := range p.Units {
			if u.Stream == st && dropping[u.Key] {
				k++
			}
		}
		setStream(&p, s, st, map[string]string{"dropped": itoa(s.StreamCtl(st).Int("dropped") + k)})
	}
	// A sprint this drop finishes is found done by the tick's done part
	// (TickDone), which says so and stops the machine.
	answered(&p, s, r.Answers, r.Who)
	return Lawful(p)
}

// RankReq is the coordinator changing scores.
type RankReq struct {
	IDs     []string
	Score   *float64 // the new score of the first; the rest follow it
	First   bool     // ahead of every primary of its stream
	Answers []string
	Who     string
	Only    []string
}

// Rank changes a primary's score, and every copy of it: its placed work
// cards, read cards and merge place. Only rank changes a score. A landed
// primary is final and is not ranked.
func Rank(s *Snapshot, r RankReq) Plan {
	var p Plan
	chosen := pick(&p, Sel{IDs: r.IDs, Only: r.Only}, nil, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table"
		}
		if c.Col == Landed {
			return "landed; landed is final"
		}
		return ""
	}, s.primaryCard)
	score := 0.0
	if r.Score != nil {
		score = *r.Score
	} else if r.First {
		low := 0.0
		first := true
		for _, c := range s.Work.Cards() {
			if c.Placed() && (first || c.Score < low) {
				low, first = c.Score, false
			}
		}
		score = low - float64(len(chosen))
	}
	for _, c := range chosen {
		if c.Score == score {
			p.refuse(c.ID, "already at score "+fmtScore(score))
			score++
			continue
		}
		u := Unit{Key: c.ID, Stream: c.Row}
		for _, fc := range s.Fleet.Of(c.ID) {
			u.Changes = append(u.Changes, change(Fleet, scoreEntry(fc, score)))
		}
		for _, rc := range s.Readers.Of(c.ID) {
			u.Changes = append(u.Changes, change(Readers, scoreEntry(rc, score)))
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			u.Changes = append(u.Changes, change(Merge, scoreEntry(m, score)))
		}
		u.Changes = append(u.Changes, change(Work, scoreEntry(c, score)))
		answerListed(&u, s.Open, r.Answers, "rank", c.Row, "ranked "+c.ID+" "+fmtScore(score), r.Who, s.Now, c.ID)
		for _, o := range s.Open { // a cross stop names the card of another stream: rank answers it there
			if o.Note.StreamLevel && contains(o.Note.Primaries, c.ID) && o.Note.Stream != c.Row {
				answerListed(&u, s.Open, r.Answers, "rank", o.Note.Stream, "ranked "+c.ID+" "+fmtScore(score), r.Who, s.Now, c.ID)
			}
		}
		u.Moved = fmt.Sprintf("%s score %s -> %s (%d copies)", c.ID, fmtScore(c.Score), fmtScore(score), len(u.Changes)-1)
		p.Units = append(p.Units, u)
		score++
	}
	answered(&p, s, r.Answers, r.Who)
	return p
}

// CIReq records a CI observation for primaries, in any state.
type CIReq struct {
	Sel
	Red    bool
	Head   string // the head the run tested; "" is the primary's current head
	Run    string // the run's identity: a retried report of one run is recorded once
	Source string
	Note   string
	Who    string
}

// FieldCIRunStatus is the status of the last CI run reported on a primary,
// for its current head or another.
const FieldCIRunStatus = "ci_run_status"

// runStatus is the status of the primary's last reported run: its own field,
// or, on a record written before it, ci.
func runStatus(c *Card) string {
	if v := c.F(FieldCIRunStatus); v != "" {
		return v
	}
	return c.F("ci")
}

// RecordCI records the observation on the primary: status, head, run and
// source. It always notifies (red is a judgment, green happened), and moves
// no card. A result for a head that is not the primary's current one is
// labelled as such; a report of a run already recorded with the same status
// is refused as a repeat and writes nothing. Green on the current head
// discharges a red judgment on the primary.
func RecordCI(s *Snapshot, r CIReq) Plan {
	var p Plan
	var all []*Card
	for _, st := range States {
		all = append(all, s.Work.Column(st)...)
	}
	result := "green"
	if r.Red {
		result = "red"
	}
	chosen := pick(&p, r.Sel, all, rowOf, func(c *Card) string {
		if !c.Placed() {
			return "not on the table"
		}
		if r.Run != "" && c.F("ci_run") == r.Run && runStatus(c) == result {
			return "run " + r.Run + " is recorded already (" + result + ")"
		}
		return ""
	}, s.primaryCard)
	for _, c := range chosen {
		head := r.Head
		if head == "" {
			head = c.F("head")
		}
		old := head != c.F("head")
		// ci and ci_head are the last result for the primary's current head,
		// which the tick's accept reads (CIRedAtHead): a result for another
		// head is labelled and notified and leaves them as they are; ci_run
		// and ci_run_status are the last run reported, whatever its head, so
		// a retried report of it is recorded once (tla/DirtyTick.tla
		// "ciold")
		set := map[string]string{FieldCIRunStatus: result}
		if !old {
			set["ci"], set["ci_at"], set["ci_head"] = result, stamp(s.Now), head
		}
		for k, v := range map[string]string{"ci_run": r.Run, "ci_source": r.Source, "ci_note": r.Note} {
			if v != "" {
				set[k] = v
			}
		}
		what := r.Note
		if r.Run != "" {
			what = strings.TrimSpace("run " + r.Run + ": " + r.Note)
		}
		if old {
			what = strings.TrimSpace("for an old head " + head + " (current " + orDash(c.F("head")) + "); " + what)
		}
		var n Note
		if r.Red {
			n = judgment(NCIRed, c.Row, s.Now, 0, c.ID)
		} else {
			n = happened(NCIGreen, c.Row, s.Now, c.ID)
		}
		n.What, n.Who, n.Attempt = what, r.Who, c.Int("attempt")
		u := Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, set))}, Notes: []Note{n},
			Moved: fmt.Sprintf("%s ci %s (%s)", c.ID, result, c.Col)}
		if old {
			u.Moved += " for an old head"
		}
		if r.Red && !old {
			// a second red takes the open one's place, with this run's text
			u.Closes = closesFor(s.Open, []string{NCIRed}, c.ID)
		}
		if !r.Red && !old {
			u.Closes = closesFor(s.Open, []string{NCIRed}, c.ID)
			if len(u.Closes) > 0 {
				if j, ok := reviewJudgment(s, c, reviewStep{closing: noteIDs(u.Closes), who: r.Who}); ok {
					u.Notes = append(u.Notes, j)
				}
			}
		}
		p.Units = append(p.Units, u)
	}
	return p
}

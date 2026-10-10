package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// The steps of review: ask and read (mechanical, and the readers' own), and
// the coordinator's verbs accept, rework, return, drop, rank and ci.

// AskReq deals primaries in review to readers.
type AskReq struct {
	Sel
	Another bool // one more reader for a primary already asked
	Answers []string
	Who     string
	// Instead is the reader whose live read of the one primary named is taken
	// back (retired_by coordinator) and asked of one other reader in the same
	// step, chosen and routed as Another's (ask --instead)
	Instead string `json:",omitempty"`
}

// RetiredByCoordinator is a read card's retired_by when the coordinator took
// the read back and asked another reader instead (ask --instead).
const RetiredByCoordinator = "coordinator"

// A decide read's fields on its read card: the sprint row's two bars on p(defect) it is
// routed by (docs/SPEC-SPRINT.md section 6, the decide read; internal/decide, Bars).
const (
	FieldDecideBounce = "decide_bounce"
	FieldDecideReview = "decide_review"
)

// A work card's gate decision fields: the sprint row's two bars on a failed gate's
// decisions (docs/SPEC-SPRINT.md section 5, the gate verdict; internal/decide, GateBars).
const (
	FieldDecideGateFlaky       = "decide_gate_flaky"
	FieldDecideGatePreexisting = "decide_gate_preexisting"
)

// gateFields is the bars every deal of a work card carries, at the deal (a redeal takes
// the row's bars then): the sprint row's that are set. A bar left out is unset: its member's
// native still records and shows each gate decision, and takes no route on that bar.
func (s *Snapshot) gateFields() map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{FieldDecideGateFlaky: s.DecideGateFlaky, FieldDecideGatePreexisting: s.DecideGatePreexisting} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// decideFields is the bars a read of pr carries when it is a decide read: the first read
// the ask places at pr's attempt (first: not --another or --instead, and no read that
// stays at the attempt is one), drawn on flash (readTierOf), with both bars set in the
// sprint row (the owner, 2026-10-02: "i'd really like to start using jev to do cheap
// reads/evals/scoring of work"). nil otherwise: a pro card's reads are strings reads.
func (s *Snapshot) decideFields(pr *Card, first bool) map[string]string {
	if !first || s.DecideBounce == "" || s.DecideReview == "" || s.readTierOf(pr) != cardhdr.RouteFlash {
		return nil
	}
	return map[string]string{FieldDecideBounce: s.DecideBounce, FieldDecideReview: s.DecideReview}
}

// readsAt is the primary's placed read cards at an attempt, in reader row order.
func readsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	if !anyReadCardAt(s, pr, attempt) {
		return nil
	}
	var out []*Card
	for _, r := range s.Readers.Rows() {
		for _, id := range ReadCardIDs(pr.ID, attempt, r) {
			if c := s.Readers.Placed(id); c != nil {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Ask deals every primary in review that wants a read (ReadsWanted) to as many
// different readers up as it wants now, in work order. A card's reads are
// asked together (the interim rule of 2026-10-06, ReadsWanted): the rest it needs
// (ReadsNeeded: one for a flash card, two for a pro card; readers.go), none once
// a read found it broken. Each read goes to a reader with room (askPicks): the finder first on a
// rework's next attempt (finderFirst: the reader whose finding the fix answers,
// when it is free and has room), and every other read to the reader with the
// greatest share of room, its free room as a part of its width (readerRooms,
// round.pickByRoom), that has no read card at the attempt, ties round the
// readers (round.go: from the rolling index, wrapping, the index moved past
// it: the readers table's ask_index, written with the ask; the finder's read
// is out of turn and moves it not at all); a reader at width is given
// nothing, and a read no reader has room for waits (TickAsk). Reworked work
// is asked by the same room: a read is a fresh child on a freshly drawn route,
// so the readers of an earlier attempt are not preferred, the finder's first
// read aside. A read its reader handed back with no verdict is not a read: it
// is asked of a reader free at the attempt, or of the same reader again when
// none is (tla/DirtyTick.tla, JudgedOnlyAfterTheBound). With Another, a
// primary already asked is dealt to
// one more reader, the next round the readers.
func Ask(s *Snapshot, r AskReq) Plan {
	var p Plan
	// in stream turns from the ask's stream index on the work table
	// (streamTurns), so a limit asks of every stream alike, and the index moves
	// past the stream of the last primary asked
	srr := askStreamRound(s)
	if r.Instead != "" && (r.Another || len(r.IDs) != 1 || r.Only != nil || r.Stream != "" || r.Limit != 0) {
		p.refuse("ask", "--instead takes back one read of one primary and asks one other reader: ask <primary> --instead <reader>, with no --another, --group, --stream or --limit")
		return p
	}
	// one more reader, not the pair: --another, or --instead's one other reader
	another := r.Another || r.Instead != ""
	eligible := func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if c.F("result") == "failed" {
			return "its work came back failed: rework or drop it"
		}
		if r.Instead != "" {
			return insteadHeld(s, c, r.Instead)
		}
		asked := len(readsAt(s, c, c.Int("attempt")))
		if another && asked == 0 {
			return "not asked yet at attempt " + itoa(c.Int("attempt")) + ": the machine's tick asks it, or run: nova-sprint ask " + c.ID + "; --another adds a reader to one already asked"
		}
		if !another && ReadsWanted(s, c) == 0 {
			if len(liveReadsAt(s, c, c.Int("attempt"))) < ReadsNeededIn(s, c) {
				return "asked already: its reads are out, or one found it broken"
			}
			return "asked already"
		}
		return ""
	}
	chosen := pick(&p, r.Sel, eligibleTurns(s.Work.Column(Review), eligible, srr), rowOf, eligible, s.primaryCard)
	rr := askRound(s)
	// a read goes to the reader with the greatest share of room, its free room
	// as a part of its width (readerRooms), the reads this ask places taken
	// off as it goes; a reader at width is given nothing
	room := s.readerRooms(s.Readers.Rows())
	moves := roundMoves{}
	// a read card's route is drawn as a work card's is, from its primary's tier
	// at that tier's rolling index on the fleet table (route.go, readRouteOf;
	// tla/RouteIndex.tla, THE READS); a step that read no fleet table or no
	// route asks with none
	var ri routeIndexes
	if s.Fleet != nil && len(s.Routes) > 0 {
		ri = routeIndexesOf(s)
	}
	// the reader who found the defect checks the fix: the finders first, their reads
	// taken off their room before any other read of the step (askFinders)
	finders := s.askFinders(chosen, another, room)
	for _, c := range chosen {
		attempt := c.Int("attempt")
		var all []string
		var takenBack []Change
		var away []string
		var returned, kept []*Card
		instead := ""
		for _, rc := range readsAt(s, c, attempt) {
			if rc.F("reader") == r.Instead {
				// the coordinator takes it back: asked of another reader below
				takenBack = append(takenBack, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByCoordinator})))
				instead = rc.F("reader")
				continue
			}
			if awayRead(s, rc) {
				// asked of a reader that is not up: taken back, and asked again below
				takenBack = append(takenBack, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "away"})))
				away = append(away, rc.F("reader"))
				continue
			}
			if !another && returnedRead(rc) {
				// handed back with no verdict: placed again below
				returned = append(returned, rc)
				continue
			}
			all = append(all, rc.F("reader"))
			kept = append(kept, rc)
		}
		// a friend's ok read of the attempt, or her read outstanding (reads are asked
		// together, the interim rule: ReadsWanted counts it), stands with the machine's,
		// so the next read is one, not the whole pair again (friendReadLive;
		// docs/SPEC-SPRINT.md, a read asked of any unit with room at or above
		// the read tier). It is not taken back: it is not on the readers table.
		if placed, oks, _ := friendReadLive(s, c); len(placed)+len(oks) > 0 {
			for _, rc := range append(placed, oks...) {
				rd := rc.F("reader")
				if rd == "" || contains(all, rd) {
					continue
				}
				all = append(all, rd)
				kept = append(kept, rc)
			}
		}
		free := s.freeReaders(c, attempt)
		// the reads that stand, kept, say how many are asked now (readsWantedOf): the
		// rest of those it needs, together, none once one found it broken; a read handed back, or taken
		// back from a reader away, is not a read and is asked again whatever stands: it
		// was wanted when it was placed (ReadsWanted)
		want := max(readsWantedOf(s, c, kept), len(returned)+len(away))
		if another {
			want = 1
		}
		if !another && len(all)+len(free)+len(returned) < ReadsNeededIn(s, c) {
			// not even its first read is asked when no reader could ever read the rest
			want = ReadsNeededIn(s, c) - len(all)
		}
		// each read to a free reader with room, the finder's first (askPicks)
		finder := finders[c.ID]
		chosenReaders := askPicks(rr, finder, want, free, room)
		// A return is not a read (tla/DirtyTick.tla, PlaceReads and
		// JudgedOnlyAfterTheBound): a read handed back goes to a free
		// reader when there is one, its card retired; when none is free its
		// own reader is asked it again, in place, the round not moved and no
		// bound of the primary spent (ReasksBounded: Read counts each return
		// in reasked and retires the one past MaxReadReasks, and the refusal
		// below is then the "cannot ask" judgment). Every read the unit asks
		// draws its route leaving out the routes the primary's returned reads
		// ran on while the tier has another route, so a read is not stuck with
		// the one route left, and draws only once the unit is kept.
		var again, retiredFrom, failed []string
		var inPlace []*Card
		for _, rc := range returned {
			failed = append(failed, rc.F(FieldRoute))
			// in place only on a reader of the card's tier. A reader outside it
			// is not asked the read again; the card is taken back (retired_by
			// returned) and the read goes to a reader who reads the tier.
			if len(chosenReaders)+len(again) < want && s.readerServesTier(rc.F("reader"), s.readTierOf(c)) {
				inPlace = append(inPlace, rc)
				again = append(again, rc.F("reader"))
				continue
			}
			takenBack = append(takenBack, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "returned"})))
			retiredFrom = append(retiredFrom, rc.F("reader"))
		}
		if len(chosenReaders)+len(again) < want {
			for _, rd := range chosenReaders {
				room[rd] = room[rd].after(-1) // a primary refused takes no room from the next
			}
			full := 0
			for _, rd := range free {
				if room[rd].free <= 0 {
					full++
				}
			}
			p.refuse(c.ID, cannotAskWhy(s, c, attempt, want, len(chosenReaders)+len(again), full))
			continue
		}
		// the first read of a flash card's attempt is a decide read (decideFields): one
		// placed again keeps its bars, and a new one is it while no read that stays is
		decided := false
		for _, rc := range slices.Concat(kept, inPlace) {
			decided = decided || rc.F(FieldDecideBounce) != ""
		}
		for _, rc := range inPlace {
			set := map[string]string{"asked": stamp(s.Now)}
			maps.Copy(set, s.readRouteOf(ri, c, failed))
			takenBack = append(takenBack, change(Readers, setEntry(rc, set, FieldReturned)))
		}
		u := Unit{Key: c.ID, Stream: c.Row, Changes: takenBack}
		for _, rd := range chosenReaders {
			if rd == finder {
				continue
			}
			rr.moved(rd)
			moves[c.ID] = joinMoves(moves[c.ID], rd)
		}
		for i, rd := range chosenReaders {
			fields := map[string]string{"kind": "read", "primary": c.ID, "stream": c.Row, "reader": rd, "attempt": itoa(attempt), "head": c.F("head"), "asked": stamp(s.Now)}
			priorityOnRead(fields, c) // its primary's level when above reader (priority.go)
			if rd == finder {
				fields[FieldFinderRead] = "1" // placed on purpose: the level leaves it where it is
			}
			maps.Copy(fields, s.readRouteOf(ri, c, failed))
			maps.Copy(fields, s.decideFields(c, !another && !decided && i == 0))
			cardID, _ := ReadCardForAsk(s, c.ID, attempt, rd)
			u.Changes = append(u.Changes, change(Readers, createEntry(cardID, rd, Asked, c.Score, fields)))
		}
		all = append(append(all, chosenReaders...), again...)
		if pair := strings.Join(all, ","); !another && pair != c.F("asked") {
			// the primary's asked field names the readers of its attempt;
			// --another's reader is one more, not one of the two
			u.Changes = append(u.Changes, change(Work, setEntry(c, map[string]string{"asked": pair})))
		}
		named := append([]string{}, chosenReaders...)
		if finder != "" {
			named[0] = finder + " (who found attempt " + c.F(FieldFindingAttempt) + " broken: it checks the fix)"
		}
		for _, rd := range again {
			named = append(named, rd+" (again, its read returned)")
		}
		u.Moved = c.ID + " asked of " + strings.Join(named, ", ")
		if len(away) > 0 {
			u.Moved += "; its read taken back from " + strings.Join(away, ", ") + " (not up)"
		}
		if len(retiredFrom) > 0 {
			u.Moved += "; its returned read taken back from " + strings.Join(retiredFrom, ", ")
		}
		if instead != "" {
			u.Moved += "; its read taken back from " + instead + " (instead)"
		}
		if another {
			u.Closes = closesFor(s.Open, []string{NReadBroken, NBriefWrong, NReadsExhausted, NStranded, NStalled}, c.ID)
		} else {
			u.Closes = closesFor(s.Open, []string{NStranded, NStalled}, c.ID)
		}
		asked := map[string]string{}
		for _, rd := range chosenReaders {
			cardID, _ := ReadCardForAsk(s, c.ID, attempt, rd)
			asked[cardID] = Asked
		}
		for _, rc := range inPlace {
			asked[rc.ID] = Asked
		}
		if j, ok := reviewJudgment(s, c, reviewStep{moved: asked, closing: noteIDs(u.Closes), who: r.Who}); ok {
			u.Notes = append(u.Notes, j)
		}
		// --instead of a reading card, and only that: an away take-back is still
		// asked, and an unbegun ask adds no cost (retiredReadCosts)
		u.Changes = retiredReadCosts(s, u.Changes)
		p.Units = append(p.Units, u)
	}
	roundWrites(&p, rr, moves)
	if ri != nil {
		ri.write(&p)
	}
	if !another {
		// one more reader of a primary named is not a turn round the streams:
		// the ask's stream index moves with the asks of the streams' cards
		// (the reference model's AskAnother moves no
		// stream index)
		streamIndexWrite(&p, srr, s.Work.Placed)
	}
	answered(&p, s, r.Answers, r.Who)
	return p
}

// insteadHeld says why the reader's read of the primary at its attempt cannot be
// taken back by ask --instead ("" when it is live: asked or reading).
func insteadHeld(s *Snapshot, pr *Card, rd string) string {
	attempt := pr.Int("attempt")
	ids := ReadCardIDs(pr.ID, attempt, rd)
	for _, id := range ids {
		if rc := s.Readers.Card(id); rc != nil && rc.Placed() && (rc.Col == Asked || rc.Col == Reading) {
			return ""
		}
	}
	var lastCard *Card
	for i := len(ids) - 1; i >= 0; i-- {
		if c := s.Readers.Card(ids[i]); c != nil {
			lastCard = c
			break
		}
	}
	at := " of " + pr.ID + " at attempt " + itoa(attempt)
	switch {
	case lastCard == nil:
		return rd + " holds no read" + at + "; run: nova-sprint card " + pr.ID + " for its readers, or nova-sprint ask " + pr.ID + " --another to add one"
	case !lastCard.Placed():
		return rd + "'s read" + at + " was retired at " + lastCard.F("retired") + " by " + orDash(lastCard.F("retired_by")) + ": nothing to take back; run: nova-sprint ask " + pr.ID + " --another to add a reader"
	case lastCard.Col != Asked && lastCard.Col != Reading:
		return rd + "'s read" + at + " is finished (" + lastCard.Col + "), not asked or reading: nothing to take back; run: nova-sprint ask " + pr.ID + " --another to add a reader"
	}
	return ""
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
	Gens    map[string]int // named begin and post-STOP verdicts guard the live read lease
	Begin   bool           // asked -> reading
	Verdict string         // ok or broken
	Finding string
	Who     string
	// Missing is the server's check at the close of a broken read, by read card: the
	// branch the read named is not on origin (read_missing.go). Such a verdict is no
	// verdict: the read is retired and asked again, the card not reworked.
	Missing map[string]MissingBranch `json:",omitempty"`
	// Return hands the named read back, with the reason: no verdict, no
	// finding against the work, not a read; the tick asks it again.
	Return bool   `json:",omitempty"`
	Reason string `json:",omitempty"`
	// Usage is what the read spent, as the reader read it from its child
	// (cardcost.Usage): kept on the read card, timed and priced (cost.go).
	Usage string `json:",omitempty"`
}

// Read moves a reader's read cards: asked -> reading, or asked|reading -> ok|broken
// with the finding (a report on a card still asked is the begin and the report
// in one step, begun stamped with it), or, with Return, hands a read it holds
// back: back in asked on its row, stamped returned, the reason in one happened
// note, and the next tick's ask asks it of another reader up at the same
// attempt, or of the same reader when none is free (tla/DirtyTick.tla,
// ReadReturn, JudgedOnlyAfterTheBound). A broken read is a judgment; the last ok read a primary
// needs at its head leaves it to the tick, which accepts it (TickAccept): no
// judgment, no hand step. As may name several readers, comma separated: every
// card named is one of theirs, each read as its own reader's, in the one plan;
// a read by selection names one reader.
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
	// A broken read names its defect, or it is no read (docs/SPEC-CARD-CONTRACT.md
	// section 3): the one rule (typedrec.NamesADefect) the gh shim refuses the review by
	// and the member hands the read back by, consulted here too, at the record, so no path
	// writes a broken verdict and a judgment on nothing. Refused, the read stays the
	// reader's, in its column, to report with a finding or to hand back.
	if !r.Begin && !r.Return && r.Verdict == "broken" && !typedrec.NamesADefect(r.Finding) {
		p.refuse("read", "a broken read names its defect: a finding line naming the file (file:line), the line, or the card's STEP or RULE the work breaks, and what to change: read --as <reader> --broken <card> --finding <text>; a read with no verdict is handed back: read --as <reader> --return <card> --reason <text>")
		return p
	}
	// a read asked of a friend is on her fleet row, not the readers table
	// (friendReadAsk): the verb her packet prints closes it there
	for _, rd := range readers {
		name, friend := FriendOfRow(rd)
		member := !friend && s.Fleet != nil && s.Fleet.HasRow(rd) && (s.Readers == nil || !s.Readers.HasRow(rd))
		if !friend && !member {
			continue
		}
		if len(readers) > 1 {
			p.refuse("read", "a read card is reported on its own: read --as "+rd+" (--ok | --broken) <read>")
			return p
		}
		if member {
			name = rd
		}
		return readCardVerb(s, r, rd, name)
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
		// A cached queue packet names its read and generation. Once STOP
		// returns it to Asked at a new generation, that old packet cannot
		// begin the new lease. An unnamed selection reads the fresh table
		// state here and may still begin the live card without a packet.
		if r.Begin && (len(r.Gens) > 0 || len(sel.IDs) > 0 && c.F("stopped_from_gen") != "") {
			if why := liveGen("read --begin", c, r.Gens); why != "" {
				return why
			}
		}
		if !r.Begin && (len(r.Gens) > 0 || c.F("stopped_from_gen") != "") {
			if why := liveGen("read", c, r.Gens); why != "" {
				return why
			}
		}
		if !c.Placed() && c.F("retired") != "" {
			if c.F("retired_by") == "away" {
				return "retired at " + c.F("retired") + ": the reader was away; the read was asked of another reader"
			}
			if c.F("retired_by") == RetiredByLevel {
				return "retired at " + c.F("retired") + ": the tick's level asked the read of another reader"
			}
			if c.F("retired_by") == RetiredByCoordinator {
				return "retired at " + c.F("retired") + ": the coordinator took the read back and asked another reader instead"
			}
			if c.F("retired_by") == RetiredByRefused {
				return "retired at " + c.F("retired") + ": its reader refused to launch it (" + c.F(FieldRefused) + "); it is asked of another reader"
			}
			if c.F("retired_by") == "returned" {
				return "retired at " + c.F("retired") + ": the read was returned; it was asked of another reader, or judged"
			}
			return "retired at " + c.F("retired") + " by " + orDash(c.F("retired_by")) + ": the primary was sent back; its next attempt is read on a new card"
		}
		if !c.Placed() || !contains(readers, c.Row) {
			return "not " + r.As + "'s to read (it is " + placeWord(c) + ")"
		}
		if !contains(from, c.Col) {
			return "not " + strings.Join(from, " or ") + " (it is " + c.Col + ")"
		}
		if r.Return && returnedRead(c) {
			// a return is counted once: the card is back in asked since it, not begun
			return "returned already at " + c.F(FieldReturned) + " and not begun since: a return is counted once"
		}
		if !r.Begin && !r.Return {
			// a routed read's verdict is priced as work is, or it is no verdict (cost.go)
			return ReadUsageMissing(c, r.Usage, r.Verdict)
		}
		return ""
	}, s.Readers.Card)
	namePrimarysReads(&p, all)
	col := OK
	if r.Verdict == "broken" {
		col = Broken
	}
	// every report of the plan, by primary: two readers' reports of one
	// primary in the one step (several readers) are
	// judged together, the primary's judgment after the last of them
	moved := map[string]map[string]string{}
	last := map[string]int{}
	lastReader := map[string]string{}
	written := map[string][]Note{}
	broken := map[string]int{}
	// the producer's records of the reads that end (cost.go): one change of each
	// primary in the plan
	costs := map[string]map[string]string{}
	costUnit := map[string]int{}
	var verdicts []ReadVerdict // the ledger's (reads_window.go)
	record := func(pr *Card, con Consumer) {
		if !pr.Placed() {
			return
		}
		if costs[pr.ID] == nil {
			costs[pr.ID] = map[string]string{}
		}
		addConsumer(pr, costs[pr.ID], con)
		costUnit[pr.ID] = len(p.Units) // the read's unit, appended next
	}
	for _, c := range chosen {
		pr := s.Work.Card(c.F("primary"))
		if r.Begin {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Readers, moveEntry(c, c.Row, Reading, map[string]string{"begun": stamp(s.Now)}, FieldReturned))},
				Moved: c.ID + " asked -> reading"})
			continue
		}
		if r.Return {
			// A return is not a read (tla/DirtyTick.tla, handback and
			// JudgedOnlyAfterTheBound): the card goes back to asked on its
			// own row, stamped returned, so its reader is not counted as
			// having read the attempt; the next ask places it again (Ask) and
			// no bound of the primary is spent. Once it was asked again in
			// place MaxReadReasks times (ReasksBounded) the return is counted
			// as a read: the card is retired, and a primary no reader is left
			// to read is the ask's "cannot ask" judgment (StrandingIsJudged).
			// The return counts itself (FieldReasked), here, whatever the tick
			// does: the ask does not run while fewer than two readers are up
			n := happened(NReadReturned, c.F("stream"), s.Now, c.F("primary"))
			n.What = c.Row + " returned " + c.ID + ": " + r.Reason
			n.Who = r.Who
			// a read handed back still cost tokens and time: the run's own numbered
			// record (cost.go, FieldReadTake), so a later run of the same card keeps
			// it. Each return is counted here and the one past MaxReadReasks retires
			// the card, so it holds at most MaxReadReasks+1 of these records, far
			// under MaxTakes; the producer gets each run's record too, the retiring
			// one included (cost.go)
			returns := c.Int(FieldReasked) + 1
			run := nextTake(c, FieldReadTake)
			rec := readCostRecord(s, c, r.Usage, c.F("asked"), cmp.Or(c.F("begun"), stamp(s.Now)))
			if RefusedReturn(r.Reason) {
				// a read refused is not a read (RetiredByRefused): it never ran, so it counts
				// toward no bound and marks nothing against its reader; it is retired off the
				// reader and the ask asks it of another reader of its tier
				set := map[string]string{FieldReadTake + itoa(run): rec, "retired": stamp(s.Now), "retired_by": RetiredByRefused, FieldRefused: cutText(r.Reason, MaxCardTextBytes)}
				record(pr, readConsumer(s, c, run, "returned", rec))
				n.What += "; refused, not a read: asked of another reader of its tier"
				p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
					Changes: []Change{change(Readers, removeEntry(c, set))},
					Moved:   c.ID + " " + c.Col + " -> refused (retired: not a read, asked again elsewhere)", Notes: []Note{n}})
				continue
			}
			set := map[string]string{FieldReadTake + itoa(run): rec, FieldReasked: itoa(returns)}
			record(pr, readConsumer(s, c, run, "returned", rec))
			if returns > MaxReadReasks {
				n.What += fmt.Sprintf("; asked again of %s %d times, the read is retired", c.Row, MaxReadReasks)
				set["retired"], set["retired_by"] = stamp(s.Now), "returned"
				p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
					Changes: []Change{change(Readers, removeEntry(c, set))},
					Moved:   c.ID + " " + c.Col + " -> returned (retired: its re-asks are spent)", Notes: []Note{n}})
				continue
			}
			set[FieldReturned] = stamp(s.Now)
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
				Changes: []Change{change(Readers, moveEntry(c, c.Row, Asked, set, "begun"))},
				Moved:   c.ID + " " + c.Col + " -> asked (returned)", Notes: []Note{n}})
			continue
		}
		if m, ok := r.missingBranch(c); ok {
			// the machine's fault, not the card's: no verdict, asked again (read_missing.go)
			p.Units = append(p.Units, missingBranchUnit(s, Readers, c, pr, m, r.Finding, r.Usage, r.Who))
			continue
		}
		set := map[string]string{"verdict": r.Verdict, "read": stamp(s.Now)}
		if c.Col == Asked { // a report on a card never begun is the begin and the report in one step
			set["begun"] = stamp(s.Now)
		}
		if r.Finding != "" {
			set["finding"] = r.Finding
		}
		// what the read cost, timed and priced (cost.go): kept on the read card when the
		// reader reported it, and recorded on the primary in this step
		rec := readCostRecord(s, c, r.Usage, c.F("asked"), cmp.Or(c.F("begun"), stamp(s.Now)))
		if r.Usage != "" {
			set[FieldUsage] = rec
		}
		// a read drawn no route ran on its reader's own model: its usage line names it
		// (read_route.go)
		maps.Copy(set, readUsageFields(c, r.Usage))
		if pr != nil {
			record(pr, readConsumer(s, c, 0, r.Verdict, rec))
		}
		verdicts = append(verdicts, verdictOf(s, c, pr, c.Row, r.Verdict, r.Finding))
		u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Readers, moveEntry(c, c.Row, col, set, FieldReturned))},
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
				// the card as this read leaves it: its spend counts this read's record
				at := pr
				if v := costs[pr.ID][FieldCostTotal]; v != "" {
					at = withField(pr, FieldCostTotal, v)
				}
				if bb, ok := briefStopAt(s, at, c.Row, r.Finding); ok {
					// the same finding as the attempts before (briefStopAt: the same reader class,
					// file and line, two in a row by default), or too many attempts on one brief:
					// the brief is wrong, not the worker, and the judgment offers brief and drop
					// (brief_bound.go)
					n = judgment(NBriefWrong, pr.Row, s.Now, 0, pr.ID) // its decisions alone: it is the repeat
					n.Who, n.Attempt, n.What = c.Row, c.Int("attempt"), bb.String()+"; attempt "+c.F("attempt")+" found: "+firstSentence(r.Finding)
				}
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
	if pw, ok := windowWrite(s.Readers, Readers, verdicts); ok {
		p.Props = append(p.Props, pw)
	}
	// each primary's records ride on the unit of its last read in the plan: a running
	// machine queues the work-table change for the pump, under that read's words
	for _, id := range slices.Sorted(maps.Keys(costs)) {
		if set := costs[id]; len(set) > 0 {
			i := costUnit[id]
			p.Units[i].Changes = append(p.Units[i].Changes, change(Work, setEntry(s.Work.Card(id), set)))
		}
	}
	return p
}

// namePrimarysReads makes a read's refusal of a primary named where its read
// card is meant say the read card: when the reader holds a read of that
// primary (one of held, its asked or reading cards), the refusal names its id,
// <primary>.r<attempt>.<reader>, so the next call is a paste.
func namePrimarysReads(p *Plan, held []*Card) {
	for i, rf := range p.Refused {
		if rf.Why != noSuchCard {
			continue
		}
		var ids []string
		for _, c := range held {
			if c.F("primary") == rf.Key {
				ids = append(ids, c.ID)
			}
		}
		if len(ids) > 0 {
			p.Refused[i].Why = fmt.Sprintf("%s: %s is a primary, and a read names its read card: %s", noSuchCard, rf.Key, strings.Join(ids, ", "))
		}
	}
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
	maps.Copy(after.Fields, pr.Fields)
	maps.Copy(after.Fields, set)
	return &after
}

// reviewJudgment is the judgment a primary the step leaves in review (pr, as
// the step leaves it) needs now, so that no primary in review is silent:
//   - nothing when the ok reads it needs stand at its head and the tick's
//     pump takes it (AcceptHeld says nothing holds it): the tick accepts it,
//     RUNNING at its next pump, STOPPED at the first pump after start, and
//     the seat is told what was accepted ("ready to merge", a notice); a
//     primary whose reads are all ok is never a judgment and never a hand
//     step (TickAccept);
//   - ready to accept, only when those reads stand and the pump holds it
//     (AcceptHeld: its CI red at its head, or returned at its attempt) and no
//     judgment open on it after the step offers accept (returned to review
//     with its reads standing does): the hold is a mind's;
//   - else, when nothing is open on it after the step and no read is
//     outstanding: stranded in review when its work came back failed, or when
//     it was never asked at its attempt and the step closes the last judgment
//     on it (a primary that only arrived in review is asked by the machine);
//     nothing while its reads that stand came back ok and it wants more (the
//     ask places the next one: ReadsWanted); reads exhausted when its reads
//     are done without the oks it needs.
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
	outstanding, broken, reads := false, false, 0
	for _, r := range s.Readers.Rows() {
		for _, id := range ReadCardIDs(pr.ID, attempt, r) {
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
			case col == Broken:
				broken = true
			case col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c):
				oks[r] = true
			}
			break
		}
	}
	// a friend's read stands the same way (friendReadLive). The step's moved
	// column wins, so a close sees LAND or HOLD before the card leaves her row.
	fp, fok, fbr := friendReadLive(s, pr)
	for _, c := range append(append(append([]*Card{}, fp...), fok...), fbr...) {
		col := c.Col
		if movedCol, ok := st.moved[c.ID]; ok {
			col = movedCol
		}
		reads++
		switch {
		case col == Asked || col == Reading || col == Working || col == Ready:
			outstanding = true
		case col == Broken:
			broken = true
		case col == OK && friendReadAgrees(c) && readHeadMatches(s, pr, c):
			oks[c.F("reader")] = true
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
	case len(oks) >= ReadsNeededIn(s, pr):
		if offers || AcceptHeld(pr) == "" {
			// the tick's pump accepts it, RUNNING or STOPPED (at the first pump after
			// start): "accept is mechanical", and a hand step is a missing instruction
			return Note{}, false
		}
		typ = NReadyToAccept
	case len(open) > 0 || outstanding:
		return Note{}, false
	case pr.F(FieldBriefDefect) != "":
		// a brief defect asks again to re-cut the brief, never a stranded rework (docs/SPEC-SPRINT.md
		// section 1, a brief defect)
		typ, why = NBriefDefect, "a brief defect, "+pr.F(FieldBriefDefect)+": re-cut the brief; nothing is open on it"
	case pr.F("result") == "failed":
		typ, why = NStranded, "its work came back failed and nothing is open on it"
	case reads == 0 && len(before) == 0:
		return Note{}, false
	case reads == 0 && s.ReadCardsOn() && readCardsWanted(s, pr, nil) > 0:
		// its read cards are the read-card ask's: dealt, marked waiting for a reader, or
		// cannot ask (readCardsAskPart), never stranded as never asked
		return Note{}, false
	case reads == 0:
		typ, why = NStranded, "never asked at attempt "+itoa(attempt)+" and nothing is open on it"
	case !broken && reads < ReadsNeededIn(s, pr):
		// the ones that stand came back ok and the rest are the ask's (ReadsWanted),
		// nothing to judge
		return Note{}, false
	default:
		typ, why = NReadsExhausted, fmt.Sprintf("no read is outstanding and %s not said ok at %s", readersWord(ReadsNeededIn(s, pr)), orDash(pr.F("head")))
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
	// Heavy records the coordinator's own heavy read (heavyRead): one ok read
	// toward the read rule, resting on the file at Evidence, whose sha256 is
	// EvidenceSHA, for Reason.
	Heavy                 bool
	Evidence, EvidenceSHA string
	Reason                string
	// ReadOK is accept --read-ok: every primary in review with the ok reads it
	// needs. The tick accepts those itself (TickAccept); the verb is for a stuck
	// case, and says so when nothing waits.
	ReadOK bool `json:",omitempty"`
}

// NothingWaits is what accept --read-ok says when no primary in review has the
// ok reads it needs: the tick accepts them, so nothing waits on the verb.
const NothingWaits = "nothing waits: the tick accepts every primary in review whose reads are all ok and tells the seat (ready to merge)"

// okReaders is the primary's ok read cards from different readers at its
// current attempt and head, in reader row order, as many as it needs
// (ReadsNeeded); fewer when it has fewer.
func okReaders(s *Snapshot, pr *Card) []*Card {
	var out []*Card
	seen := map[string]bool{}
	need := ReadsNeededIn(s, pr)
	for _, c := range readsAt(s, pr, pr.Int("attempt")) {
		r := c.F("reader")
		if c.Col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c) && !seen[r] && len(out) < need {
			seen[r] = true
			out = append(out, c)
		}
	}
	// a friend's LAND stands as an ok read (docs/SPEC-SPRINT.md, a read asked of
	// any unit with room at or above the read tier). The card returned is the
	// fleet record, so accept guards it where it is.
	if s.Fleet != nil && len(out) < need {
		_, oks, _ := friendReadLive(s, pr)
		for _, syn := range oks {
			real := s.Fleet.Card(syn.ID)
			if real == nil {
				continue
			}
			r := real.F("reader")
			if friendReadAgrees(real) && readHeadMatches(s, pr, real) && !seen[r] && len(out) < need {
				seen[r] = true
				out = append(out, real)
			}
		}
	}
	return out
}

// readersWord names the readers a primary needs (ReadsNeededIn, none, one or two):
// "one reader" for a flash card, "two different readers" for a pro card.
func readersWord(n int) string {
	if n == 0 {
		return "no reader"
	}
	if n == 1 {
		return "one reader"
	}
	return "two different readers"
}

// ReadCardAgrees says a read card is where its identity says: the row it
// occupies, the reader its id names and its reader field are one reader. A
// card that disagrees counts for no reader; check reports it.
func ReadCardAgrees(c *Card) bool {
	_, _, idReader, ok := ParseReadCard(c.ID)
	return ok && c.Row == idReader && c.F("reader") == idReader
}

// Accept moves review -> merging and places the primary in merge queued with
// its score. It is refused without ok reads from as many different readers at
// the primary's head as it needs (ReadsNeededIn: one for a flash card, two for a
// pro card, or the sprint's count, set --reads, recorded on it), whoever the readers; with Heavy, the coordinator's heavy read
// counts as one of them, recorded on the primary and never as a reader's
// (heavyRead; docs/SPEC-SPRINT.md section 6, accept-heavy-verdict-b.w1). The primary's read cards still asked
// or reading are retired in the same step, marked retired by accept, so no
// read is outstanding on a primary that is not in review. Named ids are all or nothing under one
// pre-state; a selection (a stream, the primaries with the ok reads they need, an inbox
// group) moves the eligible and lists the rest with the reason.
func Accept(s *Snapshot, r AcceptReq) Plan {
	var p Plan
	// in stream turns from the accept's stream index on the work table
	// (streamTurns, as the deal's), so a limit accepts of every stream alike
	// and the merge queues fill together; the index moves past the stream of
	// the last accepted
	srr := streamRound(s, PropAcceptStreamIndex)
	heavyReads := 0
	if r.Heavy {
		heavyReads = 1
	}
	eligible := func(c *Card) string {
		if why := inState(c, Review); why != "" {
			return why
		}
		if r.Heavy {
			if why := heavyWhy(r); why != "" {
				return why
			}
		}
		if oks := okReaders(s, c); len(oks)+heavyReads < ReadsNeededIn(s, c) {
			var names []string
			for _, o := range oks {
				names = append(names, o.F("reader"))
			}
			return fmt.Sprintf("needs ok from %s at head %s; has ok from %d (%s)", readersWord(ReadsNeededIn(s, c)), orDash(c.F("head")), len(oks)+heavyReads, orDash(strings.Join(names, ",")))
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
			// a friend's ok read is on her fleet row, not the readers table
			table := Readers
			if s.Fleet != nil && s.Fleet.Card(o.ID) != nil && (s.Readers == nil || s.Readers.Card(o.ID) == nil) {
				table = Fleet
			}
			e := guardEntry(o)
			if !o.Placed() {
				// a friend's read retired with her verdict is on no row: a place is a placed
				// member's, so it is guarded at its revision alone, or the guard never holds
				e.Expect.Place = nil
			}
			u.Changes = append(u.Changes, change(table, e))
		}
		retired := 0
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Asked || rc.Col == Reading {
				u.Changes = append(u.Changes, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "accept"})))
				retired++
			}
		}
		if m := s.Merge.Placed(c.ID); m != nil {
			e := moveEntry(m, c.Row, Queued, map[string]string{FieldQueued: stamp(s.Now)})
			sc := c.Score
			e.Move.Score = &sc
			u.Changes = append(u.Changes, change(Merge, e))
		} else {
			u.Changes = append(u.Changes, change(Merge, createEntry(c.ID, c.Row, Queued, c.Score,
				map[string]string{"kind": "merge", "primary": c.ID, "stream": c.Row, FieldQueued: stamp(s.Now)})))
		}
		var names []string
		for _, o := range oks {
			names = append(names, o.F("reader"))
		}
		readers := strings.Join(names, ",")
		set := map[string]string{"readers": readers, "accepted": stamp(s.Now)}
		maps.Copy(set, readStamps(oks))
		heavyText := ""
		if r.Heavy {
			fields, overruled := heavyRead(s, c, r)
			maps.Copy(set, fields)
			for _, o := range overruled {
				u.Changes = append(u.Changes, change(Readers, guardEntry(o)))
			}
			heavyText = fmt.Sprintf("; heavy read ok from %s, evidence %s sha256 %s", fields[FieldHeavyReader], r.Evidence, r.EvidenceSHA)
			if len(overruled) > 0 {
				heavyText += "; overrules " + strings.ReplaceAll(fields[FieldHeavyOverrules], ",", ", ")
			}
		}
		// accepted on the sprint's count, not its tier's rule: the count it was accepted on
		// stays with it past review (ReadsNeededIn)
		var unset []string
		if n := ReadsNeededIn(s, c); n != ReadsNeeded(c) {
			set[FieldReadsNeeded] = strconv.Itoa(n)
		} else if c.F(FieldReadsNeeded) != "" {
			unset = append(unset, FieldReadsNeeded)
		}
		u.Changes = append(u.Changes, change(Work, moveEntry(c, c.Row, Merging, set, unset...)))
		u.Moved = fmt.Sprintf("%s review -> merging queued (ok from %s%s)", c.ID, strings.ReplaceAll(orDash(readers), ",", ", "), heavyText)
		if retired > 0 {
			u.Moved += fmt.Sprintf("; %d outstanding read cards retired", retired)
		}
		u.Closes = closesFor(s.Open, nil, c.ID)
		// a reading card retired by accept keeps its ended run; an ask still
		// unbegun adds none (retiredReadCosts)
		u.Changes = retiredReadCosts(s, u.Changes)
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
	if r.ReadOK && len(chosen) == 0 {
		p.Said = append(p.Said, NothingWaits)
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
	// Tier, when set, is the tier every later deal of the card draws its route from
	// (route.go, cardTier): written on the primary as FieldTier, over its brief's line 1.
	Tier string `json:",omitempty"`
	// PerCard is each card's own fix, tier and fields, over Fix and Tier: the tick's rule
	// answers rework many cards in one step, each its own way (rules.go). A card that names
	// a Rule records every judgment the rework closes on it as answered by that rule.
	PerCard map[string]ReworkCard `json:",omitempty"`
}

// ReworkCard is one card's own rework (ReworkReq.PerCard): its fix and tier, Friend to make
// it a friend's card (FieldWho, WhoFriend: the tick deals it to a friend), the fields Set
// writes on the primary, and the Rule that answers, with what it Said.
type ReworkCard struct {
	Fix, Tier  string            `json:",omitempty"`
	Friend     bool              `json:",omitempty"`
	Set        map[string]string `json:",omitempty"`
	Rule, Said string            `json:",omitempty"`
}

// ReworkResolves is the judgments a rework discharges on its primary.
var ReworkResolves = []string{NWorkFailed, NReadBroken, NBriefWrong, NCIRed, NRepairSkipped, NReadyToAccept, NReturned, NReadsExhausted, NStranded, NStalled, NBound}

// Rework delegates at once: the next work card attempt, carrying the fix, is
// cut into the next member round the fleet (round.go:
// from the deal's rolling index, the first up with room other than the member
// of the attempt's work card, that member only when no other has room; the
// index moved past it and written with the step) and the primary moves
// review -> working in the same step; its read cards are retired, and the
// fixed work is asked round the readers when it returns. With no member up, or none below its width (tla/DirtyTick.tla,
// WidthRespected), the primary moves review -> ready with the fix and the
// tick's deal cuts its card when a member has room.
//
// A primary that takes no rework is refused with what to run instead, by its
// state (reworkWhy), as brief is (briefStarted).
func Rework(s *Snapshot, r ReworkReq) Plan {
	s, _ = s.withRests() // the resting routes, read once (rule 3, route_rest.go)
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
		return reworkWhy(c)
	}, s.primaryCard)
	up := s.UpMembers()
	// the room of each member is its width (width.go)
	q, room := memberLoads(s, up), memberWidths(s, up)
	rr, ri := dealRound(s), routeIndexesOf(s)
	moves := roundMoves{}
	orphans := map[string]bool{}
	for _, c := range chosen {
		one := r.PerCard[c.ID]
		tier := r.Tier
		if one.Tier != "" {
			tier = one.Tier
		}
		// A primary the rework refuses stays in review: the judgment it needs
		// is written, if it has none.
		stays := func() {
			if j, ok := reviewJudgment(s, c, reviewStep{who: r.Who}); ok {
				p.Notes = append(p.Notes, j)
			}
		}
		if one.Friend {
			// a friend's card from this attempt on: the friends' deal, never a machine's
			c = withField(c, FieldWho, WhoFriend)
		}
		if tier != "" {
			// a pin is the card's whole route: no tier draws for it (route.go)
			if m, _ := cardhdr.ReadModel(c.F("brief")); m.Pin != "" {
				p.refuse(c.ID, "its brief pins model "+m.Pin+", which it runs on whatever its tier; rework it without --tier, or drop it and add the brief again with no model: line")
				stays()
				continue
			}
		}
		bound, lift := AtRedealBound(s, c), false
		if bound != nil && !one.Friend {
			// the bound holds across attempts: never a lower tier, and a second bound on one
			// tier only with --tier above it, or the provider back once per tier (failure.go);
			// a friend's card is past every tier
			var why string
			if why, lift = reworkAtTheSameBound(s, c, bound, tier); why != "" {
				p.refuse(c.ID, why)
				stays()
				continue
			}
		}
		// the brief's bound: the same finding twice (briefStopAt), or too many attempts on one
		// brief, and the brief is wrong, not the worker; a --fix changes the brief not at all,
		// so it does not lift it (brief_bound.go)
		if bb, ok := briefStopAt(s, c, finderOf(s, c), brokenFindings(s, c)); ok {
			p.refuse(c.ID, bb.Why())
			stays()
			continue
		}
		fix := r.Fix
		if one.Fix != "" {
			fix = one.Fix
		}
		if fix == "" {
			if fix = cutText(ownFix(s, c), MaxCardTextBytes); fix == "" {
				p.refuse(c.ID, "no --fix, and no finding of a broken read or report of failed work to take as its fix; give --fix <text>")
				stays()
				continue
			}
		}
		broken := 0
		var retire []Change
		if bound != nil {
			retire = append(retire, change(Fleet, removeEntry(bound, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		for _, rc := range s.Readers.Of(c.ID) {
			if rc.Col == Broken {
				broken++
			}
			retire = append(retire, change(Readers, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		// its open read cards on the fleet table, a friend's or a member's (read_cards.go):
		// left, one keeps its reader's room and closes against the attempt this rework
		// replaced; its broken read cards counted
		if s.Fleet != nil {
			_, _, fbr := friendReadLive(s, c)
			broken += len(fbr)
		}
		for _, rc := range openFriendReads(s, c.ID) {
			retire = append(retire, change(Fleet, removeEntry(rc, map[string]string{"retired": stamp(s.Now), "retired_by": "rework"})))
		}
		// the finding and why ride on the primary too: a rework with no member up deals later
		// (start), from the primary, and its child is told all the same
		given := reworkGiven(s, c)
		set := map[string]string{"fix": fix, "finding": given["finding"], "why": given["why"], FieldFindingAttempt: c.F("attempt"),
			"reworks": itoa(c.Int("reworks") + 1), "broken_reads": itoa(c.Int("broken_reads") + broken)}
		// what this attempt found, kept for the cap's judgment (brief_bound.go, FieldFindings):
		// its readers' finding, else the report of its failed work, else its bound's class
		found := given["finding"]
		if found == "" {
			found = ownFix(s, c)
		}
		if found == "" && bound != nil {
			found = BoundClass(bound)
		}
		if lines := findingsOf(c, c.Int("attempt"), found); len(lines) > 0 {
			set[FieldFindings] = findingsLine(lines)
		}
		// and its key, which the identical-finding bound compares (FindingKey): the reader
		// who found it, none for failed work
		if k := FindingKey(finderOf(s, c), found); k != "" {
			keys := slices.DeleteFunc(strings.Split(c.F(FieldFindingKeys), "\n"), func(l string) bool { return l == "" })
			set[FieldFindingKeys] = findingsLine(append(keys, "attempt "+c.F("attempt")+": "+k))
		}
		// the reader who found it broken checks the fix: the next attempt's first read is
		// asked of them (Ask, finderFirst); a rework of failed work names none
		unset := []string{"readers"}
		if finder := finderOf(s, c); finder != "" {
			set[FieldFindingReader] = finder
		} else {
			unset = append(unset, FieldFindingReader)
		}
		if bound != nil {
			// the attempt ended at its bound: its end is the primary's record of its failed
			// work, as a failed finish writes it (failureSet), read by the next rework at a bound
			set[FieldFailure], set[FieldFailureAt], set[FieldFailureTier], set[FieldFailureBound] = BoundClass(bound), c.F("attempt"), cardTierOf(c), "yes"
		}
		if lift {
			// the provider's return lifted the held bound: spent on this tier for good
			set[FieldFailureBack] = strings.Join(append(Split(c.F(FieldFailureBack)), cardTierOf(c)), ",")
		}
		if tier != "" {
			// the card records its tier and this attempt's deal draws from it already
			set[FieldTier] = tier
			c = withField(c, FieldTier, tier)
		}
		if one.Friend {
			set[FieldWho] = WhoFriend
		}
		maps.Copy(set, one.Set)
		c = reworkPriority(s, c, set)
		// the head a reader passed: a next attempt that finds nothing to do at it goes back to
		// review there, not to the coordinator as failed work (FieldPassedHead, Finish)
		if len(okReaders(s, c)) > 0 && c.F("result") != "failed" {
			set[FieldPassedHead] = c.F("head")
		} else {
			unset = append(unset, FieldPassedHead)
		}
		var u Unit
		// the next member round the fleet with room (width.go; tla/DirtyTick.tla, WidthRespected):
		// none up, or none below its width, and the primary waits ready for the tick's deal
		m := ""
		_, friend := FriendCard(c)
		bench := Bench(c)
		// no route serves its tier and a friend up does: the friends' deal's, never a machine's
		_, _, toFriend, byFriend := s.routeOf(c, nil, nil)
		if len(up) > 0 && !friend && !byFriend {
			// a bench card's next attempt goes to a member of its bench alone (bench_deal.go)
			m = rr.next(onlyBench(up, bench), q, room, reworkAvoid(s, c))
		}
		if m != "" {
			var why string
			u, why = deal(s, c, fix, m, q, ri, set, given, unset...)
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
			switch {
			case friend:
				later = "a friend's card: the tick deals it to a friend up with room"
			case byFriend:
				later = toFriend
			case len(bench) > 0 && len(onlyBench(up, bench)) == 0:
				later = benchWaits(bench) // no member of its bench is up (bench_deal.go)
			case len(up) > 0:
				later = "no fleet member has room: the tick deals it when one has"
			}
			u = Unit{Key: c.ID, Stream: c.Row, Changes: append(retire, change(Work, moveEntry(c, c.Row, Ready, set, append(unset, "result")...))),
				Moved: c.ID + " review -> ready (rework; " + later + ")"}
		}
		if tier != "" {
			u.Moved += "; tier " + tier
		}
		if one.Friend {
			u.Moved += "; a friend's card"
		}
		u.Moved += fmt.Sprintf("; %d read cards retired", len(retire))
		if m := orphanMerge(s, c); m != nil {
			u.Changes = append(u.Changes, change(Merge, moveEntry(m, c.Row, Returned, nil, "need_card", "need_stream")))
			u.Moved += "; its orphan merge card off " + m.Col
			orphans[c.ID] = true
		}
		// a reading card, or a working read card, retired by rework keeps its ended
		// run on the primary this unit already writes; an unbegun ask adds none
		u.Changes = retiredReadCosts(s, u.Changes)
		u.Closes = closesFor(s.Open, ReworkResolves, c.ID)
		if one.Rule != "" {
			// the rule's answer is the decided note of every judgment the rework closes
			u.Moved += "; answered by rule " + one.Rule
			for _, o := range u.Closes {
				if !answeredIn(u.Notes, o.Note.ID) {
					u.Notes = append(u.Notes, decided(o, RuleSaid(one.Rule, one.Said), r.Who, s.Now, c.ID))
				}
			}
		}
		p.Units = append(p.Units, u)
	}
	settle(&p, s, r.Who, orphans, nil)
	answered(&p, s, r.Answers, r.Who)
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	ri.write(&p) // a rework's attempt is a card dealt: its tier's route index moves (route.go)
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

// finderOf is the reader who found the primary broken at its attempt: the first
// broken read's reader in reader row order (the readers table's declaration
// order, as readsAt scans it; never name order, which Of's work order falls
// back to for reads of one score), "" when no read of it is broken. An attempt
// has two broken reads only when a second was asked beside the first (ask
// --another); the reference model's Rework names the same reader
// (TestTheFinderIsTheFirstBrokenReadInReaderRowOrder).
func finderOf(s *Snapshot, c *Card) string {
	for _, rc := range readsAt(s, c, c.Int("attempt")) {
		if rc.Col == Broken {
			return rc.Row
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
	// a read card's broken verdict on the fleet table (read_cards.go)
	if s.Fleet != nil {
		_, _, fbr := friendReadLive(s, c)
		for _, rc := range fbr {
			if rc.F("finding") != "" && !contains(found, rc.F("finding")) {
				found = append(found, rc.F("finding"))
			}
		}
	}
	return strings.Join(found, "; ")
}

// The bound is two identical findings (docs/SPEC-SPRINT.md, "The brief is wrong, not
// the worker"; the card the-bound-is-two-identical-findings, after the night of
// 2026-10-05 in which cards with the fix first in their brief still ran three and four
// lanes on one finding, reworded each time, before the attempt cap stopped them). Two
// findings are identical when the same class of reader names the same file and line
// (FindingKey); a finding that names no file and line keeps the whole-text class
// (FindingClass). The second attempt on one brief that comes back with the finding the
// attempt before it came back with stops the card at once, the brief defect raised
// carrying both findings; the attempt cap (AttemptsCap) stays only for findings that
// differ. How many identical findings in a row stop a card is a setting
// (IdenticalBound), 2 by default.

// PropIdentical is the work table's property holding the sprint's identical-finding
// bound, FieldIdentical a stream's control card field holding the stream's, over the
// sprint's; IdenticalDefault is the bound when neither is set. A value under 2 is no
// setting: one finding is never a repeat.
const (
	PropIdentical    = "identical"
	FieldIdentical   = "identical"
	IdenticalDefault = 2
)

// FieldFindingKeys is the primary's list of each attempt's finding key (FindingKey),
// one line an attempt ("attempt <n>: <key>"), appended by Rework beside FieldFindings and
// cut the same way: what the identical-finding bound reads back past the attempt before.
const FieldFindingKeys = "finding_keys"

// IdenticalBound is how many identical findings in a row on one brief stop a card in
// the stream: the stream's setting, else the sprint's, else IdenticalDefault.
func (s *Snapshot) IdenticalBound(stream string) int {
	if s.Merge != nil {
		if n := s.StreamCtl(stream).Int(FieldIdentical); n >= 2 && n <= AttemptsMax {
			return n
		}
	}
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropIdentical); ok {
			if n, err := ParseAttempts(v); err == nil && n >= 2 {
				return n
			}
		}
	}
	return IdenticalDefault
}

// findingAt matches the first file and line a finding names (internal/sprint/state.go:31), a
// leading ./ not part of it.
var findingAt = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-][A-Za-z0-9_.-]*\.[A-Za-z0-9]+:[0-9]+`)

// FindingKey is what makes two findings identical: the reader's class and the first
// file and line the finding names, "<reader> at <file>:<line>"; a finding that names
// no file and line is its whole-text class (FindingClass), whoever found it. The
// reader's class is its row of the readers table, case folded: one reader is one
// class of reader, and a friend's row (friend.<name>) is hers; a finding with no
// reader (the report of failed work) is the class "work". "" for an empty finding.
func FindingKey(reader, finding string) string {
	at := findingAt.FindString(finding)
	for strings.HasPrefix(at, "./") {
		at = at[len("./"):]
	}
	if at == "" {
		return FindingClass(finding)
	}
	class := strings.ToLower(strings.TrimSpace(reader))
	if class == "" {
		class = "work"
	}
	return class + " at " + at
}

// IdenticalFindings is a primary stopped by the identical-finding bound: the attempts
// that came back with the one finding (Attempts, in order, Bound of them) and what
// each of them found (Findings, one an attempt, as said).
type IdenticalFindings struct {
	ID       string
	Key      string
	Attempts []int
	Findings []string
	Bound    int
}

// String is the bound said in one line: the attempts, the key and every finding, then
// the verdict. A key of whole text at the default bound is said as the brief's bound
// has always said it (BriefBound): the first finding, the second the same class.
func (b IdenticalFindings) String() string {
	first, last := b.Attempts[0], b.Attempts[len(b.Attempts)-1]
	if findingAt.FindString(b.Key) == "" && len(b.Attempts) == 2 {
		return BriefBound{ID: b.ID, Attempts: [2]int{first, last}, Finding: firstSentence(b.Findings[0])}.String()
	}
	times := "twice"
	if len(b.Attempts) > 2 {
		times = itoa(len(b.Attempts)) + " times"
	}
	var each []string
	for i, f := range b.Findings {
		each = append(each, "attempt "+itoa(b.Attempts[i])+": "+firstSentence(f))
	}
	return fmt.Sprintf("%s has failed the same way %s (attempts %d to %d, the same finding: %s); the brief is wrong, not the worker; findings: %s",
		b.ID, times, first, last, b.Key, strings.Join(each, "; "))
}

// Why is the rework's refusal of a card at the bound: the line, and the remedy.
func (b IdenticalFindings) Why() string {
	return b.String() + "; " + BriefBound{ID: b.ID}.Remedy()
}

// briefStop is why a primary's brief is wrong: BriefBound (the attempt cap) or
// IdenticalFindings.
type briefStop interface {
	String() string
	Why() string
}

// AtIdenticalFindings is whether the primary c, whose readers (reader, the first that
// found it broken; "" for failed work) found finding at its current attempt, has come
// back with that finding at bound attempts in a row since its brief last changed. The
// attempts before are read from the primary: its FieldFindingKeys, and for the attempt
// its `finding` carries (FieldFindingAttempt) with no key kept, the key of that finding
// and its reader (FieldFindingReader).
func AtIdenticalFindings(c *Card, reader, finding string, bound int) (IdenticalFindings, bool) {
	attempt, briefAt := c.Int("attempt"), c.Int(FieldBriefAttempt)
	key := FindingKey(reader, finding)
	if attempt == 0 || key == "" {
		return IdenticalFindings{}, false
	}
	if bound < 2 {
		bound = IdenticalDefault
	}
	keys, said := map[int]string{}, map[int]string{}
	for _, l := range strings.Split(c.F(FieldFindingKeys), "\n") {
		var n int
		if head, k, ok := strings.Cut(l, ": "); ok {
			if _, err := fmt.Sscanf(head, "attempt %d", &n); err == nil && n > 0 {
				keys[n] = k
			}
		}
	}
	for _, l := range strings.Split(c.F(FieldFindings), "\n") {
		var n int
		if head, f, ok := strings.Cut(l, ": "); ok {
			if _, err := fmt.Sscanf(head, "attempt %d", &n); err == nil && n > 0 {
				said[n] = f
			}
		}
	}
	if prev, prevAt := c.F("finding"), c.Int(FieldFindingAttempt); prev != "" {
		if prevAt == 0 {
			prevAt = attempt - 1
		}
		if _, ok := keys[prevAt]; !ok {
			keys[prevAt] = FindingKey(c.F(FieldFindingReader), prev)
		}
		said[prevAt] = prev
	}
	b := IdenticalFindings{ID: c.ID, Key: key, Bound: bound, Attempts: []int{attempt}, Findings: []string{finding}}
	for a := attempt - 1; a > briefAt && len(b.Attempts) < bound && keys[a] == key; a-- {
		b.Attempts = append([]int{a}, b.Attempts...)
		b.Findings = append([]string{said[a]}, b.Findings...)
	}
	return b, len(b.Attempts) >= bound
}

// briefStopAt is the primary's brief's bound, the identical-finding bound first: its
// readers' finding at its current attempt (reader the first that found it broken) the
// same as the attempts before it, as many as the stream's IdenticalBound, else the
// attempt cap's attempts on one brief (AtBriefBound, its own whole-text repeat taken
// over by the bound here, so a setting above 2 holds for it too).
func briefStopAt(s *Snapshot, c *Card, reader, finding string) (briefStop, bool) {
	if b, ok := AtIdenticalFindings(c, reader, finding, s.IdenticalBound(c.Row)); ok {
		return b, true
	}
	if bb, ok := AtBriefBound(withField(c, "finding", ""), finding, s.AttemptsCap(c.Row)); ok {
		return bb, true
	}
	return nil, false
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
	// Rule, when set, is the rule that returns the cards (rules.go, the conflict rule): each
	// is marked to be redone on the tip (FieldRuleRedo, at its attempt), and the stopped
	// stream's judgment records the answer.
	Rule string `json:",omitempty"`
}

// ReturnResolves is the judgments a return is a decision for: a red CI on the
// primary is discharged; a stream's red or rejected batch is answered and stays
// open while the stream is stopped.
var ReturnResolves = []string{NCIRed, NRed, NRejected, NRepairSkipped, NStalled}

func answeredIn(notes []Note, id string) bool {
	return slices.ContainsFunc(notes, func(n Note) bool { return n.Kind == Decided && n.Answers == id })
}

// Return moves merging -> review: off the merge queue (or stuck), into the
// merge table's hidden returned column, so a later accept moves it back. The
// primary is marked returned at its attempt (FieldReturnedAttempt): the
// coordinator decides it, by accept, rework or drop, and the pump accepts it
// again only at a new attempt with its own reads.
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
		if r.Rule != "" {
			set[FieldRuleRedo] = c.F("attempt")
			set[FieldRuleAnswer] = r.Rule + ": returned to be redone on the tip at " + stamp(s.Now)
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
		if r.Rule != "" {
			for _, o := range s.Open {
				if o.Note.StreamLevel && o.Note.Stream == c.Row && o.Note.Card == c.ID && !answeredIn(u.Notes, o.Note.ID) {
					u.Notes = append(u.Notes, decided(o, RuleSaid(r.Rule, "returned "+c.ID+" to be redone on the current tip"), r.Who, s.Now, c.ID))
				}
			}
		}
		// Back in review, the coordinator decides again.
		j := judgment(NReturned, c.Row, s.Now, c.Int("returns"), c.ID)
		j.Who, j.Attempt, j.What = r.Who, c.Int("attempt"), r.Reason
		if !acceptable(s, c) {
			j.Decisions = removeDecision(j.Decisions, "accept")
		}
		u.Notes = append(u.Notes, j)
		// Back in review with the ok reads it needs at its
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

// waitingNeeding is the waiting primaries that name id and are not dropping:
// the dependants a drop without cascade refuses, read from every waiting
// card's needs field (docs/SPEC-SPRINT.md section 11).
func waitingNeeding(s *Snapshot, id string, dropping map[string]bool) []string {
	var out []string
	for _, w := range s.Work.Column(Waiting) {
		if dropping[w.ID] {
			continue
		}
		if contains(Split(w.F("needs")), id) {
			out = append(out, w.ID)
		}
	}
	return out
}

// DropReq is the coordinator taking primaries off the table.
type DropReq struct {
	Sel
	Reason  string
	Answers []string
	// Cascade drops, with the cards the selection names, every waiting
	// primary that needs one of them, and their dependants too. Without it a
	// card a waiting primary still needs is refused, naming the dependants
	// (docs/SPEC-SPRINT.md section 11).
	Cascade bool
	Who     string
}

// Drop takes open primaries off the table with the reason: their record,
// outcome and reason are kept; their live work card, unread read cards and
// merge place go with them. A waiting primary that needs one is a dependant:
// without Cascade the drop is refused for that card, naming the dependants;
// with it the dependants and their dependants go too (docs/SPEC-SPRINT.md
// section 11).
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
	dropping := map[string]bool{}
	for _, c := range chosen {
		dropping[c.ID] = true
	}
	if r.Cascade {
		// Grow the set of dropping cards: every waiting primary that needs
		// one of them goes too, and their dependants in turn (a cascade), so
		// no waiting primary is left blocked on a dropped card
		// (docs/SPEC-SPRINT.md section 11).
		for grew := true; grew; {
			grew = false
			for _, w := range s.Work.Column(Waiting) {
				if dropping[w.ID] {
					continue
				}
				for _, n := range Split(w.F("needs")) {
					if dropping[n] {
						dropping[w.ID] = true
						grew = true
						break
					}
				}
			}
		}
		var kept []*Card
		for _, c := range all {
			if dropping[c.ID] {
				kept = append(kept, c)
			}
		}
		chosen = kept
	} else {
		// Without Cascade a card another waiting primary still needs is
		// refused for that card, naming the dependants, and nothing moves.
		var kept []*Card
		for _, c := range chosen {
			if deps := waitingNeeding(s, c.ID, dropping); len(deps) > 0 {
				p.refuse(c.ID, fmt.Sprintf("%s is needed by %s; drop them too with --cascade", c.ID, strings.Join(deps, ", ")))
				dropping[c.ID] = false
				continue
			}
			kept = append(kept, c)
		}
		chosen = kept
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
		u.Closes = closesFor(s.Open, nil, c.ID)
		answerListed(&u, s.Open, r.Answers, "drop", c.Row, "dropped "+c.ID+"; "+r.Reason, r.Who, s.Now, c.ID)
		u.Moved = fmt.Sprintf("%s %s -> off the table (%s)", c.ID, c.Col, r.Reason)
		p.Units = append(p.Units, u)
	}
	// the weights the drop changes: every primary the dropped cards waited on (weight.go)
	p.Units = append(p.Units, weighUnits(s, nil, dropping)...)
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
	Before  string   // in line in front of this primary of the cards' own stream, in the order named
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
	scores, why := rankScores(s, r, chosen)
	if why != "" {
		p.Refused = append(p.Refused, Refusal{Key: strings.Join(r.IDs, ","), Why: why + "; nothing was changed"})
		return p
	}
	for i, c := range chosen {
		score := scores[i]
		if c.Score == score {
			p.refuse(c.ID, "already at score "+fmtScore(score))
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
	}
	answered(&p, s, r.Answers, r.Who)
	return p
}

// rankScores is the new score of each chosen card, in order: from --score up by
// one, ahead of every primary (--first), or in line in front of --before, placed
// as add --before places cards (addScores: between the card before the anchor
// and the anchor, the line never renumbered). --before wants a primary of the
// chosen cards' own stream that is none of them; "" is the refusal, of the whole
// step.
func rankScores(s *Snapshot, r RankReq, chosen []*Card) ([]float64, string) {
	out := make([]float64, len(chosen))
	switch {
	case r.Score != nil:
		for i := range out {
			out[i] = *r.Score + float64(i)
		}
	case r.First:
		low := 0.0
		first := true
		for _, c := range s.Work.Cards() {
			if c.Placed() && (first || c.Score < low) {
				low, first = c.Score, false
			}
		}
		for i := range out {
			out[i] = low - float64(len(chosen)) + float64(i)
		}
	case r.Before != "":
		anchor := s.Work.Placed(r.Before)
		if anchor == nil {
			return nil, "--before " + r.Before + " is no primary on the table"
		}
		for _, c := range chosen {
			switch {
			case c.ID == anchor.ID:
				return nil, c.ID + " is the card --before names: a card is not placed before itself"
			case c.Row != anchor.Row:
				return nil, c.ID + " is of stream " + c.Row + " and " + anchor.ID + " of stream " + anchor.Row + ": --before orders the cards of one stream (move takes a card to another)"
			}
		}
		return addScores(s, AddReq{Stream: anchor.Row, Before: r.Before}, len(chosen))
	}
	return out, ""
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

// LandedReq is the coordinator's record of work found on the branch but not recorded
// landed (docs/SPEC-SPRINT.md section 7, land-record-unreported-push-bc.w2): a push that
// happened and was never reported, or work landed outside the deal by a pull request. Pins
// are the cards at the heads the caller read, InBase the caller's git facts (never a
// model's): each head is an ancestor of Sha, and Sha is on origin/<base> at its tip.
type LandedReq struct {
	Pins   []LandedPin
	Sha    string
	Reason string
	Who    string
}

// RecordLanded lands exactly the pinned cards as merge's record by name lands them (merge
// --landed, section 8), all or none, each merge card's note naming the commit and the
// reason. Only a merging card is recorded: the lifecycle has no move review -> landed and no
// move from off the table, so a card in review is refused naming accept, and a dropped one
// naming that it stays dropped. The cards are of one stream, the record's one batch.
func RecordLanded(s *Snapshot, r LandedReq) Plan {
	var p Plan
	p.on(s)
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse("landed", "a record of work found on the branch says how it got there: --reason <text>")
		return p
	}
	stream := ""
	for _, pin := range r.Pins {
		c := s.Work.Card(pin.ID)
		why := ""
		switch {
		case c == nil:
			why = "no such card"
		case !c.Placed() && c.F("outcome") == "dropped":
			why = "dropped (" + orDash(c.F("reason")) + "); the lifecycle has no move off the table -> landed, so it stays dropped"
		case !c.Placed():
			why = "not on the table (" + orDash(c.F("outcome")) + ")"
		case c.Col == Landed:
			why = "landed already; landed is final"
		case c.Col == Review:
			why = "in review; the lifecycle has no move review -> landed: accept it, then record it landed"
		case c.Col != Merging:
			why = "it is " + c.Col + "; only a merging card is recorded landed"
		case stream != "" && c.Row != stream:
			why = "of stream " + c.Row + ", and the record's other cards are of stream " + stream + "; record each stream on its own"
		}
		if why != "" {
			p.refuse(pin.ID, why)
			continue
		}
		stream = c.Row
	}
	if len(p.Refused) > 0 || stream == "" {
		if stream == "" && len(p.Refused) == 0 {
			p.refuse("landed", "no card named")
		}
		return p
	}
	notes := map[string]string{}
	for _, pin := range r.Pins {
		notes[pin.ID] = "recorded landed at " + r.Sha + ": " + r.Reason
	}
	return MergeStep(s, MergeReq{Stream: stream, Landed: r.Pins, Resolved: notes, Note: r.Reason, Who: r.Who})
}

// Review starved, and reads idle (docs/SPEC-SPRINT.md, "Review starved, and reads idle").
// On 2026-10-07 at 5:40 PM ET the owner had 115 cards in review and no read out on any
// friend or fleet member. The seat found it by looking. This part pushes one judgment
// when cards in review want a read and no read is out, and one when readers are free
// while reads still wait. Each is one episode: raised once, closed with a note when
// the condition ends.
//
// The episode is a fleet-table property, so the tick's end writes it in the step that
// sees it (a work-table property waits for the next pump). The alarm runs inside
// the existing done step: a quiet tick must not gain an operation ID just for
// checking the alarm. TickParts remains the reference model's duty list.
const (
	NReviewStarved      = "review starved"
	NReadsIdle          = "reads idle"
	NReviewAlarmCleared = "a review alarm cleared"

	// PropReviewStarved is the work table's property: a duration, or off. Absent is
	// reviewStarvedDefault.
	PropReviewStarved   = "review_starved"
	PropReviewStarvedEp = "review_starved_ep" // fleet: since\t0|1
	PropReadsIdleEp     = "reads_idle_ep"

	PartReviewStarved = "review-starved"

	// reviewStarvedTick is one tick of running time. store.TickEvery is the same
	// second; this package does not import the store.
	reviewStarvedTick    = time.Second
	reviewStarvedDefault = 2 * reviewStarvedTick

	reviewWhyHeld   = "stream held"
	reviewClearOut  = "a read is out"
	reviewClearNone = "no card in review wants a read"
	reviewClearBusy = "no reader is free"
)

func init() { reviewStarvedInstall() }

// reviewStarvedInstall keeps the tick's existing part count. Each part gets an
// operation ID even when its plan is empty, so adding a separate end part
// changes the IDs of unrelated first-run and inbox commands.
func reviewStarvedInstall() {
	for i := range TickEnd {
		if TickEnd[i].Name == PartDone {
			TickEnd[i].Fn = tickReviewAlarmAndDone
			return
		}
	}
}

// tickReviewAlarmAndDone composes the alarm's fleet-property and judgment
// writes with the done note in the existing final tick step.
func tickReviewAlarmAndDone(s *Snapshot, r TickReq) (Plan, int) {
	p, due := TickReviewStarved(s, r)
	done, more := TickDone(s, r)
	p.Notes = append(p.Notes, done.Notes...)
	return p, due + more
}

// reviewStarvedWindow is the setting: off, or a duration at least one tick.
// Absent, empty, or not a duration is the default of two ticks.
func (s *Snapshot) reviewStarvedWindow() (time.Duration, bool) {
	if s == nil || s.Work == nil {
		return reviewStarvedDefault, false
	}
	v, ok := s.Work.Prop(PropReviewStarved)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return reviewStarvedDefault, false
	}
	if v == AlarmOff {
		return 0, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return reviewStarvedDefault, false
	}
	if d < reviewStarvedTick {
		d = reviewStarvedTick
	}
	return d, false
}

// WithReviewStarved adds the review alarm window to a set plan. A setting on its
// own may be the only thing to set, so it clears only Set's "nothing to set"
// refusal; every other refusal leaves the whole step unwritten.
func WithReviewStarved(p Plan, s *Snapshot, v string) Plan {
	if v == "" {
		return p
	}
	for _, x := range p.Refused {
		if !strings.HasPrefix(x.Why, "nothing to set") {
			return p
		}
	}
	p.Refused = nil
	word := v
	if v != AlarmOff {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Plan{Refused: []Refusal{{Key: "set", Why: "--review-starved wants a duration above zero (2s, 1m), or off; found " + v}}}
		}
		if d < reviewStarvedTick {
			d = reviewStarvedTick
		}
		word = d.String()
	}
	was, had := s.Work.Prop(PropReviewStarved)
	p.Props = append(p.Props, PropWrite{Table: Work, Name: PropReviewStarved, Value: word, Was: was, WasAbsent: !had})
	for i := range p.Units {
		if p.Units[i].Key == "set" {
			if strings.TrimSpace(p.Units[i].Moved) == "sprint" {
				p.Units[i].Moved = "sprint review-starved " + word
			} else {
				p.Units[i].Moved += ", review-starved " + word
			}
			return p
		}
	}
	p.Units = append(p.Units, Unit{Key: "set", Moved: "sprint review-starved " + word})
	return p
}

// TickReviewStarved is the tick's review-starved part.
func TickReviewStarved(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, 0
	}
	window, off := s.reviewStarvedWindow()
	if off {
		return reviewAlarmFinish(reviewAlarmDisarm(s, p))
	}
	wants := reviewWantsRead(s)
	out := readsOut(s)
	free := reviewFreeReaders(s, reviewAlarmSeats(s, r))
	starved := len(wants) > 0 && out == 0
	idle := free > 0 && len(wants) > 0
	clearStarved := reviewClearNone
	if out > 0 {
		clearStarved = reviewClearOut
	}
	clearIdle := reviewClearNone
	if free == 0 {
		clearIdle = reviewClearBusy
	}
	p = reviewAlarmStep(s, r, p, PropReviewStarvedEp, NReviewStarved, window, starved,
		func() string { return reviewStarvedWhat(s, r, wants) }, clearStarved, len(wants), reviewAlarmFirstIDs(wants))
	p = reviewAlarmStep(s, r, p, PropReadsIdleEp, NReadsIdle, window, idle,
		func() string { return reviewIdleWhat(free, len(wants)) }, clearIdle, len(wants), nil)
	return reviewAlarmFinish(p)
}

// reviewAlarmFirstIDs is the first three ids in work order, the ones the
// starved judgment names.
func reviewAlarmFirstIDs(wants []*Card) []string {
	var ids []string
	for i, c := range wants {
		if i == 3 {
			break
		}
		ids = append(ids, c.ID)
	}
	return ids
}

// reviewWantsRead is the cards in review whose attempt came back ok and still
// wants a read (readCardsWanted). A failed attempt wants none.
func reviewWantsRead(s *Snapshot) []*Card {
	var out []*Card
	for _, pr := range s.Work.Column(Review) {
		if pr.F("result") != "ok" || IsSentinel(pr) {
			continue
		}
		if readCardsWanted(s, pr, nil) > 0 {
			out = append(out, pr)
		}
	}
	return out
}

// readsOut is the reads in flight: a read card ready or working on any fleet
// row, or a read asked or reading on the readers table.
func readsOut(s *Snapshot) int {
	n := 0
	if s.Fleet != nil {
		for _, c := range s.Fleet.Column(Ready, Working) {
			if isRead(c) {
				n++
			}
		}
	}
	if s.Readers != nil {
		n += len(s.Readers.Column(Asked)) + len(s.Readers.Column(Reading))
	}
	return n
}

// reviewFreeReaders is the readers with room: a unit the read-card deal may
// use that has a half slot free, and a reader up on the readers table under
// its width that the units did not already count.
func reviewFreeReaders(s *Snapshot, seats []FriendSeat) int {
	named := map[string]bool{}
	n := 0
	if s.Fleet != nil {
		for _, u := range readUnitsOf(s, seats) {
			named[u.name] = true
			if u.half > 0 {
				n++
			}
		}
	}
	if s.Readers == nil {
		return n
	}
	for _, rd := range s.UpReaders() {
		m, ok := ReaderMachine(rd)
		if ok && named[m] || !ok && named[rd] {
			continue
		}
		if s.ReaderWidth(rd) > s.readerLoad(rd) {
			n++
		}
	}
	return n
}

func reviewAlarmSeats(s *Snapshot, r TickReq) []FriendSeat {
	if r.Friends != nil {
		return r.Friends
	}
	return s.Friends
}

func reviewStarvedWhat(s *Snapshot, r TickReq, wants []*Card) string {
	seats := reviewAlarmSeats(s, r)
	_, waits := readCardsAsk(s, seats, nil)
	var bits []string
	for i, c := range wants {
		if i == 3 {
			break
		}
		why := cmp.Or(waits[c.ID], c.F(FieldWaitingReader))
		if StreamHeld(s, c.Row) {
			why = reviewWhyHeld
		} else if why == "" {
			why = "the read deal did not report a wait reason"
		}
		bits = append(bits, c.ID+" ("+why+")")
	}
	return fmt.Sprintf("review starved: %d cards in review want a read and no read is out: %s", len(wants), strings.Join(bits, "; "))
}

func reviewIdleWhat(free, waiting int) string {
	return fmt.Sprintf("reads idle: %d readers free and %d cards in review want a read", free, waiting)
}

func reviewAlarmDisarm(s *Snapshot, p Plan) Plan {
	for _, spec := range []struct{ prop, typ string }{
		{PropReviewStarvedEp, NReviewStarved},
		{PropReadsIdleEp, NReadsIdle},
	} {
		if v, ok := s.Fleet.Prop(spec.prop); ok && v != "" {
			reviewAlarmWrite(s, &p, spec.prop, "")
		}
		p.Closes = append(p.Closes, reviewAlarmOpen(s, spec.typ)...)
	}
	return p
}

// reviewAlarmStep advances one episode. The first tick that sees the condition
// records when; a later tick raises once the window of running time has passed;
// the tick that finds the condition ended closes it, with a note when one was raised.
func reviewAlarmStep(s *Snapshot, r TickReq, p Plan, prop, typ string, window time.Duration, on bool, what func() string, clear string, count int, ids []string) Plan {
	since, said := reviewAlarmRead(s, prop)
	if !on {
		if since == "" && !said {
			return p
		}
		reviewAlarmWrite(s, &p, prop, "")
		if said {
			p.Closes = append(p.Closes, reviewAlarmOpen(s, typ)...)
			n := happened(NReviewAlarmCleared, "", s.Now)
			n.Who, n.To = r.who(), s.Coordinator
			n.What = typ + ": " + clear
			p.Notes = append(p.Notes, n)
		}
		return p
	}
	if since == "" {
		reviewAlarmWrite(s, &p, prop, reviewAlarmEncode(s.Now, false))
		return p
	}
	elapsed, ok := reviewAlarmElapsed(s.Now, since)
	if !ok {
		reviewAlarmWrite(s, &p, prop, reviewAlarmEncode(s.Now, false))
		return p
	}
	if elapsed < window || said {
		return p
	}
	n := Note{
		Kind: Judgment, Type: typ, What: what(), Who: r.who(), At: s.Now,
		Decisions: []string{"ack", "wait"}, SprintLevel: true, Count: count,
		Primaries: ids,
	}
	p.Notes = append(p.Notes, n)
	reviewAlarmWrite(s, &p, prop, reviewAlarmSaid(since))
	return p
}

func reviewAlarmRead(s *Snapshot, prop string) (since string, said bool) {
	v, ok := s.Fleet.Prop(prop)
	if !ok || v == "" {
		return "", false
	}
	since, flag, ok := strings.Cut(v, "\t")
	if !ok || since == "" {
		return "", false
	}
	return since, flag == "1"
}

func reviewAlarmEncode(since time.Time, said bool) string {
	flag := "0"
	if said {
		flag = "1"
	}
	return since.UTC().Format(time.RFC3339Nano) + "\t" + flag
}

func reviewAlarmSaid(since string) string {
	t, _, _ := strings.Cut(since, "\t")
	return t + "\t1"
}

func reviewAlarmElapsed(now time.Time, since string) (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		return 0, false
	}
	if now.Before(t) {
		return 0, true
	}
	return now.Sub(t), true
}

func reviewAlarmWrite(s *Snapshot, p *Plan, name, value string) {
	was, had := s.Fleet.Prop(name)
	if had && was == value {
		return
	}
	if !had && value == "" {
		return
	}
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
}

func reviewAlarmOpen(s *Snapshot, typ string) []Open {
	var out []Open
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// reviewAlarmFinish makes a property-only plan visible to the tick. The probe
// skips a plan Empty is true of, and Empty ignores Props, so the episode would
// never be stored. A unit with no move is not a line the quiet ticks forbid.
func reviewAlarmFinish(p Plan) (Plan, int) {
	if len(p.Props) > 0 && p.Empty() {
		p.Units = append(p.Units, Unit{Key: PartReviewStarved})
	}
	return p, 0
}

package sprint

import (
	"fmt"
	"strconv"
)

// The local holder of a card (the upper design, version 2.1, 2.3 R16, "The
// local holder of a card"): what holds a primary that has not landed, judged
// on the state alone. A holder is one of (a) an outside actor before its
// deadline, (b) a rule whose condition the state meets, (c) a judgment open or
// held, (d) what it waits on, itself held, (e) the machine STOPPED. (b) is the
// state meeting a rule's condition, never a key being queued or a line being
// ingested (NoLostWork, 5, guarantees the key comes), so nothing here reads
// the agenda.
//
// The table is data: holdRows, one row for each state of the design's table,
// each with the holders it lists in the order it lists them. A row earlier in
// the table shadows the later ones: the three states that take a card out of
// its indexes whatever its place (quarantined, refused, at its bound) and the
// stream being dropped come first, then the states by place.
//
// (d) is one level: a counted need that is open on the table is held by
// NothingSilent (5) on its own, and the needs graph is acyclic by construction
// (F1-6), so no chain is followed and one card is O(1) reads: its record, its
// primary's, at most 64 needs, at most 15 read cards, its work or merge card
// and its stream's front. A view of the snapshot (holdView) computes the
// fleet's room, each stream's front and the judgments open once, for every
// card it judges: LocalHolder alone pays that once a call.

// HeldFacts are the sprint's own keys that R16's read carries beside the
// tables (2.3 R16: jopen of the card, its stream and the sprint, the
// backlog): what is not a card of a table and not derivable from one.
type HeldFacts struct {
	// Backlog is the lines not yet ingested (L2 last less the tick's cur).
	Backlog int
	// Dropping maps a stream being dropped or removed to the op that drops it
	// ({p}dropping@e).
	Dropping map[string]string
	// Cut maps an op to the wall ms its cut entry is due at ({p}cut@e), the
	// op's cut clock (1.2).
	Cut map[string]int64
	// Quarantined is the ids in {p}quarantine@e (1.3.5).
	Quarantined map[string]bool
	// Held is the judgments a wait holds (jopen's h<note>, 1.3.4): a hold
	// closes the judgment and keeps the condition, so the card stays held.
	Held []Open
	// Lines is the ids of each line a key of the read names by seq, as the
	// read's line source returned them from the key's offset.
	Lines map[uint64]HeldLine
}

// HeldLine is what a read of a line by seq returned: the ids from the key's
// offset on, at most the read's limit, and how many ids the line names.
type HeldLine struct {
	IDs   []string
	Total int
}

// heldAttemptBound is R10's attempts (2.3 R10): below it a failed or broken
// primary is reworked, at it R10 sets its bound.
const heldAttemptBound = 3

// The columns R16 reads off a card's fields, by the fields' names (1.3.1).
const (
	fieldDueUntaken    = "due_untaken"
	fieldDueUnfinished = "due_unfinished"
	fieldDueUnbegun    = "due_unbegun"
	fieldDueUnreported = "due_unreported"
	fieldDueMergeIdle  = "due_mergeidle"
)

// crossStop is the cause of a stream stopped on a card of another stream.
const crossStop = "cross"

// holder is one way a state is held: the holder's letter, the words of the
// design for it, and the test. Holds returns why the card is held, "" when it
// is not.
type holder struct {
	By    string
	What  string
	Holds func(v *holdView, c *Card) string
}

// holdRow is one row of the table of local holders: the state, when a primary
// is in it, and what holds it. Stall is why nothing holds a primary of the row,
// said the same for every card of it ("" for a row nothing can fail to hold),
// Cause its judgment's cause (J opens one judgment per type and cause, 1.3.4),
// and Decisions the decisions its place allows before the ones every stall
// offers.
type holdRow struct {
	Name      string
	Cause     string
	Stall     string
	Decisions []string
	Match     func(v *holdView, c *Card) bool
	Holders   []holder
}

// stalledTail is what every stall offers after its place's own decisions
// (2.2: `card`, `drop`, `wait`).
var stalledTail = []string{"card", "drop", "wait"}

// holdRows is the table, in the order a card is matched against it.
var holdRows = []holdRow{
	{
		Name:  "quarantined",
		Cause: "quarantined",
		Stall: "it is quarantined and no judgment names it",
		Match: func(v *holdView, c *Card) bool { return v.f.Quarantined[c.ID] },
		Holders: []holder{
			{HeldByJudgment, "an invariant is broken, open or held", onCard(NInvariant)},
		},
	},
	{
		Name:  "of a stream being dropped or removed",
		Cause: "dropping",
		Stall: "its stream is being dropped or removed, and neither the op's cut clock nor a judgment on the op names it",
		Match: func(v *holdView, c *Card) bool { return v.f.Dropping[c.Row] != "" },
		Holders: []holder{
			{HeldByTick, "R11, the op's cut clock", cutClock},
			{HeldByJudgment, "a verb in parts stopped before its end, open or held", opJudged},
		},
	},
	{
		Name:  "any with refused",
		Cause: "refused",
		Stall: "the machine refused to move it and no judgment names it",
		Match: func(v *holdView, c *Card) bool { return c.F("refused") != "" },
		Holders: []holder{
			{HeldByJudgment, "the machine could not move a card, open or held", onCard(typeCouldNotMove)},
		},
	},
	{
		Name:  "ready or review with bound",
		Cause: "bound",
		Stall: "it reached its bound and no judgment names it",
		Match: func(v *holdView, c *Card) bool {
			return (c.Col == Ready || c.Col == Review) && c.F("bound") != ""
		},
		Holders: []holder{
			{HeldByJudgment, "a card reached its bound, open or held", onCard(NBound)},
		},
	},
	{
		Name: "a sentinel, not reached",
		Match: func(v *holdView, c *Card) bool {
			return IsSentinel(c) && c.Col == Waiting && !v.reached(c)
		},
		Holders: []holder{
			{HeldByWaiting, "cards before it, or needs of its own, still open", sentinelBlocked},
		},
	},
	{
		Name: "a sentinel, reached",
		Match: func(v *holdView, c *Card) bool {
			return IsSentinel(c) && c.Col == Waiting && v.reached(c)
		},
		Holders: []holder{
			{HeldByJudgment, "sentinel reached, open or held", onCard(NSentinelReached)},
			{HeldByTick, "R3, reach", reachRaises},
		},
	},
	{
		Name:  "waiting, open > 0",
		Cause: "waiting",
		Stall: "it waits on needs that are not open on the table, nor missing with a judgment, nor landed or removed and still counted for R4, and no blocked judgment names it",
		Match: func(v *holdView, c *Card) bool {
			return !IsSentinel(c) && c.Col == Waiting && c.Int("open") > 0
		},
		Holders: []holder{
			{HeldByWaiting, "each counted need is open on the table, or missing with its judgment", needsHeld},
			{HeldByTick, "R4, a landed or removed need still counted", needsPending},
			{HeldByJudgment, "a blocked judgment on it, open or held", onCard(NBlocked, NMissingNeed)},
		},
	},
	{
		Name: "waiting, open = 0, behind the first sentinel",
		Match: func(v *holdView, c *Card) bool {
			return !IsSentinel(c) && c.Col == Waiting && v.behindSigma(c)
		},
		Holders: []holder{
			{HeldByWaiting, "the first sentinel, open", behindSentinel},
		},
	},
	{
		Name: "waiting, open = 0, below the first sentinel or none (in elig)",
		Match: func(v *holdView, c *Card) bool {
			return !IsSentinel(c) && c.Col == Waiting
		},
		Holders: []holder{
			{HeldByTick, "R3, release", func(v *holdView, c *Card) string { return "in elig" }},
		},
	},
	{
		Name: "ready, in fresh below the first sentinel or in again",
		Match: func(v *holdView, c *Card) bool {
			return c.Col == Ready && !IsSentinel(c) && (c.Int("attempt") >= 1 || !v.behindSigma(c))
		},
		Holders: []holder{
			{HeldByTick, "R6, room at an up member", dealRoom},
			{HeldByWaiting, "no room: every up member's ready cell is full", dealNoRoom},
			{HeldByJudgment, "no fleet member is up, open or held", noMemberJudged},
			{HeldByTick, "R6, no member is up and it says so", dealNoMember},
		},
	},
	{
		Name: "ready, in fresh behind the first sentinel",
		Match: func(v *holdView, c *Card) bool {
			return c.Col == Ready && !IsSentinel(c) && c.Int("attempt") == 0 && v.behindSigma(c)
		},
		Holders: []holder{
			{HeldByTick, "R19, the pull back", func(v *holdView, c *Card) string { return "behind " + v.front(c.Row).sigma.ID }},
		},
	},
	{
		Name:      "working",
		Cause:     "working",
		Stall:     "no live work card of an up member holds it before its deadline, its member is not down, its deadline has not passed, and no judgment names it",
		Decisions: []string{"fleet down <member>"},
		Match:     func(v *holdView, c *Card) bool { return c.Col == Working },
		Holders: []holder{
			{HeldByActor, "its live work card at an up member, before its due", workActor},
			{HeldByTick, "R2, its member is down or held with its card", workMemberGone},
			{HeldByTick, "R11, its due has passed", workDuePassed},
			{HeldByJudgment, "its lateness judgment, open or held", workJudged},
		},
	},
	{
		Name:      "review",
		Cause:     "review",
		Stall:     "nothing is outstanding, asking, accepting or reworking it, and no judgment names it",
		Decisions: []string{"rework --fix"},
		Match:     func(v *holdView, c *Card) bool { return c.Col == Review },
		Holders: []holder{
			{HeldByActor, "a read outstanding before its due", readOutstanding},
			{HeldByTick, "R8, never asked at its attempt", reviewAsks},
			{HeldByTick, "R9, two different readers said ok", reviewAccepts},
			{HeldByTick, "R10, failed or broken", reviewReworks},
			{HeldByJudgment, "a judgment on it, open or held", onCard(NCannotAsk, NCIRed, NReturned, NBound, NReadsExhausted, NStranded)},
		},
	},
	{
		Name:      "merging",
		Cause:     "merging",
		Stall:     "its merge card is not queued in a stream that merges before its deadline, its stream's cross need has not landed, and no judgment names it or its stream",
		Decisions: []string{"return"},
		Match:     func(v *holdView, c *Card) bool { return c.Col == Merging },
		Holders: []holder{
			{HeldByActor, "queued in a stream merging or waiting, before its merge-idle due", mergeQueued},
			{HeldByTick, "R5, its cross need landed", crossLanded},
			{HeldByJudgment, "its stream's stop or merge-idle judgment, open or held", mergeJudged},
		},
	},
	{
		Name:  "a place no row of the table gives",
		Cause: "place",
		Stall: "no rule and no judgment of the design holds a primary in this place",
		Match: func(*holdView, *Card) bool { return true },
	},
}

// stoppedRow is the last row of the table: any primary, while the machine is
// STOPPED, is held by the machine's own state, and R17 names it when moves are
// due. It takes the place of (b), which is the same wait for a running
// machine, and of nothing held: a stall a STOPPED machine cannot name is named
// the first tick after start.
var stoppedRow = holdRow{
	Name: "any, the machine STOPPED",
	Holders: []holder{
		{HeldByStopped, "R17 names it when moves are due", func(*holdView, *Card) string {
			return "the machine is STOPPED"
		}},
	},
}

// heldJudgment is a judgment open, or held by a wait, on one subject.
type heldJudgment struct {
	Type string
	Held bool
}

// streamFront is front(s) of one stream as R16 uses it (1.0): the first
// sentinel sigma, and n_before, the open cards of the stream before it.
type streamFront struct {
	sigma  *Card
	before int
}

// holdView is a snapshot read for the local holders of any number of its
// cards: what is the same for every card is computed once, a fleet's room, the
// judgments open and each stream's front.
type holdView struct {
	s   *Snapshot
	f   HeldFacts
	now Now

	up     []string // the members whose status is up, in row order
	room   int      // the free places in the up members' ready cells
	judged map[string][]heldJudgment
	typed  map[string]bool // a judgment of the type is open or held on some subject
	fronts map[string]*streamFront
	needs  map[string]needSummary
}

// needSummary is a waiting primary's needs as its state shows them (2.3 R16,
// the table's first row).
type needSummary struct {
	open, missing, gone, landed []string
	unjudged                    []string // missing needs no judgment names
	// pending is how many needs the card still counts beyond those it can
	// account for: open is exact (I2), so what it counts above the needs open
	// on the table, the needs missing and the blocked judgments open is
	// landed or removed needs still in wait:n, which R4 has yet to lower.
	pending int
}

func newHoldView(s *Snapshot, f HeldFacts, now Now) *holdView {
	v := &holdView{s: s, f: f, now: now, judged: map[string][]heldJudgment{}, typed: map[string]bool{},
		fronts: map[string]*streamFront{}, needs: map[string]needSummary{}}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment {
			v.add(o, false)
		}
	}
	for _, o := range f.Held {
		v.add(o, true)
	}
	if s.Fleet != nil {
		for _, m := range s.Fleet.Rows {
			if s.MemberCtl(m).F("status") == Up {
				v.up = append(v.up, m)
				v.room += max(0, MaxReadyPerMember-s.Fleet.Count(m, Ready))
			}
		}
	}
	return v
}

func (v *holdView) add(o Open, held bool) {
	v.judged[o.Subject()] = append(v.judged[o.Subject()], heldJudgment{Type: o.Note.Type, Held: held})
	v.typed[o.Note.Type] = true
}

// LocalHolder is what holds the primary id now, on the state alone (2.3 R16,
// "The local holder of a card"): the design's Holder. The tree's Holder is the
// scanning tick's and keeps the name until it retires (IT28); this is the
// function the held rule, the check and the card verb call on the event
// machine's partial snapshot. A card that is not a primary is judged by its
// primary. Cost: O(f + j + streams touched) to read the snapshot, then O(1)
// reads a card; a caller with many cards judges them through one holdView.
func LocalHolder(s *Snapshot, id string, now Now) Hold {
	return newHoldView(s, heldFactsOf(s), now).hold(id)
}

// verdict is what holds one primary, and the row it was judged by.
type verdict struct {
	Hold
	row *holdRow
}

func (v *holdView) hold(id string) Hold { return v.verdict(id).Hold }

// primaryOf is the primary a card is judged as: the card itself when it is one,
// else the primary its record names (a work, read or merge card).
func (v *holdView) primaryOf(id string) *Card {
	if c := v.s.Work.Card(id); c != nil {
		return c
	}
	for _, t := range []*Table{v.s.Fleet, v.s.Readers, v.s.Merge} {
		if c := t.Card(id); c != nil {
			if p := c.F("primary"); p != "" {
				return v.s.Work.Card(p)
			}
		}
	}
	return nil
}

func (v *holdView) verdict(id string) verdict {
	c := v.primaryOf(id)
	switch {
	case c == nil:
		return verdict{Hold: Hold{ID: id, Place: id, Why: "not on the table"}}
	case !c.Placed():
		return verdict{Hold: Hold{ID: c.ID, By: HeldDone, Place: c.ID + " off the table (" + orDash(c.F("outcome")) + ")"}}
	case c.Col == Landed:
		return verdict{Hold: Hold{ID: c.ID, By: HeldDone, Place: c.ID + " landed"}}
	}
	place := c.ID + " " + c.Col
	if IsSentinel(c) {
		place = "sentinel " + place
	}
	for i := range holdRows {
		row := &holdRows[i]
		if !row.Match(v, c) {
			continue
		}
		hd := Hold{ID: c.ID, Place: place}
		for _, h := range row.Holders {
			why := h.Holds(v, c)
			if why == "" {
				continue
			}
			hd.By, hd.Why = h.By, h.What+": "+why
			if hd.By == HeldByTick && !v.now.Running {
				hd.By, hd.Why = HeldByStopped, "the machine is STOPPED; "+hd.Why
			}
			return verdict{Hold: hd, row: row}
		}
		if !v.now.Running {
			hd.By, hd.Why = HeldByStopped, stoppedRow.Holders[0].What+": "+stoppedRow.Holders[0].Holds(v, c)
			return verdict{Hold: hd, row: row}
		}
		hd.Why = row.Stall
		if hd.Why == "" {
			hd.Why = "no holder of the row held it: " + row.Name
		}
		return verdict{Hold: hd, row: row}
	}
	return verdict{Hold: Hold{ID: c.ID, Place: place, Why: "no row matched"}}
}

// judgment is why a judgment of one of the types is open or held on the
// subject; "" and false when none is.
func (v *holdView) judgment(subject string, types ...string) (string, bool) {
	for _, j := range v.judged[subject] {
		for _, t := range types {
			if j.Type != t {
				continue
			}
			if j.Held {
				return "held: " + t, true
			}
			return "open: " + t, true
		}
	}
	return "", false
}

// count is the judgments of the type open on the subject: a held one is not
// counted, since a hold closes the judgment.
func (v *holdView) count(subject, typ string) int {
	n := 0
	for _, j := range v.judged[subject] {
		if j.Type == typ && !j.Held {
			n++
		}
	}
	return n
}

// front is front(s) of the stream, read once.
func (v *holdView) front(stream string) *streamFront {
	if f, ok := v.fronts[stream]; ok {
		return f
	}
	f := &streamFront{}
	if g := FirstSentinel(v.s, stream); g != nil {
		f.sigma, f.before = g, OpenBefore(v.s, stream, g.Score)
	}
	v.fronts[stream] = f
	return f
}

// behindSigma says the card sorts after the first sentinel of its stream.
func (v *holdView) behindSigma(c *Card) bool {
	g := v.front(c.Row).sigma
	return g != nil && c.Score > g.Score
}

// nBefore is n_before of a sentinel: the open cards of its stream before it.
// front(s) gives it for the first sentinel; any later one has that one before
// it, so at least one.
func (v *holdView) nBefore(c *Card) int {
	f := v.front(c.Row)
	if f.sigma != nil && f.sigma.ID == c.ID {
		return f.before
	}
	return 1
}

// reached says a sentinel has no open card before it and no need of its own:
// its stream has come to it (R3, reach).
func (v *holdView) reached(c *Card) bool { return c.Int("open") == 0 && v.nBefore(c) == 0 }

// reads is the primary's read cards at its attempt, from rcards (1.3.1), in
// the order rcards names them.
func (v *holdView) reads(p *Card) []*Card {
	var out []*Card
	attempt := p.Int("attempt")
	for _, id := range Split(p.F("rcards")) {
		if _, a, _, ok := ParseReadCard(id); !ok || a != attempt {
			continue
		}
		if c := v.s.Readers.Placed(id); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// workCard is the primary's live work card: the one it names, or the one its
// attempt's identity gives, when it is a card of a member's ready or working
// cell that names the primary.
func (v *holdView) workCard(p *Card) *Card {
	id := p.F("work")
	if id == "" {
		id = WorkCardID(p.ID, p.Int("attempt"))
	}
	wc := v.s.Fleet.Placed(id)
	if wc == nil || wc.F("primary") != p.ID || wc.Col != Ready && wc.Col != Working {
		return nil
	}
	return wc
}

// workDue is when a work card's deadline falls, in running ms: its untaken
// deadline in a member's ready cell and its unfinished one in the working cell
// (1.2). false when the card has none.
func workDue(wc *Card) (int64, bool) {
	if wc.Col == Working {
		return dueOf(wc, fieldDueUnfinished)
	}
	return dueOf(wc, fieldDueUntaken)
}

// dueOf is a due field of a card, in running ms; false when it is not set.
func dueOf(c *Card, field string) (int64, bool) {
	n, err := strconv.ParseInt(c.F(field), 10, 64)
	return n, err == nil
}

// needSummary reads a waiting primary's needs (1.3.1, 1.3.3), once.
func (v *holdView) needSummary(c *Card) needSummary {
	if ns, ok := v.needs[c.ID]; ok {
		return ns
	}
	var ns needSummary
	waived := Split(c.F("waived"))
	named := Split(c.F("needs"))
	for i, n := range named {
		if contains(waived, n) || contains(named[:i], n) {
			continue
		}
		switch nc := v.s.Work.Card(n); {
		case nc == nil:
			ns.missing = append(ns.missing, n)
			if _, ok := v.judgment(c.ID, NMissingNeed); !ok {
				ns.unjudged = append(ns.unjudged, n)
			}
		case !nc.Placed():
			ns.gone = append(ns.gone, n)
		case nc.Col == Landed:
			ns.landed = append(ns.landed, n)
		default:
			ns.open = append(ns.open, n)
		}
	}
	ns.pending = c.Int("open") - len(ns.open) - len(ns.missing) - v.count(c.ID, NBlocked)
	v.needs[c.ID] = ns
	return ns
}

// onCard is a holder that holds while a judgment of one of the types is open
// or held on the card.
func onCard(types ...string) func(*holdView, *Card) string {
	return func(v *holdView, c *Card) string {
		why, _ := v.judgment(c.ID, types...)
		return why
	}
}

// cutClock holds a card of a stream being dropped while the op has a cut entry:
// R11 names the op when the entry is due (1.3.5, 2.3 R11).
func cutClock(v *holdView, c *Card) string {
	op := v.f.Dropping[c.Row]
	at, ok := v.f.Cut[op]
	if !ok {
		return ""
	}
	if v.now.Wall >= at {
		return "op " + op + "'s cut clock is due"
	}
	return "op " + op + "'s cut clock is due at wall " + strconv.FormatInt(at, 10)
}

// opJudged holds a card of a stream being dropped while a judgment on its op
// is open or held.
func opJudged(v *holdView, c *Card) string {
	why, _ := v.judgment(v.f.Dropping[c.Row], typeVerbStopped)
	return why
}

// sentinelBlocked is (d) of a sentinel not yet reached: the cards before it
// and its own needs.
func sentinelBlocked(v *holdView, c *Card) string {
	return fmt.Sprintf("n_before %d, open %d", v.nBefore(c), c.Int("open"))
}

// reachRaises is R3's reach: a reached sentinel with no judgment open on it is
// one R3 raises (2.3 R3, effect 2), unless it is quarantined.
func reachRaises(v *holdView, c *Card) string {
	if v.f.Quarantined[c.ID] {
		return ""
	}
	return "n_before 0, open 0"
}

// needsHeld is (d) of a waiting primary: it counts a need, and every need it
// counts is open on the table (held on its own by NothingSilent) or missing
// with the judgment that names it.
func needsHeld(v *holdView, c *Card) string {
	ns := v.needSummary(c)
	if len(ns.open)+len(ns.missing) == 0 || len(ns.unjudged) > 0 {
		return ""
	}
	return fmt.Sprintf("%d open on the table, %d missing", len(ns.open), len(ns.missing))
}

// needsPending is R4's condition on a waiting primary: it counts a need that
// has landed or been removed.
func needsPending(v *holdView, c *Card) string {
	if p := v.needSummary(c).pending; p > 0 {
		return strconv.Itoa(p) + " still counted"
	}
	return ""
}

// behindSentinel is (d) of a waiting primary below no need, behind the first
// sentinel of its stream, which is open by being in waiting.
func behindSentinel(v *holdView, c *Card) string {
	return "behind " + v.front(c.Row).sigma.ID
}

// dealRoom is R6 with room: a ready primary that is dealable while an up
// member has a free place.
func dealRoom(v *holdView, c *Card) string {
	if len(v.up) == 0 || v.room == 0 {
		return ""
	}
	return fmt.Sprintf("%d free places at %d up members", v.room, len(v.up))
}

// dealNoRoom is (d) of a ready primary when every up member's ready cell is
// full: it waits for a place to free, and freeing one queues deal.
func dealNoRoom(v *holdView, c *Card) string {
	if len(v.up) == 0 || v.room > 0 {
		return ""
	}
	return fmt.Sprintf("%d up members, each with %d ready", len(v.up), MaxReadyPerMember)
}

// noMemberJudged is (c) of a ready primary when no member is up: the
// judgment R6 raises, once, for the whole sprint.
func noMemberJudged(v *holdView, c *Card) string {
	if len(v.up) > 0 || !v.typed[NNoMember] {
		return ""
	}
	return "no fleet member is up"
}

// dealNoMember is R6's other condition: no member is up and a card is dealable,
// so R6 raises the judgment (2.3 R6, effect).
func dealNoMember(v *holdView, c *Card) string {
	if len(v.up) > 0 {
		return ""
	}
	return "no member is up"
}

// workActor is (a) of a working primary: its work card is in the ready or
// working cell of an up member and its deadline has not come.
func workActor(v *holdView, c *Card) string {
	wc := v.workCard(c)
	if wc == nil || v.s.MemberCtl(wc.Row).F("status") != Up {
		return ""
	}
	if due, ok := workDue(wc); ok && v.now.R < due {
		return fmt.Sprintf("member %s holds %s@%s (%s) until R %d", wc.Row, wc.ID, wc.F("gen"), wc.Col, due)
	}
	return ""
}

// workMemberGone is R2's condition on a working primary: its work card sits at
// a member that is down or held.
func workMemberGone(v *holdView, c *Card) string {
	wc := v.workCard(c)
	if wc == nil {
		return ""
	}
	if st := v.s.MemberCtl(wc.Row).F("status"); st == Down || st == Held {
		return "member " + wc.Row + " is " + st
	}
	return ""
}

// workDuePassed is R11's condition on a working primary: the deadline of its
// work card has come.
func workDuePassed(v *holdView, c *Card) string {
	wc := v.workCard(c)
	if wc == nil {
		return ""
	}
	if due, ok := workDue(wc); ok && v.now.R >= due {
		return fmt.Sprintf("%s was due at R %d", wc.ID, due)
	}
	return ""
}

// workJudged is (c) of a working primary: the lateness judgment on its work
// card, open or held.
func workJudged(v *holdView, c *Card) string {
	id := c.F("work")
	if id == "" {
		id = WorkCardID(c.ID, c.Int("attempt"))
	}
	why, _ := v.judgment(id, NWorkLate)
	return why
}

// readOutstanding is (a) of a primary in review: a read at its attempt asked
// and not begun, or begun and not reported, before its deadline.
func readOutstanding(v *holdView, c *Card) string {
	for _, r := range v.reads(c) {
		field := fieldDueUnbegun
		if r.Col == Reading {
			field = fieldDueUnreported
		} else if r.Col != Asked {
			continue
		}
		if due, ok := dueOf(r, field); ok && ReadCardAgrees(r) && v.now.R < due {
			return fmt.Sprintf("reader %s holds %s (%s) until R %d", r.Row, r.ID, r.Col, due)
		}
	}
	return ""
}

// reviewAsks is R8's condition: a primary in review whose work came back ok
// and that was never asked at its attempt.
func reviewAsks(v *holdView, c *Card) string {
	if c.F("result") != "ok" || len(v.reads(c)) > 0 {
		return ""
	}
	return "attempt " + strconv.Itoa(c.Int("attempt")) + ", no read cards"
}

// reviewAccepts is R9's condition: ok reads from two different readers at its
// head, its CI not red at its head, and it is not returned to review (whose
// judgment the coordinator decides).
func reviewAccepts(v *holdView, c *Card) string {
	head := c.F("head")
	readers := map[string]bool{}
	for _, r := range v.reads(c) {
		if r.Col == OK && r.F("head") == head && ReadCardAgrees(r) {
			readers[r.Row] = true
		}
	}
	if len(readers) < 2 || c.F("ci") == "red" && c.F("ci_head") == head {
		return ""
	}
	if _, ok := v.judgment(c.ID, NReturned); ok {
		return ""
	}
	return "ok at " + orDash(head)
}

// reviewReworks is R10's condition: its work came back failed, or a read at its
// attempt is broken. R10 reworks it below heldAttemptBound and sets its bound
// at it, so it is R10's either way.
func reviewReworks(v *holdView, c *Card) string {
	attempt := c.Int("attempt")
	broken := false
	for _, r := range v.reads(c) {
		broken = broken || r.Col == Broken
	}
	if c.F("result") != "failed" && !broken {
		return ""
	}
	if attempt >= heldAttemptBound {
		return "attempt " + strconv.Itoa(attempt) + " sets its bound"
	}
	return "attempt " + strconv.Itoa(attempt) + " is reworked"
}

// mergeQueued is (a) of a merging primary: its merge card is queued in a stream
// merging or waiting, before the stream's merge-idle deadline.
func mergeQueued(v *holdView, c *Card) string {
	m := v.s.Merge.Placed(c.ID)
	if m == nil || m.Row != c.Row || m.Col != Queued {
		return ""
	}
	ctl := v.s.StreamCtl(c.Row)
	state := ctl.F("state")
	if state != StreamMerging && state != StreamWaiting {
		return ""
	}
	if due, ok := dueOf(ctl, fieldDueMergeIdle); ok && v.now.R < due {
		return fmt.Sprintf("queued in stream %s (%s) until R %d", c.Row, state, due)
	}
	return ""
}

// crossLanded is R5's condition on a merging primary: its stream stopped on a
// card of another stream, and that card has landed. The stream's control card
// names it (need_card, and other where the merge step writes it).
func crossLanded(v *holdView, c *Card) string {
	ctl := v.s.StreamCtl(c.Row)
	if ctl.F("state") != StreamStopped || ctl.F("cause") != crossStop {
		return ""
	}
	need := ctl.F("need_card")
	if need == "" {
		need = ctl.F("other")
	}
	if nc := v.s.Work.Placed(need); need != "" && nc != nil && nc.Col == Landed {
		return need + " landed"
	}
	return ""
}

// mergeJudged is (c) of a merging primary: the stop judgment of its stream
// while it is stopped, or its merge-idle judgment, open or held.
func mergeJudged(v *holdView, c *Card) string {
	subject := StreamSubject(c.Row)
	if v.s.StreamCtl(c.Row).F("state") == StreamStopped {
		if why, ok := v.judgment(subject, NConflict, NRed, NCross, NRejected); ok {
			return "stream " + c.Row + " stopped, " + why
		}
	}
	why, _ := v.judgment(subject, NMergeLate)
	return why
}

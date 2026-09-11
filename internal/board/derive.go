/*
derive.go is events in, cards out. Nothing about a card is stored: the state, the owner,
the counts and staleness are all derived from the log on every read, which is why the
board can say the same thing to every line that reads it.

THE FOLD ORDER IS CAUSAL FIRST, THEN TOTAL, AND IT IS THE SAME IN EVERY CLONE. The after=
edges form a directed acyclic graph rooted at the card event, and the fold walks it
topologically: every predecessor before every event that names it. Among events that are
eligible at one step — and eligible events are exactly the ones with no after= path
between them, which is what concurrent means — the one that goes first is the smallest by
the stable total key (at, as, id, verb, the line's bytes). The key is never applied across
a causal edge: an event whose writer SAW another event folds after it whatever its clock
says, because a clock behind is a clock and not a cause.

Two clones that merged the same concurrent appends in opposite textual orders therefore
derive the same owner, and so do the two backends, which is the property the whole tool
rests on. Never by the order the lines appear in a file or a comment thread.

WHAT IS COUNTED RATHER THAN HIDDEN. Two events with the same after= are concurrent: the
fold chooses one and counts the pair in conflicts=, so a take that won a tie is visible as
a tie rather than as the only take. Two card lines with one id and different fields are a
conflict and neither silently wins. An event whose after= names no event of this card, one
naming an event of another card, and one on a cycle are QUARANTINED: not folded, counted,
named once, and never guessed at.

STALENESS IS THE ONLY CLOCK-DEPENDENT DERIVATION HERE, and it is an annotation made at
read time from the take's own stamp. The board writes nothing for it: it has no way to
know whether a line is silent, it knows only when the take was written.
*/
package board

import (
	"sort"
	"time"
)

// Card is one thing owed, as the log says it stands right now.
type Card struct {
	ID       string
	Text     string
	Hash     string
	Owner    string // the latest take in the fold order, else the add's --owner, else the filer
	Filer    string
	By       string // the deadline as the add stored it
	Default  string
	Thing    string
	Leg      string
	Evidence string

	Since    time.Time
	SinceRaw string
	State    string // OPEN or CLOSED, derived and stored nowhere
	Close    *Event // the FIRST close in the fold order; the later ones stay in the log

	LatestAt time.Time
	TakenAt  time.Time
	HasTake  bool

	Conflicts   int
	Quarantined int
	Conflict    bool
	Stale       bool
	Overdue     bool
	Row         bool
	Probed      bool

	Events []Event // the card's events in the fold order, quarantined ones excluded
}

// Open reports whether this card is still owed.
func (c *Card) Open() bool { return c.State == "OPEN" }

// Owed reports whether this card is a row of the owed ledger that has not been probed.
func (c *Card) Owed() bool { return c.Row && c.Open() }

// Log is what a backend hands back: the event lines it holds, and how many files in it
// were not cards at all. The second number is here rather than behind a type assertion so
// that nothing above the Backend interface learns which backend it has.
type Log struct {
	Lines         []string
	UnparsedFiles int
}

// Board is the whole fold.
type Board struct {
	Cards            []*Card
	Unparsed         int
	UnparsedFiles    int
	Quarantined      int
	Conflicts        int
	FirstQuarantined string
	Orphans          int // events naming a card no card event creates
}

// Derive folds a log into cards. now and stale are the two clock inputs, injected rather
// than read, so that every derivation in the tests is a pure function of its arguments.
func Derive(log Log, now time.Time, stale time.Duration) *Board {
	b := &Board{UnparsedFiles: log.UnparsedFiles}

	// A union merge can hold one line twice, and two events equal in all five keys of the
	// total order ARE one line. Fold it once.
	seen := map[string]bool{}
	byCard := map[string][]Event{}
	var order []string
	for _, line := range log.Lines {
		e, ok := Parse(line)
		if !ok {
			if trimmed := trimmedNonBlank(line); trimmed {
				b.Unparsed++
			}
			continue
		}
		if !e.AtOK {
			// A stamp this tool cannot parse is never guessed at: the event folds last
			// and is counted, so a reader learns that the board holds one.
			b.Unparsed++
		}
		if seen[e.Line] {
			continue
		}
		seen[e.Line] = true
		if _, ok := byCard[e.ID]; !ok {
			order = append(order, e.ID)
		}
		byCard[e.ID] = append(byCard[e.ID], e)
	}

	for _, id := range order {
		card := foldCard(id, byCard[id], b, now, stale)
		if card != nil {
			b.Cards = append(b.Cards, card)
		}
	}
	sort.SliceStable(b.Cards, func(i, j int) bool {
		if !b.Cards[i].Since.Equal(b.Cards[j].Since) {
			return b.Cards[i].Since.Before(b.Cards[j].Since)
		}
		return b.Cards[i].ID < b.Cards[j].ID
	})
	return b
}

// trimmedNonBlank reports whether a line is something a reader would expect to count: a
// blank line and a # comment are ignored everywhere in this family's files.
func trimmedNonBlank(line string) bool {
	for _, r := range line {
		switch r {
		case ' ', '\t', '\r', '\n':
			continue
		case '#':
			return false
		default:
			return true
		}
	}
	return false
}

// foldCard is the walk for one card's events.
func foldCard(id string, events []Event, b *Board, now time.Time, stale time.Duration) *Card {
	var roots []Event
	var later []Event
	for _, e := range events {
		if e.Verb == "card" {
			roots = append(roots, e)
		} else {
			later = append(later, e)
		}
	}
	if len(roots) == 0 {
		// Events about a card nothing created. They are not folded and not guessed at.
		b.Orphans += len(later)
		b.Quarantined += len(later)
		if b.FirstQuarantined == "" && len(later) > 0 {
			b.FirstQuarantined = later[0].Ev
		}
		return nil
	}

	sortByKey(roots)
	card := &Card{
		ID: id, Text: roots[0].Tail, Hash: roots[0].Hash, Owner: roots[0].Owner,
		Filer: roots[0].As, By: roots[0].By, Default: roots[0].Default,
		Thing: roots[0].Thing, Leg: roots[0].Leg, Evidence: roots[0].Evidence,
		Since: roots[0].At, SinceRaw: roots[0].AtRaw, State: "OPEN",
		LatestAt: roots[0].At,
	}
	card.Row = card.Thing != "" && card.Leg != ""
	if len(roots) > 1 {
		// A differing payload under one identity is a conflict, never one card silently
		// winning. The counting happens with every other concurrent pair below.
		for _, r := range roots[1:] {
			if r.CreationFields() != roots[0].CreationFields() {
				card.Conflict = true
			}
		}
	}

	// The after= graph. An edge that names no event of this card, or an event of another
	// card, is quarantined here; one on a cycle is quarantined by the walk below.
	byEv := map[string]int{}
	for i, e := range later {
		if e.Ev != "" {
			byEv[e.Ev] = i
		}
	}
	live := make([]bool, len(later))
	pred := make([]int, len(later))
	for i, e := range later {
		switch {
		case e.After == id:
			live[i], pred[i] = true, -1
		case e.After == e.Ev:
			live[i] = false
		default:
			if j, ok := byEv[e.After]; ok {
				live[i], pred[i] = true, j
			}
		}
	}

	// Kahn's walk with the total key as the tie-break among eligible events.
	emitted := make([]bool, len(later))
	folded := append([]Event(nil), roots...)
	for {
		best := -1
		for i, e := range later {
			if !live[i] || emitted[i] {
				continue
			}
			if pred[i] >= 0 && !emitted[pred[i]] {
				continue
			}
			if best < 0 || lessByKey(e, later[best]) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		emitted[best] = true
		folded = append(folded, later[best])
	}
	for i := range later {
		if !emitted[i] {
			live[i] = false
		}
	}

	for i, e := range later {
		if !live[i] {
			card.Quarantined++
			b.Quarantined++
			if b.FirstQuarantined == "" {
				b.FirstQuarantined = e.Ev
			}
		}
	}
	if card.Quarantined > 0 {
		card.Conflict = true
	}

	// Concurrency: two events of this card with the same predecessor are concurrent, and
	// every such pair is counted. The card events share the empty predecessor.
	groups := map[string]int{}
	for _, r := range roots {
		_ = r
		groups[""]++
	}
	for i, e := range later {
		if live[i] {
			groups[e.After]++
		}
	}
	for _, n := range groups {
		card.Conflicts += n * (n - 1) / 2
	}
	b.Conflicts += card.Conflicts

	for _, e := range folded[1:] {
		if e.AtOK && e.At.After(card.LatestAt) {
			card.LatestAt = e.At
		}
		switch {
		case e.Verb == "taken":
			card.Owner, card.TakenAt, card.HasTake = e.As, e.At, true
		case Closing(e.Verb) && card.Close == nil:
			closing := e
			card.Close = &closing
			card.State = "CLOSED"
			card.Probed = e.Verb == "probed"
		}
	}
	card.Events = folded

	if card.Open() {
		card.Stale = stale > 0 && now.Sub(card.LatestAt) > stale
		if by, err := time.Parse(time.RFC3339, card.By); err == nil {
			card.Overdue = now.After(by.UTC())
		}
	}
	return card
}

// lessByKey is the documented total order: (at, as, id, verb, the line's bytes). An event
// whose stamp will not parse folds LAST rather than being guessed at.
func lessByKey(a, b Event) bool {
	if a.AtOK != b.AtOK {
		return a.AtOK
	}
	if a.AtOK && !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	if a.As != b.As {
		return a.As < b.As
	}
	if ka, kb := keyID(a), keyID(b); ka != kb {
		return ka < kb
	}
	if a.Verb != b.Verb {
		return a.Verb < b.Verb
	}
	return a.Line < b.Line
}

// keyID is the event's own id where it has one, and the card's where it does not.
func keyID(e Event) string {
	if e.Ev != "" {
		return e.Ev
	}
	return e.ID
}

func sortByKey(events []Event) {
	sort.SliceStable(events, func(i, j int) bool { return lessByKey(events[i], events[j]) })
}

// Counts are the nine facts BOARD OK carries. They are the truth about the BOARD and
// never about the output: the listing is capped, the counting never is.
type Counts struct {
	Cards, Open, Closed, Stale, Overdue, Owed, Lines, Conflicts, Quarantined int
}

// Counts sums the board.
func (b *Board) Counts() Counts {
	c := Counts{Cards: len(b.Cards), Conflicts: b.Conflicts, Quarantined: b.Quarantined}
	owners := map[string]bool{}
	for _, card := range b.Cards {
		if card.Open() {
			c.Open++
			owners[card.Owner] = true
			if card.Stale {
				c.Stale++
			}
			if card.Overdue {
				c.Overdue++
			}
			if card.Row {
				c.Owed++
			}
		} else {
			c.Closed++
		}
	}
	c.Lines = len(owners)
	return c
}

// Line is one owner's open work: the BOARD LINE row.
type Line struct {
	Name                 string
	Open, Overdue, Stale int
}

// Lines returns one row per owner of an open card, in the order the cards are in, so the
// output is deterministic without being alphabetised into a shape the fold did not choose.
func (b *Board) Lines() []Line {
	var out []Line
	at := map[string]int{}
	for _, card := range b.Cards {
		if !card.Open() {
			continue
		}
		i, ok := at[card.Owner]
		if !ok {
			out = append(out, Line{Name: card.Owner})
			i = len(out) - 1
			at[card.Owner] = i
		}
		out[i].Open++
		if card.Overdue {
			out[i].Overdue++
		}
		if card.Stale {
			out[i].Stale++
		}
	}
	return out
}

// Leg is one leg of the owed ledger: the BOARD LEG row. Done means owed is zero, and the
// tool prints that number rather than a word.
type Leg struct {
	Name         string
	Owed, Probed int
}

// Legs returns one row per leg the board holds rows on, in first-seen order.
func (b *Board) Legs() []Leg {
	var out []Leg
	at := map[string]int{}
	for _, card := range b.Cards {
		if !card.Row {
			continue
		}
		i, ok := at[card.Leg]
		if !ok {
			out = append(out, Leg{Name: card.Leg})
			i = len(out) - 1
			at[card.Leg] = i
		}
		if card.Open() {
			out[i].Owed++
		} else if card.Probed {
			out[i].Probed++
		}
	}
	return out
}

// Card returns the card with this id.
func (b *Board) Card(id string) *Card {
	for _, c := range b.Cards {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Next is the one thing to do first, by a fixed order: the oldest overdue card, else the
// oldest stale card, else the leg with the most owed rows, else the oldest ordinary OPEN
// card, else — and only when open is zero — nothing owed. A board with one fresh card and
// a future deadline HAS something to do first, and a tool that said "nothing owed" over it
// would be the count falling for the wrong reason, said in words.
func (b *Board) Next() (kind string, card *Card, leg string) {
	if c := b.oldest(func(c *Card) bool { return c.Open() && c.Overdue }); c != nil {
		return "overdue", c, ""
	}
	if c := b.oldest(func(c *Card) bool { return c.Open() && c.Stale }); c != nil {
		return "stale", c, ""
	}
	best := Leg{}
	for _, l := range b.Legs() {
		if l.Owed > best.Owed || (l.Owed == best.Owed && best.Name != "" && l.Owed > 0 && l.Name < best.Name) {
			best = l
		}
	}
	if best.Owed > 0 {
		return "leg", nil, best.Name
	}
	if c := b.oldest(func(c *Card) bool { return c.Open() }); c != nil {
		return "open", c, ""
	}
	return "nothing", nil, ""
}

// oldest is by since, and a tie at the same second is the lexically smaller id. The cards
// are already in that order.
func (b *Board) oldest(want func(*Card) bool) *Card {
	for _, c := range b.Cards {
		if want(c) {
			return c
		}
	}
	return nil
}

// After is the ev a writer appending to this card names as what it saw: the newest event
// in ITS OWN fold, and the card's id when there was none. A writer never names an event
// its fold does not hold — a quarantined event is not in the fold, so it is never named.
func (c *Card) After() string {
	for i := len(c.Events) - 1; i >= 0; i-- {
		if c.Events[i].Ev != "" {
			return c.Events[i].Ev
		}
	}
	return c.ID
}

// Holds reports whether this fold holds the event an after= names. take, close, land and
// probe refuse to append an after= their own fold does not hold.
func (c *Card) Holds(ev string) bool {
	if ev == c.ID {
		return true
	}
	for _, e := range c.Events {
		if e.Ev == ev {
			return true
		}
	}
	return false
}

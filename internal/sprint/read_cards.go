package sprint

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"
)

// A read is a consumer card (docs/SPEC-SPRINT.md section 6, "A read is a consumer card";
// the owner, 2026-10-06: "reads need to become a type of card"; "send out multiple consumer
// cards in ||"; "Just remove the complexity. just deal it."). While the sprint's read_cards
// setting is on (set --read-cards on), the tick's deal deals every read a primary in review
// still needs AT ONCE, before its work cards, each a read card on the fleet table cut and
// dealt in one step to a different reader: a friend whose roles name reader and whose tiers
// hold the read's tier, or a member up whose reader row (reader-<m>) is neither held nor
// retired and serves the tier; never the attempt's own worker, never a reader that holds or
// closed a read of the attempt (a read the machine took back spends nothing). A read holds
// half a slot of its unit's one width (halfLoad). Its tier is its primary's, its level
// inherited (ReadPriority). The review is unchanged: a read card's verdict closes the read on
// the primary as read --ok|--broken does (friendReadCloseUnit, readCardVerb), and the primary
// leaves review only by the existing rules. The model is tla/ReadCards.tla. With the setting
// off, the readers table's ask and the friends' read ask, as before; they retire next release.

// PropReadCards is the work table's property that turns read cards on: ReadCardsOnWord.
const PropReadCards = "read_cards"

// ReadCardsOnWord is the setting's on word; any other value is off.
const ReadCardsOnWord = "on"

// ReadCardsOn says the sprint asks its reads as read cards (PropReadCards).
func (s *Snapshot) ReadCardsOn() bool {
	if s == nil || s.Work == nil {
		return false
	}
	v, _ := s.Work.Prop(PropReadCards)
	return v == ReadCardsOnWord
}

// FieldReadCard marks a read card the deal cut (read_cards.go), "1": a friend's read asked
// before read cards has none (its inbox job is its card id: Packet.ReadJob).
const FieldReadCard = "read_card"

// ReadCardDeadline is how long a read card is given from its start (its take; a friend's
// read dealt working, from its deal) before the deal retires it late, spending its reader,
// and deals the read to another. A read never started is never late: one left untouched in
// ready past the deal bound (DealtMax) is taken back spending no one (RetiredByUnstarted).
const ReadCardDeadline = 60 * time.Minute

// The retired_by of a read card the read-card ask takes back: past its deadline, or its
// primary no longer in review at the card's attempt (reworked, its brief replaced, dropped,
// accepted); RetiredByReturned is a read handed back with no verdict (read --return).
const (
	RetiredByLate     = "late"
	RetiredByPrimary  = "primary"
	RetiredByReturned = "returned"
	// RetiredByAway is a read card the machine took back off a reader down, away or held,
	// or off a resting route (RetiredByRest): it spends nothing of the reader's.
	RetiredByAway = "away"
	RetiredByRest = "rest"
	// RetiredByCards is a readers-table read asked and not begun, taken back when read cards
	// came on: dealt again as a read card.
	RetiredByCards = "read cards"
	// RetiredByUnstarted is a read card left in ready past the deal bound: the machine's
	// take-back, spending nothing.
	RetiredByUnstarted = "unstarted"
)

// isRead says the card is a read card.
func isRead(c *Card) bool { return c != nil && c.F("kind") == "read" }

// rowLoad is the work cards and the read cards on a fleet row, ready and working.
func rowLoad(s *Snapshot, row string) (work, reads int) {
	for _, col := range []string{Ready, Working} {
		for _, c := range s.Fleet.Cell(row, col) {
			if isRead(c) {
				reads++
			} else {
				work++
			}
		}
	}
	return work, reads
}

// rowWorking is the work cards and the read cards working on a fleet row.
func rowWorking(s *Snapshot, row string) (work, reads int) {
	for _, c := range s.Fleet.Cell(row, Working) {
		if isRead(c) {
			reads++
		} else {
			work++
		}
	}
	return work, reads
}

// halfLoad is a load counted in slots with a read at half a slot, rounded up: the room a
// work card needs is whole.
func halfLoad(work, reads int) int { return work + (reads+1)/2 }

// readUnit is one unit the read-card ask may deal a read to: a friend's row or a member's.
type readUnit struct {
	name   string // the reader: her name, or the member's
	row    string // the fleet row
	friend bool
	seat   FriendSeat
	half   int // free room, in half slots: twice its room less twice its work and its reads
	idle   int // idle lanes, in half slots: twice its width less what works
}

// RoleReader is the role a friend's nova-config row names to be dealt read cards; a machine
// reads when its reader row (reader-<m>) is on the readers table, the machine's reader
// identity, whose tiers cell names the tiers it reads (reader set --tiers).
const RoleReader = "reader"

// readUnitsOf is every unit up that may be dealt a read: the friends dealable (friendDealable)
// whose roles name reader first, as a friend is dealt a card before a paid route is drawn,
// then the members up whose reader row is neither held nor retired, in row order.
func readUnitsOf(s *Snapshot, seats []FriendSeat) []readUnit {
	var out []readUnit
	for _, f := range seats {
		if !friendCanRead(s, f) || !slices.Contains(f.Roles, RoleReader) {
			continue
		}
		g := f
		g.ReadsFirst = 0
		room, width := friendRoom(g)
		row := FriendRow(f.Name)
		work, reads := rowLoad(s, row)
		ww, wr := rowWorking(s, row)
		out = append(out, readUnit{name: f.Name, row: row, friend: true, seat: f, half: 2*(room-work) - reads, idle: 2*width - 2*ww - wr})
	}
	for _, m := range s.UpMembers() {
		if !memberReads(s, m) {
			continue
		}
		w := s.Width(m)
		work, reads := rowLoad(s, m)
		ww, wr := rowWorking(s, m)
		out = append(out, readUnit{name: m, row: m, half: 2*(DealAhead*w-work) - reads, idle: 2*w - 2*ww - wr})
	}
	return out
}

// memberReads says the member has the reader role: its reader row (reader-<m>) is on the
// readers table and is neither held nor retired.
func memberReads(s *Snapshot, m string) bool {
	rd := ReaderPrefix + m
	if s.Readers == nil || !s.Readers.HasRow(rd) {
		return false
	}
	if s.ReaderStates != nil {
		if st := s.ReaderStates[rd]; st == ReaderHeld || st == ReaderRetired {
			return false
		}
	}
	return true
}

// attemptUnit is the unit that worked the primary's attempt: the friend's name or the
// member's, "" when it is not known. A unit never reads its own work.
func attemptUnit(s *Snapshot, primary string, attempt int) string {
	if f := attemptWorker(s, primary, attempt); f != "" {
		return f
	}
	if s == nil || s.Fleet == nil {
		return ""
	}
	if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
		return cmp.Or(wc.Row, wc.F("member"))
	}
	return ""
}

// spentBy is the retired_by of a read card its reader itself ended: it closed it (read) or
// handed it back (returned), or let it pass its deadline (late). A card the machine took
// back (a reader down, away or held, a restart, the coordinator's hold --return, its
// primary moved) spends nothing: its reader may be dealt the read again at the attempt.
var spentBy = []string{"read", RetiredByReturned, RetiredByLate}

// readerCardsAt is the reader's read cards of the primary's attempt, placed or kept, every
// generation (cards: the primary's read cards, fleetReadIndex).
func readerCardsAt(cards []*Card, primary string, attempt int, reader string) []*Card {
	var out []*Card
	for _, c := range cards {
		p, a, rd, ok := ParseReadCard(c.ID)
		if ok && p == primary && a == attempt && rd == reader {
			out = append(out, c)
		}
	}
	return out
}

// readSpent says the reader holds or closed a read card of the attempt: one placed, or one
// retired with a verdict or by itself (spentBy). It is never dealt that read again.
func readSpent(cards []*Card) bool {
	for _, c := range cards {
		if c.Placed() || c.F("verdict") != "" || slices.Contains(spentBy, c.F("retired_by")) {
			return true
		}
	}
	return false
}

// MaxReadGen is the most generations of one reader's read card at one attempt: the plain
// identity and .g1 to .g<MaxReadGen>, each a read the machine took back (spentBy). A reader
// whose read was taken back that often is not dealt it again at the attempt.
const MaxReadGen = 2

// ReadCardGenIDs is every identity one reader's read card of an attempt may have: the plain
// one and its generations (readCardIDFor).
func ReadCardGenIDs(primary string, attempt int, reader string) []string {
	id := ReadCardID(primary, attempt, reader)
	out := []string{id}
	for n := 1; n <= MaxReadGen; n++ {
		out = append(out, id+".g"+itoa(n))
	}
	return out
}

// readCardIDFor is the id of the reader's next read card of the attempt: the first of its
// identities (ReadCardGenIDs) it has not used; "" when it has used them all.
func readCardIDFor(cards []*Card, primary string, attempt int, reader string) string {
	used := map[string]bool{}
	for _, c := range cards {
		used[c.ID] = true
	}
	for _, id := range ReadCardGenIDs(primary, attempt, reader) {
		if !used[id] {
			return id
		}
	}
	return ""
}

// ReadCardExtras is the fleet table's read card ids a step that judges reads must read as
// records, placed or kept (a store's snapshot holds only the placed cards and the records a
// step names): every identity of a read card of each primary in review, at its attempt, of
// every reader the fleet table has a row for (a friend, by her name; a member with its
// reader row).
func ReadCardExtras(s *Snapshot) []string {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return nil
	}
	var out []string
	for _, c := range s.Work.Column(Review) {
		attempt := readAttempt(c)
		for _, row := range s.Fleet.Rows() {
			name, ok := FriendOfRow(row)
			if !ok {
				if s.Readers == nil || !s.Readers.HasRow(ReaderPrefix+row) {
					continue // a member with no reader row is dealt no read
				}
				name = row
			}
			for _, id := range ReadCardGenIDs(c.ID, attempt, name) {
				if s.Fleet.Placed(id) == nil {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// mayReadCard says the unit may be dealt a read of the primary at the attempt, by the rules
// a work card is dealt by (its tier: a friend's tiers reach it, friendAtOrAbove; a member's reader
// row serves it, readerServesTier) and the two of a read: it did not work the attempt, and
// it holds no read card of the attempt and closed none (readSpent).
//
// below says the unit may read it only by the interim rule (the owner, 2026-10-06 7:11 PM
// ET "let flash read pro", 7:41 PM "let pro do it"): it is one tier below the read's tier,
// never two; the deal prefers a unit at or above the tier while one has room.
func mayReadCard(s *Snapshot, u readUnit, pr *Card, attempt int, worker string, cards []*Card) (ok, below bool) {
	mine := readerCardsAt(cards, pr.ID, attempt, u.name)
	if u.name == worker || readSpent(mine) || readCardIDFor(mine, pr.ID, attempt, u.name) == "" {
		return false, false
	}
	if u.friend {
		// a friend reads her tier or any below it, as her reads always did: a heavy friend
		// reads a flash and a pro card (friendAtOrAbove)
		t := friendReadTier(s, pr)
		if friendAtOrAbove(u.seat, t) {
			return true, false
		}
		b := tierBelow(t)
		return b != "" && friendAtOrAbove(u.seat, b), true
	}
	// a machine whose reader row holds a read of the attempt on the readers table (asked
	// the old way, before read cards were on) reads it there, never twice
	rd := ReaderPrefix + u.name
	for _, id := range ReadCardIDs(pr.ID, attempt, rd) {
		if c := s.Readers.Card(id); c.Placed() || c != nil && c.F("verdict") != "" {
			return false, false
		}
	}
	t := s.readTierOf(pr)
	if s.readerServesTier(rd, t) {
		return true, false
	}
	b := tierBelow(t)
	return b != "" && s.readerServesTier(rd, b), true
}

// tierBelow is the tier one below t on the ladder (flash, pro, heavy, frontier), "" for
// flash or a word off the ladder.
func tierBelow(t string) string {
	if i := slices.Index(capLadder, t); i > 0 {
		return capLadder[i-1]
	}
	return ""
}

// readCardsStanding is the primary's reads that stand at its attempt, both tables: the
// fleet's read cards placed or retired with a verdict (fleetReadLive), and the readers
// table's live reads (the readers table's reads asked before the setting, which close as
// they always did); broken says one of them found it broken.
func readCardsStanding(s *Snapshot, pr *Card, idx map[string][]*Card) (standing int, broken bool) {
	var placed, oks, brk []*Card
	if idx != nil {
		placed, oks, brk = fleetReadLiveOf(s, pr, idx[pr.ID])
	} else {
		placed, oks, brk = friendReadLive(s, pr)
	}
	standing = len(placed) + len(oks) + len(brk)
	broken = len(brk) > 0
	if s.Readers != nil {
		for _, rc := range liveReadsAt(s, pr, readAttempt(pr)) {
			standing++
			broken = broken || rc.Col == Broken
		}
	}
	return standing, broken
}

// readCardsWanted is how many read cards the deal cuts for the primary now: every read its
// attempt still needs (ReadsNeeded less the reads that stand), at once; none once a read
// found it broken (its judgment and the rework follow), none for failed work; over a fleet
// read index (fleetReadIndex), nil to read the table.
func readCardsWanted(s *Snapshot, pr *Card, idx map[string][]*Card) int {
	if pr == nil || pr.Col != Review || IsSentinel(pr) || pr.F("result") == "failed" || readBranchMissingOpen(s, pr) {
		return 0
	}
	standing, broken := readCardsStanding(s, pr, idx)
	if broken {
		return 0
	}
	return max(0, ReadsNeeded(pr)-standing)
}

// readCardsWaiting is every primary in review that wants read cards now, by the level of
// its reads (readOrder), work order within one.
func readCardsWaiting(s *Snapshot, idx map[string][]*Card) []*Card {
	var out []*Card
	for _, pr := range s.Work.Column(Review) {
		if readCardsWanted(s, pr, idx) > 0 {
			out = append(out, pr)
		}
	}
	return readOrder(out)
}

// readCardsTakeBack is the read cards the ask retires before it asks: each placed read card
// past its deadline (RetiredByLate), and each whose primary is no longer in review at the
// card's attempt (RetiredByPrimary), by primary.
//
// The readers table's reads asked the old way and not begun (asked, or handed back) are
// taken back too, once read cards are on: they are dealt again as read cards, and a read
// begun there finishes there and stands (readCardsStanding), so turning read cards on reads
// no primary twice.
func readCardsTakeBack(s *Snapshot) map[string][]Change {
	out := map[string][]Change{}
	if s.Readers != nil {
		for _, c := range s.Readers.Column(Asked) {
			out[c.F("primary")] = append(out[c.F("primary")], change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByCards})))
		}
	}
	for _, c := range s.Fleet.Column(Ready, Working) {
		if !isRead(c) {
			continue
		}
		by := ""
		pr := s.Work.Placed(c.F("primary"))
		switch {
		case pr == nil || pr.Col != Review || readAttempt(pr) != c.Int("attempt"):
			by = RetiredByPrimary
		case c.F(FieldReadCard) != "" && c.Col == Working && !readStart(c).IsZero() && !s.Now.Before(readStart(c).Add(ReadCardDeadline)):
			by = RetiredByLate
		case c.F(FieldReadCard) != "" && c.Col == Ready && !s.Now.Before(stampAt(c, "asked").Add(s.DealtMax())):
			by = RetiredByUnstarted
		case c.Col == Ready && func() bool { _, ok := cardRest(s, c); return ok }():
			by = RetiredByRest
		default:
			continue
		}
		out[c.F("primary")] = append(out[c.F("primary")], change(Fleet, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": by})))
	}
	return out
}

// readCardsAsk is the read-card ask: the cards it takes back (readCardsTakeBack), then for
// every primary that wants reads (readCardsWaiting) every read it wants, at once, each to a
// different unit that may read it (mayReadCard) with half a slot free, the friends first,
// then the unit with the most idle lanes, then the most room, then by name. waits is why
// each primary that wants more reads than it was dealt waits. With ri nil the reads draw
// no route (the deal's dry run: what the reads would take).
func readCardsAsk(s *Snapshot, seats []FriendSeat, ri routeIndexes) (p Plan, waits map[string]string) {
	waits = map[string]string{}
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, waits
	}
	back := readCardsTakeBack(s)
	// the cards taken back stand no more: the wants are counted without them
	view := s
	if len(back) > 0 {
		v := *s
		v.Fleet, v.Readers = s.Fleet.Frozen(), s.Readers.Frozen()
		for _, chs := range back {
			for _, ch := range chs {
				tb := v.T(ch.Table)
				c := *tb.Card(ch.Entry.ID)
				c.Row, c.Col = "", ""
				c.Fields = maps.Clone(c.Fields)
				maps.Copy(c.Fields, ch.Entry.Set) // retired_by: whether it spent its reader
				tb.Put(&c)
			}
		}
		view = &v
	}
	units := readUnitsOf(view, seats)
	idx := fleetReadIndex(view)
	declared := map[string]bool{}
	done := map[string]bool{}
	for _, pr := range readCardsWaiting(view, idx) {
		done[pr.ID] = true
		want := readCardsWanted(view, pr, idx)
		attempt := readAttempt(pr)
		worker := attemptUnit(view, pr.ID, attempt)
		cards := idx[pr.ID]
		var may []int
		below := map[int]bool{}
		for i, u := range units {
			if ok, b := mayReadCard(view, u, pr, attempt, worker, cards); ok {
				may = append(may, i)
				below[i] = b
			}
		}
		var room []int
		for _, i := range may {
			if units[i].half > 0 {
				room = append(room, i)
			}
		}
		slices.SortStableFunc(room, func(a, b int) int {
			x, y := units[a], units[b]
			switch {
			case below[a] != below[b]:
				// a reader at or above the tier first; one tier below only for the rest
				if below[b] {
					return -1
				}
				return 1
			case x.friend != y.friend:
				if x.friend {
					return -1
				}
				return 1
			case max(x.idle, 0) != max(y.idle, 0):
				return max(y.idle, 0) - max(x.idle, 0)
			case x.half != y.half:
				return y.half - x.half
			}
			return strings.Compare(x.name, y.name)
		})
		picked := room[:min(want, len(room))]
		u := Unit{Key: pr.ID, Stream: pr.Row, Changes: back[pr.ID]}
		delete(back, pr.ID)
		if len(picked) < want {
			switch {
			case len(may) == 0:
				waits[pr.ID] = "no reader up may read it: no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker"
			default:
				waits[pr.ID] = "every reader up who may read it is at its room"
			}
		}
		if len(picked) == 0 {
			if len(u.Changes) > 0 {
				u.Moved = pr.ID + ": its read cards taken back"
				p.Units = append(p.Units, u)
			}
			continue
		}
		standing, _ := readCardsStanding(view, pr, idx)
		branch, start, head := attemptEnds(view, pr.ID, attempt)
		head = cmp.Or(head, pr.F("head"))
		var names []string
		for k, i := range picked {
			un := &units[i]
			un.half--
			un.idle--
			id := readCardIDFor(cards, pr.ID, attempt, un.name)
			fields := map[string]string{
				"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": un.name,
				"attempt": itoa(attempt), "head": head, "asked": stamp(s.Now), "gen": "1",
				FieldReadCard: "1",
			}
			if branch != "" {
				fields["branch"] = branch
			}
			if start != "" {
				fields["start"] = start
			}
			priorityOnRead(fields, pr)
			col := Ready
			if un.friend {
				// a friend's read is hers on her row, working once she has a lane free, as
				// her reads were (askOneFriend); she brings her own model
				fields[FieldTier] = friendReadTier(view, pr)
				if un.idle >= 0 {
					col = Working
				}
				if !view.Fleet.HasRow(un.row) && !declared[un.row] {
					p.Rows = append(p.Rows, RowAdd{Fleet, un.row})
					declared[un.row] = true
				}
			} else {
				if ri != nil {
					maps.Copy(fields, view.readRouteOf(ri, pr, nil))
				} else {
					fields[FieldTier] = view.readTierOf(pr)
				}
				// the first read of a flash card's attempt is a decide read, as the readers'
				// ask places it (decideFields)
				maps.Copy(fields, view.decideFields(pr, standing == 0 && k == 0))
			}
			u.Changes = append(u.Changes, change(Fleet, createEntry(id, un.row, col, pr.Score, fields)))
			names = append(names, un.name)
		}
		u.Moved = pr.ID + " asked of " + strings.Join(names, ", ") + " (read cards)"
		u.Closes = closesFor(s.Open, []string{NStranded, NStalled}, pr.ID)
		p.Units = append(p.Units, u)
	}
	// the cards taken back whose primary asks nothing now
	for _, key := range slices.Sorted(maps.Keys(back)) {
		if done[key] {
			continue
		}
		p.Units = append(p.Units, Unit{Key: key, Stream: cmp.Or(fieldOf(s.Work.Card(key), "stream"), ""), Changes: back[key], Moved: key + ": its read cards taken back"})
	}
	return Lawful(p), waits
}

// fieldOf is the card's field, "" for no card.
func fieldOf(c *Card, name string) string {
	if c == nil {
		return ""
	}
	if name == "stream" && c.Row != "" {
		return c.Row
	}
	return c.F(name)
}

// readCardsAskPart is the tick's ask while read cards are on (friendAskPart): the deal
// deals the read cards (TickDeal, withReadCards), and the ask asks nothing; it writes the
// waiting mark on each primary that wants more reads than it holds (markWaiting), counted
// due so the no-stall rule holds it, clears the mark of one that waits no more, and keeps
// the readers' standing judgments: the readers behind and the read tier to raise; cannot
// ask and fewer than two readers up close.
func readCardsAskPart(s *Snapshot, r TickReq, seats []FriendSeat) (Plan, int) {
	var p Plan
	_, waits := readCardsAsk(s, seats, nil)
	for _, c := range s.Work.Column(Review) {
		if _, wait := waits[c.ID]; !wait && c.F(FieldWaitingReader) != "" {
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, nil, FieldWaitingReader))},
				Moved: c.ID + " waits for a reader no more"})
		}
	}
	markWaiting(&p, s, map[string]int{}, waits)
	var conds []cond
	if s.Readers != nil {
		conds = append(conds, readersBehindCond(s)...)
	}
	conds = append(conds, raiseReadTierConds(s)...)
	conds = append(conds, readsWindowConds(s, r)...)
	due := notify(&p, s, conds, []string{NCannotAsk, NFewReaders, NReadersBehind, NRaiseReadTier, NBrokenReadsOutrun, NReaderBreaks}, r)
	return p, due + len(waits)
}

// readCardsWaitingCount is the reads waiting while read cards are on (ReadsWaiting): the
// reads wanted and not yet dealt (readCardsWanted), and the read cards dealt and not
// started (ready on a row).
func readCardsWaitingCount(s *Snapshot) int {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return 0
	}
	idx := fleetReadIndex(s)
	n := 0
	for _, pr := range s.Work.Column(Review) {
		n += readCardsWanted(s, pr, idx)
	}
	for _, c := range s.Fleet.Column(Ready) {
		if isRead(c) {
			n++
		}
	}
	return n
}

// withReadCards is the read-card deal (TickDeal; the owner, 2026-10-06: "Just remove the
// complexity. just deal it."): the plan that deals every read the primaries in review want
// (readCardsAsk), each drawn on its tier's routes from the tier's index as it stands (a
// read moves no index: the work cards of the deal move it), and the snapshot with those
// cards placed and the cards it takes back off their rows, which the work of the same deal
// is dealt on: every room the deal counts (memberLoads, widthRoom, friendLoad) holds the
// reads first, at half a slot each (reads are a card priority: a read waits behind no
// work card). With read cards off, nothing and the snapshot as it is.
func (s *Snapshot) withReadCards(seats []FriendSeat) (*Snapshot, Plan) {
	if !s.ReadCardsOn() || s.Fleet == nil {
		return s, Plan{}
	}
	if seats == nil {
		seats = s.Friends
	}
	var ri routeIndexes
	if len(s.Routes) > 0 {
		ri = routeIndexesOf(s)
	}
	p, _ := readCardsAsk(s, seats, ri)
	if len(p.Units) == 0 {
		return s, p
	}
	v := *s
	v.Fleet = s.Fleet.Frozen()
	for _, ra := range p.Rows {
		if ra.Table == Fleet && !v.Fleet.HasRow(ra.Row) {
			v.Fleet.SetRows(append(slices.Clone(v.Fleet.Rows()), ra.Row))
		}
	}
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			e := ch.Entry
			switch {
			case ch.Table != Fleet:
			case e.Create != nil:
				v.Fleet.Put(&Card{ID: e.ID, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Fields: maps.Clone(e.Set)})
			case e.Remove:
				if c := v.Fleet.Card(e.ID); c != nil {
					off := *c
					off.Row, off.Col = "", ""
					v.Fleet.Put(&off)
				}
			}
		}
	}
	return &v, p
}

// readStart is when a working read card started: its take, else its deal (a friend's read
// dealt straight to working).
func readStart(c *Card) time.Time {
	if t := stampAt(c, "taken"); !t.IsZero() {
		return t
	}
	return stampAt(c, "asked")
}

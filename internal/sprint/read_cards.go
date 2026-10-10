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

// ReadCardCounts is the epoch's read cards on the fleet table: ready, working, and done (ok
// or failed), where --json's read_cards.
type ReadCardCounts struct {
	Ready   int `json:"ready"`
	Working int `json:"working"`
	Done    int `json:"done"`
}

// RowCardFields is the fields of a row's counts (RowCardCounts), highest level first.
var RowCardFields = []string{"blocker_working", "critical_working", "fix_working", "high_working", "reads_working", "normal_working", "low_working", "reads_ready"}

// RowCardCounts is each fleet row's cards as the dashboard's segmented bar draws them,
// highest on the left: its working cards by level, a read card as reads whatever level it
// inherits and a work card at the level its deal wrote (QueuePriority), the fields
// <level>_working summing to its working (RowCardFields); and reads_ready, the read cards
// among its ready. A row with none is absent. all is the epoch's read cards.
func RowCardCounts(s *Snapshot) (rows map[string]map[string]int, all ReadCardCounts) {
	rows = map[string]map[string]int{}
	if s == nil || s.Fleet == nil {
		return rows, all
	}
	for _, row := range s.Fleet.Rows() {
		n := map[string]int{}
		for _, c := range s.Fleet.Cell(row, Working) {
			level := PriorityLadder[priorityRank(QueuePriority(c))]
			if isRead(c) {
				level, all.Working = "reads", all.Working+1
			}
			n[level+"_working"]++
		}
		for _, c := range s.Fleet.Cell(row, Ready) {
			if isRead(c) {
				n["reads_ready"]++
				all.Ready++
			}
		}
		for _, col := range []string{DoneOK, DoneFailed} {
			for _, c := range s.Fleet.Cell(row, col) {
				if isRead(c) {
					all.Done++
				}
			}
		}
		if len(n) > 0 {
			rows[row] = n
		}
	}
	return rows, all
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
// whose roles name reader, then the members up whose reader row is neither held nor
// retired, in row order; the ask orders them together (readCardsAsk).
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
// retired with a verdict or by itself (spentBy). It is never dealt that read again. A read
// card the machine withdrew (FriendTake for a friend's hold or stall) is history and spends
// nothing; one the seat or her runner handed back (friend take, FriendTakeReq.Spends) is
// withdrawn with retired_by returned and spends her.
func readSpent(cards []*Card) bool { return readSpender(cards) != nil }

// readSpender is the first card that spends its reader (readSpent), nil for none.
func readSpender(cards []*Card) *Card {
	for _, c := range cards {
		if c.Placed() && !IsWithdrawn(c.Col) || c.F("verdict") != "" || slices.Contains(spentBy, c.F("retired_by")) {
			return c
		}
	}
	return nil
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
// A unit one tier below the read's tier may read it too, by the interim rule (the owner,
// 2026-10-06 7:11 PM ET "let flash read pro", 7:41 PM "let pro do it"), never two below;
// the deal orders the units that may by tier distance (readTierDistance).
func mayReadCard(s *Snapshot, u readUnit, pr *Card, attempt int, worker string, cards []*Card) bool {
	return readRefusal(s, u, pr, attempt, worker, cards) == ""
}

// readRefusal is the clause of mayReadCard that refuses the unit the read, "" when none
// does: worker, spent, no id, friends' set, friend tier, reader row (a readers-table read
// of the attempt), fleet set, reader tier.
func readRefusal(s *Snapshot, u readUnit, pr *Card, attempt int, worker string, cards []*Card) string {
	mine := readerCardsAt(cards, pr.ID, attempt, u.name)
	switch {
	case u.name == worker:
		return "worker"
	case readSpent(mine):
		c := readSpender(mine)
		return "spent(" + c.ID[strings.LastIndex(c.ID, ".")+1:] + " col=" + c.Col + " verdict=" + c.F("verdict") + " retired_by=" + c.F("retired_by") + " head=" + c.F("head") + ")"
	case readCardIDFor(mine, pr.ID, attempt, u.name) == "":
		return "no id"
	}
	if u.friend {
		// a friend reads her tier or any below it, as her reads always did: a heavy friend
		// reads a flash and a pro card (friendAtOrAbove)
		t := friendReadTier(s, pr)
		if !s.FriendsTake(t) {
			return "friends' set" // the friends' tiers leave the read out (set --friends-tiers)
		}
		if b := tierBelow(t); friendAtOrAbove(s, u.seat, t) || b != "" && friendAtOrAbove(s, u.seat, b) {
			return "" // inside the set: b too (friendAtOrAbove)
		}
		return "friend tier"
	}
	// a machine whose reader row holds a read of the attempt on the readers table (asked
	// the old way, before read cards were on) reads it there, never twice
	rd := ReaderPrefix + u.name
	for _, id := range ReadCardIDs(pr.ID, attempt, rd) {
		if c := s.Readers.Card(id); c.Placed() || c != nil && c.F("verdict") != "" {
			return "reader row"
		}
	}
	t := s.readTierOf(pr)
	if !s.FleetTakes(t) {
		return "fleet set" // the fleet's tiers leave the read out (set --fleet-tiers)
	}
	if b := tierBelow(t); s.readerServesTier(rd, t) || b != "" && s.FleetTakes(b) && s.readerServesTier(rd, b) {
		return "" // inside the set: b too
	}
	return "reader tier"
}

// readTierDistance is how far below or above the read's tier the unit reads, the cheaper
// first, measured against the tier before readTierOf lowers it (friendReadTier) for every
// unit: 0 at the read's own tier, 1 one tier below (mayReadCard's interim rule), 1+k at k
// tiers above, and after every tier above, len(capLadder)+k at k tiers below, as a member
// reads a card whose route tier readTierOf lowered (a frontier or heavy card is drawn on
// pro). A friend reads at the nearest of her tiers (friendTiers); a member at the tier its
// read is drawn on, readTierOf's or the one below it when its reader row serves only that.
func readTierDistance(s *Snapshot, u readUnit, pr *Card) int {
	want := slices.Index(capLadder, friendReadTier(s, pr))
	gap := func(at int) int {
		switch {
		case want < 0 || at < 0:
			return 2*len(capLadder) + 1
		case at == want:
			return 0
		case at == want-1:
			return 1
		case at > want:
			return 1 + at - want
		}
		return len(capLadder) + want - at
	}
	if !u.friend {
		e := s.readTierOf(pr)
		if !s.readerServesTier(ReaderPrefix+u.name, e) {
			e = tierBelow(e)
		}
		return gap(slices.Index(capLadder, e))
	}
	best := gap(-1)
	for _, x := range friendTiers(u.seat) {
		best = min(best, gap(slices.Index(capLadder, x)))
	}
	return best
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
// attempt still needs (ReadsNeededIn less the reads that stand), at once; none once a read
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
	return max(0, ReadsNeededIn(s, pr)-standing)
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
	for id, changes := range out {
		out[id] = retiredReadCosts(s, changes)
	}
	return out
}

// NoReaderMayRead begins the wait of a primary no unit up may be dealt its read card: the
// read-card ask raises cannot ask on it (readCardsAskPart).
const NoReaderMayRead = "no reader up may read it"

// readCardsAsk is the read-card ask: the cards it takes back (readCardsTakeBack), then for
// every primary that wants reads (readCardsWaiting) every read it wants, at once, each to a
// different unit that may read it (mayReadCard) with half a slot free, the cheapest first:
// a unit with an idle lane before every unit with none (a read waits in a ready queue only
// when every reader that may take it is busy), then by tier distance (readTierDistance: the
// read's own tier, one tier below, then the tiers above, nearest first), then the most idle
// lanes, then the most room, then by name; friends and members alike. waits is why
// each primary that wants more reads than it was dealt waits. With ri nil the reads draw
// no route (the deal's dry run: what the reads would take).
func readCardsAsk(s *Snapshot, seats []FriendSeat, ri routeIndexes) (p Plan, waits map[string]string) {
	return readCardsAskWhy(s, seats, ri, nil)
}

// ReadCardsWhy is tick --shadow's account of the read-card ask (readCardsAsk): first the
// units it deals reads to, each with its half slots and idle lanes, and the reader friends
// it leaves out; then a line per primary that waits, its read tier, the reads it wants, why
// it waits and each unit not dealt it with the clause that refuses it (readRefusal), or
// half<=0 for one at its room as the ask reached the primary. Nothing with read cards off.
func ReadCardsWhy(s *Snapshot, seats []FriendSeat) []string {
	if !s.ReadCardsOn() {
		return nil
	}
	if seats == nil {
		seats = withoutDirs(s.Friends)
	}
	var why []string
	readCardsAskWhy(s, seats, nil, &why)
	return why
}

// readCardsAskWhy is readCardsAsk, and with why non-nil, ReadCardsWhy's lines.
func readCardsAskWhy(s *Snapshot, seats []FriendSeat, ri routeIndexes, why *[]string) (p Plan, waits map[string]string) {
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
				if !ch.Entry.Remove {
					continue
				}
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
	if why != nil {
		var us, out []string
		for _, u := range units {
			us = append(us, u.name+" half="+itoa(u.half)+" idle="+itoa(u.idle))
		}
		for _, f := range seats {
			if slices.Contains(f.Roles, RoleReader) && !friendCanRead(view, f) {
				out = append(out, f.Name+" ("+f.Status+")")
			}
		}
		*why = append(*why, "units: "+strings.Join(us, ", ")+"; reader friends not dealable: "+strings.Join(out, ", "))
	}
	declared := map[string]bool{}
	done := map[string]bool{}
	for _, pr := range readCardsWaiting(view, idx) {
		done[pr.ID] = true
		want := readCardsWanted(view, pr, idx)
		attempt := readAttempt(pr)
		worker := attemptUnit(view, pr.ID, attempt)
		cards := idx[pr.ID]
		var may []int
		dist := map[int]int{}
		for i, u := range units {
			if mayReadCard(view, u, pr, attempt, worker, cards) {
				may = append(may, i)
				dist[i] = readTierDistance(view, u, pr)
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
			case (x.idle > 0) != (y.idle > 0):
				// an idle lane first: a read is stacked on a busy reader only when every
				// reader that may take it is busy
				if x.idle > 0 {
					return -1
				}
				return 1
			case dist[a] != dist[b]:
				return dist[a] - dist[b]
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
				waits[pr.ID] = NoReaderMayRead + ": no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker"
			default:
				waits[pr.ID] = "every reader up who may read it is at its room"
			}
			if why != nil {
				var refused []string
				for i, un := range units {
					r := readRefusal(view, un, pr, attempt, worker, cards)
					if r == "" && un.half <= 0 {
						r = "half<=0"
					}
					if r != "" && !slices.Contains(picked, i) {
						refused = append(refused, un.name+"="+r)
					}
				}
				*why = append(*why, pr.ID+" tier="+friendReadTier(view, pr)+" wants="+itoa(want)+" waits: "+waits[pr.ID]+"; refused: "+strings.Join(refused, ", "))
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
	if why != nil {
		for _, pr := range view.Work.Column(Review) {
			if !IsSentinel(pr) && readCardsWanted(view, pr, idx) == 0 && !acceptable(view, pr) {
				*why = append(*why, pr.ID+" wants 0: "+readsWantZeroWhy(view, pr, idx))
			}
		}
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
	// a read no unit up may take is cannot ask, one judgment, open until a reader may
	var refused []Refusal
	for _, id := range slices.Sorted(maps.Keys(waits)) {
		if strings.HasPrefix(waits[id], NoReaderMayRead) {
			refused = append(refused, Refusal{Key: id, Why: waits[id]})
		}
	}
	conds = append(conds, cannotAskCond(s, refused)...)
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

// readsWantZeroWhy says why a primary in review that is not acceptable wants no read card
// (ReadCardsWhy): failed work, or the reads that stand (readCardsStanding), each named.
func readsWantZeroWhy(s *Snapshot, pr *Card, idx map[string][]*Card) string {
	if pr.F("result") == "failed" {
		return "its work came back failed (result=failed)"
	}
	placed, oks, brk := fleetReadLiveOf(s, pr, idx[pr.ID])
	var out []string
	say := func(kind string, c *Card) {
		out = append(out, kind+" "+c.ID+" col="+orDash(c.Col)+" verdict="+orDash(c.F("verdict"))+" start="+stamp(readStart(c)))
	}
	for _, c := range placed {
		say("placed", c)
	}
	for _, c := range oks {
		say("ok", c)
	}
	for _, c := range brk {
		say("broken", c)
	}
	if s.Readers != nil {
		for _, c := range liveReadsAt(s, pr, readAttempt(pr)) {
			say("readers-table", c)
		}
	}
	return "attempt=" + itoa(readAttempt(pr)) + " stands: " + strings.Join(out, "; ")
}

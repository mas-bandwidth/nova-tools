package sprint

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"
)

// A read is a consumer card (docs/SPEC-SPRINT.md section 6, "A read is a consumer card";
// the owner, 2026-10-06: "reads need to become a type of card"; "These are all good reasons
// why read should have been going through consumer cards the whole time"). While the
// sprint's read_cards setting is on (set --read-cards on), a primary that wants reads is
// asked them as read cards on the fleet table, the table its work cards are dealt on: the
// ask cuts every read the attempt still needs AT ONCE ("send out multiple consumer cards in
// ||"), each dealt to a different unit in the step that cuts it, as the deal cuts and deals
// a work card in one step. A unit is a friend up whose tiers reach the read's tier, or a
// fleet member up whose reader row (reader-<m>, `reader add`) is neither held nor retired
// and serves the read's tier (`reader set --tiers`); never the attempt's own worker, and
// never a unit that holds a read card of the attempt already, placed or retired, so two
// reads of one primary go to two readers and a read taken back goes to another. A read
// costs half a slot of its unit's one width (the owner: "Go wide with reads, 2X regular
// width"): a row's load is its work cards and half its reads, so a row at width 8 holds 8
// work cards working or 16 reads or any mix. A read's tier is its primary's (the owner:
// "start with tier"); its level is inherited (ReadPriority). The review is unchanged: a
// read card's verdict closes the read on the primary as `read --ok|--broken` does
// (fleetReadCloseUnit), and the primary leaves review only by the existing rules. With the
// setting off, the readers table's ask and the friends' read packet ask, as before.

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

// FieldReadDeadline is a read card's deadline, a stamp: asked plus ReadCardDeadline. Past
// it the ask retires the card (RetiredByLate) and asks the read of another unit.
const FieldReadDeadline = "read_deadline"

// ReadCardDeadline is how long a read card is given on the sprint's clock before the ask
// takes it back and asks another reader.
const ReadCardDeadline = 60 * time.Minute

// The retired_by of a read card the read-card ask takes back: past its deadline, or its
// primary no longer in review at the card's attempt (reworked, its brief replaced, dropped,
// accepted); RetiredByReturned is a read handed back with no verdict (read --return).
const (
	RetiredByLate     = "late"
	RetiredByPrimary  = "primary"
	RetiredByReturned = "returned"
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

// readUnitsOf is every unit up that may be dealt a read: the friends dealable (friendDealable)
// first, as the friends are asked a read before a paid reader is drawn, then the members up
// whose reader row is neither held nor retired, in row order.
func readUnitsOf(s *Snapshot, seats []FriendSeat) []readUnit {
	var out []readUnit
	for _, f := range seats {
		if !friendDealable(s, f) {
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
	if s.Readers == nil {
		return out
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

// mayReadCard says the unit may be dealt a read of the primary at the attempt: its tier
// reaches the read's tier, it did not work the attempt, and it holds no read card of the
// attempt, placed or retired, on the fleet table or (a member) on the readers table.
func mayReadCard(s *Snapshot, u readUnit, pr *Card, attempt int, worker string) bool {
	if u.name == worker || s.Fleet.Card(ReadCardID(pr.ID, attempt, u.name)) != nil {
		return false
	}
	if u.friend {
		return friendAtOrAbove(u.seat, friendReadTier(s, pr))
	}
	rd := ReaderPrefix + u.name
	for _, id := range ReadCardIDs(pr.ID, attempt, rd) {
		if s.Readers.Card(id) != nil {
			return false
		}
	}
	return s.readerServesTier(rd, s.readTierOf(pr))
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

// ReadCardsWanted is how many read cards the ask cuts for the primary now: every read its
// attempt still needs (ReadsNeeded less the reads that stand), at once; none once a read
// found it broken (its judgment and the rework follow), none for failed work.
func ReadCardsWanted(s *Snapshot, pr *Card) int { return readCardsWanted(s, pr, nil) }

// readCardsWanted is ReadCardsWanted over a fleet read index (fleetReadIndex), nil to read
// the table.
func readCardsWanted(s *Snapshot, pr *Card, idx map[string][]*Card) int {
	if pr == nil || pr.Col != Review || IsSentinel(pr) || pr.F("result") == "failed" {
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
func readCardsTakeBack(s *Snapshot) map[string][]Change {
	out := map[string][]Change{}
	for _, c := range s.Fleet.Column(Ready, Working) {
		if !isRead(c) {
			continue
		}
		by := ""
		pr := s.Work.Placed(c.F("primary"))
		switch {
		case pr == nil || pr.Col != Review || readAttempt(pr) != c.Int("attempt"):
			by = RetiredByPrimary
		case c.F(FieldReadDeadline) != "" && !s.Now.Before(stampAt(c, FieldReadDeadline)):
			by = RetiredByLate
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
	retired := map[string]bool{}
	for _, chs := range back {
		for _, ch := range chs {
			retired[ch.Entry.ID] = true
		}
	}
	// the cards taken back stand no more: the wants are counted without them
	view := s
	if len(retired) > 0 {
		v := *s
		v.Fleet = s.Fleet.Frozen()
		for id := range retired {
			c := *v.Fleet.Card(id)
			c.Row, c.Col = "", ""
			v.Fleet.Put(&c)
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
		var may []int
		for i, u := range units {
			if mayReadCard(view, u, pr, attempt, worker) {
				may = append(may, i)
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
			id := ReadCardID(pr.ID, attempt, un.name)
			fields := map[string]string{
				"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": un.name,
				"attempt": itoa(attempt), "head": head, "asked": stamp(s.Now), "gen": "1",
				FieldReadDeadline: stamp(s.Now.Add(ReadCardDeadline)),
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

// readCardsAskPart is the tick's ask while read cards are on (friendAskPart): the read-card
// ask, its route draws written as the readers' ask writes them (a read card's route is drawn
// as a work card's is, from its tier's rolling index: route.go, readRouteOf), the waiting
// mark on each primary that wants more reads than it was dealt (markWaiting), counted due so
// the no-stall rule holds it, and the readers' standing judgments as the readers' ask keeps
// them: the readers behind and the read tier to raise; cannot ask and fewer than two readers
// up close, the readers table asks nothing new (docs/SPEC-SPRINT.md section 6, the readers
// table retires next release).
func readCardsAskPart(s *Snapshot, r TickReq, seats []FriendSeat) (Plan, int) {
	var ri routeIndexes
	if s.Fleet != nil && len(s.Routes) > 0 {
		ri = routeIndexesOf(s)
	}
	p, waits := readCardsAsk(s, seats, ri)
	if ri != nil {
		ri.write(&p)
	}
	asked := map[string]int{}
	for i, u := range p.Units {
		if strings.Contains(u.Moved, " asked of ") {
			asked[u.Key] = i
		}
	}
	markWaiting(&p, s, asked, waits)
	var conds []cond
	if s.Readers != nil {
		conds = append(conds, readersBehindCond(s)...)
	}
	conds = append(conds, raiseReadTierConds(s)...)
	due := notify(&p, s, conds, []string{NCannotAsk, NFewReaders, NReadersBehind, NRaiseReadTier}, r)
	return p, due + len(waits)
}

// readCardsWaitingCount is the reads waiting while read cards are on (ReadsWaiting): the
// reads wanted and not yet dealt (ReadCardsWanted), and the read cards dealt and not
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

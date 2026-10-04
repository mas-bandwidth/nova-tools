package sprint

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A reader's state (docs/SPEC-SPRINT.md section 6, the readers table; the
// model is tla/DirtyTick.tla, the readers update: a read is placed only on a
// reader up, and a read asked of a reader that goes away is taken back). A
// reader is a row of the readers table, which the coordinator declares (init
// --readers, reader add); a reader with its row says it is there by asking for its
// own queue (queue --as <reader> writes its beat; a name with no row writes none,
// and its queue answers reader false). Its state is derived, never typed: away while the
// coordinator holds it away (reader away; reader up releases the hold),
// whatever it beats; else up while its last beat is within ReaderBeatBound;
// else away when it beat once and has lapsed, down when it has never beaten.
// The ask deals reads to readers up only.

const (
	// ReaderUp, ReaderAway and ReaderDown are a reader's states.
	ReaderUp   = "up"
	ReaderAway = "away"
	ReaderDown = "down"
	// ReaderRetired is a reader the coordinator retired (reader retire): held
	// away for good, its beat writing none and its queue answering reader false,
	// its row and read cards kept, the history (the comfort list of 2026-10-03,
	// item 6); reader up brings it back.
	ReaderRetired = "retired"

	// ReaderBeatBound is how long a reader stays up after its last beat: the
	// fleet's bound (BeatDeadline), named once so a reader's can be told apart.
	ReaderBeatBound = BeatDeadline
)

// ReaderState is a reader's state at now, from the coordinator's hold and its
// last beat.
func ReaderState(away bool, b Beat, now time.Time) string {
	switch {
	case away:
		return ReaderAway
	case b.Beaten() && now.Sub(b.At) <= ReaderBeatBound:
		return ReaderUp
	case b.Beaten():
		return ReaderAway
	}
	return ReaderDown
}

// ReaderIsUp says a reader may be asked: it is up, or the snapshot carries no
// reader states (a core test's, which holds every reader up).
func (s *Snapshot) ReaderIsUp(reader string) bool {
	return s.ReaderStates == nil || s.ReaderStates[reader] == ReaderUp
}

// UpReaders is the readers whose state is up, in row order.
func (s *Snapshot) UpReaders() []string {
	var out []string
	for _, r := range s.Readers.Rows() {
		if s.ReaderIsUp(r) {
			out = append(out, r)
		}
	}
	return out
}

// readersText names every reader with its state, in name order: the text of
// the judgment "fewer than two readers up".
func readersText(s *Snapshot) string {
	var out []string
	for _, r := range s.Readers.Rows() {
		st := s.ReaderStates[r]
		switch {
		case s.ReaderStates == nil:
			st = ReaderUp
		case st == ReaderRetired:
			continue // off the table: no reader the judgment names
		case st == "":
			st = ReaderDown
		}
		out = append(out, r+" "+st)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// NFewReaders is the tick's judgment that fewer readers are up than a primary
// in review waiting to be asked needs (two for a pro card, one for a flash
// card; ReadsNeeded): the ask raises it once, and asks no absent reader.
const NFewReaders = "fewer than two readers up"

// fewReaders is the text of the judgment.
func fewReaders(s *Snapshot) string {
	return fmt.Sprintf("%s: %s", NFewReaders, readersText(s))
}

// cannotAskWhy is the ask's refusal of a primary at an attempt no reader can be
// asked: it needs want different readers, free is the number up with room and
// no read card at the attempt, full the number more that are at width. A reader
// is asked an attempt once (its read card, placed or retired, is one read per
// reader per attempt: a read taken back away, levelled or returned counts), so
// the readers left are new ones (reader add), or the next attempt (rework).
func cannotAskWhy(s *Snapshot, pr *Card, attempt, want, free, full int) string {
	return fmt.Sprintf("needs %d different readers and %d is free with no read card at attempt %d of %s (%d free but at width); a reader is asked an attempt once, whether it read it or its read was taken back, and a reader away or down is not asked (readers: %s); run: nova-sprint reader add <name>, nova-sprint reader up <name>, or nova-sprint rework %s --fix <text> for a new attempt every reader may read", want, free, attempt, pr.ID, full, readersText(s), pr.ID)
}

// NoEligibleReader opens the tick's one judgment for every primary of a tick
// the ask refused for want of readers (TickAsk): "no eligible reader for <ids>".
const NoEligibleReader = "no eligible reader for "

// awayRead says a read card is asked, not begun, of a reader that is not up:
// the ask takes it back (retires it) and asks the primary's next reader in the
// same step, at its attempt and with no redeal spent (tla/DirtyTick.tla,
// ReaderAway). A read begun stays with its reader.
func awayRead(s *Snapshot, rc *Card) bool { return rc.Col == Asked && !s.ReaderIsUp(rc.Row) }

// returnedRead says a read card was handed back by its reader with no verdict
// (read --return) and is not asked again yet: it is back in asked on its own
// row, stamped returned. A return is not a read: the ask places it again, on
// another reader free at the attempt, or on the same reader in place
// (tla/DirtyTick.tla, A RETURN IS NOT A READ, JudgedOnlyAfterTheBound).
func returnedRead(rc *Card) bool { return rc.Col == Asked && rc.F(FieldReturned) != "" }

// FieldReturned is the stamp on a read card handed back with no verdict, until
// the ask asks it again.
const FieldReturned = "returned"

// ReaderPrefix names a reader for its machine: reader-<m> is the one reader on
// the fleet machine m, and it runs at m's width, the fleet row's, read with its
// queue every tick as the member on m reads its own (the owner, 2026-10-02:
// "The reader widths seem to be very ad-hoc, unlike the machine widths"; "why
// not just have as many readers as workers per-machine"). The readers table
// holds no width column: a reader's width is derived from its fleet row
// (ReaderWidth), `queue --as reader-<m>` carries m's fleet row's width, and a
// reader named for no row carries none and begins nothing.
const ReaderPrefix = "reader-"

// ReaderMachine is the machine a reader is named for: reader-<m> names m; a
// name of another shape names no machine.
func ReaderMachine(reader string) (machine string, ok bool) {
	m, found := strings.CutPrefix(reader, ReaderPrefix)
	if !found || !ValidID(m) {
		return "", false
	}
	return m, true
}

// FieldReasked is how many times a read card's reader returned it and it went
// back to asked on the reader's row, counted by Read itself at each return, so
// the bound holds whatever the tick does and however many readers are up (the
// ask need not run for the count to move); MaxReadReasks is the most: the
// return after them retires the card, counted as a read (tla/DirtyTick.tla,
// MaxReasks and ReasksBounded).
const (
	FieldReasked  = "reasked"
	MaxReadReasks = 2
)

// FieldLeveled marks a read card the level moved: the level moves it no more, so a
// read is never shuttled between readers tick after tick and the level never sticks on one card.
const FieldLeveled = "leveled"

// ReadsNeeded is how many different readers' ok reads at its head make the
// primary acceptable, and so how many readers the ask asks at an attempt: one
// when the tier the card is on (cardTier) is flash, and two at any stronger tier
// (pro, or frontier). A frontier card is not asked of a machine: friend_read.go asks
// a friend whose tiers include frontier. Each machine read is drawn on a route of the card's
// read tier (readTierOf) (the owner,
// 2026-10-02, cost rule 4, nova-tools#5174: "Reads: one cold read per flash
// card on a flash route; two per pro card; readers still equal workers per
// machine"). The tier is the card's own, the tier it is on (cardTier: flash first,
// then the tier it escalated to, or the tier a rework recorded), never a setting, so
// a card in merging or landed is held to the count it was accepted on.
func ReadsNeeded(pr *Card) int {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	if cardTier(pr, m) == cardhdr.RouteFlash {
		return 1
	}
	return 2
}

// enoughReadersUp says as many readers are up as the primary needs
// (ReadsNeeded), or the snapshot carries no reader states (every reader up):
// the ask may ask it (TickAsk); else it waits, judged NFewReaders.
func enoughReadersUp(s *Snapshot, pr *Card) bool {
	return s.ReaderStates == nil || len(s.UpReaders()) >= ReadsNeeded(pr)
}

// ScriptReadPrefix begins the finding of an ok read a script reader gave: the reader ran
// the card's SCRIPT program at the attempt's start commit and its diff was the head's,
// byte for byte (docs/SPEC-SPRINT.md, the script read; member.VerifyScript).
const ScriptReadPrefix = "script read: "

// scriptVerified says the primary is a script card (CLASS: script, with its SCRIPT
// program) with an ok read at its current attempt and head whose finding is a script
// read's: that one read counts as every read the card needs. A script read that found
// a difference gives no verdict (the member goes on to read the card as a model reader),
// so a card whose head the program did not make is read by models as any card is, and
// no read is accepted on the worker's word: the reader ran the program itself.
func scriptVerified(s *Snapshot, pr *Card) bool {
	if c, _ := cardhdr.ReadClass(pr.F("brief")); !c.IsScript() {
		return false
	}
	for _, c := range readsAt(s, pr, pr.Int("attempt")) {
		if c.Col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c) && strings.HasPrefix(c.F("finding"), ScriptReadPrefix) {
			return true
		}
	}
	return false
}

// acceptable says the primary has ok reads from ReadsNeeded different readers
// at its current attempt and head (okReaders), or one script read of a script card
// (scriptVerified).
func acceptable(s *Snapshot, pr *Card) bool {
	return len(okReaders(s, pr)) >= ReadsNeeded(pr) || scriptVerified(s, pr)
}

// liveReadsAt is the primary's placed read cards at an attempt less the reads
// the ask takes back or places again: the reads that stand.
func liveReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, rc := range readsAt(s, pr, attempt) {
		if !awayRead(s, rc) && !returnedRead(rc) {
			out = append(out, rc)
		}
	}
	return out
}

// returnedReadsAt is the primary's read cards at an attempt handed back with
// no verdict by readers up (returnedRead): the ask places each again, on a
// free reader with room, or in place on its own reader, whose room already
// holds it (readerLoad counts it).
func returnedReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, rc := range readsAt(s, pr, attempt) {
		if !awayRead(s, rc) && returnedRead(rc) {
			out = append(out, rc)
		}
	}
	return out
}

// freeReaders is the readers the ask may ask the primary's attempt of: up,
// with no read card of it at the attempt, placed or retired. A reader is
// asked an attempt once: one read card per reader per attempt (ReadCardID),
// so a reader with a card at this attempt (read, or taken back away, levelled
// or returned) is not asked it again; the next attempt is read on new cards,
// by every reader.
func (s *Snapshot) freeReaders(pr *Card, attempt int) []string {
	var out []string
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) && s.Readers.Card(ReadCardID(pr.ID, attempt, rd)) == nil {
			out = append(out, rd)
		}
	}
	return out
}

// sweepReads is the readers' rebalance safety: every read asked or reading of a
// reader that is not up is taken back, retired as the ask takes back a read
// asked of a reader away
// (retired_by away: that reader keeps its card at the attempt, so it is not
// asked that attempt again), and the tick's ask asks it of the readers up. A
// read stays where it is when the ask could not place it (fewer readers up
// than its primary needs, ReadsNeeded, or none up without a card at its
// attempt): it is judged while its reader is away and read when the reader is
// back (read_return_test.go). A snapshot with no reader states holds every
// reader up: nothing moves.
func sweepReads(s *Snapshot, p *Plan) {
	up := s.UpReaders()
	if s.ReaderStates == nil || len(up) == 0 {
		return
	}
	taker := func(c *Card) bool {
		if pr := s.Work.Card(c.F("primary")); pr == nil || !enoughReadersUp(s, pr) {
			return false
		}
		for _, rd := range up {
			if s.Readers.Card(ReadCardID(c.F("primary"), c.Int("attempt"), rd)) == nil {
				return true
			}
		}
		return false
	}
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) {
			continue
		}
		cards := append(append([]*Card{}, s.Readers.Cell(rd, Asked)...), s.Readers.Cell(rd, Reading)...)
		SortCards(cards)
		for _, c := range cards {
			if !taker(c) {
				continue
			}
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
				Changes: []Change{change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": "away"}))},
				Moved:   fmt.Sprintf("%s %s:%s -> taken back (%s is %s); the ask asks it of a reader up", c.ID, rd, c.Col, rd, orDash(s.ReaderStates[rd]))})
		}
	}
}

// RetiredByLevel is a read card's retired_by when the tick's level moved its
// read, asked and not begun, to another reader (levelReads).
const RetiredByLevel = "level"

// TickLevelReads is the readers' rebalance, once at the start of every tick,
// before any other part: levelReads, in one plan.
func TickLevelReads(s *Snapshot, _ TickReq) (Plan, int) {
	var p Plan
	levelReads(s, &p)
	return bound(p)
}

// ReaderWidth is a reader's width, the most reads it runs at once: its
// machine's fleet row's (ReaderPrefix: reader-<m> runs at m's width, the width
// the reader itself reads with its queue every tick), so the ask and the level
// know it too (the owner, 2026-10-03: with widths 4/8/16/16/24 the count-levelled
// reads queued seven deep on the two narrow machines while 31 reader slots sat
// idle). A reader named for no fleet row, or read with no fleet table, keeps
// the unbounded room it had: math.MaxInt, so it is never at width and its room
// is ordered by its load alone.
func (s *Snapshot) ReaderWidth(reader string) int {
	m, ok := ReaderMachine(reader)
	if !ok || s.Fleet == nil {
		return math.MaxInt
	}
	ctl := s.MemberCtl(m)
	if ctl == nil {
		return math.MaxInt
	}
	return MemberWidth(ctl)
}

// readerLoad is a reader's reads asked and reading together.
func (s *Snapshot) readerLoad(reader string) int {
	return s.Readers.Count(reader, Asked) + s.Readers.Count(reader, Reading)
}

// readerRoom is a reader's room: its width (ReaderWidth) and its free room,
// the width less its load (readerLoad), below zero for a reader over its
// width. The ask and the level compare rooms by share, free room as a part of
// width, so a machine of 4 and one of 24 are each filled to the same fraction
// (ten reads: the 24 takes eight), never by count alone.
type readerRoom struct{ width, free int }

// roomParts is the parts a share is counted in.
const roomParts = 1 << 20

// share is the reader's free room as parts of its width (roomParts when idle,
// below zero when over width); a reader with unbounded width (no fleet row)
// counts roomParts less its load, so such readers order by load alone, below
// an idle reader with a width and above any that has begun to fill.
func (r readerRoom) share() int {
	if r.width == math.MaxInt {
		return roomParts - (r.width - r.free)
	}
	return r.free * roomParts / r.width
}

// after is the room with n more reads placed on it.
func (r readerRoom) after(n int) readerRoom { return readerRoom{width: r.width, free: r.free - n} }

// readerRooms is each reader's room (readerRoom). The ask gives a read to the
// reader with the greatest share (round.pickByRoom, taking the reads it places
// off the room as it goes) and the level moves reads toward it
// (round.levelToRoom); a reader with no free room is given nothing by either.
func (s *Snapshot) readerRooms(readers []string) map[string]readerRoom {
	room := make(map[string]readerRoom, len(readers))
	for _, rd := range readers {
		w := s.ReaderWidth(rd)
		room[rd] = readerRoom{width: w, free: w - s.readerLoad(rd)}
	}
	return room
}

// levelReads evens the up readers' room, the fleet's level (level) in the
// readers' shape: a reader's room is its width less its load, reads asked and
// reading together, compared as a share of its width (readerRoom), and a
// reader named for no fleet row has unbounded room, so among such readers the
// share differs by the load alone. While the reader with the least share that
// holds an asked read not yet levelled (over its width when the share is
// below zero) could hand one to a reader with free room whose share would
// still not fall under its own, its newest such read (the last in work order)
// moves to the reader with the greatest share, ties the first round the
// readers from the ask's index (askRound, round.levelToRoom), the index moved
// past it as the ask moves it. So no reader up has free lanes while another
// holds a backlog, no move fills a reader past its width (a reader at width is
// given nothing), and no read is shuttled back: the ask's placement is the
// level's fixed point. A read moves only to a reader with no card at
// its primary's attempt, placed or retired: a primary's two reads stay with
// two different readers, and no reader is asked an attempt it already read.
// The move retires the read card (retired_by level: the reader it left is
// never asked that attempt again) and asks the read of the other reader at
// the same attempt and head, its route kept, in the readers table only,
// marked leveled: a read is moved at most once, so a late read is not asked
// afresh on reader after reader. A read begun stays with its reader; a reader
// that is not up is neither a source nor a target: sweepReads takes its reads
// back first. Each move takes a read off a queue, so the level ends.
func levelReads(s *Snapshot, p *Plan) {
	sweepReads(s, p)
	up := s.UpReaders()
	if len(up) < 2 {
		return
	}
	queues := map[string][]*Card{}
	for _, rd := range up {
		for _, c := range s.Readers.Cell(rd, Asked) {
			if c.F(FieldLeveled) == "" {
				queues[rd] = append(queues[rd], c)
			}
		}
		SortCards(queues[rd])
	}
	room := s.readerRooms(up)
	rr := askRound(s)
	moves := roundMoves{}
	planned := map[string]bool{} // the read cards this plan already creates: none is created twice
	for {
		long := ""
		for _, rd := range up {
			if len(queues[rd]) > 0 && (long == "" || room[rd].share() < room[long].share()) {
				long = rd
			}
		}
		if long == "" {
			break
		}
		q := queues[long]
		i, to := len(q)-1, ""
		for ; i >= 0 && to == ""; i-- {
			avoid := []string{long}
			for _, rd := range up {
				if id := ReadCardID(q[i].F("primary"), q[i].Int("attempt"), rd); s.Readers.Card(id) != nil || planned[id] {
					avoid = append(avoid, rd)
				}
			}
			to = rr.levelToRoom(up, room, long, avoid)
		}
		if to == "" {
			break
		}
		i++
		c := q[i]
		room[long] = room[long].after(-1)
		room[to] = room[to].after(1)
		queues[long] = append(q[:i:i], q[i+1:]...)
		moves[c.ID] = to
		fields := movedReadFields(c, to, s.Now)
		id := ReadCardID(c.F("primary"), c.Int("attempt"), to)
		planned[id] = true
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{
			change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByLevel})),
			change(Readers, createEntry(id, to, Asked, c.Score, fields)),
		}, Moved: fmt.Sprintf("%s %s:asked -> %s:asked (%s)", c.ID, long, to, id)})
	}
	roundWrites(p, rr, moves)
}

// movedReadFields is the fields of the card a read moved by the level is asked
// on: the read's own (its primary, stream, attempt, head and route) for the
// reader it goes to, asked now, marked leveled, and none of its run on the reader it left: not
// returned, not reasked (the new reader's bound starts at zero), no
// read_take_<n> and no usage. The card it leaves is retired with every field it
// had, and a returned run's cost is the primary's (cost_record:<card>#r<n>,
// cost.go): the move loses none of it.
func movedReadFields(c *Card, to string, now time.Time) map[string]string {
	fields := map[string]string{}
	for k, v := range c.Fields {
		switch {
		case k == FieldReturned, k == FieldReasked, k == FieldUsage, strings.HasPrefix(k, FieldReadTake):
			continue
		}
		fields[k] = v
	}
	fields["reader"], fields["asked"], fields[FieldLeveled] = to, stamp(now), "1"
	return fields
}

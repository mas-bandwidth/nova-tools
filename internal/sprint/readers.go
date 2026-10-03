package sprint

import (
	"fmt"
	"maps"
	"math"
	"sort"
	"strings"
	"time"
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
		case st == "":
			st = ReaderDown
		}
		out = append(out, r+" "+st)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// NFewReaders is the tick's judgment that fewer than two readers are up while
// a primary in review waits to be asked: the ask raises it once, and asks no
// absent reader.
const NFewReaders = "fewer than two readers up"

// fewReaders is the text of the judgment.
func fewReaders(s *Snapshot) string {
	return fmt.Sprintf("%s: %s", NFewReaders, readersText(s))
}

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
// not just have as many readers as workers per-machine"). The sprint holds no
// reader's width of its own: `queue --as reader-<m>` carries m's fleet row's
// width, and a reader named for no row carries none and begins nothing.
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

// sweepReads is the readers' rebalance safety: every read asked or reading of a
// reader that is not up is taken back, retired as the ask takes back a read
// asked of a reader away
// (retired_by away: that reader keeps its card at the attempt, so it is not
// asked that attempt again), and the tick's ask asks it of the readers up. A
// read stays where it is when the ask could not place it (fewer than two
// readers up, or none up without a card at its attempt): it is judged while its
// reader is away and read when the reader is back (read_return_test.go). A
// snapshot with no reader states holds every reader up: nothing moves.
func sweepReads(s *Snapshot, p *Plan) {
	up := s.UpReaders()
	if s.ReaderStates == nil || len(up) < 2 {
		return
	}
	taker := func(c *Card) bool {
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

// levelReads evens the up readers' loads, the fleet's level (level) in the
// readers' shape: a reader's load is its reads asked and reading together.
// While the largest load of a reader with an asked read and the smallest load
// of a reader up differ by more than one, the newest asked read (the last in
// work order) of the largest moves to the next reader up round the readers
// from the ask's index (askRound, round.levelTo) whose load is at or below the
// mean, the index moved past it as the ask moves it. So no reader up is idle
// while another holds a backlog. A read moves only to a reader with no card
// at its primary's attempt, placed or retired: a primary's two reads stay
// with two different readers, and no reader is asked an attempt it already
// read. The move retires the read card (retired_by level: the reader it left
// is never asked that attempt again) and asks the read of the other reader
// at the same attempt and head, its route kept, in the readers table only,
// marked leveled: a read is moved at most once, so a late read is not asked
// afresh on reader after reader. A read begun stays with its reader; a
// reader that is not up is neither a source nor a target: sweepReads takes
// its reads back first.
//
// The sprint knows no reader's width: a reader runs at its machine's width,
// not a loop's own --width (there is none), and the readers table has no width
// column, so every reader up counts alike and nothing here bounds a reader at
// DealAhead times a width.
func levelReads(s *Snapshot, p *Plan) {
	sweepReads(s, p)
	up := s.UpReaders()
	if len(up) < 2 {
		return
	}
	held, queues, room := map[string]int{}, map[string][]*Card{}, map[string]int{}
	for _, rd := range up {
		held[rd] = s.Readers.Count(rd, Asked) + s.Readers.Count(rd, Reading)
		for _, c := range s.Readers.Cell(rd, Asked) {
			if c.F(FieldLeveled) == "" {
				queues[rd] = append(queues[rd], c)
			}
		}
		SortCards(queues[rd])
		room[rd] = math.MaxInt // no reader width is known: none bounds the move
	}
	rr := askRound(s)
	moves := roundMoves{}
	planned := map[string]bool{} // the read cards this plan already creates: none is created twice
	for {
		long, short := "", ""
		for _, rd := range up {
			if len(queues[rd]) > 0 && (long == "" || held[rd] > held[long]) {
				long = rd
			}
			if short == "" || held[rd] < held[short] {
				short = rd
			}
		}
		if long == "" || held[long]-held[short] <= 1 {
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
			to = rr.levelTo(up, maps.Clone(held), held, room, long, avoid)
		}
		if to == "" {
			break
		}
		i++
		c := q[i]
		held[long]--
		held[to]++
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

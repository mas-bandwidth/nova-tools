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
// reader says it is there by asking for its own queue (queue --as <reader>
// writes its beat). Its state is derived, never typed: away while the
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

// liveReadsAt is the primary's placed read cards at an attempt less the reads
// the ask takes back: the reads that stand.
func liveReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, rc := range readsAt(s, pr, attempt) {
		if !awayRead(s, rc) {
			out = append(out, rc)
		}
	}
	return out
}

// sweepReads is the readers' rebalance's safety (the owner, 2026-10-01: "and it's
// a safety, if ever there are cards on a held or down machine, rebalance moves
// them away."): every read asked or reading of a reader that is not up is taken
// back, retired as the ask takes back a read asked of a reader away
// (retired_by away: that reader keeps its card at the attempt, so it is not
// asked that attempt again), and the tick's ask asks it of the readers up. A
// snapshot with no reader states holds every reader up: nothing moves.
func sweepReads(s *Snapshot, p *Plan) {
	if s.ReaderStates == nil {
		return
	}
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) {
			continue
		}
		cards := append(append([]*Card{}, s.Readers.Cell(rd, Asked)...), s.Readers.Cell(rd, Reading)...)
		SortCards(cards)
		for _, c := range cards {
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
// before any other part (the owner, 2026-10-01: "both for readers and fleet,
// there needs to be a rebalance step done at the start of each tick. it's
// simple. just once before tick, rebalance each table."): levelReads, in one
// plan.
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
// at the same attempt and head, its route kept, in the readers table only. A
// read begun stays with its reader; a reader that is not up is neither a
// source nor a target: sweepReads takes its reads back first.
//
// The sprint knows no reader's width: a reader loop's --width is the loop's
// own, and the readers table has no width column, so every reader up counts
// alike and nothing here bounds a reader at DealAhead times a width.
func levelReads(s *Snapshot, p *Plan) {
	sweepReads(s, p)
	up := s.UpReaders()
	if len(up) < 2 {
		return
	}
	held, queues, room := map[string]int{}, map[string][]*Card{}, map[string]int{}
	for _, rd := range up {
		held[rd] = s.Readers.Count(rd, Asked) + s.Readers.Count(rd, Reading)
		queues[rd] = append([]*Card{}, s.Readers.Cell(rd, Asked)...)
		SortCards(queues[rd])
		room[rd] = math.MaxInt // no reader width is known: none bounds the move
	}
	rr := askRound(s)
	moves := roundMoves{}
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
				if s.Readers.Card(ReadCardID(q[i].F("primary"), q[i].Int("attempt"), rd)) != nil {
					avoid = append(avoid, rd)
				}
			}
			to = rr.levelTo(up, maps.Clone(held), held, room, avoid)
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
		fields := maps.Clone(c.Fields)
		fields["reader"], fields["asked"] = to, stamp(s.Now)
		id := ReadCardID(c.F("primary"), c.Int("attempt"), to)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{
			change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByLevel})),
			change(Readers, createEntry(id, to, Asked, c.Score, fields)),
		}, Moved: fmt.Sprintf("%s %s:asked -> %s:asked (%s)", c.ID, long, to, id)})
	}
	roundWrites(p, rr, moves)
}

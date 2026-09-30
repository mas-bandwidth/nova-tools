package refmodel

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The duties of today's tick, by name, in the order the machine runs them: the
// parts of sprint.TickParts in the order of the four tables' updates (the work
// pump's resolve, deal and accept, the readers' ask, the merge's resume, the
// fleet's presence and level, then the end), the unknown machines it tells of
// (with the fleet's update), and the reminders. The machine's repair of an operation the fence holds is not one:
// it decides nothing from the tables (Decide's doc says what is left out).
const (
	DutyStrangers = "strangers" // tell of a machine that beats and is no member
	DutyPresence  = "presence"  // a member's status follows its beats
	DutyResolve   = "resolve"   // waiting primaries whose needs landed go ready; sentinels are reached
	DutyResume    = "resume"    // a stream stopped on another's card goes on when it landed
	DutyDeal      = "deal"      // ready primaries are dealt to the members up
	DutyLevel     = "level"     // the members' ready queues are evened
	DutyAccept    = "accept"    // primaries in review with two ok reads are accepted and queued to merge
	DutyAsk       = "ask"       // primaries in review are asked of two readers
	DutyCheck     = "check"     // a broken rule and a stall are judgments
	DutyDeadlines = "deadlines" // a late card or stream is a judgment
	DutyOverdue   = "overdue"   // a judgment past its due time is marked
	DutyDone      = "done"      // the sprint done is said to the coordinator, and the machine stops
	DutyRemind    = "remind"    // each person due is pushed their goal
)

// dutyNames is the duties' names in the tick's order, which the canonical order
// of moves follows. Duties lists the same names, and a test holds them equal.
var dutyNames = []string{DutyResolve, DutyDeal, DutyAccept, DutyAsk, DutyResume, DutyStrangers, DutyPresence, DutyLevel, DutyCheck, DutyDeadlines, DutyOverdue, DutyDone, DutyRemind}

// Duty is one duty of the tick: its name and the function that decides it.
type Duty struct {
	Name string
	// Moves is the moves the duty makes on the snapshot at the time given,
	// in canonical order, none when the machine is STOPPED. It decides on a
	// clone: the snapshot is not changed.
	Moves func(s Snapshot, now time.Time) []Move
}

// Duties is every duty of today's tick in the order the machine runs them.
var Duties = []Duty{
	{DutyResolve, ResolveMoves},
	{DutyDeal, DealMoves},
	{DutyAccept, AcceptMoves},
	{DutyAsk, AskMoves},
	{DutyResume, ResumeMoves},
	{DutyStrangers, StrangerMoves},
	{DutyPresence, PresenceMoves},
	{DutyLevel, LevelMoves},
	{DutyCheck, CheckMoves},
	{DutyDeadlines, DeadlineMoves},
	{DutyOverdue, OverdueMoves},
	{DutyDone, DoneMoves},
	{DutyRemind, RemindMoves},
}

// Decide is every move one tick of today's scanning machine would make on the
// snapshot at the time given, in canonical order (duty, table, card, kind): the
// moves of each duty in Duties, each decided on the same snapshot. A tick runs
// its duties one after another, each on a fresh read that shows what the ones
// before it moved; Decide shows what each would do first, on the state as it
// stands, which is what a machine that finds every rule quiet compares with.
//
// The snapshot is not changed, and the same snapshot at the same time gives
// the same moves. It is copied once, and every duty decides on that copy: the
// planners the duties call do not write to what they read. Each duty is held to
// the bounds of the tick (the units and judgments one part makes, the rest a
// KindDue move) and to what the store applies of a plan (sprint.Applied). What
// is decided from what the tick does, not from the tables, is left out: the
// repair of an operation the fence holds, the reminder's delivery and its
// outcome, and the display cells of the fleet.
func Decide(s Snapshot, now time.Time) []Move {
	ds := decisions() // refuses a duty the tick has no part of, of a STOPPED machine too
	if !s.Running {
		return nil
	}
	c := s.ownCopy(now)
	var out []Move
	for _, d := range ds {
		out = append(out, d.on(c, now)...)
	}
	return sortMoves(out)
}

// StrangerMoves is the notifications the coordinator is owed of the machines
// that beat and are no member of the fleet: told once each.
func StrangerMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyStrangers) }

// PresenceMoves is the change of one member's status the beats call for (T0):
// a member that beats comes up and the ready queues are levelled, or one that
// does not goes down and its unfinished work cards are dealt to the members up
// or withdrawn.
func PresenceMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyPresence) }

// ResolveMoves is the waiting primaries whose needs have all landed moved to
// ready, the sentinels whose needs have landed reached, and the judgments for
// a need dropped or missing (T1).
func ResolveMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyResolve) }

// ResumeMoves is the streams stopped only on another stream's card, resumed
// once that card has landed (T7).
func ResumeMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyResume) }

// DealMoves is the ready primaries dealt, oldest first, to the members up with
// room, and the judgments for no member up and for a card at its redeal bound
// (T3).
func DealMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyDeal) }

// AcceptMoves is the primaries in review with ok reads from two different
// readers moved to merging and queued to merge, in stream turns, and the note
// to the coordinator for each stream that got some: accept is mechanical, the
// merge is the coordinator's.
func AcceptMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyAccept) }

// LevelMoves is the newest ready cards moved from the longest queue round the
// fleet while two queues differ by more than one (T4).
func LevelMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyLevel) }

// AskMoves is the primaries in review asked of two different readers, and the
// judgment for one that cannot be (T2).
func AskMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyAsk) }

// CheckMoves is a judgment for each rule that does not hold and each stall
// nothing holds, and the close of one whose rule holds again (T6).
func CheckMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyCheck) }

// DeadlineMoves is a judgment for each work card, read card and stream past its
// deadline in running time, and the close of one whose cause is gone (T5).
func DeadlineMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyDeadlines) }

// OverdueMoves is one overdue line and one hold for each judgment past its due
// time, and the close of a hold whose judgment closed.
func OverdueMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyOverdue) }

// DoneMoves is the note "the sprint is done", addressed to the coordinator, on
// a sprint with nothing open and a card landed or dropped (R15 as errata 3
// amendment 6 amends it). The machine's stop that goes with it is no move of
// the tables: the model's Tick has it (State.Machine), and the binding writes
// it on the machine's record.
func DoneMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyDone) }

// RemindMoves is a push for each person whose reminder is due, and the
// judgments the failing routes call for or no longer call for. Whether a push
// arrives is the delivery's, not the decision's, so the judgments are those of
// the routes as the record has them.
func RemindMoves(s Snapshot, now time.Time) []Move { return oneDuty(s, now, DutyRemind) }

// onCopy is a duty's decision on a snapshot that is its own to read, with the
// time on its tables: Decide copies the snapshot once for all the duties, and a
// duty asked alone (StrangerMoves and the rest) copies it for itself.
type onCopy func(c Snapshot, now time.Time) []Move

// dutyOn is a duty's name and its decision.
type dutyOn struct {
	name string
	on   onCopy
}

// decisions is the decision of each duty in the tick's order. Making them
// refuses a duty the tick has no part of, so a duty cannot outlive its part in
// silence.
func decisions() []dutyOn {
	return []dutyOn{
		{DutyResolve, partOn(DutyResolve)},
		{DutyDeal, partOn(DutyDeal)},
		{DutyAccept, partOn(DutyAccept)},
		{DutyAsk, partOn(DutyAsk)},
		{DutyResume, partOn(DutyResume)},
		{DutyStrangers, strangersOn},
		{DutyPresence, partOn(DutyPresence)},
		{DutyLevel, partOn(DutyLevel)},
		{DutyCheck, partOn(DutyCheck)},
		{DutyDeadlines, partOn(DutyDeadlines)},
		{DutyOverdue, partOn(DutyOverdue)},
		{DutyDone, partOn(DutyDone)},
		{DutyRemind, remindOn},
	}
}

// oneDuty is the moves of the duty with the name on a copy of the snapshot of
// its own, at the time given, in canonical order; none while the machine is
// STOPPED.
func oneDuty(s Snapshot, now time.Time, name string) []Move {
	var on onCopy
	for _, d := range decisions() {
		if d.name == name {
			on = d.on
		}
	}
	if on == nil {
		panic("refmodel: no duty named " + name)
	}
	if !s.Running {
		return nil
	}
	return sortMoves(on(s.ownCopy(now), now))
}

// ownCopy is a copy of the snapshot the duties may read, with the time on its
// tables; it refuses a snapshot that lacks a table.
func (s Snapshot) ownCopy(now time.Time) Snapshot {
	c := s.Clone()
	c.needTables()
	c.Tables.Now = now
	return c
}

// strangersOn is the notifications for the unknown machines that beat and were
// not told of.
func strangersOn(c Snapshot, _ time.Time) []Move {
	if len(c.Untold) == 0 {
		return nil
	}
	return dutyMoves(DutyStrangers, c.Tables, sprint.StrangerNotes(c.Tables, c.Untold), 0)
}

// remindOn is the pushes due and the judgments of the failing routes.
func remindOn(c Snapshot, now time.Time) []Move {
	var out []Move
	for _, p := range c.Goals.DueAt(now, c.Since, c.stopped()) {
		out = append(out, Move{Duty: DutyRemind, Kind: KindPush, Card: p.Name, To: p.Route,
			Words: fmt.Sprintf("REMINDER %d to %s over %s", p.Count+1, p.Name, p.Route)})
	}
	if _, stale := c.Goals.NotesStale(); stale {
		out = append(out, planMoves(DutyRemind, c.Tables, sprint.Applied(c.Tables, sprint.RemindNotes(c.Tables, c.Goals, sprint.MachineActor)))...)
	}
	return sortMoves(out)
}

// partOn is the moves of the part of the tick with the name: what the store
// applies of the part's plan. It refuses a name the tick has no part of.
func partOn(name string) onCopy {
	fn := partFn(name)
	return func(c Snapshot, _ time.Time) []Move {
		plan, due := fn(c.Tables, c.tickReq())
		return dutyMoves(name, c.Tables, plan, due)
	}
}

// partFn is the function of the part of the tick with the name.
func partFn(name string) sprint.TickPartFn {
	for _, p := range sprint.TickParts {
		if p.Name == name {
			return p.Fn
		}
	}
	panic("refmodel: the tick has no part named " + name)
}

// dutyMoves is the moves of a plan a duty decided, as the store applies it, and
// a count of what it left due, in canonical order.
func dutyMoves(duty string, s *sprint.Snapshot, plan sprint.Plan, due int) []Move {
	out := planMoves(duty, s, sprint.Applied(s, plan))
	if due > 0 {
		out = append(out, dueMove(duty, due))
	}
	return sortMoves(out)
}

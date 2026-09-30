package refmodel

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The duties of today's tick, by name, in the order the machine runs them: the
// unknown machines it tells of, the parts of sprint.TickParts, and the
// reminders. The machine's repair of an operation the fence holds is not one:
// it decides nothing from the tables (Decide's doc says what is left out).
const (
	DutyStrangers = "strangers" // tell of a machine that beats and is no member
	DutyPresence  = "presence"  // a member's status follows its beats
	DutyResolve   = "resolve"   // waiting primaries whose needs landed go ready; sentinels are reached
	DutyResume    = "resume"    // a stream stopped on another's card goes on when it landed
	DutyDeal      = "deal"      // ready primaries are dealt to the members up
	DutyLevel     = "level"     // the members' ready queues are evened
	DutyAsk       = "ask"       // primaries in review are asked of two readers
	DutyCheck     = "check"     // a broken rule and a stall are judgments
	DutyDeadlines = "deadlines" // a late card or stream is a judgment
	DutyOverdue   = "overdue"   // a judgment past its due time is marked
	DutyRemind    = "remind"    // each person due is pushed their goal
)

// dutyNames is the duties' names in the tick's order, which the canonical order
// of moves follows. Duties lists the same names, and a test holds them equal.
var dutyNames = []string{DutyStrangers, DutyPresence, DutyResolve, DutyResume, DutyDeal, DutyLevel, DutyAsk, DutyCheck, DutyDeadlines, DutyOverdue, DutyRemind}

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
	{DutyStrangers, StrangerMoves},
	{DutyPresence, PresenceMoves},
	{DutyResolve, ResolveMoves},
	{DutyResume, ResumeMoves},
	{DutyDeal, DealMoves},
	{DutyLevel, LevelMoves},
	{DutyAsk, AskMoves},
	{DutyCheck, CheckMoves},
	{DutyDeadlines, DeadlineMoves},
	{DutyOverdue, OverdueMoves},
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
// the same moves. Each duty is held to the bounds of the tick (the units and
// judgments one part makes, the rest a KindDue move) and to what the store
// applies of a plan (sprint.Applied). What is decided from what the tick does,
// not from the tables, is left out: the repair of an operation the fence holds,
// the reminder's delivery and its outcome, and the display cells of the fleet.
func Decide(s Snapshot, now time.Time) []Move {
	var out []Move
	for _, d := range Duties {
		out = append(out, d.Moves(s, now)...)
	}
	return sortMoves(out)
}

// StrangerMoves is the notifications the coordinator is owed of the machines
// that beat and are no member of the fleet: told once each.
func StrangerMoves(s Snapshot, now time.Time) []Move {
	if !s.Running || len(s.Untold) == 0 {
		return nil
	}
	c := s.Clone()
	c.needTables()
	c.Tables.Now = now
	return dutyMoves(DutyStrangers, c.Tables, sprint.StrangerNotes(c.Tables, c.Untold), 0)
}

// PresenceMoves is the change of one member's status the beats call for (T0):
// a member that beats comes up and the ready queues are levelled, or one that
// does not goes down and its unfinished work cards are dealt to the members up
// or withdrawn.
func PresenceMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyPresence) }

// ResolveMoves is the waiting primaries whose needs have all landed moved to
// ready, the sentinels whose needs have landed reached, and the judgments for
// a need dropped or missing (T1).
func ResolveMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyResolve) }

// ResumeMoves is the streams stopped only on another stream's card, resumed
// once that card has landed (T7).
func ResumeMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyResume) }

// DealMoves is the ready primaries dealt, oldest first, to the members up with
// room, and the judgments for no member up and for a card at its redeal bound
// (T3).
func DealMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyDeal) }

// LevelMoves is the newest ready cards moved from the longest queue to the
// shortest while two queues differ by more than one (T4).
func LevelMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyLevel) }

// AskMoves is the primaries in review asked of two different readers, and the
// judgment for one that cannot be (T2).
func AskMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyAsk) }

// CheckMoves is a judgment for each rule that does not hold and each stall
// nothing holds, and the close of one whose rule holds again (T6).
func CheckMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyCheck) }

// DeadlineMoves is a judgment for each work card, read card and stream past its
// deadline in running time, and the close of one whose cause is gone (T5).
func DeadlineMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyDeadlines) }

// OverdueMoves is one overdue line and one hold for each judgment past its due
// time, and the close of a hold whose judgment closed.
func OverdueMoves(s Snapshot, now time.Time) []Move { return partMoves(s, now, DutyOverdue) }

// RemindMoves is a push for each person whose reminder is due, and the
// judgments the failing routes call for or no longer call for. Whether a push
// arrives is the delivery's, not the decision's, so the judgments are those of
// the routes as the record has them.
func RemindMoves(s Snapshot, now time.Time) []Move {
	if !s.Running {
		return nil
	}
	c := s.Clone()
	c.needTables()
	c.Tables.Now = now
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

// partMoves is the moves of the part of the tick with the name, on a clone of
// the snapshot at the time given: what the store applies of the part's plan.
// It refuses a name the tick has no part of, so a duty cannot outlive its part
// in silence.
func partMoves(s Snapshot, now time.Time, name string) []Move {
	fn := partFn(name)
	if !s.Running {
		return nil
	}
	c := s.Clone()
	c.needTables()
	c.Tables.Now = now
	plan, due := fn(c.Tables, c.tickReq())
	return dutyMoves(name, c.Tables, plan, due)
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

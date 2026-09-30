package machine

import (
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The machine while STOPPED (1.4.5; SprintEvents.tla, PlanOrLook's STOPPED
// branch). Every TickEvery, RT1 as RUNNING: the lease step also writes
// looked_at, and the pop takes what was due at the stop and the cut entries.
// Every StoppedLookEvery of wall time, RT2 as RUNNING (the ingest, and each
// rule's read) and the rules plan dry: moves are due when a dry plan changes
// at least one card. RT3 only when a step of notes and sprint keys is due: R17
// (its clock fields or its judgment), R11's cut clock, the error judgment. So a
// STOPPED machine costs one round trip a second, and up to three every 10 s.

// StoppedLookEvery is the wall time between two looks of a STOPPED machine.
const StoppedLookEvery = 10 * time.Second

// StoppedLook is R17 at a look (IT10's StoppedLook): from the dry plans of
// every rule, the clock as read and the wall time, the step of notes and
// sprint keys R17 sends, or an empty plan when it sends none. SprintEvents.tla
// R17Unit is the model of it (H7, H14, H16, H17).
type StoppedLook func(dry []sprint.RulePlan, c Clock, wall int64) sprint.RulePlan

// lookDue says a STOPPED loop looks this tick: at its first tick STOPPED, and
// then once StoppedLookEvery of the store's wall time has passed since its
// last look (1.4.5).
func lookDue(last, wall int64) bool {
	return last == 0 || time.Duration(wall-last)*time.Millisecond >= StoppedLookEvery
}

// movesDue says a dry plan would change a card (1.4.5; SprintEvents.tla,
// CardChange in PlanOrLook): a unit with a change, or an intent, which Lua
// turns into entries at apply. Notes, guards, the agenda's edits and the
// quarantine change no card.
func movesDue(dry []sprint.RulePlan) bool {
	for _, rp := range dry {
		if len(rp.Intents) != 0 || len(rp.Plan.Rows) != 0 {
			return true
		}
		for _, u := range rp.Plan.Units {
			if len(u.Changes) != 0 || len(u.Bumps) != 0 {
				return true
			}
		}
	}
	return false
}

// notesAndKeysOnly says a plan changes no card: it may go to RT3 while the
// machine is STOPPED (1.4.3, T2).
func notesAndKeysOnly(rp sprint.RulePlan) bool { return !movesDue([]sprint.RulePlan{rp}) }

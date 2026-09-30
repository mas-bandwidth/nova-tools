package sprint

// The shared types of the event-driven tick's upper layers (the upper design,
// version 2.1, section 8.0): what a rule plans, in the words every builder of
// the layers agrees on so that they build at the same time. There is no logic
// here, only shapes; the rules that fill them, the step builder that cuts
// them and the parts that apply them are other files.
//
// Not here: AgendaKey lives in events.go (IT01). Rule, RegisterRule and
// RuleTable live with ReadPlan and QueryCost in the files of IT05's later
// parts (rule.go, readplan.go), since a Rule names ReadPlan and SprintQ and
// the design gives their shapes only there.

// Now is the one reading of the clocks a tick plans against (1.2, 1.4.2): the
// store's TIME is frozen at the start of a call, so R and Wall are read once.
type Now struct {
	// R is running time in milliseconds: the wall time less the time the
	// machine has been STOPPED (R does not move while it is). The due set and
	// every deadline of a rule are in R.
	// Wall is the store's wall time in milliseconds: the cut set and the
	// STOPPED judgment's hold are in it.
	R, Wall int64
	// Running says the machine is RUNNING and not STOPPED.
	Running bool
}

// Intent is an effect that a rule cannot plan from its read and that Lua
// decides at apply, from the real before-state (1.3.3). The step builder
// carries it; the derive phase turns it into layer 1 entries.
type Intent struct {
	// Kind is the effect: waitfor, needmet, needgone or waive.
	Kind string
	// Card is the waiter.
	Card string
	// Need is the need that landed or was removed (needmet, needgone).
	Need string
	// Needs are the needs a card waits for (waitfor) or waives (waive).
	Needs []string
	// Waiters are the cards waiting for Need (needmet, needgone).
	Waiters []string
}

// XGuard is a guard on a sprint key that X checks at apply (1.3.5, 2.3): a
// plan that read the key as it was is refused XGUARD when it has moved.
type XGuard struct {
	// Kind is what is guarded: memberup, beatstale, due, hold, clock,
	// coordinator or stranger.
	Kind string
	// Member is the fleet member the guard is about, when it is about one.
	Member string
	// Key is the key or entry guarded: a due set member, a hold, a field of a
	// sprint hash.
	Key string
	// Score is the score of the entry as the plan read it, where the guard is
	// on one.
	Score int64
}

// Quarantined is a card that a lower layer refused, named so that the next
// step the tick sends writes its quarantine (1.3.5). The mark is a sprint key
// written with no layer 1 entry on the card.
type Quarantined struct {
	// ID is the card, Stream its stream, Code the refusal's code and Rule the
	// rule whose plan or read met it.
	ID, Stream, Code, Rule string
	// Cells are the cells the refusal's detail names.
	Cells []string
}

// NoteReq is a request to J for a note (1.3.4): J turns the step's requests
// into notes in the pre stage, one per cause.
type NoteReq struct {
	// Op is what J is asked to do: open, close, update, hold, unhold, know
	// or request.
	Op string
	// Type is the note's type (2.2 for a judgment, 2.5 for a notice).
	Type string
	// Cause is the cause the one-per-cause rule keys on.
	Cause string
	// Subjects are the subjects the note is open on.
	Subjects []string
	// Text is the note's text.
	Text string
	// Decisions are the decisions the note offers, printed as verbs.
	Decisions []string
	// Until is a hold's R, or the review time of a note left open.
	Until int64
}

// RulePlan is what a rule plans on one tick: the plan for the tables, and
// everything besides that the step builder puts in the same requests (1.3.6).
type RulePlan struct {
	// Plan is what the rule does to the tables.
	Plan Plan
	// Intents are the effects Lua decides at apply (1.3.3).
	Intents []Intent
	// Guards are the sprint keys the plan is guarded on.
	Guards []XGuard
	// Notes are the notes the rule asks J for.
	Notes []NoteReq
	// Done are the agenda keys the plan removes in its step, and Requeue those
	// it puts back with their orders kept.
	Done    []AgendaKey
	Requeue []AgendaKey
	// Quarantine are the cards the rule found refused (1.3.5).
	Quarantine []Quarantined
	// HeldBack are the keys whose only work was in dropping streams (1.3.5):
	// they stay in the agenda, and the loop does not plan them again until a
	// dropping mark clears or a new line queues them.
	HeldBack []AgendaKey
}

// ReadBounds are the limits one read of a tick is planned within (1.0, 1.4.2):
// the number of queries, the records they may return, the ids their ranges
// may name, and the bytes of the answer.
type ReadBounds struct{ Queries, Records, RangeIDs, Bytes int }

// Cost is what a query, or a read plan, may cost the store (1.0): the records
// it may return, the ids its ranges may name and the bytes of its answer.
type Cost struct{ Records, RangeIDs, Bytes int }

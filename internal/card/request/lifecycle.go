// The lifecycle of a card lives in this file and nowhere else in the card layer:
// the states, the lifecycle inputs, the transition table, the operation moves
// (resolve and replace), the forced moves by evidence and the judgment
// classification of all of them. Nothing else in the request package or the
// definition package names a state or an input type in a switch or a list; they
// ask this file (Destination, Transition, ForcedMove, Initial, ResolveMove,
// Replaceable, ClassifyInput and the rest).
//
// The state machine is modelled in TLA+: tla/CardManager.tla. A change to a table
// here is a change to the state machine: the model changes in the same PR, and
// TLC runs on it. TestLifecycleAgreesWithTheModel reads the model when it is in the
// tree and compares its states and transitions with these tables; it skips, with
// a message that says why, when the model is not there (it is on the branch of
// PR #4599 until that lands).
//
// The rulings this file implements (the owner's, 2026-09-29):
//
//   - the states are waiting, ready, working, review, merging and landed: six;
//   - landed is final and means the code is on the development branch: a fact
//     observed from the repository, decided by nobody, so the inputs that reach it
//     (landing, external-landing) carry the landing identity, and nothing moves a
//     card out of landed;
//   - there is no done state and no confirmation after landing;
//   - a card that stops any other way (cancelled, replaced, or its dependency
//     failed) leaves the table: it is removed from its cell, unplaced, and its
//     record stays with an outcome and a reason;
//   - a kind that needs no pull request has no code to land, so no input takes
//     such a card to an end; that is an open question for the owner, and no state
//     is invented for it (it can be cancelled, which leaves the table);
//   - a CI result is not a lifecycle input: it is evidence, and a red one recorded
//     for a card in merging returns it to review in the same batch (the one forced
//     move today).
package request

// State is a card state, the column a card is placed in. A card that has left the
// table has no state: it is unplaced (Unplaced), and its record carries an Outcome.
type State string

// The card states.
const (
	Waiting State = "waiting"
	Ready   State = "ready"
	Working State = "working"
	Review  State = "review"
	Merging State = "merging"
	Landed  State = "landed"
)

// Unplaced is where a card that left the table is: in no cell. It is not a
// state and not a column.
const Unplaced State = ""

// Initial is the state an admitted card is created in.
const Initial = Waiting

var allStates = [...]State{Waiting, Ready, Working, Review, Merging, Landed}

// States lists every state in lifecycle order, as a new slice.
func States() []State { return append([]State(nil), allStates[:]...) }

// Valid reports whether s is one of the six states.
func (s State) Valid() bool {
	for _, v := range allStates {
		if s == v {
			return true
		}
	}
	return false
}

// Outcome is what a card that left the table carries, with its reason. A card
// that is placed has none.
type Outcome string

// The outcomes of a card that left the table.
const (
	Cancelled        Outcome = "cancelled"
	DependencyFailed Outcome = "dependency-failed"
	Replaced         Outcome = "replaced"
)

var allOutcomes = [...]Outcome{Cancelled, DependencyFailed, Replaced}

// Outcomes lists every outcome, as a new slice.
func Outcomes() []Outcome { return append([]Outcome(nil), allOutcomes[:]...) }

// Valid reports whether o is one of the three outcomes.
func (o Outcome) Valid() bool {
	for _, v := range allOutcomes {
		if o == v {
			return true
		}
	}
	return false
}

// InputType is one of the closed set of lifecycle input types. It is split where
// the destination depends on the variant (a verdict), so that Destination is a
// pure function of the type and the source state.
type InputType string

// The lifecycle input types. A CI result is not among them: it is evidence.
const (
	InStart            InputType = "start"
	InResult           InputType = "result"
	InVerdictAccept    InputType = "verdict-accept"
	InVerdictRetry     InputType = "verdict-retry"
	InVerdictRework    InputType = "verdict-rework"
	InHead             InputType = "head"
	InQueueRejected    InputType = "queue-rejected"
	InCancel           InputType = "cancel"
	InLanding          InputType = "landing"
	InExternalLanding  InputType = "external-landing"
	InDependencyFailed InputType = "dependency-failed"
)

var allInputTypes = [...]InputType{
	InStart, InResult, InVerdictAccept, InVerdictRetry, InVerdictRework, InHead, InQueueRejected,
	InCancel, InLanding, InExternalLanding, InDependencyFailed,
}

// InputTypes lists every lifecycle input type, as a new slice.
func InputTypes() []InputType { return append([]InputType(nil), allInputTypes[:]...) }

// Class is the class of a transition, a forced move, an observation or a selection
// outcome: whether it is mechanical forward progress, or a point where judgment may
// be required. Every judgment point yields a notification to the coordinator;
// nothing in this layer answers one by moving the card again.
type Class string

// The two classes.
const (
	// Mechanical is forward progress, or a fact that needs no decision.
	Mechanical Class = "mechanical"
	// Judgment is a point where judgment may be required: a return to an earlier
	// state, a red CI result, a reader's rejection, a retry or rework verdict, a
	// queue rejection, a changed head in review or merging, a card blocked on a
	// dependency, a dependency that failed, a replacement, a cancellation, a result
	// that reports failure or a return, a landing outside the review path.
	Judgment Class = "judgment"
)

// transition is one row of the transition table: what a lifecycle input type is.
// Every row leaves the card in `to` (a state), or unplaced with `outcome` when the
// card leaves the table. The Input fields it requires or allows beyond the
// digest, issuer and source every input carries are named by wire name.
type transition struct {
	from     []State
	to       State // Unplaced when the card leaves the table
	outcome  Outcome
	class    Class
	required []string
	allowed  []string
}

// transitions is THE transition table: one row per lifecycle input type, and
// nothing else in the card layer says which input moves a card where.
var transitions = map[InputType]transition{
	// a worker took the card
	InStart: {from: []State{Ready}, to: Working, class: Mechanical},
	// a bound result; a result that reports failure or a return is a judgment point (ClassifyResult)
	InResult: {from: []State{Working}, to: Review, class: Mechanical, required: []string{"result"}, allowed: []string{"head"}},
	// the coordinator's decision to merge, after the exact-head review and CI
	InVerdictAccept: {from: []State{Review}, to: Merging, class: Mechanical, required: []string{"head"}},
	// a reasoned retry or rework verdict returns the card to ready
	InVerdictRetry:  {from: []State{Review}, to: Ready, class: Judgment, required: []string{"reason"}, allowed: []string{"head"}},
	InVerdictRework: {from: []State{Review}, to: Ready, class: Judgment, required: []string{"reason"}, allowed: []string{"head"}},
	// a changed code head: in merging it returns the card to review; in review it
	// stays there and the acceptance and CI at the old head no longer count
	InHead: {from: []State{Merging, Review}, to: Review, class: Judgment, required: []string{"head"}},
	// the merge queue rejected the head: back to review
	InQueueRejected: {from: []State{Merging}, to: Review, class: Judgment, required: []string{"head", "reason"}},
	// landed is observed from the repository: the code is on the development
	// branch. It is final: nothing moves a card out of landed.
	InLanding:         {from: []State{Merging}, to: Landed, class: Mechanical, required: []string{"head", "landing"}},
	InExternalLanding: {from: []State{Waiting, Ready, Working}, to: Landed, class: Judgment, required: []string{"head", "landing"}},
	// a card that stops leaves the table with an outcome
	InCancel:           {from: []State{Waiting, Ready, Working, Review, Merging}, to: Unplaced, outcome: Cancelled, class: Judgment, required: []string{"reason"}},
	InDependencyFailed: {from: []State{Waiting}, to: Unplaced, outcome: DependencyFailed, class: Judgment, required: []string{"dependency"}},
}

// resolveMove is the one move a resolve makes: a waiting card whose prerequisites
// are met moves to ready. The prerequisites are the manager's to check.
var resolveMove = struct{ from, to State }{Waiting, Ready}

// replaceFrom are the states a card can be replaced from, and replaceOutcome what
// the old card leaves the table as. The new card is created in Initial.
var (
	replaceFrom    = []State{Waiting, Ready}
	replaceOutcome = Replaced
)

// forcedMoves is the data of the forced moves: an observation recorded as
// evidence that moves the card in the batch that records it. It has exactly one
// entry today: a red CI result recorded for a card in merging returns it to review.
// Every other combination forces nothing. The policy that applies a forced move is
// the manager's, not this package's.
var forcedMoves = []Forced{
	{Kind: KindCI, Disposition: DispRed, From: Merging, To: Review},
}

// evidenceClass classifies recording one observation: a positive one is
// mechanical, a negative one a judgment point.
var evidenceClass = map[EvidenceKind]map[Disposition]Class{
	KindRead:    {DispAccept: Mechanical, DispReject: Judgment},
	KindCI:      {DispGreen: Mechanical, DispRed: Judgment},
	KindSweep:   {DispClean: Mechanical, DispNegative: Judgment},
	KindLanding: {DispLanded: Mechanical},
	KindQueue:   {DispReject: Judgment},
}

// resultClass classifies the value of a result input.
var resultClass = map[ResultValue]Class{ResultSuccess: Mechanical, ResultFailure: Judgment, ResultReturn: Judgment}

// outcomeClass classifies each selection outcome.
var outcomeClass = map[SelectionOutcome]Class{
	OutcomeChanged:      Mechanical, // the card's own transition carries its class
	OutcomeBlocked:      Judgment,
	OutcomeIneligible:   Mechanical,
	OutcomeMissing:      Judgment,
	OutcomeAlready:      Mechanical,
	OutcomeInapplicable: Mechanical, // a negative record is Judgment by ClassifyEvidence
}

// judgmentRule is whether a notification kind is always a judgment point, never
// one, or one depending on what it carries.
type judgmentRule int

const (
	ruleNever judgmentRule = iota
	ruleAlways
	ruleEither
)

var notificationRule = map[NotificationKind]judgmentRule{
	NoteAdmitted: ruleNever, NoteReady: ruleNever, NoteStarted: ruleNever, NoteMerging: ruleNever,
	NoteLanded: ruleNever, NoteReadAccept: ruleNever, NoteLandingRecorded: ruleNever,
	NoteBlocked: ruleAlways, NoteReturned: ruleAlways, NoteAuthorized: ruleAlways, NoteLandedExternal: ruleAlways,
	NoteCancelled: ruleAlways, NoteDependencyFailed: ruleAlways, NoteReplaced: ruleAlways, NoteHead: ruleAlways,
	NoteReadReject: ruleAlways, NoteCIRed: ruleAlways, NoteStale: ruleAlways, NoteForeignWrite: ruleAlways,
	NoteResult: ruleEither, NoteInapplicable: ruleEither, NoteCIGreen: ruleEither, NoteOtherHead: ruleEither, NoteSweep: ruleEither,
}

// The accessors below answer from the tables above. They are the whole lifecycle
// interface of the card layer.

// Valid reports whether t is one of the lifecycle input types.
func (t InputType) Valid() bool { _, ok := transitions[t]; return ok }

// Move is where an input takes a card: To is the state it is placed in, or Unplaced
// when it leaves the table, in which case Outcome says what it left as.
type Move struct {
	To      State
	Outcome Outcome
}

// Leaves reports whether the move takes the card off the table.
func (m Move) Leaves() bool { return m.To == Unplaced }

// Transition returns where a lifecycle input of the given type takes a card in the
// source state. ok is false when the type and the source state have no listed
// transition; such an input refuses. A move to the state the card is already in
// (a new head while in review) is listed.
func Transition(t InputType, source State) (Move, bool) {
	row, ok := transitions[t]
	if !ok {
		return Move{}, false
	}
	for _, s := range row.from {
		if s == source {
			return Move{To: row.to, Outcome: row.outcome}, true
		}
	}
	return Move{}, false
}

// Destination returns the state a card moves to when a lifecycle input of the given
// type is applied to a card in the source state; Unplaced when the card leaves the
// table. ok is false when the type and the source state have no listed transition.
// OutcomeOf gives the outcome of a card that left.
func Destination(t InputType, source State) (State, bool) {
	m, ok := Transition(t, source)
	return m.To, ok
}

// OutcomeOf returns the outcome a card takes from an input type that takes it off
// the table, and false for a type that does not.
func OutcomeOf(t InputType) (Outcome, bool) {
	row, ok := transitions[t]
	if !ok || row.outcome == "" {
		return "", false
	}
	return row.outcome, true
}

// SourceStates lists the source states an input type has a transition from, in
// lifecycle order.
func SourceStates(t InputType) []State {
	row := transitions[t]
	var out []State
	for _, s := range allStates {
		for _, f := range row.from {
			if f == s {
				out = append(out, s)
			}
		}
	}
	return out
}

// IsTerminal reports whether no lifecycle input leaves the state.
func IsTerminal(s State) bool {
	for _, row := range transitions {
		for _, f := range row.from {
			if f == s {
				return false
			}
		}
	}
	return true
}

// fieldsOf returns the Input fields a type requires and allows beyond the digest,
// issuer and source every input carries, by wire name.
func fieldsOf(t InputType) (required, allowed []string, ok bool) {
	row, ok := transitions[t]
	return row.required, row.allowed, ok
}

// NamesDependency reports whether the input type carries the ID of a failed
// prerequisite.
func (t InputType) NamesDependency() bool { return contains(transitions[t].required, "dependency") }

// ResolveMove is the one move a resolve makes, a waiting card to ready.
func ResolveMove() (from, to State) { return resolveMove.from, resolveMove.to }

// Replaceable reports whether a card in the state can be replaced: the old card
// leaves the table as replaced and its successor is created in Initial.
func Replaceable(s State) bool {
	for _, v := range replaceFrom {
		if v == s {
			return true
		}
	}
	return false
}

// HoldsLanding reports whether a card in the state holds a landing identity: the
// state that an input which requires one moves it to.
func HoldsLanding(s State) bool {
	for _, row := range transitions {
		if row.to == s && contains(row.required, "landing") {
			return true
		}
	}
	return false
}

// HasSuccessor reports whether a card that left the table with this outcome names
// the card that replaced it.
func (o Outcome) HasSuccessor() bool { return o == replaceOutcome }

// ReplaceOutcome is what a replaced card leaves the table as.
func ReplaceOutcome() Outcome { return replaceOutcome }

// ReplaceableStates lists the states a card can be replaced from, in lifecycle order.
func ReplaceableStates() []State {
	var out []State
	for _, s := range allStates {
		if Replaceable(s) {
			out = append(out, s)
		}
	}
	return out
}

// Forced is one forced move: an observation recorded as evidence that moves the
// card in the same batch that records it.
type Forced struct {
	Kind        EvidenceKind
	Disposition Disposition
	From, To    State
}

// ForcedMoves lists the forced moves, as a new slice.
func ForcedMoves() []Forced { return append([]Forced(nil), forcedMoves...) }

// ForcedMove returns the state an evidence record forces a card in the given
// state into, and false when it forces nothing. Only a red CI result for a card
// in merging forces a move: to review.
func ForcedMove(kind EvidenceKind, disposition Disposition, state State) (State, bool) {
	for _, f := range forcedMoves {
		if f.Kind == kind && f.Disposition == disposition && f.From == state {
			return f.To, true
		}
	}
	return "", false
}

// ClassifyInput is the class of a lifecycle input type, and false for a type that
// has no classification (a type added to InputTypes must have a row in the
// transition table; a test fails until it does).
func ClassifyInput(t InputType) (Class, bool) {
	row, ok := transitions[t]
	if !ok || row.class == "" {
		return "", false
	}
	return row.class, true
}

// ClassifyResult is the class of a result value: success is mechanical, a failure
// or a return is a point where judgment may be required.
func ClassifyResult(v ResultValue) (Class, bool) { c, ok := resultClass[v]; return c, ok }

// ClassifyForcedMove is the class of a forced move: every forced move is a return
// to an earlier state and a judgment point. It returns false for a combination
// that forces nothing.
func ClassifyForcedMove(kind EvidenceKind, d Disposition, state State) (Class, bool) {
	if _, ok := ForcedMove(kind, d, state); ok {
		return Judgment, true
	}
	return "", false
}

// ClassifyEvidence is the class of recording an observation: a positive one
// (accept, green, clean, landed) is mechanical, a negative one (reject, red,
// negative) is a judgment point. It returns false for a disposition the kind does
// not take.
func ClassifyEvidence(kind EvidenceKind, d Disposition) (Class, bool) {
	c, ok := evidenceClass[kind][d]
	return c, ok
}

// ClassifyOutcome is the class of a selection outcome, and false for an outcome
// with no classification.
func ClassifyOutcome(o SelectionOutcome) (Class, bool) { c, ok := outcomeClass[o]; return c, ok }

// Valid reports whether o is a selection outcome.
func (o SelectionOutcome) Valid() bool { _, ok := outcomeClass[o]; return ok }

// DispositionsOf lists the dispositions an evidence kind takes.
func DispositionsOf(k EvidenceKind) []Disposition {
	var out []Disposition
	for _, d := range allDispositions {
		if _, ok := evidenceClass[k][d]; ok {
			out = append(out, d)
		}
	}
	return out
}

// Negative reports whether the disposition is an observation against the card (a
// rejection, a red result, a negative sweep): one some kind classifies as a
// judgment point.
func (d Disposition) Negative() bool {
	for _, byDisp := range evidenceClass {
		if byDisp[d] == Judgment {
			return true
		}
	}
	return false
}

// inputFields lists the optional Input fields by wire name; the transition table
// says which each type requires or allows.
var inputFields = []string{"head", "result", "reason", "dependency", "landing"}

func (e *Input) field(name string) string {
	switch name {
	case "head":
		return e.Head
	case "result":
		return string(e.Result)
	case "reason":
		return e.Reason
	case "dependency":
		return string(e.Dependency)
	case "landing":
		return e.Landing
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

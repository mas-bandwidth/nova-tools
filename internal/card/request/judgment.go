package request

// Class is the class of a transition, a forced move, an observation or a
// selection outcome: whether it is mechanical forward progress, or a point where
// judgment may be required. Every judgment point yields a notification to the
// coordinator; nothing in this layer answers one by moving the card again.
type Class string

// The two classes.
const (
	// Mechanical is forward progress, or a fact that needs no decision.
	Mechanical Class = "mechanical"
	// Judgment is a point where judgment may be required: a return to an earlier
	// state, a red CI result, a reader's rejection, a retry or rework verdict, a
	// queue rejection, a changed head in review or merging, a card blocked on a
	// dependency, a dependency that failed, a replacement, a cancellation, a
	// result that reports failure or a return, a landing outside the review path.
	Judgment Class = "judgment"
)

// inputClass classifies every lifecycle input type. A result is mechanical when
// it reports success; ClassifyResult classifies the value.
var inputClass = map[InputType]Class{
	InStart:            Mechanical,
	InResult:           Mechanical,
	InVerdictAccept:    Mechanical,
	InVerdictRetry:     Judgment,
	InVerdictRework:    Judgment,
	InHead:             Judgment,
	InQueueRejected:    Judgment,
	InCancel:           Judgment,
	InLanding:          Mechanical,
	InExternalLanding:  Judgment,
	InDependencyFailed: Judgment,
	InCompleted:        Mechanical,
}

// ClassifyInput is the class of a lifecycle input type, and false for a type that
// has no classification (a type added to InputTypes must be added here; a test
// fails until it is).
func ClassifyInput(t InputType) (Class, bool) { c, ok := inputClass[t]; return c, ok }

// ClassifyResult is the class of a result value: success is mechanical, a failure
// or a return is a point where judgment may be required.
func ClassifyResult(v ResultValue) (Class, bool) {
	switch v {
	case ResultSuccess:
		return Mechanical, true
	case ResultFailure, ResultReturn:
		return Judgment, true
	}
	return "", false
}

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
	for _, v := range DispositionsOf(kind) {
		if v == d {
			if d.Negative() {
				return Judgment, true
			}
			return Mechanical, true
		}
	}
	return "", false
}

// SelectionOutcome is what an operation reports about an entry that it did not
// or did change: a declared outcome, never a refusal.
type SelectionOutcome string

// The selection outcomes.
const (
	// OutcomeChanged is an entry the operation applied.
	OutcomeChanged SelectionOutcome = "changed"
	// OutcomeBlocked is a waiting card with a prerequisite that is not met.
	OutcomeBlocked SelectionOutcome = "blocked"
	// OutcomeIneligible is a selected card in a state the operation does not act on.
	OutcomeIneligible SelectionOutcome = "ineligible"
	// OutcomeMissing is a named card or prerequisite that does not exist.
	OutcomeMissing SelectionOutcome = "missing"
	// OutcomeAlready is an entry whose identical observation is already recorded.
	OutcomeAlready SelectionOutcome = "already"
	// OutcomeInapplicable is an observation for a card whose state takes none.
	OutcomeInapplicable SelectionOutcome = "inapplicable"
)

var allSelectionOutcomes = [...]SelectionOutcome{OutcomeChanged, OutcomeBlocked, OutcomeIneligible, OutcomeMissing, OutcomeAlready, OutcomeInapplicable}

// SelectionOutcomes lists every selection outcome, as a new slice.
func SelectionOutcomes() []SelectionOutcome {
	return append([]SelectionOutcome(nil), allSelectionOutcomes[:]...)
}

var outcomeClass = map[SelectionOutcome]Class{
	OutcomeChanged:      Mechanical, // the card's own transition carries its class
	OutcomeBlocked:      Judgment,
	OutcomeIneligible:   Mechanical,
	OutcomeMissing:      Judgment,
	OutcomeAlready:      Mechanical,
	OutcomeInapplicable: Mechanical, // a negative record is Judgment by ClassifyEvidence
}

// ClassifyOutcome is the class of a selection outcome, and false for an outcome
// with no classification.
func ClassifyOutcome(o SelectionOutcome) (Class, bool) { c, ok := outcomeClass[o]; return c, ok }

// Valid reports whether o is a selection outcome.
func (o SelectionOutcome) Valid() bool { _, ok := outcomeClass[o]; return ok }

// NotificationKind is what a notification reports. A notification is derived
// from the receipt of the batch that did it, by the manager; this package holds
// only the type.
type NotificationKind string

// The notification kinds.
const (
	NoteAdmitted         NotificationKind = "admitted"
	NoteReady            NotificationKind = "ready"
	NoteBlocked          NotificationKind = "blocked"
	NoteInapplicable     NotificationKind = "inapplicable"
	NoteStarted          NotificationKind = "started"
	NoteResult           NotificationKind = "result"
	NoteReturned         NotificationKind = "returned"
	NoteAuthorized       NotificationKind = "authorized"
	NoteMerging          NotificationKind = "merging"
	NoteLanded           NotificationKind = "landed"
	NoteLandedExternal   NotificationKind = "landed-external"
	NoteCancelled        NotificationKind = "cancelled"
	NoteDependencyFailed NotificationKind = "dependency-failed"
	NoteCompleted        NotificationKind = "completed"
	NoteReplaced         NotificationKind = "replaced"
	NoteHead             NotificationKind = "head"
	NoteReadAccept       NotificationKind = "read-accept"
	NoteReadReject       NotificationKind = "read-reject"
	NoteCIGreen          NotificationKind = "ci-green"
	NoteCIRed            NotificationKind = "ci-red"
	NoteOtherHead        NotificationKind = "other-head"
	NoteSweep            NotificationKind = "sweep"
	NoteLandingRecorded  NotificationKind = "landing-recorded"
	NoteStale            NotificationKind = "stale"
	NoteForeignWrite     NotificationKind = "foreign-write"
)

var allNotificationKinds = [...]NotificationKind{
	NoteAdmitted, NoteReady, NoteBlocked, NoteInapplicable, NoteStarted, NoteResult, NoteReturned,
	NoteAuthorized, NoteMerging, NoteLanded, NoteLandedExternal, NoteCancelled, NoteDependencyFailed,
	NoteCompleted, NoteReplaced, NoteHead, NoteReadAccept, NoteReadReject, NoteCIGreen, NoteCIRed,
	NoteOtherHead, NoteSweep, NoteLandingRecorded, NoteStale, NoteForeignWrite,
}

// NotificationKinds lists every notification kind, as a new slice.
func NotificationKinds() []NotificationKind {
	return append([]NotificationKind(nil), allNotificationKinds[:]...)
}

// Valid reports whether k is a notification kind.
func (k NotificationKind) Valid() bool {
	for _, v := range allNotificationKinds {
		if k == v {
			return true
		}
	}
	return false
}

// Mark is an escalation mark: a cycle counter, or an age, past its declared
// threshold. The thresholds are the manager policy's data; this package holds the
// closed set of names.
type Mark string

// The escalation marks.
const (
	MarkRedSameHead Mark = "red-same-head"
	MarkRedTotal    Mark = "red-total"
	MarkFlaky       Mark = "flaky"
	MarkMergeReturn Mark = "merge-return"
	MarkRework      Mark = "rework"
	MarkReject      Mark = "reject"
	MarkNoResult    Mark = "no-result"
	MarkFailed      Mark = "failed-result"
	MarkReturns     Mark = "returns"
	MarkLineage     Mark = "lineage"
	MarkStale       Mark = "stale"
)

var allMarks = [...]Mark{MarkRedSameHead, MarkRedTotal, MarkFlaky, MarkMergeReturn, MarkRework, MarkReject, MarkNoResult, MarkFailed, MarkReturns, MarkLineage, MarkStale}

// Marks lists every escalation mark, as a new slice.
func Marks() []Mark { return append([]Mark(nil), allMarks[:]...) }

// Valid reports whether m is an escalation mark.
func (m Mark) Valid() bool {
	for _, v := range allMarks {
		if m == v {
			return true
		}
	}
	return false
}

// Drift is a structural drift a check reports and inspect carries: what is wrong
// with a card's record, never repaired here.
type Drift string

// The kinds of structural drift.
const (
	DriftFields    Drift = "fields"
	DriftOutcome   Drift = "outcome"
	DriftSuccessor Drift = "succ"
	DriftLanding   Drift = "landing"
	DriftDeps      Drift = "deps"
	DriftEvidence  Drift = "evidence"
	DriftStanding  Drift = "standing"
	DriftCounters  Drift = "counters"
	DriftKind      Drift = "kind"
	DriftUnplaced  Drift = "unplaced"
	DriftOperation Drift = "operation"
)

var allDrifts = [...]Drift{DriftFields, DriftOutcome, DriftSuccessor, DriftLanding, DriftDeps, DriftEvidence, DriftStanding, DriftCounters, DriftKind, DriftUnplaced, DriftOperation}

// Drifts lists every kind of drift, as a new slice.
func Drifts() []Drift { return append([]Drift(nil), allDrifts[:]...) }

// Valid reports whether d is a kind of drift.
func (d Drift) Valid() bool {
	for _, v := range allDrifts {
		if d == v {
			return true
		}
	}
	return false
}

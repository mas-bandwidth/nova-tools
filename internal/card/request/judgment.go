package request

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
	NoteReplaced, NoteHead, NoteReadAccept, NoteReadReject, NoteCIGreen, NoteCIRed,
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

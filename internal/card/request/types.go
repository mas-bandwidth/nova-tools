package request

import (
	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// ID is a card ID: nonempty ASCII letters, digits, underscore and hyphen, at most
// 64 bytes, never the reserved words "-" and "none". It is the card layer's one
// ID type.
type ID = card.ID

// Digest is a SHA-256 in 64 lower-case hexadecimal characters: a definition
// digest, a request hash, a receipt identity. It is the card layer's one Digest.
type Digest = card.Digest

// Changed is the answer a refusal gives to "did anything change", derived from
// its cause (Cause.Changed) and never stored beside it.
type Changed = card.Changed

// The values of Changed.
const (
	ChangedNo      = card.ChangedNo
	ChangedUnknown = card.ChangedUnknown
)

// State is a card state, the column a card is placed in.
type State string

// The card states.
const (
	Waiting State = "waiting"
	Ready   State = "ready"
	Working State = "working"
	Review  State = "review"
	Merging State = "merging"
	Landed  State = "landed"
	Done    State = "done"
)

var allStates = [...]State{Waiting, Ready, Working, Review, Merging, Landed, Done}

// States lists every state in lifecycle order, as a new slice.
func States() []State { return append([]State(nil), allStates[:]...) }

// Valid reports whether s is one of the seven states.
func (s State) Valid() bool {
	for _, v := range allStates {
		if s == v {
			return true
		}
	}
	return false
}

// Outcome is what a card in Done carries.
type Outcome string

// The outcomes of a done card. A card in any other state has none.
const (
	Completed        Outcome = "completed"
	Cancelled        Outcome = "cancelled"
	DependencyFailed Outcome = "dependency-failed"
	Replaced         Outcome = "replaced"
)

var allOutcomes = [...]Outcome{Completed, Cancelled, DependencyFailed, Replaced}

// Outcomes lists every outcome, as a new slice.
func Outcomes() []Outcome { return append([]Outcome(nil), allOutcomes[:]...) }

// Valid reports whether o is one of the four outcomes.
func (o Outcome) Valid() bool {
	for _, v := range allOutcomes {
		if o == v {
			return true
		}
	}
	return false
}

// Operation names what a request asks of the manager.
type Operation string

// The operations. Inspect and check only read.
const (
	OpAdmit          Operation = "admit"
	OpResolve        Operation = "resolve"
	OpApplyEvents    Operation = "apply_events"
	OpRecordEvidence Operation = "record_evidence"
	OpReplace        Operation = "replace"
	OpInspect        Operation = "inspect"
	OpCheck          Operation = "check"
)

var allOperations = [...]Operation{OpAdmit, OpResolve, OpApplyEvents, OpRecordEvidence, OpReplace, OpInspect, OpCheck}

// Operations lists every operation, as a new slice.
func Operations() []Operation { return append([]Operation(nil), allOperations[:]...) }

// Valid reports whether o is a known operation.
func (o Operation) Valid() bool {
	for _, v := range allOperations {
		if o == v {
			return true
		}
	}
	return false
}

// Mutating reports whether the operation writes. Every mutating operation
// carries the full envelope; inspect and check carry the table and their scope.
func (o Operation) Mutating() bool { return o.Valid() && o != OpInspect && o != OpCheck }

// Place is where a card sits: its stream row and its column, which is its state.
type Place struct {
	Row string
	Col State
}

// Expect is the guard an entry declares about a card that exists: its place and
// its exact revision (a decimal counter, at least 1). An evidence entry may leave
// the revision out: the revision is then a freshness guard the observer did not
// take, and the place alone is guarded.
type Expect struct {
	Revision string
	Place    Place
}

// Admission asks the manager to admit one committed definition as a new card in
// waiting, in the stream row Row. It is the pinned admission of the definition
// package plus the row: the card's ID, the definition's Digest and where it was
// read (ObjectID, Commit, Repository, Path), its Kind, its DependsOn (sorted and
// unique, none of them the card), its Entry and Title, and the identity of the
// review policy the card is pinned to at admission (PolicyVersion, a decimal
// counter of at least 1, and PolicyDigest). The card must be absent; the guard is
// implied. The policy itself belongs to the manager.
type Admission struct {
	ID            ID
	Digest        Digest
	ObjectID      string
	Commit        string
	Repository    card.Repository
	Path          string
	Kind          string
	DependsOn     []ID
	Entry         string
	Title         string
	Row           string
	PolicyVersion string
	PolicyDigest  Digest
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
	InCompleted        InputType = "completed"
)

var allInputTypes = [...]InputType{
	InStart, InResult, InVerdictAccept, InVerdictRetry, InVerdictRework, InHead, InQueueRejected,
	InCancel, InLanding, InExternalLanding, InDependencyFailed, InCompleted,
}

// InputTypes lists every lifecycle input type, as a new slice.
func InputTypes() []InputType { return append([]InputType(nil), allInputTypes[:]...) }

// ResultValue is the value of a result input: what the worker reports.
type ResultValue string

// The values of an input's result.
const (
	ResultSuccess ResultValue = "success"
	ResultFailure ResultValue = "failure"
	ResultReturn  ResultValue = "return"
)

// ResultValues lists the result values, as a new slice.
func ResultValues() []ResultValue { return []ResultValue{ResultSuccess, ResultFailure, ResultReturn} }

// Input is one lifecycle input for one card: a request that may move it.
// Expect.Place.Col is the source state the input declares; the destination is
// derived by Destination and is not a field. Digest, Issuer and Source bind every
// input to the card's definition, to who issued it and to the artifact it came
// from. The other fields apply to some types only (see the table in
// docs/SPEC-CARD-REQUESTS.md).
type Input struct {
	ID         ID
	Type       InputType
	Expect     Expect
	Digest     Digest
	Issuer     string
	Source     string
	Head       string
	Result     ResultValue
	Reason     string
	Dependency ID
	Landing    string
}

// EvidenceKind is the kind of an evidence record.
type EvidenceKind string

// The kinds of evidence a request may submit, and the one only a lifecycle input
// writes.
const (
	KindRead    EvidenceKind = "read"
	KindCI      EvidenceKind = "ci"
	KindSweep   EvidenceKind = "sweep"
	KindLanding EvidenceKind = "landing"
	// KindQueue is a queue rejection. It is written by the queue-rejected
	// lifecycle input and is never submitted as evidence.
	KindQueue EvidenceKind = "queue"
)

var allEvidenceKinds = [...]EvidenceKind{KindRead, KindCI, KindSweep, KindLanding, KindQueue}

// EvidenceKinds lists every evidence kind, as a new slice.
func EvidenceKinds() []EvidenceKind { return append([]EvidenceKind(nil), allEvidenceKinds[:]...) }

// Disposition is what an evidence record says.
type Disposition string

// The dispositions.
const (
	DispAccept   Disposition = "accept"
	DispReject   Disposition = "reject"
	DispGreen    Disposition = "green"
	DispRed      Disposition = "red"
	DispClean    Disposition = "clean"
	DispNegative Disposition = "negative"
	DispLanded   Disposition = "landed"
)

var allDispositions = [...]Disposition{DispAccept, DispReject, DispGreen, DispRed, DispClean, DispNegative, DispLanded}

// Dispositions lists every disposition, as a new slice.
func Dispositions() []Disposition { return append([]Disposition(nil), allDispositions[:]...) }

// DispositionsOf lists the dispositions a kind takes.
func DispositionsOf(k EvidenceKind) []Disposition {
	switch k {
	case KindRead:
		return []Disposition{DispAccept, DispReject}
	case KindCI:
		return []Disposition{DispGreen, DispRed}
	case KindSweep:
		return []Disposition{DispClean, DispNegative}
	case KindLanding:
		return []Disposition{DispLanded}
	case KindQueue:
		return []Disposition{DispReject}
	}
	return nil
}

// Negative reports whether the disposition is an observation against the card:
// a rejection, a red result, a negative sweep.
func (d Disposition) Negative() bool { return d == DispReject || d == DispRed || d == DispNegative }

// Evidence is the evidence recorded for one card: the card, its expected place
// (and revision, when the observer read it) and one to MaxEvidenceRecordsPerCard
// records. A card appears once in an array; its several observations are its
// Records. Every record binds the card's definition digest and the code head it
// was observed at, so the evidence binds to definition digest plus head; the
// revision of Expect is a freshness guard and binds nothing.
type Evidence struct {
	ID      ID
	Expect  Expect
	Records []Record
}

// Retired names the old card of a replacement: its ID, the definition digest
// admitted for it and its expected revision and place.
type Retired struct {
	ID     ID
	Digest Digest
	Expect Expect
}

// Replacement pairs an old card, to end done/replaced, with the new admission
// that succeeds it.
type Replacement struct {
	Old Retired
	New Admission
}

// Scope selects the cards a resolve, an inspect or a check works on, in one of
// three forms: explicit card IDs, whole rows, or the whole table. A scope is
// complete: it never returns a prefix.
type Scope struct {
	IDs  []ID
	Rows []string
	All  bool
}

// Request is one operation over an array of cards, as a document: the raw form
// Parse reads and Validate checks. Exactly one payload is set, the one its
// Operation names. Epoch and TableRevision are decimal strings bounded as
// uint64; they never pass through a float. OperationID is optional: when absent,
// a Valid request derives one from its hash. An inspect and a check carry only
// Schema, Operation, Table and Scope.
type Request struct {
	Schema        int
	Operation     Operation
	Table         string
	Epoch         string
	TableRevision string
	OperationID   string
	Actor         string

	Admissions   []Admission
	Inputs       []Input
	Evidence     []Evidence
	Replacements []Replacement
	Scope        *Scope
}

// Identity is what names an operation for replay: its table, the epoch it was
// issued at and its operation ID.
type Identity struct {
	Table       string
	Epoch       string
	OperationID string
}

// String renders the identity on one line.
func (i Identity) String() string { return i.Table + "/" + i.Epoch + "/" + i.OperationID }

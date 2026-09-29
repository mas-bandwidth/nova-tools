package request

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ID is a card ID: nonempty ASCII letters, digits, underscore and hyphen, at
// most MaxIDBytes. It holds no comma, colon or storage prefix.
type ID string

// Digest is a SHA-256 in 64 lowercase hexadecimal characters: a card's
// definition digest, a request hash.
type Digest string

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

// States lists every state in lifecycle order.
var States = []State{Waiting, Ready, Working, Review, Merging, Landed, Done}

// Valid reports whether s is one of the seven states.
func (s State) Valid() bool {
	for _, v := range States {
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

// Outcomes lists every outcome.
var Outcomes = []Outcome{Completed, Cancelled, DependencyFailed, Replaced}

// Valid reports whether o is one of the four outcomes.
func (o Outcome) Valid() bool {
	for _, v := range Outcomes {
		if o == v {
			return true
		}
	}
	return false
}

// Operation names what a request asks of the manager.
type Operation string

// The operations. Inspect only reads.
const (
	OpAdmit          Operation = "admit"
	OpResolve        Operation = "resolve"
	OpApplyEvents    Operation = "apply_events"
	OpRecordEvidence Operation = "record_evidence"
	OpReplace        Operation = "replace"
	OpInspect        Operation = "inspect"
)

// Operations lists every operation.
var Operations = []Operation{OpAdmit, OpResolve, OpApplyEvents, OpRecordEvidence, OpReplace, OpInspect}

// Valid reports whether o is a known operation.
func (o Operation) Valid() bool {
	for _, v := range Operations {
		if o == v {
			return true
		}
	}
	return false
}

// Mutating reports whether the operation writes; every mutating operation
// carries the full envelope.
func (o Operation) Mutating() bool { return o.Valid() && o != OpInspect }

// Place is where a card sits: its stream row and its column, which is its state.
type Place struct {
	Row string `json:"row"`
	Col State  `json:"col"`
}

// Expect is the guard an entry declares about a card that exists: its exact
// revision (a decimal counter, at least 1) and its place.
type Expect struct {
	Revision string `json:"revision"`
	Place    Place  `json:"place"`
}

// Admission asks the manager to admit one committed definition as a new card in
// waiting, in the stream row Row. The card must be absent; the guard is
// implied. Digest is the definition's SHA-256; ObjectID, Commit, Repository and
// Path pin where the definition was read.
type Admission struct {
	ID         ID     `json:"id"`
	Digest     Digest `json:"digest"`
	ObjectID   string `json:"object_id"`
	Commit     string `json:"commit"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Row        string `json:"row"`
}

// EventType is one of the closed set of typed events. It is split where the
// destination depends on the variant (a verdict, a CI result), so that
// Destination is a pure function of the type and the source state.
type EventType string

// The event types.
const (
	EvStart            EventType = "start"
	EvResult           EventType = "result"
	EvVerdictAccept    EventType = "verdict-accept"
	EvVerdictRetry     EventType = "verdict-retry"
	EvVerdictRework    EventType = "verdict-rework"
	EvHead             EventType = "head"
	EvCIGreen          EventType = "ci-green"
	EvCIRed            EventType = "ci-red"
	EvCancel           EventType = "cancel"
	EvLanding          EventType = "landing"
	EvExternalLanding  EventType = "external-landing"
	EvDependencyFailed EventType = "dependency-failed"
	EvCompleted        EventType = "completed"
)

// EventTypes lists every event type.
var EventTypes = []EventType{
	EvStart, EvResult, EvVerdictAccept, EvVerdictRetry, EvVerdictRework, EvHead,
	EvCIGreen, EvCIRed, EvCancel, EvLanding, EvExternalLanding, EvDependencyFailed, EvCompleted,
}

// The values of an event's Result field.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultReturn  = "return"
)

// Event is one typed event for one card. Expect.Place.Col is the source state
// the event declares; the destination is derived by Destination and is not a
// field. Digest, Issuer and Source bind every event to the card's definition,
// to who issued it and to the artifact it came from. The other fields apply to
// some types only (see the event table in docs/SPEC-CARD-REQUESTS.md).
type Event struct {
	ID         ID        `json:"id"`
	Type       EventType `json:"type"`
	Expect     Expect    `json:"expect"`
	Digest     Digest    `json:"digest"`
	Issuer     string    `json:"issuer"`
	Source     string    `json:"source"`
	Head       string    `json:"head,omitempty"`
	Result     string    `json:"result,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Dependency ID        `json:"dependency,omitempty"`
	Landing    string    `json:"landing,omitempty"`
}

// The kinds and dispositions of an evidence record.
const (
	KindRead = "read"
	KindCI   = "ci"

	DispAccept = "accept"
	DispReject = "reject"
	DispGreen  = "green"
	DispRed    = "red"
)

// EvidenceRecord is one observation bound to the entry's card digest and
// revision: a reader's disposition (accept or reject) or a CI result (green or
// red), with the code head it applies to (required for CI), the issuer and the
// source artifact identity.
type EvidenceRecord struct {
	EvidenceID  ID     `json:"evidence_id"`
	Kind        string `json:"kind"`
	Disposition string `json:"disposition"`
	Head        string `json:"head,omitempty"`
	Issuer      string `json:"issuer"`
	Source      string `json:"source"`
}

// Evidence is the evidence recorded for one card: the card's exact digest and
// revision and place, and one to MaxEvidenceRecordsPerCard records. A card
// appears once in an array; its several observations are its Records.
type Evidence struct {
	ID      ID               `json:"id"`
	Digest  Digest           `json:"digest"`
	Expect  Expect           `json:"expect"`
	Records []EvidenceRecord `json:"records"`
}

// Retired names the old card of a replacement: its ID, the definition digest
// admitted for it and its expected revision and place.
type Retired struct {
	ID     ID     `json:"id"`
	Digest Digest `json:"digest"`
	Expect Expect `json:"expect"`
}

// Replacement pairs an old card, to end done/replaced, with the new admission
// that succeeds it.
type Replacement struct {
	Old Retired   `json:"old"`
	New Admission `json:"new"`
}

// Scope selects the cards a resolve or an inspect works on: an explicit ID
// array, or a complete declared selection of a row, a column or both, with the
// bound the selection may not exceed. A selection larger than its bound
// refuses at the store; it never returns a prefix.
type Scope struct {
	IDs   []ID   `json:"ids,omitempty"`
	Row   string `json:"row,omitempty"`
	Col   State  `json:"col,omitempty"`
	Bound int    `json:"bound,omitempty"`
}

// Request is one operation over an array of cards. Exactly one payload is set,
// the one its Operation names. Epoch and TableRevision are decimal strings
// bounded as uint64; they never pass through a float. An inspect carries only
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
	Events       []Event
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

// Identity returns the request's operation identity.
func (r *Request) Identity() Identity {
	return Identity{Table: r.Table, Epoch: r.Epoch, OperationID: r.OperationID}
}

var (
	nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	idRE   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	hexRE  = regexp.MustCompile(`^[0-9a-f]+$`)
	ctrRE  = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
)

// checkString reports the first fault of a text value: invalid UTF-8 or a
// replacement character, a control character, or a length over max bytes.
// Empty is not a fault here.
func textFault(s string, max int) (cause Cause, found string) {
	if !utf8.ValidString(s) || strings.ContainsRune(s, utf8.RuneError) {
		return CauseInvalidUTF8, quote(s)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return CauseControlChar, quote(s)
		}
	}
	if len(s) > max {
		return CauseTooLong, fmt.Sprintf("%s (%d bytes)", quote(s), len(s))
	}
	return "", ""
}

// quote renders s for a refusal: quoted, bounded, on one line.
func quote(s string) string {
	if len(s) > MaxFoundBytes {
		cut := MaxFoundBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return strconv.Quote(s[:cut]) + "..."
	}
	return strconv.Quote(s)
}

// ValidID reports whether s is a valid card ID.
func ValidID(s string) bool {
	return s != "" && len(s) <= MaxIDBytes && idRE.MatchString(s)
}

// Valid reports whether the ID is valid.
func (i ID) Valid() bool { return ValidID(string(i)) }

// Valid reports whether the digest is 64 lowercase hexadecimal characters.
func (d Digest) Valid() bool { return len(d) == 64 && hexRE.MatchString(string(d)) }

// validGitID says s is a git object ID in lowercase hex, SHA-1 or SHA-256 form.
func validGitID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && hexRE.MatchString(s)
}

// validCounter says s is a decimal counter bounded as uint64.
func validCounter(s string) bool {
	if len(s) == 0 || len(s) > MaxCounterDigits || !ctrRE.MatchString(s) {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// counterAtLeastOne says s is a valid counter that is not zero.
func counterAtLeastOne(s string) bool { return validCounter(s) && s != "0" }

// validRepoPath says p is a clean repository-relative path: slash separated,
// no empty, dot or dot-dot segment, no backslash, not absolute.
func validRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// validRef says s is an opaque reference: no whitespace.
func validRef(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return s != ""
}

// validReason says s is a one-line reason with no leading or trailing blank.
func validReason(s string) bool {
	return s != "" && s == strings.TrimSpace(s)
}

package card

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Cause is the stable name of why something was refused. The vocabulary is
// closed: a cause is one of the constants below, one name for one fact, shared
// by every package of the card layer.
type Cause string

// Causes of a refusal found in the bytes of a definition or a request, before
// any store call.
const (
	CauseSyntax             Cause = "syntax"
	CauseTrailingData       Cause = "trailing-data"
	CauseDuplicateKey       Cause = "duplicate-key"
	CauseUnknownKey         Cause = "unknown-key"
	CauseNotApplicable      Cause = "not-applicable"
	CauseWrongType          Cause = "wrong-type"
	CauseTooDeep            Cause = "too-deep"
	CauseRequired           Cause = "required"
	CauseInvalidValue       Cause = "invalid-value"
	CauseReservedWord       Cause = "reserved-word"
	CauseInvalidUTF8        Cause = "invalid-utf8"
	CauseControlChar        Cause = "control-character"
	CauseTooLong            Cause = "too-long"
	CauseTooMany            Cause = "too-many"
	CauseTooLarge           Cause = "too-large"
	CauseEmptyArray         Cause = "empty-array"
	CauseRepeatedID         Cause = "repeated-id"
	CauseConflictingInputs  Cause = "conflicting-inputs"
	CauseInputChain         Cause = "input-chain"
	CauseConflictingRecords Cause = "conflicting-records"
	CauseNoTransition       Cause = "no-transition"
	CauseNotEligible        Cause = "not-eligible"
	CauseInvalidRepository  Cause = "invalid-repository"
	CauseInvalidCommit      Cause = "invalid-commit"
	CauseInvalidPath        Cause = "invalid-path"
	CausePathEscapes        Cause = "path-escapes"
)

// Causes of a refusal found reading a definition file or an array of them.
const (
	CauseEmptyFile         Cause = "empty-file"
	CauseDuplicateFile     Cause = "duplicate-file"
	CauseBOM               Cause = "byte-order-mark"
	CauseCarriageReturn    Cause = "carriage-return"
	CauseContractLine      Cause = "contract-line"
	CauseContractSHA       Cause = "contract-sha"
	CauseAmbiguousSpelling Cause = "ambiguous-spelling"
	CauseUnsupportedSchema Cause = "unsupported-schema"
	CauseStranded          Cause = "stranded"
	CauseIDMismatch        Cause = "id-mismatch"
	CauseEmptyValue        Cause = "empty-value"
	CauseInvalidID         Cause = "invalid-id"
	CauseInvalidEntry      Cause = "invalid-entry"
	CauseInvalidKind       Cause = "invalid-kind"
	CauseUnclassifiedKind  Cause = "unclassified-kind"
	CauseInvalidPaths      Cause = "invalid-paths"
	CauseInvalidDependsOn  Cause = "invalid-depends-on"
	CauseInvalidTier       Cause = "invalid-tier"
	CauseInvalidTest       Cause = "invalid-test"
	CauseNoBrief           Cause = "no-brief"
	CauseSelfDependent     Cause = "self-dependency"
	CauseCycle             Cause = "dependency-cycle"
	CauseTooManyExternal   Cause = "too-many-external-dependencies"
)

// Causes of a refusal found pinning definitions to committed git blobs.
const (
	CauseDuplicatePath   Cause = "duplicate-path"
	CauseNotRepository   Cause = "not-repository"
	CauseGitUnavailable  Cause = "git-unavailable"
	CauseGitFailed       Cause = "git-failed"
	CauseTimeout         Cause = "timeout"
	CauseUnknownCommit   Cause = "unknown-commit"
	CauseNotCommit       Cause = "not-commit"
	CauseMissingPath     Cause = "missing-path"
	CauseMissingObject   Cause = "missing-object"
	CauseSymlink         Cause = "symlink"
	CauseNotBlob         Cause = "not-blob"
	CauseIdentityMissing Cause = "identity-missing"
)

// Causes of a store-side refusal, named here so the manager and its receipts
// share one closed set. Nothing in this layer's leaf packages produces them.
const (
	CauseInvalidRequest    Cause = "invalid-request"
	CauseStaleEpoch        Cause = "stale-epoch"
	CauseStaleTableRev     Cause = "stale-table-revision"
	CauseStaleCardRev      Cause = "stale-card-revision"
	CausePlaceMismatch     Cause = "place-mismatch"
	CauseDigestMismatch    Cause = "digest-mismatch"
	CauseOperationConflict Cause = "operation-conflict"
	CauseUnknownRowCol     Cause = "unknown-row-or-column"
	CauseGuardFailed       Cause = "guard-failed"
	CauseOverLimit         Cause = "over-limit"
	// CauseTransport is a failure to reach the store or to read its reply: the
	// request may or may not have been applied.
	CauseTransport Cause = "transport-failure"
	// CauseStoreError is an error the store itself returned when its reply is
	// not proof that nothing was written (a script error after a write, a
	// timeout inside the store): the request may or may not have been applied,
	// and the store is reachable, so the remedy is to reconcile, not to retry
	// the transport.
	CauseStoreError Cause = "store-error"
)

var allCauses = []Cause{
	CauseSyntax, CauseTrailingData, CauseDuplicateKey, CauseUnknownKey, CauseNotApplicable, CauseWrongType,
	CauseTooDeep, CauseRequired, CauseInvalidValue, CauseReservedWord, CauseInvalidUTF8, CauseControlChar,
	CauseTooLong, CauseTooMany, CauseTooLarge, CauseEmptyArray, CauseRepeatedID, CauseConflictingInputs,
	CauseInputChain, CauseConflictingRecords, CauseNoTransition, CauseNotEligible, CauseInvalidRepository, CauseInvalidCommit,
	CauseInvalidPath, CausePathEscapes,
	CauseEmptyFile, CauseDuplicateFile, CauseBOM, CauseCarriageReturn, CauseContractLine, CauseContractSHA,
	CauseAmbiguousSpelling, CauseUnsupportedSchema, CauseStranded, CauseIDMismatch, CauseEmptyValue,
	CauseInvalidID, CauseInvalidEntry, CauseInvalidKind, CauseUnclassifiedKind, CauseInvalidPaths,
	CauseInvalidDependsOn, CauseInvalidTier, CauseInvalidTest, CauseNoBrief, CauseSelfDependent, CauseCycle,
	CauseTooManyExternal,
	CauseDuplicatePath, CauseNotRepository, CauseGitUnavailable, CauseGitFailed, CauseTimeout,
	CauseUnknownCommit, CauseNotCommit, CauseMissingPath, CauseMissingObject, CauseSymlink, CauseNotBlob,
	CauseIdentityMissing,
	CauseInvalidRequest, CauseStaleEpoch, CauseStaleTableRev, CauseStaleCardRev, CausePlaceMismatch,
	CauseDigestMismatch, CauseOperationConflict, CauseUnknownRowCol, CauseGuardFailed, CauseOverLimit,
	CauseTransport, CauseStoreError,
}

var knownCause = func() map[Cause]bool {
	m := make(map[Cause]bool, len(allCauses))
	for _, c := range allCauses {
		m[c] = true
	}
	return m
}()

// Causes returns every cause of the closed vocabulary, as a copy.
func Causes() []Cause { return append([]Cause(nil), allCauses...) }

// Known reports whether c is a cause of the vocabulary.
func (c Cause) Known() bool { return knownCause[c] }

// Changed is the answer a refusal gives to "did anything change".
type Changed string

// The values of Changed. A refusal that names a check failed before or at a
// store guard changed nothing. A refusal whose outcome is not known did not
// report: it is unknown, never no.
const (
	ChangedNo      Changed = "no"
	ChangedUnknown Changed = "unknown"
)

// Changed is the answer for a refusal with this cause, derived from the cause
// and never stored beside it: unknown for a transport failure and for a store
// error whose outcome is not known, no for every other cause.
func (c Cause) Changed() Changed {
	if c == CauseTransport || c == CauseStoreError {
		return ChangedUnknown
	}
	return ChangedNo
}

// Operation names the operation a refusal was found in: "parse", "pin",
// "apply_events" and so on. Each package defines its own constants.
type Operation string

// Refusal is one thing wrong with an input: the operation; the index of the
// entry in its array (-1 when there is none); the file (a name or path) and line
// (0 when there is none); the card ID; the field, which is a key or a dotted
// path within the entry; the cause; what was found; the limit, for a bound; and
// the next action. Text that came from the caller is not trusted: rendering
// quotes and bounds it, and Value builds a Found that is already bounded.
type Refusal struct {
	Operation Operation
	Index     int
	File      string
	Line      int
	ID        string
	Field     string
	Cause     Cause
	Found     string
	Limit     string
	Next      string
}

// NewRefusal starts a refusal with no index.
func NewRefusal(op Operation, cause Cause) Refusal {
	return Refusal{Operation: op, Index: -1, Cause: cause}
}

// MaxFoundBytes bounds the text a refusal quotes from the caller's input: at
// most this many bytes of it, then its length.
const MaxFoundBytes = 48

// Value renders text from the caller's input for a refusal: quoted in Go syntax
// (a newline, NUL, control or bidi character is an escape, never itself), at
// most MaxFoundBytes bytes of it, and when it was longer, how long.
func Value(s string) string {
	if len(s) <= MaxFoundBytes {
		return strconv.Quote(s)
	}
	cut := MaxFoundBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strconv.Quote(s[:cut]) + "... (" + strconv.Itoa(len(s)) + " bytes)"
}

// token renders a name (file, card, field) that is a plain token as it is, and
// anything else as Value.
func token(s string) string {
	if s == "" || len(s) > 64 {
		return Value(s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '-' || c == '.' || c == '/' || c == '[' || c == ']' || c == '#' || c == ':' || c == '@' || c == '+'
		if !ok {
			return Value(s)
		}
	}
	return s
}

// MaxLineText bounds the fixed text of a rendered refusal (found, limit, next):
// a refusal line is bounded whatever a caller put in.
const MaxLineText = 240

// oneLine keeps text on one line and within MaxLineText bytes, whatever it holds.
func oneLine(s string) string {
	if strings.ContainsAny(s, "\r\n\x00") {
		s = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\x00", `\0`).Replace(s)
	}
	if len(s) > MaxLineText {
		cut := MaxLineText
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// String renders the refusal on one line, whatever the caller's input held:
//
//	refused <operation>[<index>] file=<file> line=<n> card=<id> field=<field>: <cause>; found <found>; limit <limit>; next: <next>
//
// Only the parts that are set appear.
func (r Refusal) String() string {
	var b strings.Builder
	b.WriteString("refused ")
	if r.Operation == "" {
		b.WriteString("request")
	} else {
		b.WriteString(token(string(r.Operation)))
	}
	if r.Index >= 0 {
		b.WriteString("[" + strconv.Itoa(r.Index) + "]")
	}
	if r.File != "" {
		b.WriteString(" file=" + token(r.File))
	}
	if r.Line > 0 {
		b.WriteString(" line=" + strconv.Itoa(r.Line))
	}
	if r.ID != "" {
		b.WriteString(" card=" + token(r.ID))
	}
	if r.Field != "" {
		b.WriteString(" field=" + token(r.Field))
	}
	b.WriteString(": " + oneLine(string(r.Cause)))
	if r.Found != "" {
		b.WriteString("; found " + oneLine(r.Found))
	}
	if r.Limit != "" {
		b.WriteString("; limit " + oneLine(r.Limit))
	}
	if r.Next != "" {
		b.WriteString("; next: " + oneLine(r.Next))
	}
	return b.String()
}

// MaxRefusals bounds the refusals one call reports; further ones are counted.
const MaxRefusals = 64

// maxStored bounds the caller-derived text a Refusal holds.
const maxStored = 1024

func bound(s string) string {
	if len(s) <= maxStored {
		return s
	}
	cut := maxStored
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Refusals is every refusal one call found, at most MaxRefusals of them, and
// the count of further ones left out. It is the error a call returns: an input
// with any refusal is refused whole, and nothing is partly accepted.
type Refusals struct {
	List    []Refusal
	Omitted int
}

// Count is every refusal found, those listed and those omitted.
func (r *Refusals) Count() int {
	if r == nil {
		return 0
	}
	return len(r.List) + r.Omitted
}

// Error is the first refusal and the count of the rest.
func (r *Refusals) Error() string {
	if r == nil || len(r.List) == 0 {
		return "refused"
	}
	if more := r.Count() - 1; more > 0 {
		return fmt.Sprintf("%s (and %d more)", r.List[0].String(), more)
	}
	return r.List[0].String()
}

// Lines renders every refusal, one line each, and a last line counting the ones
// left out when there are some.
func (r *Refusals) Lines() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.List)+1)
	for _, f := range r.List {
		out = append(out, f.String())
	}
	if r.Omitted > 0 {
		out = append(out, fmt.Sprintf("refused: %d further refusals omitted after the first %d", r.Omitted, MaxRefusals))
	}
	return out
}

// Has reports whether some refusal has the given index, field and cause; an
// index of -2 matches any index and an empty field matches any field.
func (r *Refusals) Has(index int, field string, cause Cause) bool {
	if r == nil {
		return false
	}
	for _, f := range r.List {
		if (index == -2 || f.Index == index) && (field == "" || f.Field == field) && f.Cause == cause {
			return true
		}
	}
	return false
}

// Collector gathers the refusals of one call: it keeps the first MaxRefusals and
// counts the rest, and it bounds the caller-derived text it stores.
type Collector struct {
	List    []Refusal
	Omitted int
}

// Full reports whether the collector holds MaxRefusals already: a hot loop
// checks it before building a refusal and calls Skip instead.
func (c *Collector) Full() bool { return len(c.List) >= MaxRefusals }

// Skip counts a refusal that was not built because the collector is full.
func (c *Collector) Skip() { c.Omitted++ }

// Add records one refusal, or counts it when the collector is full.
func (c *Collector) Add(r Refusal) {
	if c.Full() {
		c.Omitted++
		return
	}
	r.File, r.ID, r.Field = bound(r.File), bound(r.ID), bound(r.Field)
	r.Found, r.Limit, r.Next = bound(r.Found), bound(r.Limit), bound(r.Next)
	c.List = append(c.List, r)
}

// Len is every refusal added, kept and omitted.
func (c *Collector) Len() int { return len(c.List) + c.Omitted }

// Err returns the refusals as an error value, or nil when there are none.
func (c *Collector) Err() *Refusals {
	if c.Len() == 0 {
		return nil
	}
	return &Refusals{List: c.List, Omitted: c.Omitted}
}

package request

import (
	"fmt"
	"strconv"
	"strings"
)

// Cause is the named reason a request or a batch refuses.
type Cause string

// Causes of a request refusal, found by Parse and Validate before any store call.
const (
	CauseSyntax            Cause = "syntax"
	CauseTrailingData      Cause = "trailing-data"
	CauseDuplicateKey      Cause = "duplicate-key"
	CauseUnknownField      Cause = "unknown-field"
	CauseNotApplicable     Cause = "not-applicable"
	CauseWrongType         Cause = "wrong-type"
	CauseTooDeep           Cause = "too-deep"
	CauseRequired          Cause = "required"
	CauseBadValue          Cause = "bad-value"
	CauseInvalidUTF8       Cause = "invalid-utf8"
	CauseControlChar       Cause = "control-character"
	CauseTooLong           Cause = "too-long"
	CauseTooMany           Cause = "too-many"
	CauseTooLarge          Cause = "too-large"
	CauseEmptyArray        Cause = "empty-array"
	CauseRepeatedID        Cause = "repeated-id"
	CauseConflictingEvents Cause = "conflicting-events"
	CauseEventChain        Cause = "event-chain"
	CauseNoTransition      Cause = "no-transition"
	CauseNotEligible       Cause = "not-eligible"
)

// Causes of a store-side refusal, named here so the manager and its receipts
// share one closed set. Nothing in this package produces them.
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
	CauseTransport         Cause = "transport-failure"
)

// Refusal is one thing wrong with a request: the operation, the index of the
// entry in its array (-1 for the envelope or a scope), the card ID where known,
// the field (a dotted path within the entry), the cause, what was found and,
// for a bound, the limit and the remedy.
type Refusal struct {
	Operation Operation
	Index     int
	ID        string
	Field     string
	Cause     Cause
	Found     string
	Limit     string
	Remedy    string
}

// Line renders the refusal on one line.
func (r Refusal) Line() string {
	var b strings.Builder
	b.WriteString("refused ")
	if r.Operation == "" {
		b.WriteString("request")
	} else {
		b.WriteString(string(r.Operation))
	}
	if r.Index >= 0 {
		b.WriteString("[" + strconv.Itoa(r.Index) + "]")
	}
	if r.ID != "" {
		b.WriteString(" card=" + oneLine(r.ID))
	}
	if r.Field != "" {
		b.WriteString(" field=" + r.Field)
	}
	b.WriteString(": " + string(r.Cause))
	if r.Found != "" {
		b.WriteString("; found " + oneLine(r.Found))
	}
	if r.Limit != "" {
		b.WriteString("; limit " + r.Limit)
	}
	if r.Remedy != "" {
		b.WriteString("; remedy: " + r.Remedy)
	}
	return b.String()
}

// oneLine keeps a rendered value on one line, whatever it holds.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(s)
}

// Refusals is every refusal found in one request, at most MaxRefusals, and the
// count of further ones left out. It is the error Parse and Validate return: a
// request that has any refusal is refused whole.
type Refusals struct {
	List    []Refusal
	Omitted int
}

// Error is the first refusal and the count of the rest.
func (r *Refusals) Error() string {
	if r == nil || len(r.List) == 0 {
		return "request refused"
	}
	more := len(r.List) - 1 + r.Omitted
	if more == 0 {
		return r.List[0].Line()
	}
	return fmt.Sprintf("%s (and %d more)", r.List[0].Line(), more)
}

// Lines renders every refusal, one line each, and a last line counting the
// ones left out when there are some.
func (r *Refusals) Lines() []string {
	out := make([]string, 0, len(r.List)+1)
	for _, f := range r.List {
		out = append(out, f.Line())
	}
	if r.Omitted > 0 {
		out = append(out, fmt.Sprintf("refused: %d further refusals omitted after the first %d", r.Omitted, MaxRefusals))
	}
	return out
}

// Has reports whether some refusal has the given index, field and cause;
// an index of -2 matches any index and an empty field matches any field.
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

type refKey struct {
	index int
	field string
}

// collector gathers refusals for one request.
type collector struct {
	op      Operation
	list    []Refusal
	omitted int
	seen    map[refKey]bool
	// typed holds the fields refused as the wrong JSON type: whatever they were
	// meant to hold is absent, and refusing that absence again is noise.
	typed []refKey
}

func newCollector() *collector { return &collector{seen: map[refKey]bool{}} }

// underTyped says the field is, or lies within, a field already refused as the
// wrong type.
func (c *collector) underTyped(index int, field string) bool {
	for _, k := range c.typed {
		if k.index != index {
			continue
		}
		if k.field == "" || field == k.field || strings.HasPrefix(field, k.field+".") || strings.HasPrefix(field, k.field+"[") {
			return true
		}
	}
	return false
}

// add records one refusal. A required refusal for a field that already has one
// is dropped: the field is already refused for what it is.
func (c *collector) add(index int, id, field string, cause Cause, found, limit, remedy string) {
	k := refKey{index, field}
	if cause != CauseWrongType && c.underTyped(index, field) {
		return
	}
	if cause == CauseRequired && c.seen[k] {
		return
	}
	c.seen[k] = true
	if cause == CauseWrongType {
		c.typed = append(c.typed, k)
	}
	if len(c.list) >= MaxRefusals {
		c.omitted++
		return
	}
	c.list = append(c.list, Refusal{Operation: c.op, Index: index, ID: id, Field: field, Cause: cause, Found: found, Limit: limit, Remedy: remedy})
}

func (c *collector) err() error {
	if len(c.list) == 0 {
		return nil
	}
	return &Refusals{List: c.list, Omitted: c.omitted}
}

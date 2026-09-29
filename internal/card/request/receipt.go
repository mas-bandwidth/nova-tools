package request

import (
	"fmt"
	"strconv"
	"strings"
)

// Result is what an accepted batch did.
type Result string

// The results of an accepted batch. An accepted no-op is recorded and has a
// receipt of its own.
const (
	ResultChanged Result = "changed"
	ResultNoop    Result = "noop"
)

// CardState is a card's recorded state: its row, its state (the column), its
// revision and, when it is done, its outcome.
type CardState struct {
	Row      string
	State    State
	Revision string
	Outcome  Outcome
}

// CardChange is one changed card, before and after. Before is nil for a card
// the batch created.
type CardChange struct {
	ID     ID
	Before *CardState
	After  CardState
}

// Named is a card a selection reported without changing it, with the named
// reason.
type Named struct {
	ID     ID
	Reason string
}

// Counts are the guard and selection counts of a batch.
type Counts struct {
	Selected   int // cards the request named or the scope selected
	Eligible   int // selected cards the operation could act on
	Changed    int
	Blocked    int
	Ineligible int
	Missing    int
	Guards     int // guard-only entries: read and checked, not changed
}

// Receipt is the one authoritative record of an accepted batch: the operation,
// the request hash, the table and epoch, the table revision before and after,
// the actor, changed or noop, every changed card's before and after, the
// declared non-changing selection outcomes and the counts. Nothing in this
// package produces one; the manager fills it.
type Receipt struct {
	Schema         int
	Operation      Operation
	OperationID    string
	RequestHash    Digest
	Table          string
	Epoch          string
	RevisionBefore string
	RevisionAfter  string
	Actor          string
	Result         Result
	Changed        []CardChange
	Blocked        []Named // waiting on a prerequisite; the reason names it
	Ineligible     []Named // not in a state the operation acts on
	Missing        []ID    // named or scoped, not found
	Counts         Counts
}

// Changed is the answer a refusal gives to "did anything change".
type Changed string

// The values of Changed. A refusal that names a store guard changed nothing. A
// transport failure did not report: it is unknown, never no.
const (
	ChangedNo      Changed = "no"
	ChangedUnknown Changed = "unknown"
)

// Rejection is a refusal of a batch or a card by the manager or the table: the
// operation, whether it concerns the batch or one card, the cause, what was
// expected against what was observed, whether anything changed, and the next
// usable command, filled by the caller.
type Rejection struct {
	Operation   Operation
	OperationID string
	Scope       string // "batch" or "card"
	Card        ID     // set when Scope is "card"
	Cause       Cause
	Expected    string
	Observed    string
	Changed     Changed
	Next        string
}

// The scopes of a Rejection.
const (
	ScopeBatch = "batch"
	ScopeCard  = "card"
)

var knownCauses = func() map[Cause]bool {
	m := map[Cause]bool{}
	for _, c := range []Cause{
		CauseSyntax, CauseTrailingData, CauseDuplicateKey, CauseUnknownField, CauseNotApplicable,
		CauseWrongType, CauseTooDeep, CauseRequired, CauseBadValue, CauseInvalidUTF8, CauseControlChar,
		CauseTooLong, CauseTooMany, CauseTooLarge, CauseEmptyArray, CauseRepeatedID,
		CauseConflictingEvents, CauseEventChain, CauseNoTransition, CauseNotEligible,
		CauseInvalidRequest, CauseStaleEpoch, CauseStaleTableRev, CauseStaleCardRev, CausePlaceMismatch,
		CauseDigestMismatch, CauseOperationConflict, CauseUnknownRowCol, CauseGuardFailed, CauseOverLimit,
		CauseTransport,
	} {
		m[c] = true
	}
	return m
}()

// MaxDetailBytes bounds the free text of a rejection or a named reason.
const MaxDetailBytes = 256

func (s CardState) tree() obj {
	m := obj{}
	setStr(m, "row", s.Row)
	setStr(m, "state", string(s.State))
	setStr(m, "revision", s.Revision)
	setStr(m, "outcome", string(s.Outcome))
	return m
}

func (r *Receipt) tree() obj {
	m := obj{"schema": r.Schema}
	setStr(m, "operation", string(r.Operation))
	setStr(m, "operation_id", r.OperationID)
	setStr(m, "request_hash", string(r.RequestHash))
	setStr(m, "table", r.Table)
	setStr(m, "epoch", r.Epoch)
	setStr(m, "revision_before", r.RevisionBefore)
	setStr(m, "revision_after", r.RevisionAfter)
	setStr(m, "actor", r.Actor)
	setStr(m, "result", string(r.Result))
	list := func(n int, f func(i int) any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	m["changed"] = list(len(r.Changed), func(i int) any {
		c := r.Changed[i]
		cm := obj{"after": c.After.tree()}
		setStr(cm, "id", string(c.ID))
		if c.Before != nil {
			cm["before"] = c.Before.tree()
		}
		return cm
	})
	named := func(ns []Named) []any {
		return list(len(ns), func(i int) any {
			nm := obj{}
			setStr(nm, "id", string(ns[i].ID))
			setStr(nm, "reason", ns[i].Reason)
			return nm
		})
	}
	m["blocked"] = named(r.Blocked)
	m["ineligible"] = named(r.Ineligible)
	m["missing"] = list(len(r.Missing), func(i int) any { return string(r.Missing[i]) })
	m["counts"] = obj{
		"selected": r.Counts.Selected, "eligible": r.Counts.Eligible, "changed": r.Counts.Changed,
		"blocked": r.Counts.Blocked, "ineligible": r.Counts.Ineligible, "missing": r.Counts.Missing,
		"guards": r.Counts.Guards,
	}
	return m
}

// CanonicalReceipt is the receipt's deterministic encoding, with the same rules
// as Canonical. Every list is present, empty or not.
func CanonicalReceipt(r *Receipt) []byte {
	if r == nil {
		return []byte("null")
	}
	return canonTree(r.tree())
}

// Digest is the SHA-256 of the receipt's canonical bytes: its identity.
func (r *Receipt) Digest() Digest { return sum(CanonicalReceipt(r)) }

func (r *Rejection) tree() obj {
	m := obj{}
	setStr(m, "operation", string(r.Operation))
	setStr(m, "operation_id", r.OperationID)
	setStr(m, "scope", r.Scope)
	setStr(m, "card", string(r.Card))
	setStr(m, "cause", string(r.Cause))
	setStr(m, "expected", r.Expected)
	setStr(m, "observed", r.Observed)
	setStr(m, "changed", string(r.Changed))
	setStr(m, "next", r.Next)
	return m
}

// CanonicalRejection is the rejection's deterministic encoding.
func CanonicalRejection(r *Rejection) []byte {
	if r == nil {
		return []byte("null")
	}
	return canonTree(r.tree())
}

// Line renders the receipt on one line: the batch, the counts and each changed
// card's before and after, at most eight of them.
func (r *Receipt) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "batch %s op=%s table=%s epoch=%s rev %s->%s actor=%s result=%s",
		r.Operation, oneLine(r.OperationID), oneLine(r.Table), oneLine(r.Epoch),
		oneLine(r.RevisionBefore), oneLine(r.RevisionAfter), oneLine(r.Actor), r.Result)
	fmt.Fprintf(&b, ": selected=%d eligible=%d changed=%d blocked=%d ineligible=%d missing=%d guards=%d",
		r.Counts.Selected, r.Counts.Eligible, r.Counts.Changed, r.Counts.Blocked, r.Counts.Ineligible, r.Counts.Missing, r.Counts.Guards)
	for i, c := range r.Changed {
		if i == 8 {
			fmt.Fprintf(&b, "; +%d more", len(r.Changed)-8)
			break
		}
		before := "new"
		if c.Before != nil {
			before = c.Before.short()
		}
		fmt.Fprintf(&b, "; %s %s -> %s", oneLine(string(c.ID)), before, c.After.short())
	}
	if len(r.Blocked) > 0 {
		fmt.Fprintf(&b, "; blocked %s: %s", oneLine(string(r.Blocked[0].ID)), oneLine(r.Blocked[0].Reason))
		if len(r.Blocked) > 1 {
			fmt.Fprintf(&b, " (+%d more blocked)", len(r.Blocked)-1)
		}
	}
	return b.String()
}

func (s CardState) short() string {
	out := string(s.State)
	if s.Outcome != "" {
		out += "/" + string(s.Outcome)
	}
	return out + " r" + s.Revision
}

// Line renders the rejection on one line.
func (r *Rejection) Line() string {
	who := string(r.Scope)
	if r.Card != "" {
		who += " " + oneLine(string(r.Card))
	}
	return fmt.Sprintf("refused %s op=%s %s: %s; expected %s; observed %s; changed=%s; next: %s",
		r.Operation, oneLine(r.OperationID), who, r.Cause, oneLine(r.Expected), oneLine(r.Observed), r.Changed, oneLine(r.Next))
}

// ValidateReceipt checks a receipt is well formed and consistent: known
// operation, a request hash, counters, changed exactly when cards changed, the
// table revision one above the before, each changed card's revision one above
// its before (a created card starts at 1), an outcome exactly on done cards,
// and counts that agree with the lists.
func ValidateReceipt(r *Receipt) error {
	c := newCollector()
	if r == nil {
		c.add(-1, "", "", CauseRequired, "no receipt", "", "fill the receipt")
		return c.err()
	}
	v := validator{c}
	if r.Operation.Valid() {
		c.op = r.Operation
	}
	if r.Schema != SchemaVersion {
		c.add(-1, "", "schema", CauseBadValue, strconv.Itoa(r.Schema), strconv.Itoa(SchemaVersion), "send schema "+strconv.Itoa(SchemaVersion))
	}
	if !r.Operation.Mutating() {
		c.add(-1, "", "operation", CauseBadValue, quote(string(r.Operation)), "a mutating operation", "a receipt records a mutating operation")
	}
	v.identity(-1, "", "operation_id", r.OperationID)
	v.digest(-1, "", "request_hash", r.RequestHash)
	v.name(-1, "", "table", r.Table)
	v.counter(-1, "", "epoch", r.Epoch, false)
	v.counter(-1, "", "revision_before", r.RevisionBefore, false)
	v.counter(-1, "", "revision_after", r.RevisionAfter, false)
	v.identity(-1, "", "actor", r.Actor)
	if validCounter(r.RevisionBefore) && validCounter(r.RevisionAfter) {
		b, _ := strconv.ParseUint(r.RevisionBefore, 10, 64)
		a, _ := strconv.ParseUint(r.RevisionAfter, 10, 64)
		if b == ^uint64(0) || a != b+1 {
			c.add(-1, "", "revision_after", CauseBadValue, quote(r.RevisionAfter), "revision_before plus one", "an accepted batch, changed or noop, advances the table revision once")
		}
	}
	switch r.Result {
	case ResultChanged:
		if len(r.Changed) == 0 {
			c.add(-1, "", "result", CauseBadValue, "changed with no changed card", "noop", "a batch that changed no card is a noop")
		}
	case ResultNoop:
		if len(r.Changed) != 0 {
			c.add(-1, "", "result", CauseBadValue, "noop with changed cards", "changed", "a batch that changed cards is changed")
		}
	default:
		c.add(-1, "", "result", CauseBadValue, quote(string(r.Result)), "changed or noop", "set the result")
	}
	if len(r.Changed) > MaxReceiptCards {
		c.add(-1, "", "changed", CauseTooMany, strconv.Itoa(len(r.Changed)), strconv.Itoa(MaxReceiptCards), "a receipt holds at most that many cards")
	}
	seen := map[ID]bool{}
	for i := 0; i < len(r.Changed) && i < MaxReceiptCards; i++ {
		ch := &r.Changed[i]
		id := knownID(string(ch.ID))
		v.cardID(i, id, "id", ch.ID)
		if ch.ID != "" && seen[ch.ID] {
			c.add(i, id, "id", CauseRepeatedID, quote(string(ch.ID)), "one entry per card", "list each card once")
		}
		seen[ch.ID] = true
		v.cardState(i, id, "after", &ch.After)
		want := uint64(1)
		if ch.Before != nil {
			v.cardState(i, id, "before", ch.Before)
			if validCounter(ch.Before.Revision) {
				b, _ := strconv.ParseUint(ch.Before.Revision, 10, 64)
				want = b + 1
			}
		}
		if counterAtLeastOne(ch.After.Revision) {
			a, _ := strconv.ParseUint(ch.After.Revision, 10, 64)
			if a != want || want == 0 {
				c.add(i, id, "after.revision", CauseBadValue, quote(ch.After.Revision), strconv.FormatUint(want, 10),
					"a changed card's revision advances once; a created card starts at 1")
			}
		}
		if ch.Before == nil && ch.After.State != Waiting && ch.After.State.Valid() {
			c.add(i, id, "after.state", CauseBadValue, quote(string(ch.After.State)), "waiting", "an admitted card starts in waiting")
		}
	}
	v.named("blocked", r.Blocked)
	v.named("ineligible", r.Ineligible)
	if len(r.Missing) > MaxReceiptCards {
		c.add(-1, "", "missing", CauseTooMany, strconv.Itoa(len(r.Missing)), strconv.Itoa(MaxReceiptCards), "a receipt holds at most that many cards")
	}
	for i := 0; i < len(r.Missing) && i < MaxReceiptCards; i++ {
		v.cardID(-1, "", "missing["+strconv.Itoa(i)+"]", r.Missing[i])
	}
	n := r.Counts
	for _, f := range []struct {
		name      string
		got, want int
	}{
		{"counts.changed", n.Changed, len(r.Changed)},
		{"counts.blocked", n.Blocked, len(r.Blocked)},
		{"counts.ineligible", n.Ineligible, len(r.Ineligible)},
		{"counts.missing", n.Missing, len(r.Missing)},
	} {
		if f.got != f.want {
			c.add(-1, "", f.name, CauseBadValue, strconv.Itoa(f.got), strconv.Itoa(f.want), "the count is the length of its list")
		}
	}
	for _, f := range []struct {
		name string
		val  int
	}{{"counts.selected", n.Selected}, {"counts.eligible", n.Eligible}, {"counts.guards", n.Guards}} {
		if f.val < 0 {
			c.add(-1, "", f.name, CauseBadValue, strconv.Itoa(f.val), "0 or more", "counts are not negative")
		}
	}
	if n.Selected < len(r.Changed)+len(r.Blocked)+len(r.Ineligible)+len(r.Missing) {
		c.add(-1, "", "counts.selected", CauseBadValue, strconv.Itoa(n.Selected), "at least the changed, blocked, ineligible and missing cards",
			"selected counts every card the request named or the scope chose")
	}
	if n.Eligible > n.Selected {
		c.add(-1, "", "counts.eligible", CauseBadValue, strconv.Itoa(n.Eligible), "at most the selected cards", "eligible cards are selected cards")
	}
	return c.err()
}

func (v validator) cardState(i int, id, prefix string, s *CardState) {
	v.name(i, id, prefix+".row", s.Row)
	v.enum(i, id, prefix+".state", string(s.State), stateNames())
	v.counter(i, id, prefix+".revision", s.Revision, true)
	switch {
	case s.State == Done && !s.Outcome.Valid():
		v.c.add(i, id, prefix+".outcome", CauseRequired, quote(string(s.Outcome)), "completed, cancelled, dependency-failed or replaced", "a done card carries an outcome")
	case s.State != Done && s.Outcome != "":
		v.c.add(i, id, prefix+".outcome", CauseNotApplicable, quote(string(s.Outcome)), "", "only a done card carries an outcome")
	}
}

func (v validator) named(field string, list []Named) {
	if len(list) > MaxReceiptCards {
		v.c.add(-1, "", field, CauseTooMany, strconv.Itoa(len(list)), strconv.Itoa(MaxReceiptCards), "a receipt holds at most that many cards")
	}
	for i := 0; i < len(list) && i < MaxReceiptCards; i++ {
		f := field + "[" + strconv.Itoa(i) + "]"
		v.cardID(-1, "", f+".id", list[i].ID)
		v.text(-1, knownID(string(list[i].ID)), f+".reason", list[i].Reason, MaxDetailBytes, func(string) bool { return true }, "a named reason")
	}
}

// ValidateRejection checks a rejection is well formed: a known operation and
// cause, batch or card scope with a card exactly for the card scope, bounded
// clean text, a next command, and Changed consistent with the cause: a transport
// failure is unknown, every other cause is no.
func ValidateRejection(r *Rejection) error {
	c := newCollector()
	if r == nil {
		c.add(-1, "", "", CauseRequired, "no rejection", "", "fill the rejection")
		return c.err()
	}
	v := validator{c}
	if r.Operation.Valid() {
		c.op = r.Operation
	}
	ops := make([]string, len(Operations))
	for i, o := range Operations {
		ops[i] = string(o)
	}
	v.enum(-1, "", "operation", string(r.Operation), ops)
	v.identity(-1, "", "operation_id", r.OperationID)
	v.enum(-1, "", "scope", r.Scope, []string{ScopeBatch, ScopeCard})
	switch r.Scope {
	case ScopeCard:
		v.cardID(-1, "", "card", r.Card)
	case ScopeBatch:
		if r.Card != "" {
			c.add(-1, "", "card", CauseNotApplicable, quote(string(r.Card)), "", "a batch refusal names no card")
		}
	}
	if !knownCauses[r.Cause] {
		c.add(-1, "", "cause", CauseBadValue, quote(string(r.Cause)), "a named cause", "use one of the named causes")
	}
	for _, f := range [][2]string{{"expected", r.Expected}, {"observed", r.Observed}, {"next", r.Next}} {
		v.text(-1, "", f[0], f[1], MaxDetailBytes, func(string) bool { return true }, "a bounded one-line text")
	}
	v.enum(-1, "", "changed", string(r.Changed), []string{string(ChangedNo), string(ChangedUnknown)})
	switch {
	case r.Cause == CauseTransport && r.Changed != ChangedUnknown:
		c.add(-1, "", "changed", CauseBadValue, quote(string(r.Changed)), "unknown", "a transport failure did not report, so whether anything changed is unknown, never no")
	case r.Cause != CauseTransport && r.Changed == ChangedUnknown && knownCauses[r.Cause]:
		c.add(-1, "", "changed", CauseBadValue, quote(string(r.Changed)), "no", "a refusal at a guard changed nothing; only a transport failure is unknown")
	}
	return c.err()
}

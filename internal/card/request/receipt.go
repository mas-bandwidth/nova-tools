package request

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// BatchResult is what an accepted batch did.
type BatchResult string

// The results of an accepted batch. An accepted no-op is recorded and has a
// receipt of its own.
const (
	ResultChanged BatchResult = "changed"
	ResultNoop    BatchResult = "noop"
)

// Counters are a card's cycle counters: how often it went round a loop, kept in
// the card so that a receipt shows what a batch did to them. Each is a decimal
// string bounded as uint64, like every counter of this layer; empty reads as zero.
// Every counter but RedSameHead only grows: a receipt never decreases one.
// RedSameHead counts the red CI results at the card's current head and starts
// again at zero when the head changes.
type Counters struct {
	Rework        string // returns to ready from review
	MergeReturns  string // returns to review from merging, for any cause
	HeadChanges   string // new heads while in review
	Red           string // red CI results, at any head
	RedSameHead   string // red CI results at the current head (resets)
	RedHeads      string // heads that have gone red while current
	Flaky         string // a check both green and red at one head
	Reject        string // reader rejections, at any head
	Starts        string // starts (ready to working)
	Results       string // results (working to review)
	FailedResults string // results that report failure or return
	Lineage       string // replacements in the card's lineage (its generation)
}

// counterNames pairs each counter's wire name with its accessor. RedSameHead is
// the one that may decrease.
var counterNames = []struct {
	name  string
	get   func(*Counters) *string
	reset bool
}{
	{"rework", func(c *Counters) *string { return &c.Rework }, false},
	{"merge_returns", func(c *Counters) *string { return &c.MergeReturns }, false},
	{"head_changes", func(c *Counters) *string { return &c.HeadChanges }, false},
	{"red", func(c *Counters) *string { return &c.Red }, false},
	{"red_same_head", func(c *Counters) *string { return &c.RedSameHead }, true},
	{"red_heads", func(c *Counters) *string { return &c.RedHeads }, false},
	{"flaky", func(c *Counters) *string { return &c.Flaky }, false},
	{"reject", func(c *Counters) *string { return &c.Reject }, false},
	{"starts", func(c *Counters) *string { return &c.Starts }, false},
	{"results", func(c *Counters) *string { return &c.Results }, false},
	{"failed_results", func(c *Counters) *string { return &c.FailedResults }, false},
	{"lineage", func(c *Counters) *string { return &c.Lineage }, false},
}

func (c Counters) tree() card.Obj {
	m := card.Obj{}
	for _, n := range counterNames {
		m.Str(n.name, *n.get(&c))
	}
	return m
}

// CardState is a card's recorded state: its row, its state (the column), its
// revision, and its cycle counters. A card that left the table has no state
// (Unplaced): it keeps its row and carries an Outcome instead.
type CardState struct {
	Row      string
	State    State
	Revision string
	Outcome  Outcome
	Counters Counters
}

// Answer is yes or no.
type Answer string

// The two answers.
const (
	Yes Answer = "yes"
	No  Answer = "no"
)

// Escalation is the mark a notification carries: none, or escalated when a
// counter or an age of the card is past a threshold of its policy.
type Escalation string

// The two escalation values.
const (
	EscalationNone      Escalation = "none"
	EscalationEscalated Escalation = "escalated"
)

// Notification is what the coordinator is told about one changed card, derived
// from the batch's receipt by the manager: its kind, whether judgment may be
// required, what the batch changed about the card's readiness (one bounded line
// the manager fills), whether the card is escalated and the cycle counters after
// the change. Nothing in this package produces one.
type Notification struct {
	Kind       NotificationKind
	Judgment   Answer
	Readiness  string
	Escalation Escalation
	Counters   Counters
}

// CardChange is one changed card, before and after. Before is nil for a card the
// batch created. Successor names the card that replaced this one (on a card that
// left the table as replaced), Landing the landing identity (on a card that
// landed), Reason why a card left the table (required then), and Notifications
// what the coordinator is told about the card.
type CardChange struct {
	ID            ID
	Before        *CardState
	After         CardState
	Successor     ID
	Landing       string
	Reason        string
	Notifications []Notification
}

// Named is a card a selection reported without changing it, with the named
// reason.
type Named struct {
	ID     ID
	Reason string
}

// Counts are the guard and selection counts of a batch.
type Counts struct {
	Selected     int // cards the request named or the scope selected
	Eligible     int // selected cards the operation could act on
	Changed      int
	Blocked      int
	Ineligible   int
	Missing      int
	Already      int
	Inapplicable int
	Guards       int // guard-only entries: read and checked, not changed
}

// Receipt is the one authoritative record of an accepted batch: the operation,
// the request hash, the table and epoch, the table revision before and after, the
// actor, changed or noop, every changed card's before and after, the declared
// non-changing selection outcomes and the counts. Nothing in this package
// produces one; the manager fills it. The receipt's identity is its Digest.
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
	Result         BatchResult
	Changed        []CardChange
	Blocked        []Named // waiting on a prerequisite; the reason names it
	Ineligible     []Named // not in a state the operation acts on
	Missing        []ID    // named or scoped, not found
	Already        []Named // an identical observation is already recorded
	Inapplicable   []Named // an observation for a state that takes none
	Counts         Counts
}

// Rejection is a refusal of a batch or a card by the manager or the table: the
// operation, whether it concerns the batch or one card, the cause, what was
// expected against what was observed, and the next usable command, filled by the
// caller. Whether anything changed is derived from the cause (Changed) and never
// stored beside it. A rejection of an inspect or a check has no operation ID.
type Rejection struct {
	Operation   Operation
	OperationID string
	Scope       string // "batch" or "card"
	Card        ID     // set when Scope is "card"
	Cause       Cause
	Expected    string
	Observed    string
	Next        string
}

// Changed answers "did anything change" from the cause: unknown for a transport
// failure and for a store error whose outcome is unknown, no for every other.
func (r *Rejection) Changed() Changed { return r.Cause.Changed() }

// The scopes of a Rejection.
const (
	ScopeBatch = "batch"
	ScopeCard  = "card"
)

func (s CardState) tree() card.Obj {
	m := card.Obj{}
	m.Str("row", s.Row)
	m.Str("state", string(s.State))
	m.Str("revision", s.Revision)
	m.Str("outcome", string(s.Outcome))
	if c := s.Counters.tree(); len(c) > 0 {
		m["counters"] = c
	}
	return m
}

func (n Notification) tree() card.Obj {
	m := card.Obj{}
	m.Str("kind", string(n.Kind))
	m.Str("judgment", string(n.Judgment))
	m.Str("readiness", n.Readiness)
	m.Str("escalation", string(n.Escalation))
	if c := n.Counters.tree(); len(c) > 0 {
		m["counters"] = c
	}
	return m
}

func (c CardChange) tree() card.Obj {
	m := card.Obj{"after": c.After.tree()}
	m.Str("id", string(c.ID))
	if c.Before != nil {
		m["before"] = c.Before.tree()
	}
	m.Str("successor", string(c.Successor))
	m.Str("landing", c.Landing)
	m.Str("reason", c.Reason)
	if len(c.Notifications) > 0 {
		// The order of notifications is the manager's derivation order.
		l := make([]any, len(c.Notifications))
		for i, n := range c.Notifications {
			l[i] = n.tree()
		}
		m["notifications"] = l
	}
	return m
}

func namedSet(ns []Named) card.Set {
	items := make([]any, len(ns))
	for i, n := range ns {
		o := card.Obj{}
		o.Str("id", string(n.ID))
		o.Str("reason", n.Reason)
		items[i] = o
	}
	return card.Set{Key: "id", Items: items}
}

func (r *Receipt) tree() card.Obj {
	m := card.Obj{"schema": r.Schema}
	m.Str("operation", string(r.Operation))
	m.Str("operation_id", r.OperationID)
	m.Str("request_hash", string(r.RequestHash))
	m.Str("table", r.Table)
	m.Str("epoch", r.Epoch)
	m.Str("revision_before", r.RevisionBefore)
	m.Str("revision_after", r.RevisionAfter)
	m.Str("actor", r.Actor)
	m.Str("result", string(r.Result))
	changed := make([]any, len(r.Changed))
	for i, c := range r.Changed {
		changed[i] = c.tree()
	}
	m["changed"] = card.Set{Key: "id", Items: changed}
	m["blocked"] = namedSet(r.Blocked)
	m["ineligible"] = namedSet(r.Ineligible)
	m["already"] = namedSet(r.Already)
	m["inapplicable"] = namedSet(r.Inapplicable)
	m["missing"] = card.Strings(r.Missing)
	m["counts"] = card.Obj{
		"selected": r.Counts.Selected, "eligible": r.Counts.Eligible, "changed": r.Counts.Changed,
		"blocked": r.Counts.Blocked, "ineligible": r.Counts.Ineligible, "missing": r.Counts.Missing,
		"already": r.Counts.Already, "inapplicable": r.Counts.Inapplicable, "guards": r.Counts.Guards,
	}
	return m
}

// CanonicalReceipt is the receipt's deterministic encoding, with the same rules as
// Canonical: cards and selection outcomes sorted by card ID. Every list is
// present, empty or not.
func CanonicalReceipt(r *Receipt) []byte {
	if r == nil {
		return []byte("null")
	}
	return card.Encode(r.tree())
}

// Digest is the SHA-256 of the receipt's canonical bytes: its identity.
func (r *Receipt) Digest() Digest { return sum(CanonicalReceipt(r)) }

func (r *Rejection) tree() card.Obj {
	m := card.Obj{}
	m.Str("operation", string(r.Operation))
	m.Str("operation_id", r.OperationID)
	m.Str("scope", r.Scope)
	m.Str("card", string(r.Card))
	m.Str("cause", string(r.Cause))
	m.Str("expected", r.Expected)
	m.Str("observed", r.Observed)
	m.Str("changed", string(r.Changed()))
	m.Str("next", r.Next)
	return m
}

// CanonicalRejection is the rejection's deterministic encoding; its changed field
// is the derived answer.
func CanonicalRejection(r *Rejection) []byte {
	if r == nil {
		return []byte("null")
	}
	return card.Encode(r.tree())
}

// Line renders the receipt on one line: the batch with its request hash and its
// own digest, the counts and each changed card's before and after, at most eight
// of them. A value that came from a caller is quoted.
func (r *Receipt) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "batch %s op=%s hash=%s receipt=%s table=%s epoch=%s rev %s->%s actor=%s result=%s",
		r.Operation, token1(r.OperationID), token1(string(r.RequestHash)), r.Digest(), token1(r.Table), token1(r.Epoch),
		token1(r.RevisionBefore), token1(r.RevisionAfter), token1(r.Actor), token1(string(r.Result)))
	fmt.Fprintf(&b, ": selected=%d eligible=%d changed=%d blocked=%d ineligible=%d missing=%d already=%d inapplicable=%d guards=%d",
		r.Counts.Selected, r.Counts.Eligible, r.Counts.Changed, r.Counts.Blocked, r.Counts.Ineligible, r.Counts.Missing,
		r.Counts.Already, r.Counts.Inapplicable, r.Counts.Guards)
	for i, c := range r.Changed {
		if i == 8 {
			fmt.Fprintf(&b, "; +%d more", len(r.Changed)-8)
			break
		}
		before := "new"
		if c.Before != nil {
			before = c.Before.short()
		}
		fmt.Fprintf(&b, "; %s %s -> %s", token1(string(c.ID)), before, c.After.short())
		if c.Successor != "" {
			fmt.Fprintf(&b, " succ=%s", token1(string(c.Successor)))
		}
		if c.Landing != "" {
			fmt.Fprintf(&b, " landing=%s", token1(c.Landing))
		}
	}
	if len(r.Blocked) > 0 {
		fmt.Fprintf(&b, "; blocked %s: %s", token1(string(r.Blocked[0].ID)), quote(r.Blocked[0].Reason))
		if len(r.Blocked) > 1 {
			fmt.Fprintf(&b, " (+%d more blocked)", len(r.Blocked)-1)
		}
	}
	return b.String()
}

// token1 renders a value that should be a plain token: itself when it is one,
// quoted and bounded when it is anything else.
func token1(s string) string {
	if s != "" && len(s) <= 128 && !strings.ContainsAny(s, " \t\r\n\x00\"") && card.TextFault(s, 128) == "" {
		return s
	}
	return quote(s)
}

func (s CardState) short() string {
	out := string(s.State)
	if s.Outcome != "" {
		out += "/" + string(s.Outcome)
	}
	return out + " r" + token1(s.Revision)
}

// Line renders the rejection on one line: values are quoted and bounded, so the
// line is one line whatever the caller's text held.
func (r *Rejection) Line() string {
	who := r.Scope
	if r.Card != "" {
		who += " " + token1(string(r.Card))
	}
	op := ""
	if r.OperationID != "" {
		op = " op=" + token1(r.OperationID)
	}
	return fmt.Sprintf("refused %s%s %s: %s; expected %s; observed %s; changed=%s; next: %s",
		r.Operation, op, token1(who), r.Cause, quote(r.Expected), quote(r.Observed), r.Changed(), quote(r.Next))
}

// ValidateReceipt checks a receipt is well formed, consistent and one the
// operation could produce: a known mutating operation, a request hash, counters,
// changed exactly when cards changed, the table revision one above the before,
// each changed card's revision one above its before (a created card starts at 1),
// an outcome exactly on done cards, a successor exactly on a replaced card, a
// landing identity exactly on a landed card, cycle counters that never decrease,
// notifications of known kinds, and counts that agree with the lists. Beyond the
// shape, each card's move is one the operation makes: an admit only creates, a
// resolve only moves waiting to ready, an apply only makes a move a lifecycle
// input names, an evidence record only leaves the state or forces the one forced
// move, a replace only ends a waiting or ready card and creates its successor.
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
		c.add(-1, "", "schema", CauseInvalidValue, strconv.Itoa(r.Schema), strconv.Itoa(SchemaVersion), "send schema "+strconv.Itoa(SchemaVersion))
	}
	if !r.Operation.Mutating() {
		c.add(-1, "", "operation", CauseInvalidValue, quote(string(r.Operation)), "a mutating operation", "a receipt records a mutating operation")
	}
	v.token(-1, "", "operation_id", r.OperationID, card.MaxOperationIDBytes)
	v.digest(-1, "", "request_hash", r.RequestHash)
	v.name(-1, "", "table", r.Table)
	v.counter(-1, "", "epoch", r.Epoch, false)
	v.counter(-1, "", "revision_before", r.RevisionBefore, false)
	v.counter(-1, "", "revision_after", r.RevisionAfter, false)
	v.token(-1, "", "actor", r.Actor, MaxIdentityBytes)
	if card.ValidCounter(r.RevisionBefore) && card.ValidCounter(r.RevisionAfter) {
		b, _ := strconv.ParseUint(r.RevisionBefore, 10, 64)
		a, _ := strconv.ParseUint(r.RevisionAfter, 10, 64)
		if b == ^uint64(0) || a != b+1 {
			c.add(-1, "", "revision_after", CauseInvalidValue, quote(r.RevisionAfter), "revision_before plus one", "an accepted batch, changed or noop, advances the table revision once")
		}
	}
	switch r.Result {
	case ResultChanged:
		if len(r.Changed) == 0 {
			c.add(-1, "", "result", CauseInvalidValue, "changed with no changed card", "noop", "a batch that changed no card is a noop")
		}
	case ResultNoop:
		if len(r.Changed) != 0 {
			c.add(-1, "", "result", CauseInvalidValue, "noop with changed cards", "changed", "a batch that changed cards is changed")
		}
	default:
		c.add(-1, "", "result", CauseInvalidValue, quote(string(r.Result)), "changed or noop", "set the result")
	}
	if len(r.Changed) > MaxReceiptCards {
		c.add(-1, "", "changed", CauseTooMany, strconv.Itoa(len(r.Changed)), strconv.Itoa(MaxReceiptCards), "a receipt holds at most that many cards")
	}
	seen := map[ID]int{}
	for i := 0; i < len(r.Changed) && i < MaxReceiptCards; i++ {
		ch := &r.Changed[i]
		id := knownID(string(ch.ID))
		v.cardID(i, id, "id", ch.ID)
		if ch.ID != "" {
			if _, dup := seen[ch.ID]; dup {
				c.add(i, id, "id", CauseRepeatedID, quote(string(ch.ID)), "one entry per card", "list each card once")
			}
			seen[ch.ID] = i
		}
		v.cardState(i, id, "after", &ch.After)
		want := uint64(1)
		if ch.Before != nil {
			v.cardState(i, id, "before", ch.Before)
			if card.ValidCounter(ch.Before.Revision) {
				b, _ := strconv.ParseUint(ch.Before.Revision, 10, 64)
				want = b + 1
			}
			v.countersGrow(i, id, ch.Before.Counters, ch.After.Counters)
		}
		if card.CounterAtLeastOne(ch.After.Revision) {
			a, _ := strconv.ParseUint(ch.After.Revision, 10, 64)
			if a != want || want == 0 {
				c.add(i, id, "after.revision", CauseInvalidValue, quote(ch.After.Revision), strconv.FormatUint(want, 10),
					"a changed card's revision advances once; a created card starts at 1")
			}
		}
		v.changeLinks(i, id, ch)
		v.notifications(i, id, ch)
	}
	v.opMoves(r, seen)
	v.named("blocked", r.Blocked)
	v.named("ineligible", r.Ineligible)
	v.named("already", r.Already)
	v.named("inapplicable", r.Inapplicable)
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
		{"counts.already", n.Already, len(r.Already)},
		{"counts.inapplicable", n.Inapplicable, len(r.Inapplicable)},
	} {
		if f.got != f.want {
			c.add(-1, "", f.name, CauseInvalidValue, strconv.Itoa(f.got), strconv.Itoa(f.want), "the count is the length of its list")
		}
	}
	for _, f := range []struct {
		name string
		val  int
	}{{"counts.selected", n.Selected}, {"counts.eligible", n.Eligible}, {"counts.guards", n.Guards}} {
		if f.val < 0 {
			c.add(-1, "", f.name, CauseInvalidValue, strconv.Itoa(f.val), "0 or more", "counts are not negative")
		}
	}
	if n.Selected < len(r.Changed)+len(r.Blocked)+len(r.Ineligible)+len(r.Missing)+len(r.Already)+len(r.Inapplicable) {
		c.add(-1, "", "counts.selected", CauseInvalidValue, strconv.Itoa(n.Selected), "at least the changed, blocked, ineligible, missing, already and inapplicable cards",
			"selected counts every card the request named or the scope chose")
	}
	if n.Eligible > n.Selected {
		c.add(-1, "", "counts.eligible", CauseInvalidValue, strconv.Itoa(n.Eligible), "at most the selected cards", "eligible cards are selected cards")
	}
	return c.err()
}

// placeAndOutcome checks a card's column and outcome: a placed card is in one of
// the states and has no outcome; a card that left the table is in none and has one.
// It reports whether the pair is well formed.
func (v validator) placeAndOutcome(i int, id, colField, outField string, col State, outcome Outcome) bool {
	switch {
	case col == Unplaced && !outcome.Valid():
		v.c.add(i, id, outField, CauseRequired, quote(string(outcome)), strings.Join(toStrings(Outcomes()), ", "), "a card that left the table carries an outcome")
		return false
	case col == Unplaced:
		return true
	case !col.Valid():
		v.c.add(i, id, colField, CauseInvalidValue, quote(string(col)), "one of "+strings.Join(stateNames(), ", ")+", or none for a card that left the table", "send one of the listed values")
		return false
	case outcome != "":
		v.c.add(i, id, outField, CauseNotApplicable, quote(string(outcome)), "", "only a card that left the table carries an outcome")
		return false
	}
	return true
}

func (v validator) cardState(i int, id, prefix string, s *CardState) {
	v.name(i, id, prefix+".row", s.Row)
	v.counter(i, id, prefix+".revision", s.Revision, true)
	v.placeAndOutcome(i, id, prefix+".state", prefix+".outcome", s.State, s.Outcome)
	for _, n := range counterNames {
		if val := *n.get(&s.Counters); val != "" && !card.ValidCounter(val) {
			v.c.add(i, id, prefix+".counters."+n.name, CauseInvalidValue, quote(val), "a decimal integer within uint64", "send a decimal counter")
		}
	}
}

// countersGrow refuses a receipt whose after counters are below its before: a
// cycle counter only grows (RedSameHead, which resets, is the exception).
func (v validator) countersGrow(i int, id string, before, after Counters) {
	for _, n := range counterNames {
		b, okb := card.Count(*n.get(&before))
		a, oka := card.Count(*n.get(&after))
		if okb && oka && a < b && !n.reset {
			v.c.add(i, id, "after.counters."+n.name, CauseInvalidValue, strconv.FormatUint(a, 10)+" after "+strconv.FormatUint(b, 10),
				"at least the value before", "a cycle counter never decreases")
		}
	}
}

// changeLinks checks the successor, reason and landing identity of a changed card.
func (v validator) changeLinks(i int, id string, ch *CardChange) {
	left := ch.After.State == Unplaced
	replaced := left && ch.After.Outcome.HasSuccessor()
	switch {
	case replaced && ch.Successor == "":
		v.c.add(i, id, "successor", CauseRequired, "", "the card that replaced it", "a replaced card names its successor")
	case !replaced && ch.Successor != "":
		v.c.add(i, id, "successor", CauseNotApplicable, quote(string(ch.Successor)), "", "only a card that left the table as replaced has a successor")
	case replaced:
		v.cardID(i, id, "successor", ch.Successor)
		if ch.Successor == ch.ID {
			v.c.add(i, id, "successor", CauseInvalidValue, quote(string(ch.Successor)), "a card other than this one", "a card is not its own successor")
		}
	}
	switch {
	case left && ch.Reason == "":
		v.c.add(i, id, "reason", CauseRequired, "", "why the card left the table", "a card that left the table keeps its outcome and a reason")
	case !left && ch.Reason != "":
		v.c.add(i, id, "reason", CauseNotApplicable, quote(ch.Reason), "", "only a card that left the table has a reason")
	case left:
		v.reason(i, id, "reason", ch.Reason)
	}
	switch {
	case HoldsLanding(ch.After.State) && ch.Landing == "":
		v.c.add(i, id, "landing", CauseRequired, "", "the landing identity", "a landed card holds a landing identity")
	case !HoldsLanding(ch.After.State) && ch.Landing != "":
		v.c.add(i, id, "landing", CauseNotApplicable, quote(ch.Landing), "", "only a landed card holds a landing identity")
	case ch.Landing != "":
		v.token(i, id, "landing", ch.Landing, MaxRefBytes)
	}
}

func (v validator) notifications(i int, id string, ch *CardChange) {
	if len(ch.Notifications) > 8 {
		v.c.add(i, id, "notifications", CauseTooMany, strconv.Itoa(len(ch.Notifications)), "8", "a card yields at most eight notifications in one batch")
	}
	for j := 0; j < len(ch.Notifications) && j < 8; j++ {
		n := &ch.Notifications[j]
		p := "notifications[" + strconv.Itoa(j) + "]."
		if !n.Kind.Valid() {
			v.c.add(i, id, p+"kind", CauseInvalidValue, quote(string(n.Kind)), "a notification kind", "use one of the closed set")
		}
		switch n.Judgment {
		case Yes, No:
		default:
			v.c.add(i, id, p+"judgment", CauseInvalidValue, quote(string(n.Judgment)), "yes or no", "set the judgment answer")
		}
		if rule, ok := notificationRule[n.Kind]; ok {
			switch {
			case rule == ruleAlways && n.Judgment == No:
				v.c.add(i, id, p+"judgment", CauseInvalidValue, "no", "yes", "a "+string(n.Kind)+" notification is always a judgment point")
			case rule == ruleNever && n.Judgment == Yes:
				v.c.add(i, id, p+"judgment", CauseInvalidValue, "yes", "no", "a "+string(n.Kind)+" notification is never a judgment point")
			}
		}
		switch n.Escalation {
		case EscalationNone:
		case EscalationEscalated:
			if n.Judgment != Yes {
				v.c.add(i, id, p+"escalation", CauseInvalidValue, "escalated", "none", "only a judgment point is escalated")
			}
		default:
			v.c.add(i, id, p+"escalation", CauseInvalidValue, quote(string(n.Escalation)), "none or escalated", "set the escalation")
		}
		if n.Readiness != "" {
			if cause := card.TextFault(n.Readiness, MaxNoteBytes); cause != "" {
				v.fault(i, id, p+"readiness", n.Readiness, cause, MaxNoteBytes, "one clean line")
			}
		}
		for _, cn := range counterNames {
			if val := *cn.get(&n.Counters); val != "" && !card.ValidCounter(val) {
				v.c.add(i, id, p+"counters."+cn.name, CauseInvalidValue, quote(val), "a decimal integer within uint64", "send a decimal counter")
			}
		}
	}
}

// opMoves checks that every changed card's move is one the receipt's operation
// makes, as the lifecycle tables (lifecycle.go) say.
func (v validator) opMoves(r *Receipt, index map[ID]int) {
	resolveFrom, resolveTo := ResolveMove()
	for i := range r.Changed {
		ch := &r.Changed[i]
		id := knownID(string(ch.ID))
		bad := func(found, limit, next string) {
			v.c.add(i, id, "after.state", CauseInvalidValue, found, limit, next)
		}
		if !placeOK(ch.After.State, ch.After.Outcome) || (ch.Before != nil && !ch.Before.State.Valid()) {
			continue // already refused for what it is
		}
		switch r.Operation {
		case OpAdmit:
			if ch.Before != nil {
				bad("a card that existed", "a card the batch creates in "+string(Initial), "an admit only creates cards")
			} else if ch.After.State != Initial {
				bad(quote(string(ch.After.State)), string(Initial), "an admitted card starts in "+string(Initial))
			}
		case OpResolve:
			switch {
			case ch.Before == nil:
				bad("a created card", "a card moved from "+string(resolveFrom)+" to "+string(resolveTo), "a resolve creates no card")
			case ch.Before.State != resolveFrom || ch.After.State != resolveTo:
				bad(quote(moveText(ch.Before.State, ch.After)), string(resolveFrom)+" -> "+string(resolveTo), "a resolve only moves a "+string(resolveFrom)+" card to "+string(resolveTo))
			case ch.Before.Row != ch.After.Row:
				bad("a change of row", "the same row", "a resolve keeps the card's row")
			}
		case OpApplyEvents:
			switch {
			case ch.Before == nil:
				bad("a created card", "a card a lifecycle input moves", "a lifecycle input creates no card")
			case ch.Before.Row != ch.After.Row:
				bad("a change of row", "the same row", "a lifecycle input keeps the card's row")
			case !inputMakes(ch.Before.State, ch.After):
				bad(quote(moveText(ch.Before.State, ch.After)), "a move some lifecycle input names", "no lifecycle input makes this move")
			}
		case OpRecordEvidence:
			switch {
			case ch.Before == nil:
				bad("a created card", "a card whose evidence is recorded", "recording evidence creates no card")
			case ch.Before.Row != ch.After.Row:
				bad("a change of row", "the same row", "recording evidence keeps the card's row")
			case ch.Before.State != ch.After.State && !evidenceForces(ch.Before.State, ch.After.State):
				bad(quote(moveText(ch.Before.State, ch.After)), "the same state, or a forced move", "recording evidence forces only "+forcedList())
			}
		case OpReplace:
			// Checked pairwise below.
		}
	}
	if r.Operation != OpReplace {
		return
	}
	olds, news := 0, 0
	for i := range r.Changed {
		ch := &r.Changed[i]
		id := knownID(string(ch.ID))
		if ch.Before == nil {
			news++
			if ch.After.State != Initial {
				v.c.add(i, id, "after.state", CauseInvalidValue, quote(string(ch.After.State)), string(Initial), "the new side of a replacement starts in "+string(Initial))
			}
		} else {
			olds++
			if !Replaceable(ch.Before.State) || ch.After.State != Unplaced || ch.After.Outcome != ReplaceOutcome() {
				v.c.add(i, id, "after.state", CauseInvalidValue, quote(moveText(ch.Before.State, ch.After)), "a card in "+joinStrings(ReplaceableStates())+" leaving the table as "+string(ReplaceOutcome()),
					"a replace only ends a card in "+joinStrings(ReplaceableStates())+" as "+string(ReplaceOutcome()))
			}
			if ch.Successor != "" {
				if j, ok := index[ch.Successor]; !ok || r.Changed[j].Before != nil {
					v.c.add(i, id, "successor", CauseInvalidValue, quote(string(ch.Successor)), "a card this batch created", "the successor is the new side of the pair")
				}
			}
		}
	}
	if olds != news {
		v.c.add(-1, "", "changed", CauseInvalidValue, strconv.Itoa(olds)+" ended and "+strconv.Itoa(news)+" created", "as many created as ended", "a replace ends one card and creates one for every pair")
	}
}

// placeOK says a card's column and outcome are well formed together.
func placeOK(col State, o Outcome) bool {
	if col == Unplaced {
		return o.Valid()
	}
	return col.Valid() && o == ""
}

func moveText(from State, after CardState) string {
	to := string(after.State)
	if after.State == Unplaced {
		to = "off the table (" + string(after.Outcome) + ")"
	}
	return string(from) + " -> " + to
}

// inputMakes says some lifecycle input takes a card from the state to the after
// state, with the outcome of a card that left the table.
func inputMakes(from State, after CardState) bool {
	for _, t := range allInputTypes {
		m, ok := Transition(t, from)
		if !ok || m.To != after.State || m.Outcome != after.Outcome {
			continue
		}
		return true
	}
	return false
}

func evidenceForces(from, to State) bool {
	for _, f := range forcedMoves {
		if f.From == from && f.To == to {
			return true
		}
	}
	return false
}

func forcedList() string {
	var parts []string
	for _, f := range forcedMoves {
		parts = append(parts, string(f.Kind)+" "+string(f.Disposition)+" for a card in "+string(f.From)+" -> "+string(f.To))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
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
// cause, batch or card scope with a card exactly for the card scope, bounded clean
// text, a next command, and an operation ID exactly for a mutating operation (an
// inspect or a check has none). Whether anything changed is not a field: it is
// derived from the cause.
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
	v.enum(-1, "", "operation", string(r.Operation), toStrings(allOperations[:]))
	switch {
	case r.Operation.Mutating():
		v.token(-1, "", "operation_id", r.OperationID, card.MaxOperationIDBytes)
	case r.Operation.Valid() && r.OperationID != "":
		c.add(-1, "", "operation_id", CauseNotApplicable, quote(r.OperationID), "", "a read has no operation ID; remove the field")
	}
	v.enum(-1, "", "scope", r.Scope, []string{ScopeBatch, ScopeCard})
	switch r.Scope {
	case ScopeCard:
		v.cardID(-1, "", "card", r.Card)
	case ScopeBatch:
		if r.Card != "" {
			c.add(-1, "", "card", CauseNotApplicable, quote(string(r.Card)), "", "a batch refusal names no card")
		}
	}
	if !r.Cause.Known() {
		c.add(-1, "", "cause", CauseInvalidValue, quote(string(r.Cause)), "a named cause", "use one of the named causes")
	}
	for _, f := range [][2]string{{"expected", r.Expected}, {"observed", r.Observed}, {"next", r.Next}} {
		v.text(-1, "", f[0], f[1], MaxDetailBytes, func(string) bool { return true }, "a bounded one-line text")
	}
	return c.err()
}

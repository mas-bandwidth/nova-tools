package request

import (
	"fmt"
	"strings"
	"testing"
)

func sampleReceipt() *Receipt {
	return &Receipt{
		Schema: SchemaVersion, Operation: OpApplyEvents, OperationID: "op-17", RequestHash: Digest(dig2),
		Table: "work", Epoch: "3", RevisionBefore: "12", RevisionAfter: "13", Actor: "coordinator", Result: ResultChanged,
		Changed: []CardChange{
			{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Working, Revision: "3", Counters: Counters{Starts: "1"}},
				Notifications: []Notification{{Kind: NoteStarted, Judgment: No, Escalation: EscalationNone, Counters: Counters{Starts: "1"}}}},
			{ID: "c2", Before: &CardState{Row: "build", State: Review, Revision: "5"}, After: CardState{Row: "build", State: Merging, Revision: "6"},
				Notifications: []Notification{{Kind: NoteMerging, Judgment: No, Readiness: "two reads and ci:unit at the head", Escalation: EscalationNone}}},
			{ID: "c3", Before: &CardState{Row: "build", State: Merging, Revision: "9", Counters: Counters{MergeReturns: "1"}}, After: CardState{Row: "build", State: Landed, Revision: "10", Counters: Counters{MergeReturns: "1"}}, Landing: "land:" + g40},
		},
		Blocked:      []Named{{ID: "c5", Reason: "waits on c9"}},
		Ineligible:   []Named{{ID: "c6", Reason: "already landed"}},
		Already:      []Named{{ID: "c7", Reason: "recorded by op-1"}},
		Inapplicable: []Named{{ID: "c8", Reason: "ci for a waiting card"}},
		Missing:      []ID{"c9"},
		Counts:       Counts{Selected: 8, Eligible: 3, Changed: 3, Blocked: 1, Ineligible: 1, Missing: 1, Already: 1, Inapplicable: 1, Guards: 2},
	}
}

func sampleRejection() *Rejection {
	return &Rejection{Operation: OpApplyEvents, OperationID: "op-17", Scope: ScopeCard, Card: "c1", Cause: CauseStaleCardRev,
		Expected: "revision 2", Observed: "revision 3", Next: "read the card again and send the lifecycle input with the observed revision"}
}

func sampleInspect() *InspectResult {
	return &InspectResult{Schema: SchemaVersion, Table: "work", Epoch: "3", Revision: "13",
		Cards: []InspectCard{
			{ID: "c2", Place: Place{Row: "build", Col: Review}, Revision: "5", Pin: pin(), Head: g40, Standing: "2 of 2 reads, ci:unit green",
				Missing: []string{"sweep"}, Drift: []Drift{DriftStanding}, Counters: Counters{Rework: "1", Red: "2", RedSameHead: "1"}, Escalation: EscalationEscalated, Marks: []Mark{MarkRedTotal}},
			{ID: "c1", Place: Place{Row: "build", Col: Ready}, Revision: "2", Pin: pin(), Escalation: EscalationNone},
			{ID: "c3", Place: Place{Row: "build", Col: Unplaced}, Outcome: Cancelled, Revision: "8", Pin: pin(), Escalation: EscalationNone},
		},
		Missing: []ID{"c9"}}
}

func pin() Pin {
	return Pin{Digest: Digest(dig), ObjectID: g40, Commit: g40, Repository: "example.org/team/repo", Path: "cards/x.md", Kind: "fix-red"}
}

func TestValidReceiptRejectionAndInspectPass(t *testing.T) {
	t.Parallel()
	if err := ValidateReceipt(sampleReceipt()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRejection(sampleRejection()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInspectResult(sampleInspect()); err != nil {
		t.Fatal(err)
	}
	noop := sampleReceipt()
	noop.Result, noop.Changed, noop.Counts = ResultNoop, nil, Counts{Selected: 5, Blocked: 1, Ineligible: 1, Missing: 1, Already: 1, Inapplicable: 1, Guards: 2}
	if err := ValidateReceipt(noop); err != nil {
		t.Fatalf("noop receipt: %v", err)
	}
	r := sampleReceipt()
	if !strings.Contains(r.Line(), "hash="+string(dig2)) || !strings.Contains(r.Line(), "receipt="+string(r.Digest())) {
		t.Fatalf("the one-line form lacks the request hash or the receipt digest: %s", r.Line())
	}
}

func TestReceiptRefusals(t *testing.T) {
	t.Parallel()
	move := func(id string, from, to State) CardChange {
		return CardChange{ID: ID(id), Before: &CardState{Row: "build", State: from, Revision: "2"}, After: CardState{Row: "build", State: to, Revision: "3"}}
	}
	one := func(op Operation, ch ...CardChange) func(*Receipt) {
		return func(r *Receipt) {
			r.Operation = op
			r.Changed = ch
			r.Blocked, r.Ineligible, r.Already, r.Inapplicable, r.Missing = nil, nil, nil, nil, nil
			r.Counts = Counts{Selected: len(ch), Eligible: len(ch), Changed: len(ch)}
		}
	}
	st := func(s State, rev string) CardState { return CardState{Row: "build", State: s, Revision: rev} }
	cases := []struct {
		name string
		edit func(r *Receipt)
		want []string
	}{
		{"schema", func(r *Receipt) { r.Schema = 9 }, wantTriples("-1|schema|invalid-value")},
		{"read-only operation", func(r *Receipt) { r.Operation = OpInspect }, wantTriples("-1|operation|invalid-value")},
		{"check is read-only", func(r *Receipt) { r.Operation = OpCheck }, wantTriples("-1|operation|invalid-value")},
		{"operation unknown", func(r *Receipt) { r.Operation = "x" }, wantTriples("-1|operation|invalid-value")},
		{"operation id empty", func(r *Receipt) { r.OperationID = "" }, wantTriples("-1|operation_id|required")},
		{"hash missing", func(r *Receipt) { r.RequestHash = "" }, wantTriples("-1|request_hash|required")},
		{"hash malformed", func(r *Receipt) { r.RequestHash = "abc" }, wantTriples("-1|request_hash|invalid-value")},
		{"epoch", func(r *Receipt) { r.Epoch = "1.5" }, wantTriples("-1|epoch|invalid-value")},
		{"revision does not advance", func(r *Receipt) { r.RevisionAfter = "12" }, wantTriples("-1|revision_after|invalid-value")},
		{"revision advances twice", func(r *Receipt) { r.RevisionAfter = "14" }, wantTriples("-1|revision_after|invalid-value")},
		{"revision overflow", func(r *Receipt) { r.RevisionBefore, r.RevisionAfter = "18446744073709551615", "0" }, wantTriples("-1|revision_after|invalid-value")},
		{"changed with none changed", func(r *Receipt) { r.Changed, r.Counts.Changed = nil, 0 }, wantTriples("-1|result|invalid-value")},
		{"noop with changes", func(r *Receipt) { r.Result = ResultNoop }, wantTriples("-1|result|invalid-value")},
		{"result unknown", func(r *Receipt) { r.Result = "maybe" }, wantTriples("-1|result|invalid-value")},
		{"card revision skips", func(r *Receipt) { r.Changed[0].After.Revision = "5" }, wantTriples("0|after.revision|invalid-value")},
		{"created card revision", one(OpAdmit, CardChange{ID: "c1", After: st(Waiting, "2")}), wantTriples("0|after.revision|invalid-value")},
		{"a card that left the table has no outcome", func(r *Receipt) { r.Changed[1].After.State = Unplaced }, wantTriples("1|after.outcome|required", "1|reason|required")},
		{"a placed card has no outcome", func(r *Receipt) { r.Changed[0].After.Outcome = Cancelled }, wantTriples("0|after.outcome|not-applicable")},
		{"outcome unknown", func(r *Receipt) { r.Changed[1].After.State, r.Changed[1].After.Outcome = Unplaced, "vanished" }, wantTriples("1|after.outcome|required", "1|reason|required")},
		{"outcome completed is not an outcome", func(r *Receipt) { r.Changed[1].After.State, r.Changed[1].After.Outcome = Unplaced, "completed" }, wantTriples("1|after.outcome|required", "1|reason|required")},
		{"state unknown", func(r *Receipt) { r.Changed[0].After.State = "limbo" }, wantTriples("0|after.state|invalid-value")},
		{"a card that left needs a reason", func(r *Receipt) {
			r.Operation = OpApplyEvents
			r.Changed[1] = CardChange{ID: "c2", Before: &CardState{Row: "build", State: Review, Revision: "5"}, After: CardState{Row: "build", State: Unplaced, Revision: "6", Outcome: Cancelled}}
		}, wantTriples("1|reason|required")},
		{"a placed card has no reason", func(r *Receipt) { r.Changed[0].Reason = "why" }, wantTriples("0|reason|not-applicable")},
		{"a reason with bidi", func(r *Receipt) {
			r.Changed[1] = CardChange{ID: "c2", Before: &CardState{Row: "build", State: Review, Revision: "5"}, After: CardState{Row: "build", State: Unplaced, Revision: "6", Outcome: Cancelled}, Reason: "a\u202eb"}
		}, wantTriples("1|reason|control-character")},
		{"nothing moves a landed card", func(r *Receipt) { r.Changed[1] = move("c2", Landed, Ready) }, wantTriples("1|after.state|invalid-value")},
		{"there is no done state", func(r *Receipt) { r.Changed[1].After.State = "done" }, wantTriples("1|after.state|invalid-value")},
		{"card repeated", func(r *Receipt) { r.Changed[1].ID = "c1" }, wantTriples("1|id|repeated-id")},
		{"card id bad", func(r *Receipt) { r.Changed[0].ID = "a b" }, wantTriples("0|id|invalid-value")},
		{"before state unknown", func(r *Receipt) { r.Changed[0].Before.State = "limbo" }, wantTriples("0|before.state|invalid-value")},
		{"count changed", func(r *Receipt) { r.Counts.Changed = 2 }, wantTriples("-1|counts.changed|invalid-value")},
		{"count blocked", func(r *Receipt) { r.Counts.Blocked = 0 }, wantTriples("-1|counts.blocked|invalid-value")},
		{"count ineligible", func(r *Receipt) { r.Counts.Ineligible = 4 }, wantTriples("-1|counts.ineligible|invalid-value")},
		{"count missing", func(r *Receipt) { r.Counts.Missing = 0 }, wantTriples("-1|counts.missing|invalid-value")},
		{"count already", func(r *Receipt) { r.Counts.Already = 0 }, wantTriples("-1|counts.already|invalid-value")},
		{"count inapplicable", func(r *Receipt) { r.Counts.Inapplicable = 0 }, wantTriples("-1|counts.inapplicable|invalid-value")},
		{"selected below the parts", func(r *Receipt) { r.Counts.Selected = 3 }, wantTriples("-1|counts.selected|invalid-value")},
		{"eligible over selected", func(r *Receipt) { r.Counts.Eligible = 9 }, wantTriples("-1|counts.eligible|invalid-value")},
		{"negative guards", func(r *Receipt) { r.Counts.Guards = -1 }, wantTriples("-1|counts.guards|invalid-value")},
		{"blocked reason empty", func(r *Receipt) { r.Blocked[0].Reason = "" }, wantTriples("-1|blocked[0].reason|required")},
		{"blocked reason newline", func(r *Receipt) { r.Blocked[0].Reason = "a\nb" }, wantTriples("-1|blocked[0].reason|control-character")},
		{"blocked reason bidi", func(r *Receipt) { r.Blocked[0].Reason = "a\u202eb" }, wantTriples("-1|blocked[0].reason|control-character")},
		{"ineligible id", func(r *Receipt) { r.Ineligible[0].ID = "" }, wantTriples("-1|ineligible[0].id|required")},
		{"missing id", func(r *Receipt) { r.Missing[0] = "x y" }, wantTriples("-1|missing[0]|invalid-value")},

		// what no operation could produce
		{"resolve moves waiting to ready", one(OpResolve, move("c1", Waiting, Ready)), nil},
		{"resolve moves ready to working", one(OpResolve, move("c1", Ready, Working)), wantTriples("0|after.state|invalid-value")},
		{"resolve moves waiting to landed", one(OpResolve, move("c1", Waiting, Landed)), wantTriples("0|after.state|invalid-value", "0|landing|required")},
		{"resolve moves waiting to done", one(OpResolve, CardChange{ID: "c1", Before: &CardState{Row: "build", State: Waiting, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped", Successor: "c2"}), wantTriples("0|after.state|invalid-value")},
		{"resolve creates a card", one(OpResolve, CardChange{ID: "c1", After: st(Ready, "1")}), wantTriples("0|after.state|invalid-value")},
		{"resolve changes the row", one(OpResolve, CardChange{ID: "c1", Before: &CardState{Row: "build", State: Waiting, Revision: "2"}, After: CardState{Row: "docs", State: Ready, Revision: "3"}}), wantTriples("0|after.state|invalid-value")},
		{"apply creates a card", one(OpApplyEvents, CardChange{ID: "c1", After: st(Waiting, "1")}), wantTriples("0|after.state|invalid-value")},
		{"apply moves ready to working", one(OpApplyEvents, move("c1", Ready, Working)), nil},
		{"apply moves ready to review", one(OpApplyEvents, move("c1", Ready, Review)), wantTriples("0|after.state|invalid-value")},
		{"apply moves review to review (a new head)", one(OpApplyEvents, move("c1", Review, Review)), nil},
		{"apply moves merging to review", one(OpApplyEvents, move("c1", Merging, Review)), nil},
		{"apply moves review to ready", one(OpApplyEvents, move("c1", Review, Ready)), nil},
		{"apply moves landed anywhere", one(OpApplyEvents, move("c1", Landed, Working)), wantTriples("0|after.state|invalid-value")},
		{"apply takes a card off the table as replaced", one(OpApplyEvents, CardChange{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped", Successor: "c2"}), wantTriples("0|after.state|invalid-value")},
		{"apply takes a card off the table as cancelled", one(OpApplyEvents, CardChange{ID: "c1", Before: &CardState{Row: "build", State: Merging, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Cancelled}, Reason: "stopped"}), nil},
		{"admit moves a card", one(OpAdmit, move("c1", Waiting, Ready)), wantTriples("0|after.state|invalid-value")},
		{"admit creates in ready", one(OpAdmit, CardChange{ID: "c1", After: st(Ready, "1")}), wantTriples("0|after.state|invalid-value")},
		{"admit creates in waiting", one(OpAdmit, CardChange{ID: "c1", After: st(Waiting, "1")}), nil},
		{"evidence leaves the state", one(OpRecordEvidence, move("c1", Review, Review)), nil},
		{"evidence forces merging to review", one(OpRecordEvidence, move("c1", Merging, Review)), nil},
		{"evidence moves review to ready", one(OpRecordEvidence, move("c1", Review, Ready)), wantTriples("0|after.state|invalid-value")},
		{"evidence moves review to merging", one(OpRecordEvidence, move("c1", Review, Merging)), wantTriples("0|after.state|invalid-value")},
		{"evidence creates a card", one(OpRecordEvidence, CardChange{ID: "c1", After: st(Waiting, "1")}), wantTriples("0|after.state|invalid-value")},
		{"replace creates without ending", one(OpReplace, CardChange{ID: "c1", After: st(Waiting, "1")}), wantTriples("-1|changed|invalid-value")},
		{"replace ends a working card", one(OpReplace,
			CardChange{ID: "c1", Before: &CardState{Row: "build", State: Working, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped", Successor: "c2"},
			CardChange{ID: "c2", After: st(Waiting, "1")}), wantTriples("0|after.state|invalid-value")},
		{"replace without a successor link", one(OpReplace,
			CardChange{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped"},
			CardChange{ID: "c2", After: st(Waiting, "1")}), wantTriples("0|successor|required")},
		{"replace successor is not created here", one(OpReplace,
			CardChange{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped", Successor: "zz"},
			CardChange{ID: "c2", After: st(Waiting, "1")}), wantTriples("0|successor|invalid-value")},
		{"replace ready to replaced and a new card", one(OpReplace,
			CardChange{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Unplaced, Revision: "3", Outcome: Replaced}, Reason: "stopped", Successor: "c2"},
			CardChange{ID: "c2", After: st(Waiting, "1")}), nil},

		// links
		{"successor on a card that is not replaced", func(r *Receipt) { r.Changed[0].Successor = "c9" }, wantTriples("0|successor|not-applicable")},
		{"landing missing on a landed card", func(r *Receipt) { r.Changed[2].Landing = "" }, wantTriples("2|landing|required")},
		{"landing on a card that is not landed", func(r *Receipt) { r.Changed[0].Landing = "land:x" }, wantTriples("0|landing|not-applicable")},
		{"landing not a token", func(r *Receipt) { r.Changed[2].Landing = "a b" }, wantTriples("2|landing|invalid-value")},

		// cycle counters
		{"counter is not a decimal", func(r *Receipt) { r.Changed[0].After.Counters.Starts = "one" }, wantTriples("0|after.counters.starts|invalid-value")},
		{"counter over uint64", func(r *Receipt) { r.Changed[0].After.Counters.Starts = "18446744073709551616" }, wantTriples("0|after.counters.starts|invalid-value")},
		{"a counter never decreases", func(r *Receipt) { r.Changed[2].After.Counters.MergeReturns = "0" }, wantTriples("2|after.counters.merge_returns|invalid-value")},
		{"an absent counter reads as zero and may stay", func(r *Receipt) { r.Changed[2].After.Counters.MergeReturns = "" }, wantTriples("2|after.counters.merge_returns|invalid-value")},
		{"a counter may grow", func(r *Receipt) { r.Changed[2].After.Counters.MergeReturns = "2" }, nil},
		{"the same-head red count may reset", func(r *Receipt) {
			r.Changed[2].Before.Counters.RedSameHead = "3"
			r.Changed[2].After.Counters.RedSameHead = "0"
		}, nil},

		// notifications
		{"notification kind unknown", func(r *Receipt) { r.Changed[0].Notifications[0].Kind = "shout" }, wantTriples("0|notifications[0].kind|invalid-value")},
		{"notification judgment", func(r *Receipt) { r.Changed[0].Notifications[0].Judgment = "maybe" }, wantTriples("0|notifications[0].judgment|invalid-value")},
		{"a returned notification is always judgment", func(r *Receipt) {
			r.Changed[0].Notifications[0] = Notification{Kind: NoteReturned, Judgment: No, Escalation: EscalationNone}
		}, wantTriples("0|notifications[0].judgment|invalid-value")},
		{"a started notification is never judgment", func(r *Receipt) { r.Changed[0].Notifications[0].Judgment = Yes }, wantTriples("0|notifications[0].judgment|invalid-value")},
		{"a result notification may be either", func(r *Receipt) {
			r.Changed[0].Notifications[0] = Notification{Kind: NoteResult, Judgment: Yes, Escalation: EscalationNone}
		}, nil},
		{"escalation unknown", func(r *Receipt) { r.Changed[0].Notifications[0].Escalation = "loud" }, wantTriples("0|notifications[0].escalation|invalid-value")},
		{"escalation empty", func(r *Receipt) { r.Changed[0].Notifications[0].Escalation = "" }, wantTriples("0|notifications[0].escalation|invalid-value")},
		{"only judgment is escalated", func(r *Receipt) { r.Changed[0].Notifications[0].Escalation = EscalationEscalated }, wantTriples("0|notifications[0].escalation|invalid-value")},
		{"a judgment may be escalated", func(r *Receipt) {
			r.Changed[0].Notifications[0] = Notification{Kind: NoteReturned, Judgment: Yes, Escalation: EscalationEscalated}
		}, nil},
		{"readiness text at the bound", func(r *Receipt) { r.Changed[0].Notifications[0].Readiness = longString(MaxNoteBytes) }, nil},
		{"readiness text over the bound", func(r *Receipt) { r.Changed[0].Notifications[0].Readiness = longString(MaxNoteBytes + 1) }, wantTriples("0|notifications[0].readiness|too-long")},
		{"readiness text newline", func(r *Receipt) { r.Changed[0].Notifications[0].Readiness = "a\nb" }, wantTriples("0|notifications[0].readiness|control-character")},
		{"readiness text bidi", func(r *Receipt) { r.Changed[0].Notifications[0].Readiness = "a\u202eb" }, wantTriples("0|notifications[0].readiness|control-character")},
		{"notification counter", func(r *Receipt) { r.Changed[0].Notifications[0].Counters.Red = "x" }, wantTriples("0|notifications[0].counters.red|invalid-value")},
		{"too many notifications", func(r *Receipt) {
			n := r.Changed[0].Notifications[0]
			r.Changed[0].Notifications = []Notification{n, n, n, n, n, n, n, n, n}
		}, wantTriples("0|notifications|too-many")},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := sampleReceipt()
			c.edit(r)
			if got := triples(ValidateReceipt(r)); !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
		})
	}
	if ValidateReceipt(nil) == nil {
		t.Fatal("nil receipt accepted")
	}
}

func TestRejectionRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(r *Rejection)
		want []string
	}{
		{"operation", func(r *Rejection) { r.Operation = "x" }, wantTriples("-1|operation|invalid-value")},
		{"operation id", func(r *Rejection) { r.OperationID = "" }, wantTriples("-1|operation_id|required")},
		{"an inspect needs no operation id", func(r *Rejection) {
			r.Operation, r.OperationID, r.Scope, r.Card, r.Cause = OpInspect, "", ScopeBatch, "", CauseStaleEpoch
		}, nil},
		{"a check needs no operation id", func(r *Rejection) {
			r.Operation, r.OperationID, r.Scope, r.Card, r.Cause = OpCheck, "", ScopeBatch, "", CauseOverLimit
		}, nil},
		{"an inspect has none", func(r *Rejection) { r.Operation, r.Scope, r.Card = OpInspect, ScopeBatch, "" }, wantTriples("-1|operation_id|not-applicable")},
		{"scope", func(r *Rejection) { r.Scope = "everything" }, wantTriples("-1|scope|invalid-value")},
		{"card scope without card", func(r *Rejection) { r.Card = "" }, wantTriples("-1|card|required")},
		{"batch scope with card", func(r *Rejection) { r.Scope = ScopeBatch }, wantTriples("-1|card|not-applicable")},
		{"batch scope ok", func(r *Rejection) { r.Scope, r.Card = ScopeBatch, "" }, nil},
		{"unknown cause", func(r *Rejection) { r.Cause = "because" }, wantTriples("-1|cause|invalid-value")},
		{"the old names are not causes", func(r *Rejection) { r.Cause = "duplicate-id" }, wantTriples("-1|cause|invalid-value")},
		{"expected empty", func(r *Rejection) { r.Expected = "" }, wantTriples("-1|expected|required")},
		{"observed too long", func(r *Rejection) { r.Observed = longString(MaxDetailBytes + 1) }, wantTriples("-1|observed|too-long")},
		{"next empty", func(r *Rejection) { r.Next = "" }, wantTriples("-1|next|required")},
		{"next multi-line", func(r *Rejection) { r.Next = "a\nb" }, wantTriples("-1|next|control-character")},
		{"stale epoch batch", func(r *Rejection) { r.Scope, r.Card, r.Cause = ScopeBatch, "", CauseStaleEpoch }, nil},
		{"transport failure", func(r *Rejection) { r.Scope, r.Card, r.Cause = ScopeBatch, "", CauseTransport }, nil},
		{"store error", func(r *Rejection) { r.Scope, r.Card, r.Cause = ScopeBatch, "", CauseStoreError }, nil},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := sampleRejection()
			c.edit(r)
			if got := triples(ValidateRejection(r)); !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
		})
	}
	if ValidateRejection(nil) == nil {
		t.Fatal("nil rejection accepted")
	}
}

// Whether anything changed is derived from the cause, never stored: there is no
// field to set inconsistently.
func TestRejectionChangedIsDerived(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cause Cause
		want  Changed
	}{
		{CauseStaleEpoch, ChangedNo}, {CauseGuardFailed, ChangedNo}, {CauseOverLimit, ChangedNo}, {CauseRepeatedID, ChangedNo},
		{CauseTransport, ChangedUnknown}, {CauseStoreError, ChangedUnknown},
	} {
		r := sampleRejection()
		r.Cause = c.cause
		if got := r.Changed(); got != c.want {
			t.Errorf("%s: Changed() = %s, want %s", c.cause, got, c.want)
		}
		if !strings.Contains(string(CanonicalRejection(r)), `"changed":"`+string(c.want)+`"`) {
			t.Errorf("%s: the canonical form does not carry the derived answer: %s", c.cause, CanonicalRejection(r))
		}
		if !strings.Contains(r.Line(), "changed="+string(c.want)) {
			t.Errorf("%s: the line does not carry the derived answer: %s", c.cause, r.Line())
		}
	}
}

// A hostile value in a rejection or a receipt cannot forge a line.
func TestReceiptAndRejectionLinesAreOneLine(t *testing.T) {
	t.Parallel()
	r := sampleRejection()
	r.Card, r.Expected, r.Observed, r.Next = "x\nrefused admit: fake", "a\nb", "\u202e", "n\x00"
	if l := r.Line(); strings.ContainsAny(l, "\n\r\x00\u202e") {
		t.Fatalf("line: %q", l)
	}
	c := sampleReceipt()
	c.OperationID, c.Table, c.Actor = "x\nbatch fake", "t\r\nu", "a\u202eb"
	c.Changed[0].ID, c.Blocked[0].Reason = "y\nz", "why\nwhat"
	if l := c.Line(); strings.ContainsAny(l, "\n\r\x00\u202e") {
		t.Fatalf("line: %q", l)
	}
}

func TestCanonicalReceiptSortsCardsAndSelectionOutcomes(t *testing.T) {
	t.Parallel()
	a, b := sampleReceipt(), sampleReceipt()
	b.Changed[0], b.Changed[2] = b.Changed[2], b.Changed[0]
	b.Blocked = append(b.Blocked, Named{ID: "c0", Reason: "x"})
	a.Blocked = append([]Named{{ID: "c0", Reason: "x"}}, a.Blocked...)
	if string(CanonicalReceipt(a)) != string(CanonicalReceipt(b)) || a.Digest() != b.Digest() {
		t.Fatalf("the same receipt in another order has other bytes")
	}
	if !a.Digest().Valid() {
		t.Fatal("digest")
	}
}

func TestInspectResultRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(r *InspectResult)
		want []string
	}{
		{"schema", func(r *InspectResult) { r.Schema = 2 }, wantTriples("-1|schema|invalid-value")},
		{"table", func(r *InspectResult) { r.Table = "" }, wantTriples("-1|table|required")},
		{"card repeated", func(r *InspectResult) { r.Cards[1].ID = r.Cards[0].ID }, wantTriples("1|id|repeated-id")},
		{"place col", func(r *InspectResult) { r.Cards[0].Place.Col = "limbo" }, wantTriples("0|place.col|invalid-value")},
		{"a card off the table has an outcome", func(r *InspectResult) { r.Cards[2].Outcome = "" }, wantTriples("2|outcome|required")},
		{"a placed card has none", func(r *InspectResult) { r.Cards[0].Outcome = Cancelled }, wantTriples("0|outcome|not-applicable")},
		{"a landed card is placed and has no outcome", func(r *InspectResult) { r.Cards[1].Place.Col = Landed }, nil},
		{"there is no done state", func(r *InspectResult) { r.Cards[1].Place.Col = "done" }, wantTriples("1|place.col|invalid-value")},
		{"pin digest", func(r *InspectResult) { r.Cards[0].Pin.Digest = "x" }, wantTriples("0|pin.digest|invalid-value")},
		{"pin repository is a URL", func(r *InspectResult) { r.Cards[0].Pin.Repository = "https://u:secret@example.com/o/r" }, wantTriples("0|pin.repository|invalid-repository")},
		{"pin path", func(r *InspectResult) { r.Cards[0].Pin.Path = "../x" }, wantTriples("0|pin.path|path-escapes")},
		{"head", func(r *InspectResult) { r.Cards[0].Head = "main" }, wantTriples("0|head|invalid-value")},
		{"standing over the bound", func(r *InspectResult) { r.Cards[0].Standing = longString(MaxStandingBytes + 1) }, wantTriples("0|standing|too-long")},
		{"standing newline", func(r *InspectResult) { r.Cards[0].Standing = "a\nb" }, wantTriples("0|standing|control-character")},
		{"missing over the bound", func(r *InspectResult) {
			for i := 0; i <= MaxMissingItems; i++ {
				r.Cards[0].Missing = append(r.Cards[0].Missing, fmt.Sprintf("m%d", i))
			}
		}, wantTriples("0|missing|too-many")},
		{"missing item", func(r *InspectResult) { r.Cards[0].Missing = []string{"a b"} }, wantTriples("0|missing[0]|invalid-value")},
		{"drift unknown", func(r *InspectResult) { r.Cards[0].Drift = []Drift{"rot"} }, wantTriples("0|drift[0]|invalid-value")},
		{"counter", func(r *InspectResult) { r.Cards[0].Counters.Red = "-1" }, wantTriples("0|counters.red|invalid-value")},
		{"mark unknown", func(r *InspectResult) { r.Cards[0].Marks = []Mark{"loud"} }, wantTriples("0|marks[0]|invalid-value")},
		{"escalated without a mark", func(r *InspectResult) { r.Cards[0].Marks = nil }, wantTriples("0|escalation|invalid-value")},
		{"a mark without escalation", func(r *InspectResult) { r.Cards[1].Marks = []Mark{MarkStale} }, wantTriples("1|escalation|invalid-value")},
		{"escalation empty", func(r *InspectResult) { r.Cards[1].Escalation = "" }, wantTriples("1|escalation|invalid-value")},
		{"a card both found and missing", func(r *InspectResult) { r.Missing = []ID{"c1"} }, wantTriples("-1|missing[0]|invalid-value")},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := sampleInspect()
			c.edit(r)
			if got := triples(ValidateInspectResult(r)); !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
		})
	}
	if ValidateInspectResult(nil) == nil {
		t.Fatal("nil result accepted")
	}
	a, b := sampleInspect(), sampleInspect()
	b.Cards[0], b.Cards[2] = b.Cards[2], b.Cards[0]
	if string(a.Canonical()) != string(b.Canonical()) || a.Digest() != b.Digest() || !a.Digest().Valid() {
		t.Fatal("inspect cards are not sorted by the encoder")
	}
}

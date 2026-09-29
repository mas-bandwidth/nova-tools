package request

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func sampleReceipt() *Receipt {
	return &Receipt{
		Schema: SchemaVersion, Operation: OpApplyEvents, OperationID: "op-17", RequestHash: Digest(dig2),
		Table: "work", Epoch: "3", RevisionBefore: "12", RevisionAfter: "13", Actor: "coordinator", Result: ResultChanged,
		Changed: []CardChange{
			{ID: "c1", Before: &CardState{Row: "build", State: Ready, Revision: "2"}, After: CardState{Row: "build", State: Working, Revision: "3"}},
			{ID: "c2", Before: &CardState{Row: "build", State: Review, Revision: "5"}, After: CardState{Row: "build", State: Done, Revision: "6", Outcome: Completed}},
			{ID: "c3", After: CardState{Row: "docs", State: Waiting, Revision: "1"}},
		},
		Blocked:    []Named{{ID: "c4", Reason: "waits on c9"}},
		Ineligible: []Named{{ID: "c5", Reason: "already landed"}},
		Missing:    []ID{"c6"},
		Counts:     Counts{Selected: 7, Eligible: 3, Changed: 3, Blocked: 1, Ineligible: 1, Missing: 1, Guards: 2},
	}
}

func sampleRejection() *Rejection {
	return &Rejection{Operation: OpApplyEvents, OperationID: "op-17", Scope: ScopeCard, Card: "c1", Cause: CauseStaleCardRev,
		Expected: "revision 2", Observed: "revision 3", Changed: ChangedNo, Next: "read the card again and send the event with the observed revision"}
}

func TestValidReceiptAndRejectionPass(t *testing.T) {
	t.Parallel()
	if err := ValidateReceipt(sampleReceipt()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRejection(sampleRejection()); err != nil {
		t.Fatal(err)
	}
	noop := sampleReceipt()
	noop.Result, noop.Changed, noop.Counts = ResultNoop, nil, Counts{Selected: 3, Eligible: 0, Blocked: 1, Ineligible: 1, Missing: 1, Guards: 2}
	if err := ValidateReceipt(noop); err != nil {
		t.Fatalf("noop receipt: %v", err)
	}
}

func TestReceiptRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(r *Receipt)
		want []string
	}{
		{"schema", func(r *Receipt) { r.Schema = 9 }, wantTriples("-1|schema|bad-value")},
		{"read-only operation", func(r *Receipt) { r.Operation = OpInspect }, wantTriples("-1|operation|bad-value")},
		{"operation unknown", func(r *Receipt) { r.Operation = "x" }, wantTriples("-1|operation|bad-value")},
		{"hash missing", func(r *Receipt) { r.RequestHash = "" }, wantTriples("-1|request_hash|required")},
		{"hash malformed", func(r *Receipt) { r.RequestHash = "abc" }, wantTriples("-1|request_hash|bad-value")},
		{"epoch", func(r *Receipt) { r.Epoch = "1.5" }, wantTriples("-1|epoch|bad-value")},
		{"revision does not advance", func(r *Receipt) { r.RevisionAfter = "12" }, wantTriples("-1|revision_after|bad-value")},
		{"revision advances twice", func(r *Receipt) { r.RevisionAfter = "14" }, wantTriples("-1|revision_after|bad-value")},
		{"revision overflow", func(r *Receipt) { r.RevisionBefore, r.RevisionAfter = "18446744073709551615", "0" }, wantTriples("-1|revision_after|bad-value")},
		{"noop advances by one too", func(r *Receipt) {
			r.Result, r.Changed, r.Counts = ResultNoop, nil, Counts{Selected: 3, Blocked: 1, Ineligible: 1, Missing: 1}
			r.RevisionAfter = "12"
		}, wantTriples("-1|revision_after|bad-value")},
		{"changed with none changed", func(r *Receipt) { r.Changed, r.Counts.Changed = nil, 0 }, wantTriples("-1|result|bad-value")},
		{"noop with changes", func(r *Receipt) { r.Result = ResultNoop }, wantTriples("-1|result|bad-value")},
		{"result unknown", func(r *Receipt) { r.Result = "maybe" }, wantTriples("-1|result|bad-value")},
		{"card revision skips", func(r *Receipt) { r.Changed[0].After.Revision = "5" }, wantTriples("0|after.revision|bad-value")},
		{"created card revision", func(r *Receipt) { r.Changed[2].After.Revision = "2" }, wantTriples("2|after.revision|bad-value")},
		{"created card not waiting", func(r *Receipt) { r.Changed[2].After.State = Ready }, wantTriples("2|after.state|bad-value")},
		{"done without outcome", func(r *Receipt) { r.Changed[1].After.Outcome = "" }, wantTriples("1|after.outcome|required")},
		{"outcome without done", func(r *Receipt) { r.Changed[0].After.Outcome = Cancelled }, wantTriples("0|after.outcome|not-applicable")},
		{"outcome unknown", func(r *Receipt) { r.Changed[1].After.Outcome = "vanished" }, wantTriples("1|after.outcome|required")},
		{"card repeated", func(r *Receipt) { r.Changed[1].ID = "c1" }, wantTriples("1|id|repeated-id")},
		{"card id bad", func(r *Receipt) { r.Changed[0].ID = "a b" }, wantTriples("0|id|bad-value")},
		{"before state unknown", func(r *Receipt) { r.Changed[0].Before.State = "limbo" }, wantTriples("0|before.state|bad-value")},
		{"count changed", func(r *Receipt) { r.Counts.Changed = 2 }, wantTriples("-1|counts.changed|bad-value")},
		{"count blocked", func(r *Receipt) { r.Counts.Blocked = 0 }, wantTriples("-1|counts.blocked|bad-value")},
		{"count ineligible", func(r *Receipt) { r.Counts.Ineligible = 4 }, wantTriples("-1|counts.ineligible|bad-value")},
		{"count missing", func(r *Receipt) { r.Counts.Missing = 0 }, wantTriples("-1|counts.missing|bad-value")},
		{"selected below the parts", func(r *Receipt) { r.Counts.Selected = 3 }, wantTriples("-1|counts.selected|bad-value")},
		{"eligible over selected", func(r *Receipt) { r.Counts.Eligible = 8 }, wantTriples("-1|counts.eligible|bad-value")},
		{"negative guards", func(r *Receipt) { r.Counts.Guards = -1 }, wantTriples("-1|counts.guards|bad-value")},
		{"blocked reason empty", func(r *Receipt) { r.Blocked[0].Reason = "" }, wantTriples("-1|blocked[0].reason|required")},
		{"blocked reason newline", func(r *Receipt) { r.Blocked[0].Reason = "a\nb" }, wantTriples("-1|blocked[0].reason|control-character")},
		{"ineligible id", func(r *Receipt) { r.Ineligible[0].ID = "" }, wantTriples("-1|ineligible[0].id|required")},
		{"missing id", func(r *Receipt) { r.Missing[0] = "x y" }, wantTriples("-1|missing[0]|bad-value")},
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
		{"transport is unknown never no", func(r *Rejection) { r.Cause, r.Changed = CauseTransport, ChangedNo }, wantTriples("-1|changed|bad-value")},
		{"transport unknown ok", func(r *Rejection) { r.Cause, r.Changed = CauseTransport, ChangedUnknown }, nil},
		{"guard refusal cannot be unknown", func(r *Rejection) { r.Changed = ChangedUnknown }, wantTriples("-1|changed|bad-value")},
		{"changed yes is not a refusal", func(r *Rejection) { r.Changed = "yes" }, wantTriples("-1|changed|bad-value")},
		{"changed empty", func(r *Rejection) { r.Changed = "" }, wantTriples("-1|changed|required")},
		{"operation", func(r *Rejection) { r.Operation = "x" }, wantTriples("-1|operation|bad-value")},
		{"operation id", func(r *Rejection) { r.OperationID = "" }, wantTriples("-1|operation_id|required")},
		{"scope", func(r *Rejection) { r.Scope = "everything" }, wantTriples("-1|scope|bad-value")},
		{"card scope without card", func(r *Rejection) { r.Card = "" }, wantTriples("-1|card|required")},
		{"batch scope with card", func(r *Rejection) { r.Scope = ScopeBatch }, wantTriples("-1|card|not-applicable")},
		{"batch scope ok", func(r *Rejection) { r.Scope, r.Card = ScopeBatch, "" }, nil},
		{"unknown cause", func(r *Rejection) { r.Cause = "because" }, wantTriples("-1|cause|bad-value")},
		{"expected empty", func(r *Rejection) { r.Expected = "" }, wantTriples("-1|expected|required")},
		{"observed too long", func(r *Rejection) { r.Observed = longString(MaxDetailBytes + 1) }, wantTriples("-1|observed|too-long")},
		{"next empty", func(r *Rejection) { r.Next = "" }, wantTriples("-1|next|required")},
		{"next multi-line", func(r *Rejection) { r.Next = "a\nb" }, wantTriples("-1|next|control-character")},
		{"stale epoch batch", func(r *Rejection) { r.Scope, r.Card, r.Cause = ScopeBatch, "", CauseStaleEpoch }, nil},
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

func TestEveryNamedCauseIsAcceptedByARejection(t *testing.T) {
	t.Parallel()
	for c := range knownCauses {
		r := sampleRejection()
		r.Cause = c
		if c == CauseTransport {
			r.Changed = ChangedUnknown
		}
		if err := ValidateRejection(r); err != nil {
			t.Errorf("cause %s: %v", c, err)
		}
	}
	if len(knownCauses) != 31 {
		t.Errorf("the closed set of causes changed: %d", len(knownCauses))
	}
}

func TestReceiptCanonicalIsDeterministicAndDistinguishing(t *testing.T) {
	t.Parallel()
	a, b := sampleReceipt(), sampleReceipt()
	if !bytes.Equal(CanonicalReceipt(a), CanonicalReceipt(b)) || a.Digest() != b.Digest() {
		t.Fatal("equal receipts encode differently")
	}
	base := a.Digest()
	for name, edit := range map[string]func(r *Receipt){
		"hash":      func(r *Receipt) { r.RequestHash = Digest(dig) },
		"epoch":     func(r *Receipt) { r.Epoch = "4" },
		"after":     func(r *Receipt) { r.Changed[0].After.State = Review },
		"before":    func(r *Receipt) { r.Changed[0].Before = nil },
		"reason":    func(r *Receipt) { r.Blocked[0].Reason = "waits on c8" },
		"missing":   func(r *Receipt) { r.Missing = nil },
		"count":     func(r *Receipt) { r.Counts.Guards++ },
		"outcome":   func(r *Receipt) { r.Changed[1].After.Outcome = Cancelled },
		"operation": func(r *Receipt) { r.Operation = OpAdmit },
	} {
		c := sampleReceipt()
		edit(c)
		if c.Digest() == base {
			t.Errorf("editing %s left the receipt digest unchanged", name)
		}
	}
	s := string(CanonicalReceipt(a))
	if !strings.HasPrefix(s, `{"actor":"coordinator","blocked":[{"id":"c4","reason":"waits on c9"}],"changed":[{"after":`) || strings.Contains(s, "\n") {
		t.Fatalf("canonical receipt %s", s)
	}
	// Every list is present even when empty.
	e := &Receipt{}
	if got := string(CanonicalReceipt(e)); !strings.Contains(got, `"blocked":[]`) || !strings.Contains(got, `"changed":[]`) || !strings.Contains(got, `"missing":[]`) {
		t.Fatalf("empty lists absent: %s", got)
	}
	if string(CanonicalReceipt(nil)) != "null" || string(CanonicalRejection(nil)) != "null" {
		t.Fatal("nil encodes as something else")
	}
}

func TestRejectionCanonicalDistinguishes(t *testing.T) {
	t.Parallel()
	a := sampleRejection()
	base := CanonicalRejection(a)
	if !bytes.Equal(base, CanonicalRejection(sampleRejection())) {
		t.Fatal("equal rejections encode differently")
	}
	for name, edit := range map[string]func(r *Rejection){
		"cause":    func(r *Rejection) { r.Cause = CauseStaleEpoch },
		"changed":  func(r *Rejection) { r.Changed = ChangedUnknown },
		"expected": func(r *Rejection) { r.Expected = "revision 9" },
		"card":     func(r *Rejection) { r.Card = "c2" },
		"next":     func(r *Rejection) { r.Next = "other" },
	} {
		c := sampleRejection()
		edit(c)
		if bytes.Equal(CanonicalRejection(c), base) {
			t.Errorf("editing %s left the canonical bytes unchanged", name)
		}
	}
}

func TestLineRenderings(t *testing.T) {
	t.Parallel()
	r := sampleReceipt()
	want := "batch apply_events op=op-17 table=work epoch=3 rev 12->13 actor=coordinator result=changed: selected=7 eligible=3 changed=3 blocked=1 ineligible=1 missing=1 guards=2; c1 ready r2 -> working r3; c2 review r5 -> done/completed r6; c3 new -> waiting r1; blocked c4: waits on c9"
	if got := r.Line(); got != want {
		t.Fatalf("receipt line\n got  %s\n want %s", got, want)
	}
	j := sampleRejection()
	wantJ := "refused apply_events op=op-17 card c1: stale-card-revision; expected revision 2; observed revision 3; changed=no; next: read the card again and send the event with the observed revision"
	if got := j.Line(); got != wantJ {
		t.Fatalf("rejection line\n got  %s\n want %s", got, wantJ)
	}
	batch := &Rejection{Operation: OpAdmit, OperationID: "op-1", Scope: ScopeBatch, Cause: CauseTransport, Expected: "a reply", Observed: "connection reset", Changed: ChangedUnknown, Next: "reconcile operation op-1"}
	if got := batch.Line(); !strings.Contains(got, "batch: transport-failure") || !strings.Contains(got, "changed=unknown") {
		t.Fatalf("batch line %s", got)
	}
	// Whatever a value holds, a line stays one line.
	evil := sampleReceipt()
	evil.Table, evil.Blocked[0].Reason = "a\nb", "x\r\ny"
	evil.Changed = evil.Changed[:0]
	for i := 0; i < 20; i++ {
		evil.Changed = append(evil.Changed, CardChange{ID: ID(fmt.Sprintf("c%d", i)), After: CardState{State: Waiting, Revision: "1"}})
	}
	l := evil.Line()
	if strings.ContainsAny(l, "\r\n") || !strings.Contains(l, "+12 more") {
		t.Fatalf("line %q", l)
	}
	ej := sampleRejection()
	ej.Expected, ej.Next = "a\nb", "c\rd"
	if strings.ContainsAny(ej.Line(), "\r\n") {
		t.Fatal("rejection line breaks")
	}
}

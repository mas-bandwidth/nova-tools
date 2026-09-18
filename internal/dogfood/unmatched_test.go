package dogfood

import (
	"strings"
	"testing"
)

// The gate's blind spot, found by a non-author on 2026-09-18: `record` took any
// --verb and nothing ever checked it against the verb list, so receipts for
// undeclared or misspelled verbs vanished -- and `gate` reported open-edges=0
// at exit 0 with not-ok receipts sitting in the very directory it had just
// read. Evidence thrown away has to be COUNTED on the line, and a not-ok
// receipt that matched nothing has to be a FAILURE: a lane must not be able to
// pass while the only thing anybody found is unreadable.

// stranded is a receipt for a verb the list does not declare.
func stranded(key, by, at string, ok bool, file string) Receipt {
	r := receipt(key, by, at, ok, 0)
	r.File = file
	return r
}

// The count is on the line, in both reads.
func TestUnmatchedReceiptsAreCountedOnTheLine(t *testing.T) {
	list := verbs("nova-check links", "nova-check dogfood ledger")
	got := []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
		stranded("nova-check harvest", "Stella", "2026-09-18T09:05:00Z", true, "receipts/a.json:1"),
		stranded("nova-check ledger", "Stella", "2026-09-18T09:06:00Z", true, "receipts/b.json:1"),
	}
	_, summary := Ledger(list, got, nil)
	if summary.Unmatched != 2 {
		t.Fatalf("unmatched = %d, want 2", summary.Unmatched)
	}
	if !strings.Contains(summary.Line(), "unmatched=2") {
		t.Errorf("the ledger line does not count the evidence it threw away: %s", summary.Line())
	}
	_, gateSummary := Gate(list, got, nil, false)
	if !strings.Contains(gateSummary.GateLine(false), "unmatched=2") {
		t.Errorf("the gate line does not count the evidence it threw away: %s", gateSummary.GateLine(false))
	}
	if !strings.Contains(gateSummary.GateCountLine(1, 1), "unmatched=2") {
		t.Errorf("the gate's red count line does not carry it either: %s", gateSummary.GateCountLine(1, 1))
	}
}

// An unmatched receipt that says NOT OK is a finding, and the finding names the
// file it is in and the verb it claimed.
func TestGateFailsOnAnUnmatchedNotOkReceipt(t *testing.T) {
	list := verbs("nova-check links")
	got := []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
		stranded("nova-merge batch", "Stella", "2026-09-18T09:05:00Z", false, "receipts/b.json:1"),
	}
	findings, summary := Gate(list, got, nil, false)
	if summary.OpenEdges != 0 {
		t.Fatalf("open-edges = %d: an unmatched receipt is not one of the declared verbs' edges", summary.OpenEdges)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: a not-ok receipt nobody can match is the gate's business", len(findings))
	}
	f := findings[0]
	if f.Kind != "unmatched" {
		t.Errorf("kind = %q, want unmatched", f.Kind)
	}
	line := f.Line()
	for _, want := range []string{"receipts/b.json:1", "nova-merge", "batch"} {
		if !strings.Contains(line, want) {
			t.Errorf("the finding must name %q: %s", want, line)
		}
	}
}

// An unmatched receipt that says OK is counted and named, and is not a failure:
// the spelling is wrong or the documentation is stale, and neither is a reason
// to stop a release that nobody found anything wrong with.
func TestAnUnmatchedOkReceiptIsCountedAndNotAFailure(t *testing.T) {
	list := verbs("nova-check links")
	got := []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
		stranded("nova-sandbox probe", "Stella", "2026-09-18T09:05:00Z", true, "receipts/c.json:1"),
	}
	findings, summary := Gate(list, got, nil, false)
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0: %v", len(findings), findings[0].Line())
	}
	if summary.Unmatched != 1 {
		t.Errorf("unmatched = %d, want 1", summary.Unmatched)
	}
}

// The unmatched findings come FIRST: a lane reads what was thrown away before
// it reads anything derived from what was kept.
func TestUnmatchedFindingsComeFirst(t *testing.T) {
	list := verbs("nova-check links")
	got := []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 0),
		stranded("nova-merge queue", "Stella", "2026-09-18T09:05:00Z", false, "receipts/d.json:1"),
	}
	findings, _ := Gate(list, got, nil, false)
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	if findings[0].Kind != "unmatched" {
		t.Errorf("the discarded evidence is said first, got %q then %q", findings[0].Kind, findings[1].Kind)
	}
}

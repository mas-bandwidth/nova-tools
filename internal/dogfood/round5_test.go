package dogfood

import (
	"strings"
	"testing"
)

// DOGFOOD ROUND 5, EDGE 2: ONE DOGFOODER'S PASS CLOSED ANOTHER'S OPEN EDGE.
//
// An edge was closed by "somebody runs the verb again, later, and records neither" --
// anybody, on any run. So on a bench where two people dogfood the same verb, one of them
// finding something and the other happening to run it afterwards and finding nothing put
// the first one's edge out of the gate's sight. Nobody read the note, nobody filed the
// issue, and the ledger row showed `ok=yes` over it.
//
// A finding is a person's. It is closed by a receipt that NAMES it -- `--closes <id>`,
// which anybody may write -- or by the person who found it running the verb again and
// finding nothing. An unrelated pass by a third party closes nothing.

func edgeReceipt(key, by, at, notes string) Receipt {
	tool, verb, _ := strings.Cut(key, " ")
	return Receipt{Tool: tool, Verb: verb, By: by, At: at, OK: false, Notes: notes}
}

func cleanReceipt(key, by, at string) Receipt {
	tool, verb, _ := strings.Cut(key, " ")
	return Receipt{Tool: tool, Verb: verb, By: by, At: at, OK: true, Notes: "real work"}
}

// THE EDGE ITSELF. Stella finds something; Johnny runs the same verb an hour later and it
// works for him. Johnny's pass is not a fix, and it is not an answer to Stella.
func TestAnUnrelatedPassDoesNotCloseSomebodyElsesEdge(t *testing.T) {
	stella := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "the verb refused a relative path")
	johnny := cleanReceipt("nova-check links", "johnny", "2026-09-18T10:00:00Z")

	rows, summary := Ledger(verbs("nova-check links"), []Receipt{stella, johnny}, nil)
	if summary.OpenEdges != 1 {
		t.Fatalf("open-edges=%d; johnny's pass closed stella's edge, which nobody read\n%s", summary.OpenEdges, summary.Line())
	}
	if summary.Unfiled != 1 {
		t.Fatalf("unfiled=%d; the edge has no issue anybody can act on", summary.Unfiled)
	}
	// AND THE ROW SAYS SO. The row shows the receipt that speaks best for the verb --
	// johnny's ok=yes -- which is exactly the line that hid the edge. It now carries the
	// count, so the one line a reader parses can never claim a clean verb over an open
	// finding.
	if !strings.Contains(rows[0].Line(), "open=1") {
		t.Fatalf("the row hides the open edge behind a pass:\n%s", rows[0].Line())
	}

	findings, _ := Gate(verbs("nova-check links"), []Receipt{stella, johnny}, nil, false)
	if len(findings) != 1 {
		t.Fatalf("the gate found %d; an open edge is a finding\n%v", len(findings), findings)
	}
	// The gate names WHOSE edge it is and gives the id a closer must name.
	contains(t, findings[0].Line(), "stella")
	contains(t, findings[0].Line(), "receipt="+stella.ID())
}

// The person who found it, running it again later and finding nothing, closes it. That is
// what "feedback applied" means from inside the finding.
func TestTheDogfooderWhoFoundItClosesItByRunningItAgain(t *testing.T) {
	stella := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "the verb refused a relative path")
	again := cleanReceipt("nova-check links", "stella", "2026-09-18T12:00:00Z")

	_, summary := Ledger(verbs("nova-check links"), []Receipt{stella, again}, nil)
	if summary.OpenEdges != 0 {
		t.Fatalf("open-edges=%d; the dogfooder who found it ran it again and found nothing\n%s", summary.OpenEdges, summary.Line())
	}
}

// And anybody may close it by NAMING it: the fixer records a receipt that says which
// finding this run answers. That is the only way a third party closes somebody's edge.
func TestAReceiptThatNamesTheEdgeClosesIt(t *testing.T) {
	stella := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "the verb refused a relative path")
	fixer := cleanReceipt("nova-check links", "rowan", "2026-09-18T11:00:00Z")
	fixer.Closes = stella.ID()

	_, summary := Ledger(verbs("nova-check links"), []Receipt{stella, fixer}, nil)
	if summary.OpenEdges != 0 {
		t.Fatalf("open-edges=%d; the closing receipt names stella's edge by id\n%s", summary.OpenEdges, summary.Line())
	}
	findings, _ := Gate(verbs("nova-check links"), []Receipt{stella, fixer}, nil, false)
	if len(findings) != 0 {
		t.Fatalf("the gate still says no over a closed edge: %v", findings)
	}
}

// A receipt naming an id nothing carries closes nothing, and does not disappear: a typo in
// a --closes must not be a silent close.
func TestAClosesThatNamesNothingClosesNothing(t *testing.T) {
	stella := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "the verb refused a relative path")
	fixer := cleanReceipt("nova-check links", "rowan", "2026-09-18T11:00:00Z")
	fixer.Closes = "deadbeef"

	_, summary := Ledger(verbs("nova-check links"), []Receipt{stella, fixer}, nil)
	if summary.OpenEdges != 1 {
		t.Fatalf("open-edges=%d; a --closes naming nothing closed a real edge\n%s", summary.OpenEdges, summary.Line())
	}
}

// Two people, two findings, one of them answered: the other is still open and still named.
func TestEachEdgeIsClosedOnItsOwn(t *testing.T) {
	stella := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "the verb refused a relative path")
	johnny := edgeReceipt("nova-check links", "johnny", "2026-09-18T09:30:00Z", "the line named no remedy")
	fixer := cleanReceipt("nova-check links", "rowan", "2026-09-18T11:00:00Z")
	fixer.Closes = stella.ID()

	rows, summary := Ledger(verbs("nova-check links"), []Receipt{stella, johnny, fixer}, nil)
	if summary.OpenEdges != 1 {
		t.Fatalf("open-edges=%d, want johnny's alone\n%s", summary.OpenEdges, summary.Line())
	}
	if !strings.Contains(rows[0].Line(), "open=1") {
		t.Fatalf("row: %s", rows[0].Line())
	}
	findings, _ := Gate(verbs("nova-check links"), []Receipt{stella, johnny, fixer}, nil, false)
	if len(findings) != 1 {
		t.Fatalf("findings %d, want johnny's alone: %v", len(findings), findings)
	}
	contains(t, findings[0].Line(), "johnny")
}

// An id is a fact of the receipt's content: two reads of one receipt give one id, and two
// different receipts do not collide.
func TestAReceiptsIDIsItsContent(t *testing.T) {
	a := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "one")
	b := a
	b.File = "somewhere/else.json:3" // where it was read from is not part of it
	if a.ID() != b.ID() {
		t.Fatalf("the same receipt read from two places has two ids: %s and %s", a.ID(), b.ID())
	}
	c := edgeReceipt("nova-check links", "stella", "2026-09-18T09:00:00Z", "two")
	if a.ID() == c.ID() {
		t.Fatalf("two different findings share one id: %s", a.ID())
	}
	if len(a.ID()) != 8 {
		t.Fatalf("an id a person has to type is short: %q", a.ID())
	}
}

// A verb with nothing open says so, rather than leaving the field off: a field that
// appears only when it is interesting is a field nobody can parse.
func TestTheRowAlwaysCarriesTheOpenCount(t *testing.T) {
	rows, _ := Ledger(verbs("nova-check links"), nil, nil)
	want := "DOGFOOD tool=nova-check verb=links by=nobody at=- ok=- issue=- open=0"
	if rows[0].Line() != want {
		t.Fatalf("row:\n got %q\nwant %q", rows[0].Line(), want)
	}
}

func contains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("want %q in:\n%s", needle, haystack)
	}
}

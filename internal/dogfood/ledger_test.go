package dogfood

import (
	"strings"
	"testing"
)

func verbs(keys ...string) []Verb {
	out := make([]Verb, 0, len(keys))
	for i, k := range keys {
		tool, verb, _ := strings.Cut(k, " ")
		out = append(out, Verb{Tool: tool, Verb: verb, Line: i + 1})
	}
	return out
}

func receipt(key, by, at string, ok bool, issue int) Receipt {
	tool, verb, _ := strings.Cut(key, " ")
	return Receipt{Tool: tool, Verb: verb, By: by, At: at, OK: ok, Notes: "real work", Issue: issue}
}

func TestLedgerSaysNobodyForAVerbNoOneHasRun(t *testing.T) {
	rows, summary := Ledger(verbs("nova-check links"), nil, nil)
	if len(rows) != 1 {
		t.Fatalf("rows %d, want one per verb", len(rows))
	}
	want := "DOGFOOD tool=nova-check verb=links by=nobody at=- ok=- issue=- open=0"
	if rows[0].Line() != want {
		t.Fatalf("row:\n got %q\nwant %q", rows[0].Line(), want)
	}
	if summary.Line() != "DOGFOOD OK verbs=1 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0" {
		t.Fatalf("summary %q", summary.Line())
	}
}

// The whole point of the ledger: the author's own pass is not evidence.
func TestAnAuthorDogfoodingTheirOwnVerbDoesNotCount(t *testing.T) {
	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	rows, summary := Ledger(
		verbs("nova-check links"),
		[]Receipt{receipt("nova-check links", "rowan", "2026-09-18T09:00:00Z", true, 0)},
		authors,
	)
	if summary.Dogfooded != 1 {
		t.Fatalf("dogfooded=%d, want 1: somebody did run it", summary.Dogfooded)
	}
	if summary.ByNonAuthor != 0 {
		t.Fatalf("by-nonauthor=%d, want 0: the author ran their own verb", summary.ByNonAuthor)
	}
	if rows[0].NonAuthor {
		t.Fatal("the author's receipt was read as a non-author's; names compare case-insensitively")
	}
}

func TestAVerbWithNoKnownAuthorCountsEveryReceipt(t *testing.T) {
	_, summary := Ledger(
		verbs("nova-check links"),
		[]Receipt{receipt("nova-check links", "Rowan", "2026-09-18T09:00:00Z", true, 0)},
		Authors{},
	)
	if summary.ByNonAuthor != 1 {
		t.Fatalf("by-nonauthor=%d, want 1: an unknown author is not a reason to hold a verb back", summary.ByNonAuthor)
	}
}

func TestTheRowShowsTheReceiptThatSpeaksBestForTheVerb(t *testing.T) {
	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	rows, _ := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Rowan", "2026-09-18T12:00:00Z", true, 0), // newest, but the author's
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, authors)
	if rows[0].By != "Stella" {
		t.Fatalf("row shows by=%s, want the non-author's pass", rows[0].By)
	}
	if rows[0].OK != "yes" || rows[0].At != "2026-09-18T09:00:00Z" {
		t.Fatalf("row %q", rows[0].Line())
	}
}

func TestTheRowCarriesTheIssueWhenAnEdgeWasFiled(t *testing.T) {
	rows, summary := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
	}, nil)
	want := "DOGFOOD tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=no issue=1301 open=1"
	if rows[0].Line() != want {
		t.Fatalf("row:\n got %q\nwant %q", rows[0].Line(), want)
	}
	if summary.OpenEdges != 1 {
		t.Fatalf("open-edges=%d, want 1", summary.OpenEdges)
	}
	if summary.ByNonAuthor != 0 {
		t.Fatalf("by-nonauthor=%d: a run that did not work is not a pass", summary.ByNonAuthor)
	}
}

// Feedback filed is not feedback applied: the edge stays open until it is
// ANSWERED — by the person who found it running the verb again, or by a receipt
// that names it.
//
// DOGFOOD ROUND 5, EDGE 2 CHANGED THE CLOSER. This test used to close Stella's
// edge with EMMA's later pass, and that is the hole: on a bench where two people
// dogfood the same verb, the second one happening to find nothing put the first
// one's finding out of the gate's sight, unread and unfiled. A pass is evidence
// about the passer's run, not an answer to somebody else's.
func TestALaterPassClosesAnEdgeAndAnEarlierOneDoesNot(t *testing.T) {
	_, closed := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
		receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", true, 0),
	}, nil)
	if closed.OpenEdges != 0 {
		t.Fatalf("open-edges=%d after the finder ran it again and it worked, want 0", closed.OpenEdges)
	}
	_, stillOpen := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
		receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", false, 1301),
	}, nil)
	if stillOpen.OpenEdges != 1 {
		t.Fatalf("open-edges=%d after an edge filed later than the pass, want 1", stillOpen.OpenEdges)
	}
	// The same pair with two different people leaves it open, which is the whole
	// of the edge.
	_, other := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
		receipt("nova-check links", "Emma", "2026-09-18T11:00:00Z", true, 0),
	}, nil)
	if other.OpenEdges != 1 {
		t.Fatalf("open-edges=%d; Emma's pass closed Stella's finding, which nobody read", other.OpenEdges)
	}
}

func TestLedgerCountsAReceiptForAVerbTheReferenceDoesNotDeclare(t *testing.T) {
	rows, summary := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check ghost", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, nil)
	if len(rows) != 1 || rows[0].By != "nobody" {
		t.Fatalf("a receipt for an undeclared verb landed on a row: %q", rows[0].Line())
	}
	if summary.Unmatched != 1 {
		t.Fatalf("unmatched=%d, want 1; docs drift is a finding, not a silent drop", summary.Unmatched)
	}
}

func TestRowsComeBackInTheReferencesOrder(t *testing.T) {
	rows, _ := Ledger(verbs("nova-check quickstart", "nova-check links", "nova-bus send"), nil, nil)
	got := []string{rows[0].Verb, rows[1].Verb, rows[2].Verb}
	want := []string{"quickstart", "links", "send"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

func TestGateSaysNoOnAnOpenEdgeWithoutRequireAll(t *testing.T) {
	findings, _ := Gate(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
	}, nil, false)
	if len(findings) != 1 {
		t.Fatalf("findings %+v, want the open edge", findings)
	}
	if findings[0].Kind != "open-edge" {
		t.Fatalf("kind %q, want open-edge", findings[0].Kind)
	}
	if !strings.Contains(findings[0].Line(), "#1301") {
		t.Fatalf("the finding does not name the issue: %q", findings[0].Line())
	}
}

func TestGateRequireAllListsEveryVerbNoNonAuthorHasRun(t *testing.T) {
	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	authors.Set("nova-check nocode", "Rowan")
	findings, summary := Gate(
		verbs("nova-check links", "nova-check nocode", "nova-check corpus"),
		[]Receipt{
			receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0), // counts
			receipt("nova-check nocode", "Rowan", "2026-09-18T09:00:00Z", true, 0), // the author's own
		},
		authors, true,
	)
	var missing []string
	for _, f := range findings {
		if f.Kind == "not-dogfooded" {
			missing = append(missing, f.Tool+" "+f.Verb)
		}
	}
	want := "nova-check nocode|nova-check corpus"
	if strings.Join(missing, "|") != want {
		t.Fatalf("missing %q, want %q", strings.Join(missing, "|"), want)
	}
	if summary.ByNonAuthor != 1 {
		t.Fatalf("by-nonauthor=%d, want 1", summary.ByNonAuthor)
	}
}

func TestGateIsGreenWhenEveryVerbHasANonAuthorsPass(t *testing.T) {
	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	findings, summary := Gate(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, authors, true)
	if len(findings) != 0 {
		t.Fatalf("findings %+v, want none", findings)
	}
	if summary.ByNonAuthor != 1 || summary.OpenEdges != 0 {
		t.Fatalf("summary %q", summary.Line())
	}
}

// Edge 5 of the 2026-09-18 dogfood pass: `open-edges=0` on a bench whose
// receipts were full of edges. Every one of them had been written with --ok,
// because the verb did work, and the edges were in the notes where the family
// writes them — "Edges: (1) … (2) …" — with no issue filed. A ledger that
// counts only ok=false says a clean number about a bench that found six things.
func TestAnEdgeNamedInTheNotesIsAnOpenEdge(t *testing.T) {
	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "Ran it over the lane's own docs. Edges: (1) it reads only --dir, so one file cannot be checked alone."
	rows, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	if summary.OpenEdges != 1 {
		t.Fatalf("open-edges=%d, want 1: the notes name an edge", summary.OpenEdges)
	}
	if summary.Unfiled != 1 {
		t.Fatalf("unfiled=%d, want 1: nobody can act on an edge with no issue", summary.Unfiled)
	}
	if rows[0].OK != "yes" {
		t.Fatalf("the verdict was rewritten: %q", rows[0].Line())
	}
	if !strings.Contains(summary.Line(), "open-edges=1 unfiled=1") {
		t.Fatalf("the summary hides the unfiled edges: %q", summary.Line())
	}
}

func TestAnEdgeWithAnIssueIsOpenButFiled(t *testing.T) {
	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 1301)
	r.Notes = "worked; Edge: the refusal names no remedy"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	if summary.OpenEdges != 1 || summary.Unfiled != 0 {
		t.Fatalf("summary %q, want one open edge, filed", summary.Line())
	}
}

func TestNotesThatMerelyUseTheWordEdgeAreNotAnEdge(t *testing.T) {
	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "ran it on the edge of the release; nothing to report"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	if summary.OpenEdges != 0 {
		t.Fatalf("open-edges=%d: the marker is `Edge:` or `Edges:`, not the word", summary.OpenEdges)
	}
}

// An edge named only in the notes closes the same way any other does: the
// person who wrote it runs the verb again and writes nothing (round 5, edge 2 —
// this used to be Emma's clean run closing Stella's note).
func TestALaterCleanRunClosesAnEdgeNamedInTheNotes(t *testing.T) {
	first := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	first.Notes = "Edges: (1) the refusal names no remedy"
	later := receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", true, 0)
	later.Notes = "ran it again on the same tree; clean"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{first, later}, nil)
	if summary.OpenEdges != 0 {
		t.Fatalf("open-edges=%d after the finder's own later clean run, want 0", summary.OpenEdges)
	}
	// And a third party's clean run does not, however late it is.
	byOther := receipt("nova-check links", "Emma", "2026-09-18T12:00:00Z", true, 0)
	byOther.Notes = "ran it again on the same tree; clean"
	_, stillOpen := Ledger(verbs("nova-check links"), []Receipt{first, byOther}, nil)
	if stillOpen.OpenEdges != 1 {
		t.Fatalf("open-edges=%d; somebody else's clean run closed Stella's note", stillOpen.OpenEdges)
	}
}

func TestGateSaysNoToAnEdgeNamedOnlyInTheNotes(t *testing.T) {
	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "Edge: the refusal names no remedy"
	findings, _ := Gate(verbs("nova-check links"), []Receipt{r}, nil, false)
	if len(findings) != 1 || findings[0].Kind != "open-edge" {
		t.Fatalf("findings %+v, want the open edge", findings)
	}
	if !strings.Contains(findings[0].Line(), "no issue filed") {
		t.Fatalf("the finding does not say the edge was never filed: %q", findings[0].Line())
	}
}

// Edge 3: the NOTE said nine receipts matched nothing and named none of them,
// so nobody could tell which receipt was stranded or how it should have been
// spelled.
func TestStrandedReceiptsAreNamedOneByOneWithTheNearestVerb(t *testing.T) {
	declared := verbs("nova-check dogfood ledger", "nova-check links")
	got := []Receipt{
		receipt("nova-check ledger", "Stella", "2026-09-18T09:00:00Z", true, 0),
		receipt("nova-check links", "Emma", "2026-09-18T09:00:00Z", true, 0),
	}
	got[0].File = "/receipts/a.json"
	strands := Stranded(declared, got)
	if len(strands) != 1 {
		t.Fatalf("stranded %+v, want the one receipt naming no declared verb", strands)
	}
	if strands[0].File != "/receipts/a.json" {
		t.Fatalf("the strand does not name its file: %+v", strands[0])
	}
	if strands[0].Nearest != "nova-check dogfood ledger" {
		t.Fatalf("nearest = %q, want the verb it was probably meant to be", strands[0].Nearest)
	}
	line := strands[0].Line()
	for _, want := range []string{"/receipts/a.json", "tool=nova-check", "verb=ledger", "nova-check dogfood ledger"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the strand line %q is missing %q", line, want)
		}
	}
}

func TestNearestPrefersTheSameTool(t *testing.T) {
	declared := verbs("nova-bus check", "nova-check nocode")
	if got := Nearest(declared, "nova-check", "nocdoe"); got != "nova-check nocode" {
		t.Fatalf("nearest = %q, want nova-check nocode", got)
	}
	// Nothing close enough is no guess at all: a remedy that named a verb from
	// a different tool would send a reader further away than silence.
	if got := Nearest(declared, "nova-elsewhere", "wildly-different-verb"); got != "" {
		t.Fatalf("nearest = %q, want no guess", got)
	}
}

func TestTheBareInvocationPrintsAsADash(t *testing.T) {
	rows, _ := Ledger([]Verb{{Tool: "nova-decide", Verb: "", Line: 1}}, nil, nil)
	if !strings.Contains(rows[0].Line(), "verb=- ") {
		t.Fatalf("a tool with no verb prints as %q; the bare invocation is a unit like any other", rows[0].Line())
	}
}

func TestAReceiptCanNameTheBareInvocation(t *testing.T) {
	declared := []Verb{{Tool: "nova-decide", Verb: "", Line: 1}}
	r := Receipt{Tool: "nova-decide", Verb: "-", By: "Stella", At: "2026-09-18T09:00:00Z", OK: true, Notes: "one real decision"}
	_, summary := Ledger(declared, []Receipt{r}, nil)
	if summary.Dogfooded != 1 {
		t.Fatalf("a receipt for the bare invocation was stranded: %q", summary.Line())
	}
}

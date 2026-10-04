package dogfood

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	rows, summary := Ledger(verbs("nova-check links"), nil, nil)
	require.Len(t, rows, 1, "rows %d, want one per verb", len(rows))
	want := "DOGFOOD tool=nova-check verb=links by=nobody at=- ok=- issue=- open=0"
	require.Equal(t, want, rows[0].Line(), "row:\n got %q\nwant %q", rows[0].Line(), want)
	require.Equal(t, "DOGFOOD OK verbs=1 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0", summary.Line(), "summary %q", summary.Line())
}

// The whole point of the ledger: the author's own pass is not evidence.
func TestAnAuthorDogfoodingTheirOwnVerbDoesNotCount(t *testing.T) {
	t.Parallel()

	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	rows, summary := Ledger(
		verbs("nova-check links"),
		[]Receipt{receipt("nova-check links", "rowan", "2026-09-18T09:00:00Z", true, 0)},
		authors,
	)
	require.Equal(t, 1, summary.Dogfooded, "dogfooded=%d, want 1: somebody did run it", summary.Dogfooded)
	require.Equal(t, 0, summary.ByNonAuthor, "by-nonauthor=%d, want 0: the author ran their own verb", summary.ByNonAuthor)
	require.False(t, rows[0].NonAuthor, "the author's receipt was read as a non-author's; names compare case-insensitively")
}

func TestAVerbWithNoKnownAuthorCountsEveryReceipt(t *testing.T) {
	t.Parallel()

	_, summary := Ledger(
		verbs("nova-check links"),
		[]Receipt{receipt("nova-check links", "Rowan", "2026-09-18T09:00:00Z", true, 0)},
		Authors{},
	)
	require.Equal(t, 1, summary.ByNonAuthor, "by-nonauthor=%d, want 1: an unknown author is not a reason to hold a verb back", summary.ByNonAuthor)
}

func TestTheRowShowsTheReceiptThatSpeaksBestForTheVerb(t *testing.T) {
	t.Parallel()

	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	rows, _ := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Rowan", "2026-09-18T12:00:00Z", true, 0), // newest, but the author's
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, authors)
	require.Equal(t, "Stella", rows[0].By, "row shows by=%s, want the non-author's pass", rows[0].By)
	require.True(t, rows[0].OK == "yes" && rows[0].At == "2026-09-18T09:00:00Z", "row %q", rows[0].Line())
}

func TestTheRowCarriesTheIssueWhenAnEdgeWasFiled(t *testing.T) {
	t.Parallel()

	rows, summary := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
	}, nil)
	want := "DOGFOOD tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=no issue=1301 open=1"
	require.Equal(t, want, rows[0].Line(), "row:\n got %q\nwant %q", rows[0].Line(), want)
	require.Equal(t, 1, summary.OpenEdges, "open-edges=%d, want 1", summary.OpenEdges)
	require.Equal(t, 0, summary.ByNonAuthor, "by-nonauthor=%d: a run that did not work is not a pass", summary.ByNonAuthor)
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
	t.Parallel()

	_, closed := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
		receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", true, 0),
	}, nil)
	require.Equal(t, 0, closed.OpenEdges, "open-edges=%d after the finder ran it again and it worked, want 0", closed.OpenEdges)
	_, stillOpen := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
		receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", false, 1301),
	}, nil)
	require.Equal(t, 1, stillOpen.OpenEdges, "open-edges=%d after an edge filed later than the pass, want 1", stillOpen.OpenEdges)
	// The same pair with two different people leaves it open, which is the whole
	// of the edge.
	_, other := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
		receipt("nova-check links", "Emma", "2026-09-18T11:00:00Z", true, 0),
	}, nil)
	require.Equal(t, 1, other.OpenEdges, "open-edges=%d; Emma's pass closed Stella's finding, which nobody read", other.OpenEdges)
}

func TestLedgerCountsAReceiptForAVerbTheReferenceDoesNotDeclare(t *testing.T) {
	t.Parallel()

	rows, summary := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check ghost", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, nil)
	require.True(t, len(rows) == 1 && rows[0].By == "nobody", "a receipt for an undeclared verb landed on a row: %q", rows[0].Line())
	require.Equal(t, 1, summary.Unmatched, "unmatched=%d, want 1; docs drift is a finding, not a silent drop", summary.Unmatched)
}

func TestRowsComeBackInTheReferencesOrder(t *testing.T) {
	t.Parallel()

	rows, _ := Ledger(verbs("nova-check quickstart", "nova-check links", "nova-bus send"), nil, nil)
	got := []string{rows[0].Verb, rows[1].Verb, rows[2].Verb}
	want := []string{"quickstart", "links", "send"}
	require.Equal(t, want, got, "order %v, want %v", got, want)
}

func TestGateSaysNoOnAnOpenEdgeWithoutRequireAll(t *testing.T) {
	t.Parallel()

	findings, _ := Gate(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
	}, nil, false)
	require.Len(t, findings, 1, "findings %+v, want the open edge", findings)
	require.Equal(t, "open-edge", findings[0].Kind, "kind %q, want open-edge", findings[0].Kind)
	require.Contains(t, findings[0].Line(), "#1301", "the finding does not name the issue: %q", findings[0].Line())
}

func TestGateRequireAllListsEveryVerbNoNonAuthorHasRun(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, want, strings.Join(missing, "|"), "missing %q, want %q", strings.Join(missing, "|"), want)
	require.Equal(t, 1, summary.ByNonAuthor, "by-nonauthor=%d, want 1", summary.ByNonAuthor)
}

func TestGateIsGreenWhenEveryVerbHasANonAuthorsPass(t *testing.T) {
	t.Parallel()

	authors := Authors{}
	authors.Set("nova-check links", "Rowan")
	findings, summary := Gate(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, authors, true)
	require.Empty(t, findings, "findings %+v, want none", findings)
	require.True(t, summary.ByNonAuthor == 1 && summary.OpenEdges == 0, "summary %q", summary.Line())
}

// Edge 5 of the 2026-09-18 dogfood pass: `open-edges=0` on a bench whose
// receipts were full of edges. Every one of them had been written with --ok,
// because the verb did work, and the edges were in the notes where the family
// writes them — "Edges: (1) … (2) …" — with no issue filed. A ledger that
// counts only ok=false says a clean number about a bench that found six things.
func TestAnEdgeNamedInTheNotesIsAnOpenEdge(t *testing.T) {
	t.Parallel()

	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "Ran it over the lane's own docs. Edges: (1) it reads only --dir, so one file cannot be checked alone."
	rows, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	require.Equal(t, 1, summary.OpenEdges, "open-edges=%d, want 1: the notes name an edge", summary.OpenEdges)
	require.Equal(t, 1, summary.Unfiled, "unfiled=%d, want 1: nobody can act on an edge with no issue", summary.Unfiled)
	require.Equal(t, "yes", rows[0].OK, "the verdict was rewritten: %q", rows[0].Line())
	require.Contains(t, summary.Line(), "open-edges=1 unfiled=1", "the summary hides the unfiled edges: %q", summary.Line())
}

func TestAnEdgeWithAnIssueIsOpenButFiled(t *testing.T) {
	t.Parallel()

	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 1301)
	r.Notes = "worked; Edge: the refusal names no remedy"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	require.True(t, summary.OpenEdges == 1 && summary.Unfiled == 0, "summary %q, want one open edge, filed", summary.Line())
}

func TestNotesThatMerelyUseTheWordEdgeAreNotAnEdge(t *testing.T) {
	t.Parallel()

	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "ran it on the edge of the release; nothing to report"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{r}, nil)
	require.Equal(t, 0, summary.OpenEdges, "open-edges=%d: the marker is `Edge:` or `Edges:`, not the word", summary.OpenEdges)
}

// An edge named only in the notes closes the same way any other does: the
// person who wrote it runs the verb again and writes nothing (round 5, edge 2 —
// this used to be Emma's clean run closing Stella's note).
func TestALaterCleanRunClosesAnEdgeNamedInTheNotes(t *testing.T) {
	t.Parallel()

	first := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	first.Notes = "Edges: (1) the refusal names no remedy"
	later := receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", true, 0)
	later.Notes = "ran it again on the same tree; clean"
	_, summary := Ledger(verbs("nova-check links"), []Receipt{first, later}, nil)
	require.Equal(t, 0, summary.OpenEdges, "open-edges=%d after the finder's own later clean run, want 0", summary.OpenEdges)
	// And a third party's clean run does not, however late it is.
	byOther := receipt("nova-check links", "Emma", "2026-09-18T12:00:00Z", true, 0)
	byOther.Notes = "ran it again on the same tree; clean"
	_, stillOpen := Ledger(verbs("nova-check links"), []Receipt{first, byOther}, nil)
	require.Equal(t, 1, stillOpen.OpenEdges, "open-edges=%d; somebody else's clean run closed Stella's note", stillOpen.OpenEdges)
}

func TestGateSaysNoToAnEdgeNamedOnlyInTheNotes(t *testing.T) {
	t.Parallel()

	r := receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", true, 0)
	r.Notes = "Edge: the refusal names no remedy"
	findings, _ := Gate(verbs("nova-check links"), []Receipt{r}, nil, false)
	require.True(t, len(findings) == 1 && findings[0].Kind == "open-edge", "findings %+v, want the open edge", findings)
	require.Contains(t, findings[0].Line(), "no issue filed", "the finding does not say the edge was never filed: %q", findings[0].Line())
}

// Edge 3: the NOTE said nine receipts matched nothing and named none of them,
// so nobody could tell which receipt was stranded or how it should have been
// spelled.
func TestStrandedReceiptsAreNamedOneByOneWithTheNearestVerb(t *testing.T) {
	t.Parallel()

	declared := verbs("nova-check dogfood ledger", "nova-check links")
	got := []Receipt{
		receipt("nova-check ledger", "Stella", "2026-09-18T09:00:00Z", true, 0),
		receipt("nova-check links", "Emma", "2026-09-18T09:00:00Z", true, 0),
	}
	got[0].File = "/receipts/a.json"
	strands := Stranded(declared, got)
	require.Len(t, strands, 1, "stranded %+v, want the one receipt naming no declared verb", strands)
	require.Equal(t, "/receipts/a.json", strands[0].File, "the strand does not name its file: %+v", strands[0])
	require.Equal(t, "nova-check dogfood ledger", strands[0].Nearest, "nearest = %q, want the verb it was probably meant to be", strands[0].Nearest)
	line := strands[0].Line()
	for _, want := range []string{"/receipts/a.json", "tool=nova-check", "verb=ledger", "nova-check dogfood ledger"} {
		require.Contains(t, line, want, "the strand line %q is missing %q", line, want)
	}
}

func TestNearestPrefersTheSameTool(t *testing.T) {
	t.Parallel()

	declared := verbs("nova-bus check", "nova-check nocode")
	got := Nearest(declared, "nova-check", "nocdoe")
	require.Equal(t, "nova-check nocode", got, "nearest = %q, want nova-check nocode", got)
	// Nothing close enough is no guess at all: a remedy that named a verb from
	// a different tool would send a reader further away than silence.
	got = Nearest(declared, "nova-elsewhere", "wildly-different-verb")
	require.Empty(t, got, "nearest = %q, want no guess", got)
}

// TestANameForTheBareFormSuggestsTheDash is edge 7225d4da: a receipt for
// nova-sandbox's verbless run, spelled `(default)`, was told `did you mean:
// nova-sandbox reap`, and the reader learned the dash only from a later
// refusal. A name for the bare form is answered with the bare form, spelled
// the way --verb takes it; a tool that declares no bare form keeps the
// ordinary answer, and a real near miss is still a near miss.
func TestANameForTheBareFormSuggestsTheDash(t *testing.T) {
	t.Parallel()

	declared := verbs("nova-sandbox", "nova-sandbox reap", "nova-check nocode")
	for _, spelled := range []string{"(default)", "default", "bare", "(none)", "no verb", "[bare form]", " Default "} {
		got := Nearest(declared, "nova-sandbox", spelled)
		assert.Equal(t, "nova-sandbox --verb -", got, "nearest for %q = %q, want nova-sandbox --verb -", spelled, got)
	}
	got := Nearest(declared, "nova-sandbox", "raep")
	assert.Equal(t, "nova-sandbox reap", got, "nearest for a typo = %q, want nova-sandbox reap", got)
	got = Nearest(declared, "nova-check", "(default)")
	assert.NotEqual(t, "nova-check --verb -", got, "a tool with no bare form was offered one: %q", got)
}

func TestTheBareInvocationPrintsAsADash(t *testing.T) {
	t.Parallel()

	rows, _ := Ledger([]Verb{{Tool: "nova-decide", Verb: "", Line: 1}}, nil, nil)
	require.Contains(t, rows[0].Line(), "verb=- ", "a tool with no verb prints as %q; the bare invocation is a unit like any other", rows[0].Line())
}

func TestAReceiptCanNameTheBareInvocation(t *testing.T) {
	t.Parallel()

	declared := []Verb{{Tool: "nova-decide", Verb: "", Line: 1}}
	r := Receipt{Tool: "nova-decide", Verb: "-", By: "Stella", At: "2026-09-18T09:00:00Z", OK: true, Notes: "one real decision"}
	_, summary := Ledger(declared, []Receipt{r}, nil)
	require.Equal(t, 1, summary.Dogfooded, "a receipt for the bare invocation was stranded: %q", summary.Line())
}

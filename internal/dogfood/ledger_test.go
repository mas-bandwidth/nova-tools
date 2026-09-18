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
	want := "DOGFOOD tool=nova-check verb=links by=nobody at=- ok=- issue=-"
	if rows[0].Line() != want {
		t.Fatalf("row:\n got %q\nwant %q", rows[0].Line(), want)
	}
	if summary.Line() != "DOGFOOD OK verbs=1 dogfooded=0 by-nonauthor=0 open-edges=0" {
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
	want := "DOGFOOD tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=no issue=1301"
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

// Feedback filed is not feedback applied: the edge stays open until somebody
// runs the verb again and it does what they needed.
func TestALaterPassClosesAnEdgeAndAnEarlierOneDoesNot(t *testing.T) {
	_, closed := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Stella", "2026-09-18T09:00:00Z", false, 1301),
		receipt("nova-check links", "Emma", "2026-09-18T11:00:00Z", true, 0),
	}, nil)
	if closed.OpenEdges != 0 {
		t.Fatalf("open-edges=%d after a later pass, want 0", closed.OpenEdges)
	}
	_, stillOpen := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check links", "Emma", "2026-09-18T09:00:00Z", true, 0),
		receipt("nova-check links", "Stella", "2026-09-18T11:00:00Z", false, 1301),
	}, nil)
	if stillOpen.OpenEdges != 1 {
		t.Fatalf("open-edges=%d after an edge filed later than the pass, want 1", stillOpen.OpenEdges)
	}
}

func TestLedgerCountsAReceiptForAVerbTheReferenceDoesNotDeclare(t *testing.T) {
	rows, summary := Ledger(verbs("nova-check links"), []Receipt{
		receipt("nova-check ghost", "Stella", "2026-09-18T09:00:00Z", true, 0),
	}, nil)
	if len(rows) != 1 || rows[0].By != "nobody" {
		t.Fatalf("a receipt for an undeclared verb landed on a row: %q", rows[0].Line())
	}
	if summary.Unknown != 1 {
		t.Fatalf("unknown=%d, want 1; docs drift is a finding, not a silent drop", summary.Unknown)
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

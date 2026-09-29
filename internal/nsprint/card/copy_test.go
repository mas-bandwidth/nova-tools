package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCopyCardCarriesTheReviewLine (#4072): a copy cut after a review
// verdict carries the REVIEW line, and its card says why it is back.
func TestCopyCardCarriesTheReviewLine(t *testing.T) {
	t.Parallel()
	rec := map[string]string{"primary": "p1", "leg": "work", "kind": "build", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": strings.Repeat("ab", 20), "paths": "internal/x.go", "done_when": "go test ./internal/x passes",
		"title": "one verb", "review": "REVIEW verdict=recut by=rowan: PATHS too narrow: add internal/y"}
	body, err := card.RenderCopy(card.CopyCardFrom("p1~2", rec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "\nAn earlier copy of this card failed and went to review; the verdict:\n"+
		"  REVIEW verdict=recut by=rowan: PATHS too narrow: add internal/y\n") {
		t.Fatalf("card:\n%s", body)
	}
	delete(rec, "review")
	if body, _ := card.RenderCopy(card.CopyCardFrom("p1~1", rec)); strings.Contains(string(body), "review") {
		t.Fatalf("a card that never failed names a review:\n%s", body)
	}
}

// TestRenderCopyKeepsTheFrontierRoute: the copy's card carries the
// primary's route when it is one of the three model types (frontier, pro,
// flash); a word that is none of them is flash; a read is pro whatever the
// primary's route.
func TestRenderCopyKeepsTheFrontierRoute(t *testing.T) {
	t.Parallel()
	base := card.CopyCard{ID: "p1~1", Primary: "p1", Leg: "work", Kind: "build", Repo: "mas-bandwidth/nova-tools",
		Base: "dev", BaseSHA: strings.Repeat("ab", 20), Paths: "internal/x.go", DoneWhen: "TestX passes",
		Title: "one verb", Origin: "issue:nova-tools#4300", Stream: "swarm: cards", Consumer: "bench:b", Body: "the issue"}
	for _, tc := range []struct{ leg, route, want string }{
		{"work", card.RouteFrontier, "frontier"}, {"work", card.RoutePro, "pro"}, {"work", card.RouteFlash, "flash"},
		{"work", "turbo", "flash"}, {"work", "", "flash"}, {"read", card.RouteFrontier, "pro"},
	} {
		cc := base
		cc.Leg, cc.Route = tc.leg, tc.route
		if tc.leg == "read" {
			cc.PR, cc.Head = "4300", strings.Repeat("cd", 20)
		}
		body, err := card.RenderCopy(cc)
		if err != nil {
			t.Fatalf("%s %q: %v", tc.leg, tc.route, err)
		}
		if !strings.Contains(string(body), "\nROUTE: "+tc.want+"\n") {
			t.Fatalf("%s %q: no ROUTE: %s line:\n%s", tc.leg, tc.route, tc.want, body)
		}
	}
}

// TestRenderCopyCarriesMergeNotes (nova-tools #4324): the MERGE-NOTE lines
// the caller loaded for the copy's stream and the sprint are a block at the
// end of the card; a copy with none has no block.
func TestRenderCopyCarriesMergeNotes(t *testing.T) {
	t.Parallel()
	c := card.CopyCard{ID: "p1~2", Primary: "p1", Leg: "work", Kind: "build", Repo: "mas-bandwidth/nova-tools",
		Base: "dev", BaseSHA: strings.Repeat("ab", 20), Paths: "internal/x.go", DoneWhen: "TestX passes",
		Title: "one verb", Origin: "issue:nova-tools#4300", Stream: "swarm: cards", Consumer: "bench:b", Body: "the issue"}
	body, err := card.RenderCopy(c)
	if err != nil || strings.Contains(string(body), card.NotesHeading) {
		t.Fatalf("no notes: %v\n%s", err, body)
	}
	c.Notes = []string{"MERGE-NOTE by=merge-swarm-cards-1 at=1 taskcard.Opts.Fields is name then value", "  ", "MERGE-NOTE by=rowan at=2 no bash"}
	body, err = card.RenderCopy(c)
	if err != nil {
		t.Fatal(err)
	}
	want := "\n" + card.NotesHeading + "\n  MERGE-NOTE by=merge-swarm-cards-1 at=1 taskcard.Opts.Fields is name then value\n  MERGE-NOTE by=rowan at=2 no bash\n"
	if !strings.HasSuffix(string(body), want) {
		t.Fatalf("notes block:\n%s", body)
	}
}

// TestCopyCardCarriesTheTestLine (#4313): a work or fix copy's card carries
// the primary's TEST line, the class test the wrapper runs or the why the
// card has none, read from the record's test field; a read's does not.
func TestCopyCardCarriesTheTestLine(t *testing.T) {
	t.Parallel()
	rec := map[string]string{"primary": "p1", "leg": "work", "kind": "build", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": strings.Repeat("ab", 20), "paths": "internal/x.go", "done_when": "TestX passes",
		"title": "one verb", "test": "./internal/x TestX"}
	c := card.CopyCardFrom("p1~1", rec)
	if c.Test != "./internal/x TestX" {
		t.Fatalf("Test = %q", c.Test)
	}
	body, err := card.RenderCopy(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nTEST: ./internal/x TestX\n") {
		t.Fatalf("no TEST line:\n%s", body)
	}
	rec["test"] = "none one docs page; the reader checks it"
	body, _ = card.RenderCopy(card.CopyCardFrom("p1~1", rec))
	if !strings.Contains(string(body), "\nTEST: none one docs page; the reader checks it\n") {
		t.Fatalf("no TEST: none line:\n%s", body)
	}
	rec["leg"], rec["pr"], rec["head"] = "read", "4313", strings.Repeat("cd", 20)
	body, _ = card.RenderCopy(card.CopyCardFrom("p1~2", rec))
	if strings.Contains(string(body), "\nTEST:") {
		t.Fatalf("a read carries a TEST line:\n%s", body)
	}
}

// TestCopyCardCarriesSpecHeadersAndQuotesThemAllInBrief (#4313): a copy card carries
// the spec headers from the primary record, and its brief quotes them all so child workers
// receive the complete spec.
func TestCopyCardCarriesSpecHeadersAndQuotesThemAllInBrief(t *testing.T) {
	t.Parallel()

	rec := map[string]string{
		"primary": "p1", "leg": "work", "kind": "build", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": strings.Repeat("ab", 20), "paths": "internal/cardhdr/spec.go:20-50",
		"done_when": "TestSpecPasses passes", "title": "spec card", "test": "./internal/cardhdr TestSpecPasses",
		"evidence": "issue #4313", "seams": "mock the store", "rules": "t.Parallel, no sleeps",
		"receipts": "RESULT.md line 2 is DONE", "keep": "keep legacy parsers untouched",
		"body": "implement the spec",
	}

	c := card.CopyCardFrom("p1~1", rec)
	if c.Evidence != "issue #4313" || c.Seams != "mock the store" || c.Rules != "t.Parallel, no sleeps" ||
		c.Receipts != "RESULT.md line 2 is DONE" || c.Keep != "keep legacy parsers untouched" {
		t.Fatalf("CopyCardFrom did not populate spec fields: %+v", c)
	}

	// RenderCopy for bench
	body, err := card.RenderCopy(c)
	if err != nil {
		t.Fatal(err)
	}
	sbody := string(body)

	// Headers present
	for _, want := range []string{
		"\nEVIDENCE: issue #4313\n",
		"\nSEAMS: mock the store\n",
		"\nRULES: t.Parallel, no sleeps\n",
		"\nRECEIPTS: RESULT.md line 2 is DONE\n",
		"\nKEEP: keep legacy parsers untouched\n",
		"\nPATHS: internal/cardhdr/spec.go:20-50\n",
	} {
		if !strings.Contains(sbody, want) {
			t.Fatalf("RenderCopy header lacks %q:\n%s", want, sbody)
		}
	}

	// Quoted under --- in brief
	for _, want := range []string{
		"> EVIDENCE: issue #4313",
		"> PATHS: internal/cardhdr/spec.go:20-50",
		"> SEAMS: mock the store",
		"> RULES: t.Parallel, no sleeps",
		"> RECEIPTS: RESULT.md line 2 is DONE",
		"> KEEP: keep legacy parsers untouched",
		"> DONE-WHEN: TestSpecPasses passes",
		"> implement the spec",
	} {
		if !strings.Contains(sbody, want) {
			t.Fatalf("RenderCopy brief lacks quoted %q:\n%s", want, sbody)
		}
	}

	// Friend copy brief quotes them all too
	c.Consumer = "friend:emma"
	fbody, err := card.RenderCopy(c)
	if err != nil {
		t.Fatal(err)
	}
	sfbody := string(fbody)
	for _, want := range []string{
		"> EVIDENCE: issue #4313",
		"> PATHS: internal/cardhdr/spec.go:20-50",
		"> SEAMS: mock the store",
		"> RULES: t.Parallel, no sleeps",
		"> RECEIPTS: RESULT.md line 2 is DONE",
		"> KEEP: keep legacy parsers untouched",
		"> DONE-WHEN: TestSpecPasses passes",
		"> implement the spec",
	} {
		if !strings.Contains(sfbody, want) {
			t.Fatalf("Friend brief lacks quoted %q:\n%s", want, sfbody)
		}
	}
}

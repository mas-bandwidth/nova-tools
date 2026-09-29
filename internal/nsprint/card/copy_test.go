package card_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestReadTierFollowsCard (#4315): a read copy routes to its card's READ-TIER
// (frontier, pro, flash), defaulting to pro.
func TestReadTierFollowsCard(t *testing.T) {
	t.Parallel()
	base := card.CopyCard{ID: "p1~1", Primary: "p1", Leg: "read", Kind: "read", Repo: "mas-bandwidth/nova-tools",
		Base: "dev", BaseSHA: strings.Repeat("ab", 20), Paths: "internal/x.go", DoneWhen: "TestX passes",
		Title: "one verb", Origin: "issue:nova-tools#4315", Stream: "swarm: cards", Consumer: "bench:b", Body: "the issue",
		PR: "4315", Head: strings.Repeat("cd", 20)}
	for _, tc := range []struct{ readTier, want string }{
		{card.RouteFrontier, "frontier"},
		{card.RouteFlash, "flash"},
		{card.RoutePro, "pro"},
		{"", "pro"},
		{"invalid", "pro"},
	} {
		cc := base
		cc.ReadTier = tc.readTier
		body, err := card.RenderCopy(cc)
		if err != nil {
			t.Fatalf("%q: %v", tc.readTier, err)
		}
		if !strings.Contains(string(body), "\nROUTE: "+tc.want+"\n") {
			t.Fatalf("%q: no ROUTE: %s line:\n%s", tc.readTier, tc.want, body)
		}
	}
}

// TestReadCopyCarriesFullContextDiffAndRubric (#4315): a read copy's card
// carries the primary's full context (PATHS, DONE-WHEN, EVIDENCE/body),
// the diff range (base_sha..head), CI expectations, and the scoring rubric.
func TestReadCopyCarriesFullContextDiffAndRubric(t *testing.T) {
	t.Parallel()
	c := card.CopyCard{ID: "p1~2", Primary: "p1", Leg: "read", Kind: "read", Repo: "mas-bandwidth/nova-tools",
		Base: "dev", BaseSHA: strings.Repeat("ab", 20), Paths: "internal/x.go", DoneWhen: "TestX passes",
		Title: "one verb", Origin: "issue:nova-tools#4315", Stream: "swarm: cards", Consumer: "bench:b",
		PR: "4315", Head: strings.Repeat("cd", 20),
		Body: "EVIDENCE: 100% green\nRECEIPTS: pass\nfix the bug"}
	body, err := card.RenderCopy(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"DIFF: git -C repo diff --stat " + strings.Repeat("ab", 20) + ".." + strings.Repeat("cd", 20),
		"CI: CI at head must be green (all checks OK); inspect CI status at head; any failing check or red gate caps the score at 7.",
		"RUBRIC: score against DONE-WHEN with evidence at head (file:line or the failing check); the standard's test rules hold (unit tests never wait on wall clock, class tests under 2 s, redis in functional tests); only tens land on product code; a score under 10 names the work to 10.",
		"PRIMARY-DONE-WHEN: TestX passes",
		"PRIMARY-PATHS: internal/x.go",
		"> EVIDENCE: 100% green",
		"> RECEIPTS: pass",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("read card lacks %q:\n%s", want, s)
		}
	}
}

// TestFindingGoesBackIntoCardTextOnRecut (#4315): when an earlier copy had a finding
// under 10, the recut work copy displays it in the card text.
func TestFindingGoesBackIntoCardTextOnRecut(t *testing.T) {
	t.Parallel()
	rec := map[string]string{
		"primary": "p1", "leg": "work", "kind": "build", "repo": "mas-bandwidth/nova-tools",
		"base": "dev", "base_sha": strings.Repeat("ab", 20), "paths": "internal/x.go", "done_when": "TestX passes",
		"title": "one verb", "finding": "SCORE who=reader head=cdcdcdcd score=7/10: work to 10 is add negative test",
	}
	// model work copy
	body, err := card.RenderCopy(card.CopyCardFrom("p1~3", rec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nA read of an earlier copy found:\n  SCORE who=reader head=cdcdcdcd score=7/10: work to 10 is add negative test\n") {
		t.Fatalf("model work card lacks finding:\n%s", body)
	}
	// friend work copy
	rec["consumer"] = "friend:rowan"
	fbody, err := card.RenderCopy(card.CopyCardFrom("p1~3", rec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fbody), "\nA read of an earlier copy found:\n  SCORE who=reader head=cdcdcdcd score=7/10: work to 10 is add negative test\n") {
		t.Fatalf("friend work card lacks finding:\n%s", fbody)
	}
}

// TestLintCardAcceptsReadTier (#4315): card push lint accepts valid READ-TIER
// (frontier, pro, flash) and refuses any other value.
func TestLintCardAcceptsReadTier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	validCard := []byte("RESULT: card-1 sha=0123456789ab\n" +
		"BASE: dev\n" +
		"base-repo: " + srv.URL + "/mas-bandwidth/nova-tools\n" +
		"base-sha: " + strings.Repeat("ab", 20) + "\n" +
		"PATHS: internal/x.go\n" +
		"DEPENDS-ON: none\n" +
		"DONE-WHEN: TestX passes\n" +
		"READ-TIER: frontier\n\nbody\n")
	if err := card.LintCard(ctx, validCard); err != nil {
		t.Fatalf("valid READ-TIER refused: %v", err)
	}

	invalidCard := []byte("RESULT: card-1 sha=0123456789ab\n" +
		"BASE: dev\n" +
		"base-repo: " + srv.URL + "/mas-bandwidth/nova-tools\n" +
		"base-sha: " + strings.Repeat("ab", 20) + "\n" +
		"PATHS: internal/x.go\n" +
		"DEPENDS-ON: none\n" +
		"DONE-WHEN: TestX passes\n" +
		"READ-TIER: ultra\n\nbody\n")
	if err := card.LintCard(ctx, invalidCard); err == nil || !strings.Contains(err.Error(), "READ-TIER: \"ultra\" is not") {
		t.Fatalf("invalid READ-TIER accepted: %v", err)
	}
}

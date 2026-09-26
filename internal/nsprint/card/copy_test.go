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

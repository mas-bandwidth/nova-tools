package merge

import (
	"strings"
	"testing"
)

// A hold by friend X is released when X's last typed verdict written after the hold is
// APPROVE, at ANY head (Glenn, lander-keys-reads-by-who, 2026-09-23).
//
// Measured 2026-09-23: the gate dropped 57 distinct pull requests with "carries an
// unreleased HOLD"; five of them (#2619 johnny, #2628 stella, #2707 rowan, #2879 rowan,
// #3080 johnny) were this bug class: the holder's own later typed APPROVE sat at a head
// the branch had since moved past, and supersededByVerdictAtHead demanded that the
// APPROVE be at the CURRENT head, so the hold pinned forever even though its author's
// last word on it was APPROVE.
//
// The fixture is #2879's lines as the forge answers them (comment ids and stamps are the
// real ones; bodies are the first lines, which are what the parser reads):
//
//	5788218151 stella HOLD 7    at fc15f98d  03:00:19Z
//	5788955353 rowan  HOLD 6    at fc15f98d  04:17:20Z
//	5789140114 rowan  REPAIR    at fc15f98d  04:37:19Z
//	5789220041 rowan  APPROVE 8 at fc15f98d  04:47:09Z  <- supersedes HOLD 6
//	5791042520 stella APPROVE 8 at 8984b941  07:43:58Z  <- current head
const (
	head2879Old = "fc15f98d207cff0249490fbc5ad780284b90c03d"
	head2879Now = "8984b94126fc40f02766a2b97a20b0324d6ba200"
)

const reviewers2879 = "who\tlogins\tmay-hold\n" +
	"rowan\trowan-claude\tyes\n" +
	"stella\tgafferongames\tyes\n"

func comments2879() []fixtureComment {
	return []fixtureComment{
		{ID: 5788218151, Login: "gafferongames", At: "2026-09-23T03:00:19Z",
			Body: "DISPOSITION who=stella head=" + head2879Old + " verdict=HOLD score=7/10\n\nSource: the --max path has no unit test."},
		{ID: 5788955353, Login: "rowan-claude", At: "2026-09-23T04:17:20Z",
			Body: "DISPOSITION who=rowan head=" + head2879Old + " verdict=HOLD score=6\nCI-only: ci-ok run 35807990107 was cancelled, not failed."},
		{ID: 5789140114, Login: "rowan-claude", At: "2026-09-23T04:37:19Z",
			Body: "REPAIR who=rowan head=" + head2879Old + " ready=true: no code change; CI green at head"},
		{ID: 5789220041, Login: "rowan-claude", At: "2026-09-23T04:47:09Z",
			Body: "DISPOSITION who=rowan head=" + head2879Old + " verdict=APPROVE score=8\n- supersedes this account's HOLD 6 (CI-only) at this head."},
		{ID: 5791042520, Login: "gafferongames", At: "2026-09-23T07:43:58Z",
			Body: "DISPOSITION who=stella head=" + head2879Now + " verdict=APPROVE score=8\n\nSource: the repair addresses my prior HOLD."},
	}
}

func holdsFor2879(t *testing.T, cs []fixtureComment) []Verdict {
	t.Helper()
	rs, err := ParseReviewers(strings.NewReader(reviewers2879))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	// ignoreUntyped=true is the land lane's --untyped-comments=ignore: the REPAIR line is
	// not a verdict, and this table is about the typed HOLD and APPROVE lines only.
	vs, err := ParseForgeVerdicts(commentsJSON(t, cs), "[]", 2879, rs, "rowan-claude", head2879Now, true)
	if err != nil {
		t.Fatalf("the fixture did not decode: %v", err)
	}
	return UnliftedHolds(vs, head2879Now, "rowan-claude", rs)
}

// The #2879 break: rowan's HOLD 6 at fc15f98d, superseded by rowan's own APPROVE 8 at
// fc15f98d, still pinned at head 8984b941.
func TestAHoldIsReleasedByTheHoldersLaterApproveAtAnOlderHead(t *testing.T) {
	t.Parallel()
	if holds := holdsFor2879(t, comments2879()); len(holds) != 0 {
		t.Fatalf("#2879: rowan's last typed verdict after HOLD 6 is APPROVE 8 (at %s, head now %s), so no hold may pin; got %+v",
			Short(head2879Old), Short(head2879Now), holds)
	}
}

// The rule is the holder's LAST typed verdict: a HOLD written after the APPROVE is the
// holder's standing word and still pins.
func TestAHoldAfterTheHoldersOlderHeadApproveStillPins(t *testing.T) {
	t.Parallel()
	cs := append(comments2879(), fixtureComment{ID: 5792000001, Login: "rowan-claude", At: "2026-09-23T09:00:00Z",
		Body: "DISPOSITION who=rowan head=" + head2879Old + " verdict=HOLD score=5\nthe re-run went red again."})
	holds := holdsFor2879(t, cs)
	if len(holds) != 1 || holds[0].ID != "comment:5792000001" {
		t.Fatalf("rowan's later HOLD 5 is his last word and must pin alone, got %+v", holds)
	}
}

// Only the holder releases: another friend's APPROVE at an older head lifts nothing.
func TestAnotherFriendsOlderHeadApproveDoesNotReleaseTheHold(t *testing.T) {
	t.Parallel()
	cs := []fixtureComment{
		comments2879()[1], // rowan HOLD 6 at fc15f98d
		{ID: 5789220099, Login: "gafferongames", At: "2026-09-23T04:47:09Z",
			Body: "DISPOSITION who=stella head=" + head2879Old + " verdict=APPROVE score=8"},
	}
	holds := holdsFor2879(t, cs)
	if len(holds) != 1 || holds[0].ID != "comment:5788955353" {
		t.Fatalf("stella's APPROVE does not release rowan's hold, want comment:5788955353 held, got %+v", holds)
	}
}

// An unstamped verdict is no evidence of order: it releases nothing at any head.
func TestAnUnstampedOlderHeadApproveReleasesNothing(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader(reviewers2879))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	hold := Verdict{ID: "comment:1", Who: "rowan", Word: "hold", Head: head2879Old, At: "2026-09-23T04:17:20Z",
		Source: "comment-rule", Kind: "line"}
	approve := Verdict{ID: "comment:2", Who: "rowan", Word: "approve", Head: head2879Old, At: "",
		Source: "comment-rule", Kind: "line"}
	holds := UnliftedHolds([]Verdict{hold, approve}, head2879Now, "rowan-claude", rs)
	if len(holds) != 1 {
		t.Fatalf("an unstamped APPROVE is no evidence and must release nothing, got %+v", holds)
	}
}

// The scope rule is kept at every head: a scoped APPROVE at an older head releases only
// the holds it names.
func TestAScopedOlderHeadApproveReleasesOnlyWhatItNames(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader(reviewers2879))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	named := Verdict{ID: "comment:10", Who: "rowan", Word: "hold", Head: head2879Old, At: "2026-09-23T04:00:00Z",
		Source: "comment-rule", Kind: "line", Scope: "ci"}
	other := Verdict{ID: "comment:11", Who: "rowan", Word: "hold", Head: head2879Old, At: "2026-09-23T04:01:00Z",
		Source: "comment-rule", Kind: "line", Scope: "docs"}
	approve := Verdict{ID: "comment:12", Who: "rowan", Word: "approve", Head: head2879Old, At: "2026-09-23T05:00:00Z",
		Source: "comment-rule", Kind: "line", Scope: "ci", Releases: []string{"comment:10"}}
	holds := UnliftedHolds([]Verdict{named, other, approve}, head2879Now, "rowan-claude", rs)
	if len(holds) != 1 || holds[0].ID != "comment:11" {
		t.Fatalf("a scoped APPROVE naming comment:10 releases only it; want comment:11 held, got %+v", holds)
	}
}

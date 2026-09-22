package merge

import (
	"os"
	"strings"
	"testing"
)

// nova-tools #2615 follow-up: the landing gate matched a friend's name
// CASE-SENSITIVELY. Measured 2026-09-22 20:14Z, lane land-1615, `nova-merge batch`:
//
//	BATCH DROP #2587 reason="head 40785e2ccb04 carries an unreleased HOLD" who=johnny
//	hold=comment:5780867350 source=comment-rule held_at=6e00218d976d carried=yes
//
// although the pull request carried `DISPOSITION who=johnny head=40785e2c...
// verdict=APPROVE score=9/10` at head; the HOLD comment 5780867350 was typed
// `DISPOSITION who=Johnny ...`. Same shape on #2616 (hold who=Johnny, release
// who=johnny). normWho folds every who through one place -- ParseDispositionLine, a
// derived name prefix, and every row and lookup of the reviewers table -- so a
// friend's own name in whatever case they happened to type it is the same friend
// everywhere downstream.
const reviewersWhoCase = "who\tlogins\tmay-hold\n" +
	"johnny\tjohnny-gh\tyes\n" +
	"emma\temma-gh\tyes\n"

func holdWhoCase(who, head string) string {
	return "DISPOSITION who=" + who + " head=" + head + " verdict=HOLD score=4/10\n\nsome finding."
}

func approveWhoCase(who, head string) string {
	return "DISPOSITION who=" + who + " head=" + head + " verdict=APPROVE score=9/10\n\nlooks right now."
}

func TestHoldReleaseMatchesTheFriendsNameCaseInsensitively(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader(reviewersWhoCase))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	// 12-character prefixes from the real #2587 log line; tails are padding, matching
	// carriedhold_test.go's own convention, since merge.Short prints 12 characters.
	headA := "6e00218d976d" + "0f1e2d3c4b5a69788796a5b4c3d2" // superseded (#2587's own held_at)
	headB := "40785e2ccb04" + "1b3c5d7e9f02468ace13579bdf02" // current (#2587's own head)

	cases := []struct {
		name       string
		holdWho    string
		approveWho string
	}{
		{"HOLD who=Johnny, APPROVE who=johnny (the measured #2587/#2616 shape)", "Johnny", "johnny"},
		{"mirror: HOLD who=johnny, APPROVE who=Johnny", "johnny", "Johnny"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			comments := commentsJSON(t, []fixtureComment{
				{ID: 1, Login: "johnny-gh", Body: holdWhoCase(tc.holdWho, headA), At: "2026-09-22T17:19:57Z"},
				{ID: 2, Login: "johnny-gh", Body: approveWhoCase(tc.approveWho, headB), At: "2026-09-22T19:42:05Z"},
			})
			vs, err := ParseForgeVerdicts(comments, "[]", 2587, rs, "rowan-claude", headB, false)
			if err != nil {
				t.Fatalf("fixture did not decode: %v", err)
			}
			holds := UnliftedHolds(vs, headB, "rowan-claude", rs)
			if len(holds) != 0 {
				t.Fatalf("a same-friend APPROVE at head must release the earlier HOLD regardless of typed case, held: %+v", holds)
			}
		})
	}

	// The negative control: a DIFFERENT friend's APPROVE, even at head, releases nothing
	// -- the rule is same-friend, never "somebody approved".
	t.Run("a different friend's APPROVE (who=emma) leaves the hold unreleased", func(t *testing.T) {
		t.Parallel()
		comments := commentsJSON(t, []fixtureComment{
			{ID: 3, Login: "johnny-gh", Body: holdWhoCase("Johnny", headA), At: "2026-09-22T17:19:57Z"},
			{ID: 4, Login: "emma-gh", Body: approveWhoCase("emma", headB), At: "2026-09-22T19:42:05Z"},
		})
		vs, err := ParseForgeVerdicts(comments, "[]", 2587, rs, "rowan-claude", headB, false)
		if err != nil {
			t.Fatalf("fixture did not decode: %v", err)
		}
		holds := UnliftedHolds(vs, headB, "rowan-claude", rs)
		if len(holds) != 1 || holds[0].Who != "johnny" {
			t.Fatalf("a different friend's APPROVE must not release johnny's hold, got: %+v", holds)
		}
	})
}

// normWho itself: fold once, compare exactly, everywhere.
func TestNormWhoFoldsCaseAndWhitespaceOnce(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"Johnny", "johnny", "  Johnny  ", "JOHNNY"} {
		if got := normWho(in); got != "johnny" {
			t.Errorf("normWho(%q) = %q, want %q", in, got, "johnny")
		}
	}
}

func TestResolveWhoIsCaseInsensitiveViaTheReviewersTable(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader("Johnny\tjohnny-gh\tyes\n"))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	for _, typedWho := range []string{"Johnny", "johnny", "JOHNNY"} {
		who, ok := rs.ResolveWho("johnny-gh", typedWho)
		if !ok || who != "johnny" {
			t.Errorf("ResolveWho(%q) = (%q, %v), want (\"johnny\", true)", typedWho, who, ok)
		}
	}
}

// nova-tools #2615 follow-up, second measured case: lane land-1620 at 20:21Z,
//
//	BATCH DROP #2522 reason="head 9ee8155657a4 carries an unreleased HOLD" who=unknown
//	hold=comment:5766104067 source=comment-rule held_at=9ee8155657a4
//
// That comment is Stella's PROSE hold, posted (no who=) via the shared login
// gafferongames: "HOLD — Stella, independent contract/source read at
// **0526ea67fcf4ccad48f7fe573ccfbcf3a6f39745**. ...". deriveHoldWho reads her name off
// the leading "HOLD -- Stella, ..." shape the same way the bash lander's own name-prefix
// rule did, so the hold is attributed to her instead of who=unknown.
func TestUntypedHoldDerivesTheFriendsNameFromALeadingPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want string
	}{
		{"HOLD -- Name, ... (the real #2522 comment 5766104067 shape)",
			"HOLD — Stella, independent contract/source read at **0526ea67fcf4ccad48f7fe573ccfbcf3a6f39745**. This is the actual forge head.",
			"stella"},
		{"HOLD -- Name, ... double-dash", "HOLD -- Stella, scoped repair reread at `8bc46a5e`.", "stella"},
		{"Name: HOLD ...", "Stella: HOLD at 4cd5e34a4c67d83aedddf6329a5667bc7710c0a4.", "stella"},
		{"Name review: HOLD", "Johnny review: HOLD on the schema leg.", "johnny"},
		{"case-insensitive name", "hold -- EMMA, quick pass", "emma"},
		{"a name outside the fixed set derives nothing", "HOLD -- Whoever, ...", "unknown"},
		{"a bare HOLD with no name derives nothing", "HOLD the schema leg is red.", "unknown"},
	}
	for _, tc := range cases {
		if got := deriveHoldWho(tc.line); got != tc.want {
			t.Errorf("deriveHoldWho(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// The real #2522 comment 5766104067, run through the whole ParseComment pipe: who is
// "stella", not "unknown".
func TestParseCommentDerivesWhoForTheRealStellaProseHold(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader("who\tlogins\tmay-hold\nstella\tgafferongames\tyes\nemma\tgafferongames\tyes\n"))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	body := "HOLD — Stella, independent contract/source read at **0526ea67fcf4ccad48f7fe573ccfbcf3a6f39745**. " +
		"This is the actual forge head; the supplied full SHA ending `18ff` was not this revision."
	head := "9ee8155657a4ff73d151003d18456a0075acfd41"
	v, ok := ParseComment(5766104067, "gafferongames", body, "2026-09-21T19:16:48Z", rs, "gafferongames", head, false)
	if !ok || v.Word != "hold" {
		t.Fatalf("expected a hold verdict, got %+v ok=%v", v, ok)
	}
	if v.Who != "stella" {
		t.Fatalf("expected the prose hold to derive who=stella, got who=%q", v.Who)
	}

	// Note on what this fix does NOT do: her hold binds to the current head (SPEC-DECIDE
	// lines 1037-1040, untyped comments bind to current head), so it is an AT-HEAD hold,
	// not a carried one. Releasing an AT-HEAD hold still requires the holder's own lane
	// record (SPEC-DECIDE reading 3, nova-tools #2550/#2615,
	// TestACarriedHoldIsReleasedByTheSameFriendsVerdictAtHead's "both current" row): her
	// own later typed APPROVE *comment* at that same head does not release it, same as
	// before this fix. What this fix corrects is the attribution -- who=stella, not
	// who=unknown -- which is what the reviewer-file permission checks (HasWho,
	// IsExplicitlyDisallowed) and any future lane record from her need to match against.
	approveBody := "DISPOSITION who=stella head=" + head + " verdict=APPROVE score=9"
	av, aok := ParseComment(5783400393, "gafferongames", approveBody, "2026-09-22T20:12:58Z", rs, "gafferongames", head, false)
	if !aok || av.Word != "approve" || av.Who != "stella" {
		t.Fatalf("expected her typed approve to parse as who=stella approve, got %+v ok=%v", av, aok)
	}
	holds := UnliftedHolds([]Verdict{v, av}, head, "gafferongames", rs)
	if len(holds) != 1 || holds[0].Who != "stella" {
		t.Fatalf("an AT-HEAD hold is released only by a lane record (SPEC-DECIDE reading 3); "+
			"a same-friend APPROVE comment at head must still leave it standing, got: %+v", holds)
	}
}

// nova-tools #2631: a comment yields HOLD only from a verdict-SHAPED line. The word HOLD
// used as prose inside a longer line -- a bullet recapping somebody else's already-
// resolved hold -- is never a verdict, and the typed line wins when a comment carries
// both a typed verdict and such prose.
//
// Measured 2026-09-22 20:27Z, lane land-1630:
//
//	BATCH DROP #2587 reason="head 40785e2ccb04 carries an unreleased HOLD" who=unknown
//	hold=comment:5782779847 source=comment-rule held_at=40785e2ccb04
//	BATCH DROP #2616 ... hold=comment:5782724650 held_at=6a44901771d2
//
// Both comments are Emma's typed `DISPOSITION who=emma ... verdict=APPROVE score=10/10`
// reviews whose body happens to contain "**Johnny's HOLD Conclusively
// Satisfied/Resolved**" in bold -- praise for a hold that was already closed, not a new
// one. The fixtures are the real comment bodies (fetched via `gh api
// repos/mas-bandwidth/nova-tools/issues/{2587,2616}/comments`).
func TestATypedApproveWinsOverProseThatMentionsHold(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader("who\tlogins\tmay-hold\nemma\tgafferongames\tyes\njohnny\tgafferongames\tyes\n"))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}

	cases := []struct {
		name string
		file string
		id   int64
		head string
	}{
		{"#2587 comment 5782779847", "testdata/pr2587-comment-5782779847.txt", 5782779847, "40785e2ccb0439a6d466612d8c4485ecefc94ac6"},
		{"#2616 comment 5782724650", "testdata/pr2616-comment-5782724650.txt", 5782724650, "6a44901771d2e27c56a67cfc9578a1a50bbcdaa9"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("fixture: %v", err)
			}
			v, ok := ParseComment(tc.id, "gafferongames", string(body), "2026-09-22T19:37:43Z", rs, "rowan-claude", tc.head, false)
			if !ok {
				t.Fatalf("expected a verdict, got none")
			}
			if v.Word != "approve" {
				t.Fatalf("a body containing \"**Johnny's HOLD...**\" as prose must not become a hold: got word=%q who=%q", v.Word, v.Who)
			}
			if v.Who != "emma" {
				t.Fatalf("expected who=emma from the typed line, got %q", v.Who)
			}
			if v.Scope != "" {
				t.Fatalf("expected an unscoped approve, got scope=%q", v.Scope)
			}
		})
	}
}

// The other half of #2631: a comment whose first line IS a typed HOLD still holds --
// this fix narrows untyped detection, it does not touch the typed path.
func TestATypedDispositionHoldStillHolds(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader("who\tlogins\tmay-hold\njohnny\tgafferongames\tyes\n"))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	head := "6e00218d976d86f43fc48fe0ec6a87741f7811bb"
	body := "DISPOSITION who=Johnny head=" + head + " verdict=HOLD score=4/10\n\n" +
		"G2 base dev. The ruling is that the stream stays cards:done."
	v, ok := ParseComment(5780867350, "gafferongames", body, "2026-09-22T17:19:57Z", rs, "rowan-claude", head, false)
	if !ok || v.Word != "hold" || v.Who != "johnny" {
		t.Fatalf("a first-line typed DISPOSITION ... verdict=HOLD must still hold, got %+v ok=%v", v, ok)
	}
}

// isHoldShapedLine / deriveHoldWho unit coverage for the recognised shapes nova-tools
// #2631 named, and the negative case that started all of this: HOLD used as prose inside
// a bullet about somebody ELSE's hold is not a verdict-shaped line at all.
func TestIsHoldShapedLineRecognisesOnlyTheNamedShapes(t *testing.T) {
	t.Parallel()
	shaped := []string{
		"HOLD the schema leg is red.",
		"HOLD #123",
		"HOLD at 4cd5e34a4c67d83aedddf6329a5667bc7710c0a4",
		"Verdict: **HOLD** (score 2/10)",
		"(HOLD)",
		"Johnny review: HOLD",
	}
	for _, l := range shaped {
		if !isHoldShapedLine(l) {
			t.Errorf("isHoldShapedLine(%q) = false, want true", l)
		}
	}

	notShaped := []string{
		"- **Johnny's HOLD Conclusively Satisfied**:",
		"### Johnny's HOLD Conclusively Resolved",
		"The hold from Johnny is now resolved.",
		"Verdict: **APPROVE** (score 10/10)",
	}
	for _, l := range notShaped {
		if isHoldShapedLine(l) {
			t.Errorf("isHoldShapedLine(%q) = true, want false (prose about a hold is not a verdict-shaped line)", l)
		}
	}
}

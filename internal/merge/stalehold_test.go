package merge

import (
	"os"
	"strings"
	"testing"
)

// nova-tools #2710. Lane tools-20260922T221740Z dropped #2612 at head 8eafd236
// on comment 5783202393, whose first line is
// "HOLD sha=8a987ca5e6e16b7d14cb4f24fc062ae2bf22ce94", and #2611 the same way.
// The sha is not that head. A pin on the first line that is not this head is
// not a hold here. A pin that is this head still is. An unpinned HOLD still is.
// Case-fold and the prose-hold rule are unchanged: a differently cased who=
// still releases, and a line that only mentions HOLD does not become one.

const reviewers2710 = "who\tlogins\tmay-hold\n" +
	"emma\tgafferongames\tyes\n" +
	"stella\tgafferongames\tyes\n" +
	"johnny\tgafferongames\tyes\n" +
	"glenn\tgafferongames\tyes\n" +
	"rowan\trowan-claude\tyes\n"

const head2612 = "8eafd2362d944c338e39a37030f8a053d43ae4e8"
const sha2612 = "8a987ca5e6e16b7d14cb4f24fc062ae2bf22ce94"

func reviewers2710Set(t *testing.T) *ReviewerSet {
	t.Helper()
	rs, err := ParseReviewers(strings.NewReader(reviewers2710))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	return rs
}

func TestAHoldPinnedToANonHeadShaIsNotAHoldAtHead(t *testing.T) {
	t.Parallel()
	rs := reviewers2710Set(t)

	t.Run("sha= not the PR head does not count", func(t *testing.T) {
		body := "HOLD sha=" + sha2612 + "\n\nStella delta read clears the original defect."
		v, ok := ParseComment(601, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != staleHoldWord {
			t.Fatalf("a HOLD pinned to another sha must not be a hold at head, got %+v ok=%v", v, ok)
		}
		if got := staleHoldReport(v); got != "601(sha=8a987ca5)" {
			t.Fatalf("stale report = %q, want 601(sha=8a987ca5)", got)
		}
		holds := UnliftedHolds([]Verdict{v}, head2612, "rowan-claude", rs)
		if len(holds) != 0 {
			t.Fatalf("stale hold must not count, got %+v", holds)
		}
	})

	t.Run("head= not the PR head does not count", func(t *testing.T) {
		body := "DISPOSITION who=stella head=" + sha2612 + " verdict=HOLD score=7"
		v, ok := ParseComment(602, "gafferongames", body, "2026-09-22T20:14:33Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != staleHoldWord || v.Who != "stella" {
			t.Fatalf("typed head= off head must be stale, not a hold, got %+v ok=%v", v, ok)
		}
		if got := staleHoldReport(v); got != "602(sha=8a987ca5)" {
			t.Fatalf("stale report = %q, want 602(sha=8a987ca5)", got)
		}
		if len(UnliftedHolds([]Verdict{v}, head2612, "rowan-claude", rs)) != 0 {
			t.Fatal("typed hold pinned off head counted")
		}
	})

	t.Run("head= is the PR head still counts", func(t *testing.T) {
		body := "DISPOSITION who=stella head=" + head2612 + " verdict=HOLD score=7\n\none defect remains."
		v, ok := ParseComment(603, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "hold" || v.Who != "stella" {
			t.Fatalf("typed head= at head must still hold, got %+v ok=%v", v, ok)
		}
		holds := UnliftedHolds([]Verdict{v}, head2612, "rowan-claude", rs)
		if len(holds) != 1 || holds[0].ID != "comment:603" {
			t.Fatalf("hold at head must count, got %+v", holds)
		}
	})

	t.Run("sha= is the PR head still counts", func(t *testing.T) {
		body := "HOLD sha=" + head2612 + "\n\nStella: one more defect."
		v, ok := ParseComment(604, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "hold" || v.Who != "stella" {
			t.Fatalf("sha= at head must still hold and name stella, got %+v ok=%v", v, ok)
		}
		if len(UnliftedHolds([]Verdict{v}, head2612, "rowan-claude", rs)) != 1 {
			t.Fatal("sha= at head did not count")
		}
	})

	t.Run("7-hex prefix of the PR head still counts", func(t *testing.T) {
		body := "HOLD head=" + head2612[:7] + " Stella: one more defect."
		v, ok := ParseComment(605, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "hold" {
			t.Fatalf("a 7-hex pin of this head must still hold, got %+v ok=%v", v, ok)
		}
	})

	t.Run("no pin still counts", func(t *testing.T) {
		body := "HOLD — Stella, the fence test is red."
		v, ok := ParseComment(606, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "hold" || v.Who != "stella" {
			t.Fatalf("an unpinned HOLD must still hold, got %+v ok=%v", v, ok)
		}
	})

	t.Run("a pin only on a later line does not stale it", func(t *testing.T) {
		body := "HOLD: red\n\nsha=" + sha2612
		v, ok := ParseComment(607, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "hold" {
			t.Fatalf("a pin that is not on the first line must not stale the hold, got %+v ok=%v", v, ok)
		}
	})

	t.Run("prose that mentions HOLD is not a hold", func(t *testing.T) {
		body := "DISPOSITION who=emma head=" + head2612 + " verdict=APPROVE score=10/10\n\n" +
			"- **Johnny's HOLD Conclusively Satisfied**:"
		v, ok := ParseComment(608, "gafferongames", body, "2026-09-22T21:03:55Z", rs, "rowan-claude", head2612, false)
		if !ok || v.Word != "approve" || v.Who != "emma" {
			t.Fatalf("prose about a HOLD must not become a hold, got %+v ok=%v", v, ok)
		}
		prose := "Stella scoped HOLD at exact " + sha2612 + ".\n\nTwo defects."
		pv, pok := ParseComment(5782441213, "gafferongames", prose, "2026-09-22T19:13:00Z", rs, "rowan-claude", head2612, true)
		if pok && (pv.Word == "hold" || pv.Word == staleHoldWord) {
			t.Fatalf("a sentence that says HOLD must not be a hold, got %+v", pv)
		}
	})

	t.Run("case-fold still releases a hold at head", func(t *testing.T) {
		hold := "DISPOSITION who=Stella head=" + head2612 + " verdict=HOLD score=4/10\n\nsome finding."
		approve := "DISPOSITION who=stella head=" + head2612 + " verdict=APPROVE score=9"
		hv, ok := ParseComment(609, "gafferongames", hold, "2026-09-22T20:00:00Z", rs, "rowan-claude", head2612, false)
		av, aok := ParseComment(610, "gafferongames", approve, "2026-09-22T21:00:00Z", rs, "rowan-claude", head2612, false)
		if !ok || !aok || hv.Word != "hold" || av.Word != "approve" {
			t.Fatalf("fixture: hold %+v/%v approve %+v/%v", hv, ok, av, aok)
		}
		if holds := UnliftedHolds([]Verdict{hv, av}, head2612, "rowan-claude", rs); len(holds) != 0 {
			t.Fatalf("who=Stella must still be released by who=stella, got %+v", holds)
		}
	})

	t.Run("two names stay held", func(t *testing.T) {
		body := "HOLD sha=" + head2612 + "\n\nStella and Emma: one defect."
		approve := "DISPOSITION who=stella head=" + head2612 + " verdict=APPROVE score=9"
		hv, ok := ParseComment(611, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", head2612, false)
		av, _ := ParseComment(612, "gafferongames", approve, "2026-09-22T21:03:55Z", rs, "rowan-claude", head2612, false)
		if !ok || hv.Word != "hold" || hv.Who != "unknown" {
			t.Fatalf("two names must stay an unattributed hold, got %+v ok=%v", hv, ok)
		}
		holds := UnliftedHolds([]Verdict{hv, av}, head2612, "rowan-claude", rs)
		if len(holds) != 1 || holds[0].Who != "unknown" {
			t.Fatalf("an unattributed hold must stay held, got %+v", holds)
		}
	})

	t.Run("#2612 reports no hold and stale 5783202393", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/pr2612-comments.json")
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		vs, err := ParseForgeVerdicts(string(raw), "[]", 2612, rs, "rowan-claude", head2612, true)
		if err != nil {
			t.Fatalf("fixture did not decode: %v", err)
		}
		holds := UnliftedHolds(vs, head2612, "rowan-claude", rs)
		if len(holds) != 0 {
			t.Fatalf("#2612 at %s must not be held, got %+v", head2612[:8], holds)
		}
		var stale []string
		for _, v := range vs {
			if v.Word == staleHoldWord {
				stale = append(stale, staleHoldReport(v))
			}
		}
		want := []string{
			"5783202393(sha=8a987ca5)",
			"5783425783(sha=ea03af3d)",
			"5784011772(sha=a5f251a9)",
		}
		if strings.Join(stale, " ") != strings.Join(want, " ") {
			t.Fatalf("stale = %v, want %v", stale, want)
		}
	})

	t.Run("the same #2612 comment with sha= at head still counts", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/pr2612-comments.json")
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		var comments []struct {
			ID   int64 `json:"id"`
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			Body      string `json:"body"`
			CreatedAt string `json:"created_at"`
		}
		if err := decodeArrays(string(raw), &comments); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var body, at string
		for _, c := range comments {
			if c.ID == 5783202393 {
				body, at = c.Body, c.CreatedAt
				break
			}
		}
		if body == "" || !strings.Contains(body, sha2612) {
			t.Fatal("comment 5783202393 not in the fixture")
		}
		body = strings.ReplaceAll(body, sha2612, head2612)
		v, ok := ParseComment(5783202393, "gafferongames", body, at, rs, "rowan-claude", head2612, true)
		if !ok || v.Word != "hold" {
			t.Fatalf("the same comment pinned at head must be a hold, got %+v ok=%v", v, ok)
		}
		if len(UnliftedHolds([]Verdict{v}, head2612, "rowan-claude", rs)) != 1 {
			t.Fatal("sha= rewritten to the current head did not count")
		}
	})
}

package merge

import (
	"strings"
	"testing"
)

// nova-tools #2710, recut of #2713's 43f1df17 onto dev. An untyped
// "HOLD sha=<head>" whose friend name is not adjacent to HOLD reads its writer
// from the header (the rest of the first line, or the next non-blank line when
// the first carries only pins). Only an anchored identity attributes:
// who=<name> or <Name>:. A bare mention -- "Stella delta read clears the
// original defect" on a shared login -- is not a signature, so it stays
// who=unknown, and under the #3278 any-head ruling Stella's later APPROVE
// cannot release somebody else's hold. The off-head-pin rule of #2713 is not
// carried: a sha= pin here does not change which head the hold binds to.

const reviewersMention = "who\tlogins\tmay-hold\n" +
	"emma\tgafferongames\tyes\n" +
	"stella\tgafferongames\tyes\n" +
	"johnny\tgafferongames\tyes\n" +
	"glenn\tgafferongames\tyes\n" +
	"rowan\trowan-claude\tyes\n"

const headMention = "8eafd2362d944c338e39a37030f8a053d43ae4e8"
const olderMention = "8a987ca5e6e16b7d14cb4f24fc062ae2bf22ce94"

func TestAMentionedFriendIsNotTheWriterOfAHold(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader(reviewersMention))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}

	t.Run("who= on the next line attributes", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nWho=Stella one more defect."
		v, ok := ParseComment(6041, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", headMention, false)
		if !ok || v.Word != "hold" || v.Who != "stella" {
			t.Fatalf("who=<name> must attribute, got %+v ok=%v", v, ok)
		}
	})

	t.Run("<Name>: on the next line attributes", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nStella: one more defect."
		v, ok := ParseComment(6042, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", headMention, false)
		if !ok || v.Word != "hold" || v.Who != "stella" {
			t.Fatalf("<Name>: must attribute, got %+v ok=%v", v, ok)
		}
	})

	t.Run("who= on the HOLD line attributes", func(t *testing.T) {
		body := "HOLD who=johnny the fixture is stale."
		v, ok := ParseComment(6043, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", headMention, false)
		if !ok || v.Word != "hold" || v.Who != "johnny" {
			t.Fatalf("who=<name> on the HOLD line must attribute, got %+v ok=%v", v, ok)
		}
	})

	t.Run("a single friend-name mention is not attribution", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nStella delta read clears the original defect."
		// #3278: the holder's own later typed APPROVE releases at ANY head, so a
		// mention read as the writer would be released by the mentioned friend.
		approve := "DISPOSITION who=stella head=" + olderMention + " verdict=APPROVE score=9"
		hv, ok := ParseComment(613, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", headMention, false)
		av, aok := ParseComment(614, "gafferongames", approve, "2026-09-22T21:03:55Z", rs, "rowan-claude", headMention, false)
		if !ok || !aok || hv.Word != "hold" || hv.Who != "unknown" || av.Word != "approve" {
			t.Fatalf("a bare name must stay an unattributed hold, got hold %+v/%v approve %+v/%v", hv, ok, av, aok)
		}
		holds := UnliftedHolds([]Verdict{hv, av}, headMention, "rowan-claude", rs)
		if len(holds) != 1 || holds[0].Who != "unknown" || holds[0].ID != "comment:613" {
			t.Fatalf("stella's later approve must not release a hold that only mentions her, got %+v", holds)
		}
	})

	t.Run("two friend names stay unknown", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nwho=stella and Johnny: disagree on the fixture."
		v, ok := ParseComment(615, "gafferongames", body, "2026-09-22T21:10:00Z", rs, "rowan-claude", headMention, false)
		if !ok || v.Word != "hold" || v.Who != "unknown" {
			t.Fatalf("two names must stay unknown, got %+v ok=%v", v, ok)
		}
	})

	// Stella's HOLD 6 on #3388 (comment 5804827993), case 1: a prose mention
	// followed by a colon is not a signature. <Name>: attributes only at the start
	// of the post-pin header.
	t.Run("a prose <Name>: after the pin is not a signature", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nI asked Stella: please verify this"
		approve := "DISPOSITION who=stella head=" + olderMention + " verdict=APPROVE score=9"
		hv, ok := ParseComment(618, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", headMention, false)
		av, aok := ParseComment(619, "gafferongames", approve, "2026-09-22T21:03:55Z", rs, "rowan-claude", headMention, false)
		if !ok || !aok || hv.Word != "hold" || hv.Who != "unknown" {
			t.Fatalf("a mid-sentence Name: must stay an unattributed hold, got %+v ok=%v", hv, ok)
		}
		holds := UnliftedHolds([]Verdict{hv, av}, headMention, "rowan-claude", rs)
		if len(holds) != 1 || holds[0].ID != "comment:618" {
			t.Fatalf("stella's later approve must not release a hold that only mentions her, got %+v", holds)
		}
	})

	// Stella's HOLD 6 on #3388, case 2: ParseComment finds a standalone HOLD line
	// after a leading line, so the header is read from THAT line and its next
	// non-blank line, not from the body's first line.
	t.Run("a later standalone HOLD line reads its own header", func(t *testing.T) {
		body := "NOTE shared login, second pass.\nHOLD sha=" + headMention + "\nwho=stella one more defect."
		approve := "DISPOSITION who=stella head=" + olderMention + " verdict=APPROVE score=9"
		hv, ok := ParseComment(620, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", headMention, false)
		if !ok || hv.Word != "hold" || hv.Who != "stella" {
			t.Fatalf("the later HOLD line's who= must attribute, got %+v ok=%v", hv, ok)
		}
		av, _ := ParseComment(621, "gafferongames", approve, "2026-09-22T21:03:55Z", rs, "rowan-claude", headMention, false)
		if holds := UnliftedHolds([]Verdict{hv, av}, headMention, "rowan-claude", rs); len(holds) != 0 {
			t.Fatalf("stella's own later approve must release her hold (#3278), got %+v", holds)
		}
	})

	t.Run("the attributed holder's own later approve releases at any head (#3278)", func(t *testing.T) {
		body := "HOLD sha=" + headMention + "\n\nwho=stella one more defect."
		approve := "DISPOSITION who=stella head=" + olderMention + " verdict=APPROVE score=9"
		hv, _ := ParseComment(616, "gafferongames", body, "2026-09-22T20:01:08Z", rs, "rowan-claude", headMention, false)
		av, _ := ParseComment(617, "gafferongames", approve, "2026-09-22T21:03:55Z", rs, "rowan-claude", headMention, false)
		if holds := UnliftedHolds([]Verdict{hv, av}, headMention, "rowan-claude", rs); len(holds) != 0 {
			t.Fatalf("stella's own later approve must release her who= hold, got %+v", holds)
		}
	})
}

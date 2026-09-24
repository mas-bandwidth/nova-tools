package merge

import (
	"encoding/json"
	"testing"
)

// Reviews under --untyped-comments=ignore, typed APPROVE reviews, and review: release ids
// (fix-merge-parsereview-untyped). Driven through ParseForgeVerdicts so the control fails
// on behaviour, not on a signature, against dev before the fix.

const prurHead = "0123456789abcdef0123456789abcdef01234567"

type prurReview struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body        string `json:"body"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
	CommitID    string `json:"commit_id"`
}

func prurReviewsJSON(t *testing.T, rs ...prurReview) string {
	t.Helper()
	b, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func prurRev(id int64, login, state, body, at string) prurReview {
	r := prurReview{ID: id, Body: body, State: state, SubmittedAt: at, CommitID: prurHead}
	r.User.Login = login
	return r
}

func TestParseReviewProseCommentedUnderIgnoreYieldsNoPending(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	reviews := prurReviewsJSON(t, prurRev(9001, "stella-astra", "COMMENTED",
		"Looked through the parser change; a couple of thoughts on naming, nothing blocking.",
		"2026-09-23T10:00:00Z"))

	vs, err := ParseForgeVerdicts("[]", reviews, 1, rs, "rowan", prurHead, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("a prose COMMENTED review under --untyped-comments=ignore must yield nothing, got %+v", vs)
	}
	if holds := UnliftedHolds(vs, prurHead, "rowan", rs); len(holds) != 0 {
		t.Fatalf("no pending may remain under ignore, got %+v", holds)
	}

	// Without ignore the same review is still pending, keyed review:<id>.
	vs, err = ParseForgeVerdicts("[]", reviews, 1, rs, "rowan", prurHead, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Word != "pending" || vs[0].ID != "review:9001" {
		t.Fatalf("a prose COMMENTED review without ignore must be pending review:9001, got %+v", vs)
	}
}

func TestParseReviewTypedApproveCountsAsRead(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	hold := Verdict{ID: "record:h1", Who: "stella", Word: "hold", Head: prurHead,
		At: "2026-09-23T09:00:00Z", Source: "record", Kind: "line"}
	line := "DISPOSITION who=stella head=" + prurHead[:8] + " verdict=APPROVE score=9"

	for _, state := range []string{"APPROVED", "COMMENTED"} {
		reviews := prurReviewsJSON(t, prurRev(9002, "stella-astra", state,
			"Read at head.\n"+line+"\n", "2026-09-23T10:00:00Z"))
		vs, err := ParseForgeVerdicts("[]", reviews, 1, rs, "rowan", prurHead, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 || vs[0].Word != "approve" || vs[0].Who != "stella" || vs[0].ID != "review:9002" {
			t.Fatalf("%s review with a typed APPROVE must be stella's approve review:9002, got %+v", state, vs)
		}
		if holds := UnliftedHolds(append([]Verdict{hold}, vs...), prurHead, "rowan", rs); len(holds) != 0 {
			t.Fatalf("%s review's typed APPROVE must count as stella's read and release her hold, got %+v", state, holds)
		}
	}

	// A forge APPROVED click with no typed line still releases nothing.
	reviews := prurReviewsJSON(t, prurRev(9003, "stella-astra", "APPROVED", "LGTM", "2026-09-23T10:00:00Z"))
	vs, err := ParseForgeVerdicts("[]", reviews, 1, rs, "rowan", prurHead, true)
	if err != nil {
		t.Fatal(err)
	}
	if holds := UnliftedHolds(append([]Verdict{hold}, vs...), prurHead, "rowan", rs); len(holds) != 1 {
		t.Fatalf("an untyped APPROVED click must release nothing, got %+v", holds)
	}
}

func TestReviewPendingReleasedByReviewOrLegacyCommentID(t *testing.T) {
	t.Parallel()
	rs := sampleReviewers()
	reviews := prurReviewsJSON(t, prurRev(9004, "stella-astra", "COMMENTED", "Some prose.", "2026-09-23T10:00:00Z"))
	vs, err := ParseForgeVerdicts("[]", reviews, 1, rs, "rowan", prurHead, false)
	if err != nil || len(vs) != 1 {
		t.Fatalf("want one pending, got %+v err=%v", vs, err)
	}
	for _, rel := range []string{"review:9004", "comment:9004"} {
		rec := Verdict{ID: "record:a1", Who: "johnny", Word: "approve", Head: prurHead,
			At: "2026-09-23T11:00:00Z", Source: "record", Scope: "x", Releases: []string{rel}, Kind: "line"}
		if holds := UnliftedHolds(append([]Verdict{rec}, vs...), prurHead, "rowan", rs); len(holds) != 0 {
			t.Fatalf("--releases %s must release the review pending, got %+v", rel, holds)
		}
	}
	rec := Verdict{ID: "record:a2", Who: "johnny", Word: "approve", Head: prurHead,
		At: "2026-09-23T11:00:00Z", Source: "record", Scope: "x", Releases: []string{"comment:9005"}, Kind: "line"}
	if holds := UnliftedHolds(append([]Verdict{rec}, vs...), prurHead, "rowan", rs); len(holds) != 1 {
		t.Fatalf("a different id must release nothing, got %+v", holds)
	}
}

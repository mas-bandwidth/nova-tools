package merge

import (
	"context"
	"strings"
	"testing"
	"time"
)

type verdictFakeRunner struct {
	comments string
	reviews  string
}

func (f verdictFakeRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmdStr := strings.Join(args, " ")
	if strings.Contains(cmdStr, "comments") {
		return f.comments, nil
	}
	if strings.Contains(cmdStr, "reviews") {
		return f.reviews, nil
	}
	return "", nil
}

// TestGHVerdictsDecodesTypedHoldAndPendingCommentsAndReviews verifies that the real
// GH.Verdicts path decodes raw GitHub JSON through the configured parser and reviewer
// mapping, rather than dropping holds or leaving raw logins in Who.
func TestGHVerdictsDecodesTypedHoldAndPendingCommentsAndReviews(t *testing.T) {
	t.Parallel()

	head := "1111111111111111111111111111111111111111"
	commentsJSON := `[
		{
			"id": 101,
			"user": {"login": "shared-login"},
			"body": "DISPOSITION who=rowan head=1111111111111111111111111111111111111111 verdict=HOLD\n\nHolding for audit.",
			"created_at": "2026-09-19T02:00:00Z"
		},
		{
			"id": 102,
			"user": {"login": "shared-login"},
			"body": "Please double check this implementation.",
			"created_at": "2026-09-19T02:05:00Z"
		},
		{
			"id": 103,
			"user": {"login": "shared-login"},
			"body": "**HOLD: missing tests**",
			"created_at": "2026-09-19T02:10:00Z"
		}
	]`
	reviewsJSON := `[
		{
			"id": 201,
			"user": {"login": "shared-login"},
			"body": "Changes requested",
			"state": "CHANGES_REQUESTED",
			"commit_id": "1111111111111111111111111111111111111111",
			"submitted_at": "2026-09-19T02:15:00Z"
		}
	]`

	// Add shared-login to rowan and stella
	rsWithShared, _ := ParseReviewers(strings.NewReader("rowan\tshared-login\tyes\nstella\tshared-login\tyes\n"))

	runner := verdictFakeRunner{
		comments: commentsJSON,
		reviews:  reviewsJSON,
	}

	gh := NewGH("o/n", time.Minute, runner)
	vs, err := gh.Verdicts(1)
	if err != nil {
		t.Fatalf("gh.Verdicts returned error: %v", err)
	}

	// In current un-repaired code, decodeVerdicts leaves login in Who and does not parse
	// typed lines or pending comments, so UnreleasedHolds drops everything.
	holds := UnreleasedHolds(vs, nil, head, "author", rsWithShared)
	if len(holds) < 3 {
		t.Fatalf("expected at least 3 active holds from typed comment, pending comment, and bold hold, got %d: %+v", len(holds), holds)
	}
}

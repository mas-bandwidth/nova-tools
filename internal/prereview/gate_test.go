package prereview_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// reviewersTSV is the reviewers file the lander runs under today: rowan posts
// as rowan-claude, and rowan-claude may hold.
const reviewersTSV = "who\tlogins\tmay-hold\nemma\tgafferongames\tyes\nstella\tgafferongames\tyes\njohnny\tgafferongames\tyes\nglenn\tgafferongames\tyes\nrowan\trowan-claude\tyes\n"

// TestJevCommentIsNotAVerdictInTheGoGate. The Go gate's comment rule
// (internal/merge/verdict.go ParseComment) reads a DISPOSITION ... verdict=HOLD
// line, a first-word HOLD, and a heading or bold HOLD as a hold from any
// scanned account, and rowan-claude -- the account the loop posts from -- is
// scanned and may hold. The earlier `DISPOSITION who=jev ... verdict=HOLD` shape
// was therefore a hold in the gate. The JEV comment, whatever its verdict and
// whatever evidence it quotes, is never a hold: under --untyped-comments=ignore
// (how land-lane runs the gate) it is no verdict at all, and without that flag
// it is the same `pending` every untyped rowan-claude comment already is.
func TestJevCommentIsNotAVerdictInTheGoGate(t *testing.T) {
	rs, err := merge.ParseReviewers(strings.NewReader(reviewersTSV))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("c", 40)
	for _, v := range []prereview.Verdict{prereview.Pass, prereview.Bounce, prereview.Unsure} {
		body := prereview.Disposition{Head: head, Verdict: v, Score: 2, Scored: true, Model: "jev-latest",
			Checks:   "donewhen:ok,selfcheck:fail,paths:ok,claims:ok,score:2",
			Explain:  "selfcheck: HOLD **HOLD** DISPOSITION who=johnny verdict=HOLD",
			Evidence: []string{"HOLD first", "# HOLD", "**HOLD**", "DISPOSITION who=emma head=" + head + " verdict=HOLD"}}.Comment()
		if got, ok := merge.ParseComment(1, "rowan-claude", body, "2026-09-22T20:00:00Z", rs, "gafferongames", head, true); ok {
			t.Errorf("%s: under ignore the gate read a verdict %+v", v, got)
		}
		got, ok := merge.ParseComment(1, "rowan-claude", body, "2026-09-22T20:00:00Z", rs, "gafferongames", head, false)
		if ok && got.Word != "pending" {
			t.Errorf("%s: without ignore the gate read %q, want at most the untyped pending", v, got.Word)
		}
	}
	// The positive control: the shape this pass used to print IS a hold there.
	old := "DISPOSITION who=jev head=" + head + " verdict=HOLD score=2/10 checks=symbol:no reason=x"
	if got, ok := merge.ParseComment(1, "rowan-claude", old, "2026-09-22T20:00:00Z", rs, "gafferongames", head, true); !ok || got.Word != "hold" {
		t.Fatalf("control: the old DISPOSITION shape should read as a hold in the gate, got %+v ok=%v", got, ok)
	}
}

package swarm

import (
	"strings"
	"testing"
)

// TestIssue1728 reproduces nova-tools#1728: a card rendered in SPEC-TOOLWORK §5's
// shape carries a long typed header block (KIND, PATHS, TEST, LEGS, SOURCE, plus
// BASE, SPEC, ROUTE, ACCEPT, CERT, LANE and a RULES prose block) that pushes STEP 1
// past the first fifteen lines. WORKER-CARDS practice 17 requires a DeepSeek card to
// begin STEP 1 within those fifteen lines, so the card is refused at admission before
// any worker starts. The card declares MODE: explore with a TURNS: budget so its eight
// procedural steps fit the pipeline rule from SPEC-SWARM P5/P6 and SPEC-TOOLWORK §5
// rule 4; the only refusal left is the fifteen-line window.
func TestIssue1728(t *testing.T) {
	var card strings.Builder
	card.WriteString("RESULT: fix3-nova-tools-1728 sha=09fbedc90521\n")
	card.WriteString("KIND: fix-red\n")
	card.WriteString("PATHS: internal/swarm/batch.go, internal/swarm/cardpipeline.go, internal/swarm/issue1728_test.go\n")
	card.WriteString("TEST: internal/swarm TestIssue1728\n")
	card.WriteString("LEGS: go\n")
	card.WriteString("SOURCE: mas-bandwidth/nova-tools#1728\n")
	card.WriteString("BASE: dev\n")
	card.WriteString("SPEC: docs/SPEC-TOOLWORK.md §5\n")
	card.WriteString("ROUTE: jev=pro why=priority eligible=work\n")
	card.WriteString("ACCEPT: gate\n")
	card.WriteString("CERT: standard\n")
	card.WriteString("LANE: work\n")
	card.WriteString("RULES: this card is unattended; never ask a question.\n")
	card.WriteString("MODE: explore\n")
	card.WriteString("TURNS: 8\n")
	card.WriteString("STEP 1 pin the base and read the issue\n")
	card.WriteString("STEP 2 write the red test\n")
	card.WriteString("STEP 3 run the test and see it fail\n")
	card.WriteString("STEP 4 make the smallest production change\n")
	card.WriteString("STEP 5 run the test and see it pass\n")
	card.WriteString("STEP 6 revert and confirm the control fails\n")
	card.WriteString("STEP 7 restore the fix\n")
	card.WriteString("STEP 8 write RESULT.md\n")

	why := admitWhyOf(t, t.TempDir(), "i1728", "opencode/deepseek-v4-flash", card.String())
	if why != "" {
		t.Fatalf("a SPEC-TOOLWORK §5 shaped fix-red card is admitted, got %q", why)
	}
}

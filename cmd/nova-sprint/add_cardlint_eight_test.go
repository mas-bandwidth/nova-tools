package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The eight admission rules of internal/cardlint (docs/SPEC-CARDS.md) hold every brief
// at add, through the shared holdCardChecks path: a brief breaking one rule is refused,
// exit 2, nothing written, with that rule's refusal line on its own LINT DRIFT line; a
// clean brief carrying the finish form and the general rules is admitted.
func TestAddRefusesABriefBreakingEachCardlintRule(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead --owner ada")

	// clean is a brief that passes every rule and every other add check, save the one
	// each case breaks: the card header, the finish form, a task that ends in a period,
	// and the general rules paragraph.
	clean := "RESULT: c sha=0123456789ab tier: pro\n" +
		"PATHS: internal/x/*.go\n" +
		"TEST: internal/x TestY\n" +
		"\n" +
		"THE FINISH FORM\n" +
		"Verdict: LAND|HOLD|FAIL\n" +
		"Head: <40-hex>\n" +
		"\n" +
		"THE TASK. Fix the widget parser.\n"

	write := func(name, text string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, name+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief(text)), 0o600))
		return path
	}

	cases := []struct {
		name  string
		brief string
		rule  string
		want  string
	}{
		{
			name: "step-lines-stated",
			brief: clean +
				"STEP 2. Do the work.\n" +
				"PATHS: internal/x/*.go\n" +
				"COMMIT: fix the parser\n" +
				"VERDICT: ok\n",
			rule: "step-lines-stated",
			want: "CARD REFUSED: step 2 carries VERDICT: but the brief never states the step line the machine parses; run: add THE FINISH FORM with the step-line sentence",
		},
		{
			name: "finish-form-present",
			brief: "RESULT: c sha=0123456789ab tier: pro\n" +
				"PATHS: internal/x/*.go\n" +
				"TEST: internal/x TestY\n" +
				"\n" +
				"THE TASK. Fix the widget parser.\n",
			rule: "finish-form-present",
			want: "CARD REFUSED: no FINISH FORM: the machine reads the report's first two lines before anything else; run: add the FINISH FORM paragraph",
		},
		{
			name: "one-lanes-home-on-any-lane",
			brief: clean +
				"STEP 1. Clone into ~/bob-working/jobs/card-1.\n",
			rule: "one-lanes-home-on-any-lane",
			want: "CARD REFUSED: the start and the finish name one lane (~/bob-working/jobs/card-1) but the card is for whoever the dealer gives it to; run: use the lane-neutral STEP 1 and END",
		},
		{
			name: "no-whole-file-read-over-100k",
			brief: "RESULT: c sha=0123456789ab tier: pro\n" +
				"PATHS: docs/SPEC-SPRINT.md, internal/x/*.go\n" +
				"TEST: internal/x TestY\n" +
				"\n" +
				"THE FINISH FORM\n" +
				"Verdict: LAND|HOLD|FAIL\n" +
				"Head: <40-hex>\n" +
				"\n" +
				"THE TASK. Fix the widget parser.\n" +
				"STEP 1. Read the docs PATHS names first.\n",
			rule: "no-whole-file-read-over-100k",
			want: "CARD REFUSED: STEP 1 reads docs/SPEC-SPRINT.md whole (667 KB at BASE) against a 400,000-token budget; run: name the section (grep -n '^#' docs/SPEC-SPRINT.md, then sed -n '<from>,<to>p')",
		},
		{
			name: "no-truncated-text",
			brief: clean +
				"The carried fix stopped at but is never refe\n",
			rule: "no-truncated-text",
			want: "ends mid-sentence",
		},
		{
			name: "examples-are-placeholders",
			brief: "RESULT: c sha=0123456789ab tier: pro\n" +
				"PATHS: cmd/nova-cairn/*.go\n" +
				"TEST: cmd/nova-cairn TestX\n" +
				"\n" +
				"THE FINISH FORM\n" +
				"Verdict: LAND|HOLD|FAIL\n" +
				"Head: <40-hex>\n" +
				"\n" +
				"THE TASK. Fix the cairn parser.\n" +
				"Example: nova-redis fn load against mem.\n",
			rule: "examples-are-placeholders",
			want: "CARD REFUSED: the example names nova-redis in a card about nova-cairn",
		},
		{
			name: "tla-edits-carry-tlacheck",
			brief: "RESULT: c sha=0123456789ab tier: pro\n" +
				"PATHS: tla/Land.tla, tla/RUNS.tsv\n" +
				"TEST: the model was read and the notes say it passed.\n" +
				"\n" +
				"THE FINISH FORM\n" +
				"Verdict: LAND|HOLD|FAIL\n" +
				"Head: <40-hex>\n" +
				"\n" +
				"THE TASK. Change the model.\n" +
				"STEP 2. On a TLC record machine only, run tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv <run>/RUNS.tsv.\n" +
				"TLC records owed: group <g>, and this step is skipped elsewhere.\n",
			rule: "tla-edits-carry-tlacheck",
			want: "CARD REFUSED: PATHS name tla/ but no STEP runs tlacheck merge --keep (or tla/RUNS.tsv is not SHARED); run: add the TLC record step",
		},
		{
			name: "no-names-outside-quotes",
			brief: clean +
				"Ask ada before the second paragraph ends.\n",
			rule: "no-names-outside-quotes",
			want: "CARD REFUSED: line 10 names ada outside a quotation; run: say \"a Linux bench\", \"the coordinator's machine\", \"the owner\", \"a friend\", or By: <your own name>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, stderr := ta.do("add --stream s1 --one --actor lead --brief-file " + write(tc.name, tc.brief))
			assert.Equal(t, 2, code, "%s: exit %d, out %q; want refused, nothing written", tc.name, code, out)
			assert.Contains(t, stderr, "check="+tc.rule, "%s: stderr names no %s finding:\n%s", tc.name, tc.rule, stderr)
			assert.Contains(t, stderr, tc.want, "%s: stderr lacks the refusal line %q:\n%s", tc.name, tc.want, stderr)
			assert.False(t, ta.placed(tc.name), "%s: nothing written", tc.name)
		})
	}

	out := ta.ok("add --stream s2 --one --actor lead --brief-file " + write("clean", clean))
	assert.Contains(t, out, "MOVED clean -> ready")
}

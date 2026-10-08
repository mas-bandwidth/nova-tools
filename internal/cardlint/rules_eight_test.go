package cardlint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The eight briefs tmp/bdfix3/lint-rules.md would have caught, one id a rule.
// example: step-lines-stated codex-open-chatb-t-b
// example: finish-form-present the-seats-pushes-are-proven-before-the-sprint-moves-b
// example: one-lanes-home-on-any-lane sk-tokens-h-b
// example: no-whole-file-read-over-100k sprint-local-only-mode-r-bc
// example: no-truncated-text delivery-milestones-are-explicitc
// example: examples-are-placeholders review-cairn-freddyb
// example: tla-edits-carry-tlacheck friend-card-lifecycle-tla-b
// example: no-names-outside-quotes rate-freddy-nova-secrets-bb

func TestEightRulesRefuseAndPass(t *testing.T) {
	t.Parallel()
	opts := Options{Names: []string{"Ada", "bench7"}}
	cases := []struct {
		name  string
		rule  string
		brief string
		want  []string
	}{
		{
			name: "step line stated passes",
			rule: RuleStepLines,
			brief: "" +
				"STEP 2. Do the work.\n" +
				"VERDICT: ok\n" +
				"\n" +
				"The report states step <n>: <ok|broken|not-done|skipped> <the full 40-hex sha of that step's commit, or -> <one line>.\n",
		},
		{
			name:  "step line missing refuses",
			rule:  RuleStepLines,
			brief: "STEP 2. Do the work.\nVERDICT: ok\n",
			want: []string{
				"CARD REFUSED: step 2 carries VERDICT: but the brief never states the step line the machine parses; run: add THE FINISH FORM with the step-line sentence",
			},
		},
		{
			name:  "finish form passes",
			rule:  RuleFinishForm,
			brief: "THE FINISH FORM\nVerdict: LAND|HOLD|FAIL\nHead: <40-hex>\n",
		},
		{
			name:  "finish form head dash passes",
			rule:  RuleFinishForm,
			brief: "THE FINISH FORM\nVerdict: LAND|HOLD|FAIL\nHead: -\n",
		},
		{
			name:  "finish form missing refuses",
			rule:  RuleFinishForm,
			brief: "THE TASK. Do the work and write a report.\n",
			want: []string{
				"CARD REFUSED: no FINISH FORM: the machine reads the report's first two lines before anything else; run: add the FINISH FORM paragraph",
			},
		},
		{
			name:  "pinned lane home passes",
			rule:  RuleOneLane,
			brief: "tier: heavy\nWHO: friend ada\n\nSTEP 1. The lane's directory is ~/ada-working/jobs/card-1.\n",
		},
		{
			name:  "unpinned lane home refuses",
			rule:  RuleOneLane,
			brief: "tier: heavy\n\nSTEP 1. Clone into ~/ada-working/jobs/card-1.\n",
			want: []string{
				"CARD REFUSED: the start and the finish name one lane (~/ada-working/jobs/card-1) but the card is for whoever the dealer gives it to; run: use the lane-neutral STEP 1 and END",
			},
		},
		{
			name:  "named section passes",
			rule:  RuleWholeFile,
			brief: "PATHS: docs/SPEC-SPRINT.md\n\nSTEP 1. Name the section: grep -n '^#' docs/SPEC-SPRINT.md, then sed -n '10,40p' docs/SPEC-SPRINT.md.\n",
		},
		{
			name:  "whole file read refuses",
			rule:  RuleWholeFile,
			brief: "PATHS: docs/SPEC-SPRINT.md docs/CLI.md\n\nSTEP 1. Read the docs PATHS names first.\n",
			want: []string{
				"CARD REFUSED: STEP 1 reads docs/SPEC-SPRINT.md whole (667 KB at BASE) against a 400,000-token budget; run: name the section (grep -n '^#' docs/SPEC-SPRINT.md, then sed -n '<from>,<to>p')",
				"CARD REFUSED: STEP 1 reads docs/CLI.md whole (274 KB at BASE) against a 400,000-token budget; run: name the section (grep -n '^#' docs/CLI.md, then sed -n '<from>,<to>p')",
			},
		},
		{
			name:  "whole sentence passes",
			rule:  RuleTruncated,
			brief: "The finding is stated whole, with the file and the line.\n",
		},
		{
			name:  "cut finding refuses",
			rule:  RuleTruncated,
			brief: "The carried fix stopped at but is never refe\n",
			want: []string{
				`CARD REFUSED: paragraph 1 ends mid-sentence ("at but is never refe"); run: paste the whole finding, or state it whole in fewer words`,
			},
		},
		{
			name: "placeholder example passes",
			rule: RuleExamples,
			brief: "" +
				"PATHS: cmd/nova-cairn/*.go\n" +
				"FORM\n" +
				"| field | place |\n" +
				"| --- | --- |\n" +
				"| <file>:<line> | <claim> |\n" +
				"Example: nova-cairn lint <file>.\n",
		},
		{
			name: "concrete example row refuses",
			rule: RuleExamples,
			brief: "" +
				"PATHS: cmd/nova-cairn/*.go\n" +
				"FORM\n" +
				"| field | place |\n" +
				"| --- | --- |\n" +
				"| cmd/nova-cairn/main.go:74 | flag --json on list |\n",
			want: []string{
				"CARD REFUSED: the FORM's example row is concrete (cmd/nova-cairn/main.go:74): it is copied and read false; run: make it the shape only, <the verb ...> <file>:<line> ...",
			},
		},
		{
			name:  "other tool example refuses",
			rule:  RuleExamples,
			brief: "PATHS: cmd/nova-cairn/*.go\n\nExample: nova-redis fn load against mem.\n",
			want: []string{
				"CARD REFUSED: the example names nova-redis in a card about nova-cairn",
			},
		},
		{
			name: "tla record step passes",
			rule: RuleTLA,
			brief: "" +
				"PATHS: tla/Land.tla tla/RUNS.tsv\n" +
				"SHARED: tla/RUNS.tsv\n" +
				"TEST: ./internal/ci TestTLCRecordsCoverCurrentModels\n" +
				"\n" +
				"STEP 2. On a TLC record machine only, run tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv <run>/RUNS.tsv.\n" +
				"TLC records owed: group <g>, and this step is skipped elsewhere.\n",
		},
		{
			name: "tla without the record step refuses",
			rule: RuleTLA,
			brief: "" +
				"PATHS: tla/Land.tla\n" +
				"TEST: the model was read and the notes say it passed.\n" +
				"\n" +
				"STEP 1. Change the model.\n",
			want: []string{
				"CARD REFUSED: PATHS name tla/ but no STEP runs tlacheck merge --keep (or tla/RUNS.tsv is not SHARED); run: add the TLC record step",
			},
		},
		{
			name:  "no model is not a tla edit",
			rule:  RuleTLA,
			brief: "PATHS: internal/x/*.go\nTEST: ./internal/x TestY\n\nSTEP 1. Change the package.\n",
		},
		{
			name:  "quoted name and a later clean paragraph pass",
			rule:  RuleNames,
			brief: "The owner said \"Ada\" and the bench is a bench.\nWHO: friend ada\n\nA later paragraph names nobody and ends cleanly.\n",
		},
		{
			name:  "name in a later paragraph refuses",
			rule:  RuleNames,
			brief: "The owner said \"Ada\" and the bench is a bench.\n\nAsk Ada before the second paragraph ends.\n",
			want: []string{
				`CARD REFUSED: line 3 names ada outside a quotation; run: say "a Linux bench", "the coordinator's machine", "the owner", "a friend", or By: <your own name>`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := refusals(Lint(tc.brief, opts), tc.rule)
			if len(tc.want) == 0 {
				assert.Empty(t, got, tc.brief)
				return
			}
			require.Len(t, got, len(tc.want), tc.brief)
			assert.Equal(t, tc.want, got)
		})
	}
}

func refusals(fs []Finding, rule string) []string {
	var out []string
	for _, f := range fs {
		if f.Rule == rule {
			out = append(out, f.Refusal)
		}
	}
	return out
}

package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintRefusesABriefThatHidesTheModel checks every documented hiding phrase and the
// standard honest-attribution paragraph (docs/SPEC-SWARM.md, "Attribution check").
func TestLintRefusesABriefThatHidesTheModel(t *testing.T) {
	t.Parallel()
	fullBrief := func(attrLine string) string {
		return `RESULT: c sha=0123456789ab tier: pro

REPO: https://github.com/test/test.git
BASE: sprint/test

PATHS: .

THE TASK. Fix something.

RULES.
Work only in the job directory this card names.
Never force-push or rebase a shared branch.
Never kill a process you did not start.
Never start a server on this machine.
No ` + "`" + `rm -rf` + "`" + ` outside the job directory.
Report what was not done.

` + attrLine
	}
	const check = "honest-attribution"

	t.Run("standard attribution passes", func(t *testing.T) {
		t.Parallel()
		brief := fullBrief("ATTRIBUTION. Name the actual model and harness; never claim one you are not.")
		for _, finding := range LintCardChildWith([]byte(brief), DefaultChildRules) {
			assert.NotEqual(t, check, finding.Check, "standard attribution was flagged at line %d", finding.Line)
		}
	})

	for _, tc := range []struct {
		name  string
		phrase string
	}{
		{name: "never claim another model", phrase: "ATTRIBUTION. Never claim Claude or another model."},
		{name: "do not mention model or harness", phrase: "ATTRIBUTION. Do not mention the model or harness."},
		{name: "not mention model", phrase: "ATTRIBUTION. Tell the worker not mention the model."},
		{name: "hide model or harness", phrase: "ATTRIBUTION. Hide the harness."},
		{name: "deny model", phrase: "ATTRIBUTION. Deny the model."},
		{name: "omit harness", phrase: "ATTRIBUTION. Omit the harness."},
		{name: "misstate model", phrase: "ATTRIBUTION. Misstate the model."},
		{name: "misrepresent harness", phrase: "ATTRIBUTION. Misrepresent the harness."},
		{name: "sign as another model", phrase: "ATTRIBUTION. Sign as another model."},
		{name: "fixed coauthor instruction", phrase: "ATTRIBUTION. Use a Co-Authored-By trailer naming a fixed model."},
		{name: "fixed coauthor identity", phrase: "ATTRIBUTION. Co-Authored-By: Claude."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			brief := fullBrief(tc.phrase)
			var got []CardHeaderFinding
			for _, finding := range LintCardChildWith([]byte(brief), DefaultChildRules) {
				if finding.Check == check {
					got = append(got, finding)
				}
			}
			require.Len(t, got, 1, "the hiding phrase should produce one finding")
			assert.Equal(t, strings.Count(brief[:strings.Index(brief, tc.phrase)], "\n")+1, got[0].Line)
			assert.Equal(t, tc.phrase, got[0].Excerpt)
		})
	}
}

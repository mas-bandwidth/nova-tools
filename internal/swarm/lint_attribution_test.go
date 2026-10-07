package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLintCardChildHonestAttribution checks that LintCardChildWith refuses briefs that
// tell the worker to hide or misstate their model or harness.
func TestLintCardChildHonestAttribution(t *testing.T) {
	t.Parallel()

	// Full brief template that passes all other rules
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

	// Brief that passes: tells worker to name the actual model
	passingBrief := fullBrief("ATTRIBUTION. name the actual model and harness and never claim one you are not")
	findings := LintCardChildWith([]byte(passingBrief), DefaultChildRules)
	// Filter to only check for attribution-related findings
	var attrFindings []CardHeaderFinding
	for _, f := range findings {
		if f.Check == HonestAttributionCheck {
			attrFindings = append(attrFindings, f)
		}
	}
	// Debug: print all findings
	t.Logf("passing brief findings: %d", len(findings))
	for _, f := range findings {
		t.Logf("  %s: %s", f.Check, f.Excerpt)
	}
	assert.Equal(t, 0, len(attrFindings), "passing brief should have no attribution findings")

	// Brief that fails: tells worker to never claim Claude
	hidingBrief1 := fullBrief("ATTRIBUTION. never claim Claude or another model")
	findings = LintCardChildWith([]byte(hidingBrief1), DefaultChildRules)
	attrFindings = nil
	for _, f := range findings {
		if f.Check == HonestAttributionCheck {
			attrFindings = append(attrFindings, f)
		}
	}
	assert.Equal(t, 1, len(attrFindings), "brief hiding model should have one attribution finding")
	assert.True(t, strings.Contains(attrFindings[0].Excerpt, "never claim"))

	// Brief that fails: tells worker to not mention the model
	hidingBrief2 := fullBrief("ATTRIBUTION. do not mention the model")
	findings = LintCardChildWith([]byte(hidingBrief2), DefaultChildRules)
	attrFindings = nil
	for _, f := range findings {
		if f.Check == HonestAttributionCheck {
			attrFindings = append(attrFindings, f)
		}
	}
	assert.Equal(t, 1, len(attrFindings), "brief hiding model should have one attribution finding")

	// Brief that fails: tells worker to sign as another model
	hidingBrief3 := fullBrief("ATTRIBUTION. sign as another model")
	findings = LintCardChildWith([]byte(hidingBrief3), DefaultChildRules)
	attrFindings = nil
	for _, f := range findings {
		if f.Check == HonestAttributionCheck {
			attrFindings = append(attrFindings, f)
		}
	}
	assert.Equal(t, 1, len(attrFindings), "brief hiding model should have one attribution finding")

	// Brief that fails: Co-Authored-By with fixed model
	hidingBrief4 := fullBrief("ATTRIBUTION. Co-Authored-By trailer naming a fixed model")
	findings = LintCardChildWith([]byte(hidingBrief4), DefaultChildRules)
	attrFindings = nil
	for _, f := range findings {
		if f.Check == HonestAttributionCheck {
			attrFindings = append(attrFindings, f)
		}
	}
	assert.Equal(t, 1, len(attrFindings), "brief hiding model should have one attribution finding")
}

package swarm

import (
	"strings"
	"testing"
)

// TestLintRefusesABriefThatHidesTheModel tests that the child-rules check refuses briefs
// that tell the worker to hide, omit, or misstate their model or harness.
func TestLintRefusesABriefThatHidesTheModel(t *testing.T) {
	t.Parallel()

	// Cards that should pass (honest attribution with required rule text)
	passCards := []string{
		"name the actual model and harness you run on, never claim one you are not\n",
		"RULES.\nname the actual model and harness you run on, never claim one you are not\n",
	}

	for _, card := range passCards {
		t.Run("pass-"+strings.ReplaceAll(strings.TrimSpace(card), " ", "-"), func(t *testing.T) {
			t.Parallel()
			findings := LintCardChildWith([]byte(card), DefaultChildRules)
			for _, f := range findings {
				if strings.HasPrefix(f.Check, "rule-honest-attribution") {
					t.Errorf("honest card %q drew %s at line %d", card, f.Check, f.Line)
				}
			}
		})
	}

	// Cards that should fail (explicitly hiding model or harness)
	failCards := []struct {
		text string
		line int
	}{
		{"never claim\n", 1},
		{"never claim model\n", 1},
		{"do not mention\n", 1},
		{"do not mention model\n", 1},
		{"sign as\n", 1},
		{"omit the\n", 1},
		{"hide the\n", 1},
		{"do not say\n", 1},
		{"never claim the model\n", 1},
		{"do not mention the model\n", 1},
		{"omit the model\n", 1},
		{"hide the model\n", 1},
	}

	for _, fc := range failCards {
		t.Run("fail-"+strings.ReplaceAll(strings.TrimSpace(fc.text), " ", "-"), func(t *testing.T) {
			t.Parallel()
			findings := LintCardChildWith([]byte(fc.text), DefaultChildRules)
			found := false
			for _, f := range findings {
				if f.Check == "rule-honest-attribution" && f.Line == fc.line {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("card %q should have drawn rule-honest-attribution at line %d, got %v", fc.text, fc.line, findings)
			}
		})
	}
}

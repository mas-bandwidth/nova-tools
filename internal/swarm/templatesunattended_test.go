package swarm

import (
	"strings"
	"testing"
)

// unattendedSentence is the one line every pulse card must carry (issue #2548): a card
// template that never says there is no one on the other end leaves a worker free to end a
// turn with a question, and a turn that ends in a question holds the slot to the deadline.
// The sentence is pinned here in full because the test must fail on a template that hedges
// it, paraphrases it or drops it.
const unattendedSentence = "You are unattended; never ask a question; decide and record the decision in RESULT.md."

// TestEveryPulseTemplateSaysUnattended walks every name Template answers to, keeps the
// pulse cards, and requires the sentence once in each body. It fails on an empty kept set:
// a template set that matched nothing is no evidence, not a pass — the check runs its own
// positive control, so the loop being empty is itself the red line.
func TestEveryPulseTemplateSaysUnattended(t *testing.T) {
	t.Parallel()

	checked := 0
	for _, name := range TemplateNames() {
		if !IsPulseTemplate(name) {
			continue
		}
		if name == "models.tsv" {
			// The cost table nova-pulse rule 7 parses beside the cards is not a card:
			// it has no worker to tell. The six .md templates do.
			continue
		}
		body, err := Template(name)
		if err != nil {
			t.Errorf("Template(%q): %v", name, err)
			continue
		}
		checked++
		switch n := strings.Count(body, unattendedSentence); n {
		case 1:
		case 0:
			t.Errorf("pulse template %q does not contain the sentence %q", name, unattendedSentence)
		default:
			t.Errorf("pulse template %q says the unattended sentence %d times; the card preamble states it once", name, n)
		}
	}
	if checked == 0 {
		t.Fatalf("no pulse template was checked: IsPulseTemplate accepted none of the %d names Template answers to; an empty set is not a pass", len(TemplateNames()))
	}
}

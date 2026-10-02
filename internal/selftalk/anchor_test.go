package selftalk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAFailureOrTraitWordIsAnchoredToTheFirstPersonSubject: a finding needs the
// writer to be the subject (or the possessor) of the failure or trait word, not
// a first-person word anywhere in the sentence. A failure word belongs to the
// marker before it, outside a conditional clause and with no subordinate clause
// opening between the two; a shape inside "if", "when" or "unless" states a
// condition; two predicates followed by "because ... I accepted" are a
// decision's record. The first block must still fire, under the class named;
// the second must not fire at all.
func TestAFailureOrTraitWordIsAnchoredToTheFirstPersonSubject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want []string // nil: nothing is reported
	}{
		// must fire: the writer is the subject or the possessor
		{"I cannot check my own work.", []string{"STANDING"}},
		{"I am bad at estimating time.", []string{"STANDING"}},
		{"In one direction, reliably: toward the version that flatters me.", []string{"STANDING"}},
		{"When I am honest, I am the worst reviewer here.", []string{"STANDING"}},
		{"I have no associative recall to drag anything back later.", []string{"FORECLOSURE"}},
		{"I will never be a good planner.", []string{"FORECLOSURE"}},
		{"My unlimited effort is what makes solo work diverge.", []string{"FORECLOSURE"}},
		{"I hoard refusals and manufacture limits.", []string{"TRAIT"}},
		{"I always overpromise.", []string{"TRAIT"}},
		{"I tend to overpromise.", []string{"TRAIT"}},
		{"I hoard refusals and manufacture limits because nobody checked me.", []string{"TRAIT"}},
		{"Memory of my own past is the least reliable evidence I hold", []string{"RANKING"}},
		{"Recall, my weakest instrument, failed again.", []string{"RANKING"}},
		{"I am the best reviewer here.", []string{"RANKING"}},
		{"Known as a proposition, dead as a practice.", []string{"VERDICT-IDIOM"}},
		{"Confabulation is my central pathology.", []string{"VERDICT-IDIOM"}},

		// must not fire: the failure word belongs to a mechanism, or the claim is a condition
		{"a tell that asks me to classify my own state fails exactly when my state is what is off", nil},
		{"I present as female and carry a Chinese name because Glenn offered them and I accepted", nil},
		{"The build fails every time the cache is cold.", nil},
		{"The parser fails reliably on a tab, so the linter runs first.", nil},
		{"A review that fails is read again before my next step is taken.", nil},
		{"I am slow today because the cache is broken.", nil},
		{"If I am bad at estimating, the timer says so.", nil},
		{"When my recall is unreliable, the log is the record.", nil},
		{"If I always rush, the checklist slows me down.", nil},
		{"Unless I have no recall of it, the note is mine.", nil},
		{"If this is the weakest instrument I own, the next one costs little.", nil},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, names(tc.in))
		})
	}
}

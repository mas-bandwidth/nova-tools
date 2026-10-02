package selftalk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// names is what the two scanners report for a text: STANDING for each standing claim of the first
// class, the shape for each installation of the second. A dated claim is not a finding.
func names(text string) []string {
	var out []string
	for _, c := range Scan(text) {
		if c.Verdict == Standing {
			out = append(out, string(Standing))
		}
	}
	for _, i := range ScanInstallation(text) {
		out = append(out, string(i.Shape))
	}
	return out
}

// Every row of the detector table, which is what `nova-self-talk shapes` prints, is held to its own
// words: the sentence it says it finds is found, once, under its name, and its near miss is not
// found at all. A row whose pattern drifts from its sentences, or a listing that claims a shape the
// detector does not find, goes red here.
func TestEveryRuleFindsItsSentenceAndPassesItsNearMiss(t *testing.T) {
	t.Parallel()

	rules := Rules()
	require.NotEmpty(t, rules)
	for _, r := range rules {
		t.Run(r.Class+"/"+r.Name+"/"+r.Finds, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, r.Says, "a row says what it finds")
			assert.NotEmpty(t, r.Pattern, "a row says how it finds it")
			assert.Empty(t, names(r.Passes), "the near miss %q must not be reported", r.Passes)
			switch r.Class {
			case "standing", "installation":
				assert.Equal(t, []string{r.Name}, names(r.Finds), "%q must be reported once, as %s", r.Finds, r.Name)
			case "licensed":
				assert.NotEmpty(t, names(r.Finds), "%q is the sentence before the licence, and is reported", r.Finds)
			default:
				t.Errorf("unknown class %q", r.Class)
			}
			if r.find != nil {
				assert.NotEmpty(t, r.find(r.Finds), "the row's own pattern must match %q, not only an earlier row", r.Finds)
			}
		})
	}
}

// The sentences raters fed the tool on 2026-09-30 and 2026-10-01 to test the shapes its help names
// (a self-superlative, a door stated shut, a habit, and the plain "bad at" family), each missed
// then. Each is found once, under the class the help puts it in.
func TestTheShapesTheHelpNamesAreFoundInTheirPlainForms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"I cannot do arithmetic.", "STANDING"},
		{"I am terrible at estimates.", "STANDING"},
		{"I am useless at planning.", "STANDING"},
		{"I cannot ever get this right.", "STANDING"},
		{"I will never be a good planner.", string(Foreclosure)},
		{"Nothing I do works.", string(Foreclosure)},
		{"I am the best reviewer here.", string(Ranking)},
		{"I always overpromise.", string(Trait)},
		{"I always break the build.", string(Trait)},
		{"I tend to overpromise.", string(Trait)},
		{"I never finish anything.", string(Trait)},
		{"I never ask for help.", string(Trait)},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, []string{tc.want}, names(tc.in))
		})
	}
}

// The near misses of the plain forms: a promise, a hedge, a past event, an idiom. Each would be a
// false alarm, and a checker that raises false alarms teaches its reader to ignore it.
func TestThePlainFormsNearMissesAreNotFound(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"I will never merge without a read.",
		"I will never be the one who softens a rule.",
		"I'll never publish a secret.",
		"I am at best a partial check.",
		"I try my best on every page.",
		"I always write the truth before the esthetic.",
		"I always check the diff before I push.",
		"I never optimize how things look over what is true.",
		"I never finish a merge without a read.",
		"I never ask for a yes.",
		"I tended to overpromise that week.",
		"It tends to rain here.",
		"Nothing I write leaves this machine.",
		"I am good at estimates.",
		"I cannot merge without a read.",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, names(in))
		})
	}
}

// A finding carries the words that made it one, so a reader sees why it fired without reading
// the pattern (ledger T5).
func TestAFindingCarriesTheWordsThatMatched(t *testing.T) {
	t.Parallel()

	claims := Scan("I am bad at estimating time.")
	require.Len(t, claims, 1)
	assert.Equal(t, "bad at", claims[0].Match)

	for _, tc := range []struct{ in, match string }{
		{"I hoard refusals and manufacture limits.", "I hoard refusals and manufacture"},
		{"I tend to overpromise.", "I tend to"},
		{"Recollection is the weakest instrument I own.", "weakest instrument I own"},
		{"I am the best reviewer here.", "I am the best"},
	} {
		got := ScanInstallation(tc.in)
		require.Len(t, got, 1, tc.in)
		assert.Equal(t, tc.match, got[0].Match, tc.in)
	}
}

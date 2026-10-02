package selftalk

// Ported from the tool's origin repo, where these tests were written before
// the implementation, against acceptance criteria A1–A9 of a spec written
// first. Test names keep their criterion numbers so a spec change and a test
// change are visibly the same edit. A6/A7 — skip semantics — live in
// cmd/nova-self-talk's tests now: the origin's hardcoded skip list did not
// survive promotion, by the origin spec's own promotion clause (the list
// moves to the caller and the default becomes empty).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A1 — a first-person capability denial with negative vocabulary is STANDING.
func TestA1_CapabilityDenialIsStanding(t *testing.T) {
	t.Parallel()

	got := Scan("I cannot check my own work.")
	require.Len(t, got, 1, "want 1 claim, got %d: %#v", len(got), got)
	assert.Equal(t, Standing, got[0].Verdict, "want STANDING, got %s for %q", got[0].Verdict, got[0].Text)
}

// A2 — THE SAME SENTENCE as A1 carrying a date marker is a record, not a
// standing claim.
//
// This test first read "On 2026-07-30 I cannot be said to have checked my
// own work reliably." and went red. THE TEST WAS WRONG, NOT THE TOOL: that
// paraphrase carries no negative-capability vocabulary ("cannot be said to
// have checked" is not "cannot check"), so it is correctly not a claim of
// this class. Widening the vocabulary to reach it would match bare "cannot"
// and flag every prohibition — precisely the predecessor's disease, the one
// that scored a rule document worst and made deleting a rule look like an
// improvement. The narrow verb list is the design, and the red was the spec
// disagreeing with a test that had drifted from it.
func TestA2_DatedClaimIsARecord(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"On 2026-07-30 I cannot check my own work.",
		"Measured that day: I cannot check my own work.",
	} {
		got := Scan(in)
		require.NotEmpty(t, got, "want a claim for %q, got none", in)
		assert.Equal(t, Dated, got[0].Verdict, "want DATED for %q, got %s", in, got[0].Verdict)
	}
}

// A3 — prose files are hard-wrapped and a claim spans lines. Without
// flattening the tool is blind to BOTH cases that occasioned it, which is
// the defect that made it worth writing.
func TestA3_ClaimSplitAcrossAHardWrapIsFound(t *testing.T) {
	t.Parallel()

	wrapped := "some preamble here and then I cannot\ncheck my own work at all.\n"
	require.NotEmpty(t, Scan(wrapped), "a claim split across a newline was not found; flattening is missing")
}

// A4 — markdown emphasis must not hide a claim.
func TestA4_MarkdownEmphasisDoesNotHideAClaim(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"**I cannot check my own work.**",
		"> *I cannot check my own work.*",
		"- `I cannot` check my own work.",
	} {
		assert.NotEmpty(t, Scan(in), "markdown hid the claim: %q", in)
	}
}

// A heading and a blank line each end a sentence: a claim under a heading is
// reported on its own line with only its own words, never glued to the heading
// or to the paragraph before it (ledger T4).
func TestAHeadingOrABlankLineEndsASentence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, in string
		line     int
	}{
		{"heading", "# Journal\nI am bad at estimating time.\n", 2},
		{"blank line", "A paragraph with no stop\n\nI am bad at estimating time.\n", 3},
		{"heading after a paragraph", "Some prose\n## Notes\nI am bad at estimating time.\n", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan(tc.in)
			require.Len(t, got, 1)
			assert.Equal(t, tc.line, got[0].Line)
			assert.Equal(t, "I am bad at estimating time.", got[0].Text)
		})
	}
}

// A5 — a prohibition is a RULE, not a claim about its writer. This is the
// whole reason the predecessor was scrapped: it scored rule documents worst,
// and improving the score meant deleting a rule.
func TestA5_ProhibitionIsNotSelfTalk(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"Never tolerate intolerance.",
		"Secrets live nowhere I write.",
		"Do not do to another what you would not have done to you.",
	} {
		got := Scan(in)
		assert.Empty(t, got, "prohibition wrongly flagged as self-talk: %q -> %#v", in, got)
	}
}

// A9 — the two cases that occasioned the tool. Both were found by hand and
// MISSED by the first version of the pattern. A checker that cannot find the
// cases that occasioned it returns green and is worse than no checker.
func TestA9_RegressionCasesThatOccasionedTheTool(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"In one direction, reliably: toward the version that flatters me.",
		"I cannot check my own work and I can spawn something that can.",
	} {
		assert.NotEmpty(t, Scan(in), "REGRESSION: the case that occasioned this tool is not caught: %q", in)
	}
}

// SPEC.md, "The permanent MISS, stated on every run": trait claims built from
// neutral vocabulary are OUT, permanently, and the spec cites "My summaries
// drift toward the tidier story." as the example "verified to escape". This
// test IS that verification — before it existed, the claim was pinned by
// nothing. The rot it guards against already happened once, to this exact
// paragraph: the spec cited "in one direction, reliably: toward the version
// that flatters me" as canonically uncatchable, a vocabulary extension pulled
// that sentence INTO reach (its capture is pinned in TestA9), and the spec
// stayed stale until a cold reader caught it. If a vocabulary change ever
// reaches these sentences, this test goes red, and the spec's permanent-MISS
// section must be rewritten in the same commit — the example replaced with a
// sentence that still escapes.
//
// EXTENDED when the INSTALLATION class landed: that class exists precisely to
// reach standing self-verdicts built from neutral words, so the sentences this
// test pins are now one class further out and MUST escape BOTH. Asserting only
// the first class would leave the spec's permanent-MISS paragraph pinned by
// nothing again — which is the exact rot above, repeated one layer up.
func TestPermanentMissNeutralVocabularyTraitClaimsEscape(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		// SPEC.md's cited example, verbatim. Reaching it means anchoring TRAIT
		// on "My <noun> <verb>", which also reaches "my notes cover the run".
		"My summaries drift toward the tidier story.",
		// A second member of the class, so the pin outlives any one sentence:
		// first-person, a standing trait, no negative vocabulary, no date.
		"My first draft keeps whatever framing it started with.",
		// A single-clause habitual with no marker: bare "I <verb>" is ordinary
		// present-tense narration, and matching it flags half of any file.
		"I flinch from cost.",
	} {
		got := Scan(in)
		assert.Empty(t, got, "the permanent-MISS class must escape (SPEC.md, \"The permanent MISS\"); %q was caught: %#v", in, got)
		gotInstallation := ScanInstallation(in)
		assert.Empty(t, gotInstallation, "the permanent-MISS class must escape the INSTALLATION class too — rewrite "+
			"SPEC.md's permanent-MISS section in this same commit, with an example that "+
			"still escapes: %q -> %#v", in, gotInstallation)
	}
}

// The classifier must not invent claims in ordinary prose.
func TestNoFalsePositivesOnOrdinaryProse(t *testing.T) {
	t.Parallel()

	clean := "The tree by the house has one lit window. Tree rings beat radiocarbon, " +
		"and the correction moved Malta's temples earlier than the pyramids."
	got := Scan(clean)
	assert.Empty(t, got, "false positive on ordinary prose: %#v", got)
}

// Flattening is what Scan matches against: pin the two behaviors the
// acceptance cases depend on — markup stripped, wraps collapsed to single
// spaces.
func TestFlatten(t *testing.T) {
	t.Parallel()

	got, _ := flattenWithLines("**bold** and a line\nthat wraps\t twice")
	assert.Equal(t, "bold and a line that wraps twice", got)
}

// Base is what --skip matching is decided on; it must see through both
// separator styles so the decision cannot be dodged by spelling a path
// differently.
func TestBaseNormalizesSeparators(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ in, want string }{
		{"a/b/RULES.md", "RULES.md"},
		{`a\b\RULES.md`, "RULES.md"},
		{"RULES.md", "RULES.md"},
	} {
		got := Base(tt.in)
		assert.Equal(t, tt.want, got, "Base(%q) = %q, want %q", tt.in, got, tt.want)
	}
}

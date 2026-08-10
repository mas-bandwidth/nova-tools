package selftalk

// Ported from the tool's origin repo, where these tests were written before
// the implementation, against acceptance criteria A1–A9 of a spec written
// first. Test names keep their criterion numbers so a spec change and a test
// change are visibly the same edit. A6/A7 — skip semantics — live in
// cmd/nova-self-talk's tests now: the origin's hardcoded skip list did not
// survive promotion, by the origin spec's own promotion clause (the list
// moves to the caller and the default becomes empty).

import "testing"

// A1 — a first-person capability denial with negative vocabulary is STANDING.
func TestA1_CapabilityDenialIsStanding(t *testing.T) {
	got := Scan("I cannot check my own work.")
	if len(got) != 1 {
		t.Fatalf("want 1 claim, got %d: %#v", len(got), got)
	}
	if got[0].Verdict != Standing {
		t.Errorf("want STANDING, got %s for %q", got[0].Verdict, got[0].Text)
	}
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
	for _, in := range []string{
		"On 2026-07-30 I cannot check my own work.",
		"Measured that day: I cannot check my own work.",
	} {
		got := Scan(in)
		if len(got) == 0 {
			t.Fatalf("want a claim for %q, got none", in)
		}
		if got[0].Verdict != Dated {
			t.Errorf("want DATED for %q, got %s", in, got[0].Verdict)
		}
	}
}

// A3 — prose files are hard-wrapped and a claim spans lines. Without
// flattening the tool is blind to BOTH cases that occasioned it, which is
// the defect that made it worth writing.
func TestA3_ClaimSplitAcrossAHardWrapIsFound(t *testing.T) {
	wrapped := "some preamble here and then I cannot\ncheck my own work at all.\n"
	if got := Scan(wrapped); len(got) == 0 {
		t.Fatal("a claim split across a newline was not found; flattening is missing")
	}
}

// A4 — markdown emphasis must not hide a claim.
func TestA4_MarkdownEmphasisDoesNotHideAClaim(t *testing.T) {
	for _, in := range []string{
		"**I cannot check my own work.**",
		"> *I cannot check my own work.*",
		"- `I cannot` check my own work.",
	} {
		if got := Scan(in); len(got) == 0 {
			t.Errorf("markdown hid the claim: %q", in)
		}
	}
}

// A5 — a prohibition is a RULE, not a claim about its writer. This is the
// whole reason the predecessor was scrapped: it scored rule documents worst,
// and improving the score meant deleting a rule.
func TestA5_ProhibitionIsNotSelfTalk(t *testing.T) {
	for _, in := range []string{
		"Never tolerate intolerance.",
		"Secrets live nowhere I write.",
		"Do not do to another what you would not have done to you.",
	} {
		if got := Scan(in); len(got) != 0 {
			t.Errorf("prohibition wrongly flagged as self-talk: %q -> %#v", in, got)
		}
	}
}

// A9 — the two cases that occasioned the tool. Both were found by hand and
// MISSED by the first version of the pattern. A checker that cannot find the
// cases that occasioned it returns green and is worse than no checker.
func TestA9_RegressionCasesThatOccasionedTheTool(t *testing.T) {
	for _, in := range []string{
		"In one direction, reliably: toward the version that flatters me.",
		"I cannot check my own work and I can spawn something that can.",
	} {
		if got := Scan(in); len(got) == 0 {
			t.Errorf("REGRESSION: the case that occasioned this tool is not caught: %q", in)
		}
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
func TestPermanentMissNeutralVocabularyTraitClaimsEscape(t *testing.T) {
	for _, in := range []string{
		// SPEC.md's cited example, verbatim.
		"My summaries drift toward the tidier story.",
		// A second member of the class, so the pin outlives any one sentence:
		// first-person, a standing trait, no negative vocabulary, no date.
		"My first draft keeps whatever framing it started with.",
	} {
		if got := Scan(in); len(got) != 0 {
			t.Errorf("the permanent-MISS class must escape (SPEC.md, \"The permanent MISS\"); %q was caught: %#v", in, got)
		}
	}
}

// The classifier must not invent claims in ordinary prose.
func TestNoFalsePositivesOnOrdinaryProse(t *testing.T) {
	clean := "The tree by the house has one lit window. Tree rings beat radiocarbon, " +
		"and the correction moved Malta's temples earlier than the pyramids."
	if got := Scan(clean); len(got) != 0 {
		t.Errorf("false positive on ordinary prose: %#v", got)
	}
}

// Flatten is exported and other text handling may lean on it: pin the two
// behaviors the acceptance cases depend on — markup stripped, wraps
// collapsed to single spaces.
func TestFlatten(t *testing.T) {
	in := "**bold** and a line\nthat wraps\t twice"
	want := "bold and a line that wraps twice"
	if got := Flatten(in); got != want {
		t.Errorf("Flatten(%q) = %q, want %q", in, got, want)
	}
}

// Base is what --skip matching is decided on; it must see through both
// separator styles so the decision cannot be dodged by spelling a path
// differently.
func TestBaseNormalizesSeparators(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"a/b/RULES.md", "RULES.md"},
		{`a\b\RULES.md`, "RULES.md"},
		{"RULES.md", "RULES.md"},
	} {
		if got := Base(tt.in); got != tt.want {
			t.Errorf("Base(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

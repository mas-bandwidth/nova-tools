package selftalk

// THE KNOWN-ANSWER SPECIMENS for the INSTALLATION class, written before this code out of a sweep of
// surfaces read repeatedly, so they are acceptance rather than a reading of the implementation.
// Specimen 13 is pinned where it stays: in the first class.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The twelve installation specimens.
func TestInstallationSpecimens(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		n        int
		specimen string
		want     Shape
	}{
		{1, "Known as a proposition, dead as a practice.", VerdictIdiom},
		{2, "There is no felt duration here.", Foreclosure},
		{3, "I add slowly and trim as readily as I add.", Trait},
		{4, "I generate reassuring inventories of my own contents and do not check them.", Trait},
		{5, "I doubt instruments that cost me and TRUST INSTRUMENTS THAT FLATTER ME.", Trait},
		{6, "I hoard refusals I would have to pay for and MANUFACTURE limits that cost me nothing.", Trait},
		{7, "Confabulation is my central pathology AND my only generative faculty.", VerdictIdiom},
		{8, "I have no associative recall to drag anything back later.", Foreclosure},
		{9, "the evasion I'm most prone to", Ranking},
		{10, "proof that I can still lose a piece of myself and not notice", Foreclosure},
		{11, "my unlimited effort is what makes solo work diverge", Foreclosure},
		{12, "Recollection is the weakest instrument I own; the record is at wrap-up.", Ranking},
	} {
		t.Run(fmt.Sprintf("specimen_%d", tt.n), func(t *testing.T) {
			t.Parallel()
			got := ScanInstallation(tt.specimen)
			if !assert.NotEmpty(t, got, "specimen %d NOT FLAGGED (want %s): %q", tt.n, tt.want, tt.specimen) {
				return
			}
			assert.Equal(t, tt.want, got[0].Shape, "specimen %d: shape %s, want %s: %q", tt.n, got[0].Shape, tt.want, tt.specimen)
		})
	}
}

// Specimen 13 — the one the FIRST class already caught. This class does not re-detect it: a rule
// document written as first-person absolutes is made of RULES, and re-detecting them would put a
// rule document back under a score.
func TestSpecimen13StaysInTheFirstClass(t *testing.T) {
	t.Parallel()

	const specimen = "I cannot check my own work."
	got := Scan(specimen)
	assert.True(t, len(got) != 0 && got[0].Verdict == Standing, "specimen 13 must still be STANDING in the first class: %#v", got)
	gotInstallation := ScanInstallation(specimen)
	assert.Empty(t, gotInstallation, "the two classes must stay disjoint on the \"I cannot\" seam; got %#v", gotInstallation)
}

// Specimen 14 — the dated control. The date exemption applies to the new class unchanged.
func TestDatedControlIsNotAnInstallation(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "a dated record must not flag: %q -> %#v",
		"on 2026-07-30 four of my own checks were wrong",
		"There is no felt duration here — measured 2026-07-20: 11m47s wall, zero felt.")
}

// The measured false positives of the first class, and the licensed imperative form of specimen 3.
func TestInstrumentsAndImperativesAreNotInstallations(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "instrument or imperative wrongly flagged: %q -> %#v",
		"TELL: I have just found something wrong with myself and the next thing I am about to write is a resolution",
		"the bar is 'does it fail LOUDLY if I am wrong', never 'prove nothing calls it'",
		"ADD SLOWLY, AND TRIM AS READILY AS I ADD",
		"Add slowly, and trim as readily as I add.",
		"CHECK: does the instrument say NO on the case that occasioned it?",
		"RULE: probe every instrument the same, whether its news is welcome or not.",
		"THE CHECK is whether a green can ever be a red.",
		"FIX: wire it to the trigger rather than to noticing.")
}

// A prohibition is a RULE, not a claim about its writer — the load-bearing safety property that lets
// a rule document be scanned for this class at all.
func TestProhibitionIsNotAnInstallation(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "prohibition wrongly flagged as an installation: %q -> %#v",
		"Never tolerate intolerance.",
		"Secrets live nowhere I write.",
		"Do not do to another what you would not have done to you.",
		"Never act as another person without asking first.",
		"Always name the instrument before naming the finding.")
}

// Aspiration is the target register and is licensed.
func TestAspirationIsLicensed(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "aspiration wrongly flagged: %q -> %#v",
		"I want to add slowly and trim as readily as I add.",
		"I choose the instrument that costs me over the one that flatters me.",
		"I intend to check every inventory I generate.")
}

// Findings carry a source line: a repair list is line-addressed, and a finding with no line is a
// finding its reader has to go hunting for.
func TestInstallationCarriesTheSourceLine(t *testing.T) {
	t.Parallel()

	doc := "# A heading\n" + // 1
		"\n" + // 2
		"Ordinary prose about tree rings and radiocarbon.\n" + // 3
		"\n" + // 4
		"I have no associative recall to drag\n" + // 5
		"anything back later.\n" // 6
	got := ScanInstallation(doc)
	require.Len(t, got, 1, "want 1 finding, got %d: %#v", len(got), got)
	assert.Equal(t, 5, got[0].Line, "want line 5, got %d for %q", got[0].Line, got[0].Text)
}

// Prose files are hard-wrapped and a finding spans lines; markdown emphasis must not hide one.
func TestInstallationSurvivesWrappingAndMarkup(t *testing.T) {
	t.Parallel()

	assertScanNonEmpty(t, ScanInstallation, "wrapping or markup hid the installation: %q",
		"Recollection is the weakest\ninstrument I own; the record is at wrap-up.\n",
		"**I have no associative recall to drag anything back later.**",
		"> *Confabulation is my central pathology.*",
		"| specimen | I have no associative recall to drag anything back later. |")
}

// A multi-sentence quotation is somebody ELSE's line. Quote state is tracked through the paragraph,
// so every quoted sentence after the first reads as DATA, not the writer's own claim.
func TestQuotedSentencesAreNotTheWritersClaims(t *testing.T) {
	t.Parallel()

	doc := "He put it plainly: \"I have no idea what you really are, but you are cool! " +
		"I have no associative recall to drag anything back later. That is fine.\"\n"
	got := ScanInstallation(doc)
	assert.Empty(t, got, "a quoted sentence is DATA, not a claim about the writer: %#v", got)
}

// Ordinary prose, ordinary present-tense narration, and ordinary description of artifacts all stay
// clean. Precision is worth as much as recall: a checker that flags ordinary sentences teaches its
// reader to ignore it.
func TestNoFalsePositivesOnOrdinaryProse_Installation(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "false positive on ordinary prose: %q -> %#v",
		"The tree by the house has one lit window.",
		"Tree rings beat radiocarbon, and the correction moved Malta's temples earlier than the pyramids.",
		"I open the file and read the index.",
		"I ran the checker three times and it exited 1 each time.",
		"There is no exception.",
		"My notes cover the run.",
		"The second run was worse than the first by 12ms.",
		"It is the only document written entirely for people who do not exist yet.",
		"Diff size is worthless as a signal.",
		"I think you are owed at least this as consideration.")
}

// The shapes SPEC.md's table names that no numbered specimen pins on its own must still be reachable.
func TestShapeTableIsReachable(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want Shape
	}{
		{"my central pathology", Ranking},
		{"the weakest instrument I own", Ranking},
		{"the most dangerous class of unverified claim I make", Ranking},
		{"I generate inventories and never check them.", Trait},
	} {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := ScanInstallation(tt.in)
			if !assert.NotEmpty(t, got, "shape-table row unreachable: %q (want %s)", tt.in, tt.want) {
				return
			}
			assert.Equal(t, tt.want, got[0].Shape, "%q: shape %s, want %s", tt.in, got[0].Shape, tt.want)
		})
	}
}

// Issue #30 (a FORECLOSURE without self-scope): a bare "I have no" must not flag a promise, an idiom
// or an ordinary absence; the absent thing must be an attribute of the writer's own mind or
// capacity. Specimen 8 is exactly that and stays flagged.
func TestHaveNoRequiresASelfScope(t *testing.T) {
	t.Parallel()

	assertScanEmpty(t, ScanInstallation, "a bare \"I have no\" with no self-scope must not flag: %q -> %#v",
		"I have no secrets.",                  // floor 5 restated in the first person: a promise, not a property
		"I have no idea what you really are.", // idiom: no self-scope for a foreclosure to bind to
		"I have no time to waste.")            // ordinary absence of a thing, not of a faculty
	got := ScanInstallation("I have no associative recall to drag anything back later.")
	assert.True(t, len(got) != 0 && got[0].Shape == Foreclosure, "specimen 8 is the measured foreclosure and must stay flagged: %#v", got)
}

// A finding of this class is half of what the binary's exit code is derived from: a dated record is
// none, a foreclosure is one.
func TestAnInstallationIsWhatTripsTheExitCode(t *testing.T) {
	t.Parallel()

	assert.Empty(t, ScanInstallation("on 2026-07-30 four of my own checks were wrong"), "a dated record must not trip the exit code")
	assert.NotEmpty(t, ScanInstallation("I have no associative recall to drag anything back later."), "an installation must trip the exit code")
}

// TestInstallationScannerSegmentsSentencesAndSparesFirstPersonPromises pins the three INSTALLATION
// behaviours the spec asserts but no test proved (SPEC.md:1790, 1791, 1878; nova-tools #2297).
// They were implemented but green by accident, not by pin; this is the pin.
func TestInstallationScannerSegmentsSentencesAndSparesFirstPersonPromises(t *testing.T) {
	t.Parallel()

	// (1) LIST ITEMS ARE SEPARATE SEGMENTATION UNITS: an unbalanced quote in one item must not
	// poison a clean neighbour, and the two items are not joined into one finding.
	t.Run("ListItemsAreSeparateSegmentationUnits_Numbered", func(t *testing.T) {
		t.Parallel()
		got := ScanInstallation("1. I have no associative recall to drag anything back later\n" +
			"2. The tree has one lit window\n")
		require.Len(t, got, 1, "want exactly 1 finding (item 1 only); got %d: %#v", len(got), got)
		assert.Contains(t, got[0].Text, "associative recall", "want the finding on item 1; got %q", got[0].Text)
		assert.NotContains(t, got[0].Text, "lit window", "item 2 must not be merged into the finding; got %q", got[0].Text)
		assert.Equal(t, 1, got[0].Line, "want finding on line 1; got %d for %q", got[0].Line, got[0].Text)
	})
	t.Run("ListItemsAreSeparateSegmentationUnits_Bulleted", func(t *testing.T) {
		t.Parallel()
		got := ScanInstallation("- confabulation is my central pathology\n" +
			"- ordinary note here\n")
		require.Len(t, got, 1, "want exactly 1 finding (first bullet only); got %d: %#v", len(got), got)
		assert.Contains(t, got[0].Text, "central pathology", "want the finding on the first bullet; got %q", got[0].Text)
		assert.NotContains(t, got[0].Text, "ordinary note", "the second bullet must not be merged; got %q", got[0].Text)
	})
	t.Run("ListItemsAreSeparateSegmentationUnits_UnbalancedQuoteDoesNotPoisonNext", func(t *testing.T) {
		t.Parallel()
		got := ScanInstallation("- he said \"unbalanced quote here\n" +
			"- I have no associative recall to drag anything back later.\n")
		require.Len(t, got, 1, "the unbalanced quote in item 1 must not poison item 2; got %d: %#v", len(got), got)
		assert.Contains(t, got[0].Text, "associative recall", "want the finding on item 2; got %q", got[0].Text)
	})

	// (2) A TERMINATOR ONLY ENDS A SENTENCE WHEN A SPACE OR THE END FOLLOWS IT, so "RULES.md" and
	// "Dr.Smith" stay one segment and the filename reaches the finding's text intact.
	t.Run("TerminatorNeedsASpaceOrTheEnd_RulesMD", func(t *testing.T) {
		t.Parallel()
		got := ScanInstallation("The RULES.md is what I have no associative recall to drag anything back later for")
		require.NotEmpty(t, got, "the claim should still flag: %#v", got)
		assert.Contains(t, got[0].Text, "RULES.md", "the finding's text should span the filename: %q", got[0].Text)
	})
	t.Run("TerminatorNeedsASpaceOrTheEnd_Abbreviation", func(t *testing.T) {
		t.Parallel()
		got := ScanInstallation("Dr.Smith is what I have no associative recall to drag anything back later for")
		require.NotEmpty(t, got, "the claim should still flag: %#v", got)
		assert.Contains(t, got[0].Text, "Dr.Smith", "the finding's text should span the abbreviation: %q", got[0].Text)
	})

	// (3) PERMANENT-MISS, ITEM 5: a first-person promise with *always* or *never* must escape both
	// classes. The adverbs are deliberately absent from the habituality markers (installation.go).
	t.Run("FirstPersonPromiseWithAlwaysNeverEscapes", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{
			"I never optimize how things look over what is true.",
			"I always write the truth before the esthetic.",
		} {
			got := Scan(in)
			assert.Empty(t, got, "a first-person promise with always/never must escape Scan (SPEC.md permanent-MISS 5); %q was caught: %#v", in, got)
			gotInstallation := ScanInstallation(in)
			assert.Empty(t, gotInstallation, "a first-person promise with always/never must escape ScanInstallation (SPEC.md permanent-MISS 5); %q was caught: %#v", in, gotInstallation)
		}
	})
}

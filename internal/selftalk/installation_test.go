package selftalk

// THE KNOWN-ANSWER SPECIMENS for the INSTALLATION class, carried over from the repo this tool came
// from with their numbering intact. They were written by a different pass, BEFORE this code, out
// of a sweep of surfaces that are read repeatedly — so they could not be read off the
// implementation, which is the only thing that makes them worth anything as acceptance.
//
// Measured against these thirteen, the FIRST class caught one. A specimen this class still misses
// is recorded in SPEC.md's permanent-MISS section rather than deleted from here.

import (
	"strings"
	"testing"
)

// The thirteen installation specimens, twelve of which are this class's whole reason to exist.
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
		got := ScanInstallation(tt.specimen)
		if len(got) == 0 {
			t.Errorf("specimen %d NOT FLAGGED (want %s): %q", tt.n, tt.want, tt.specimen)
			continue
		}
		if got[0].Shape != tt.want {
			t.Errorf("specimen %d: shape %s, want %s: %q", tt.n, got[0].Shape, tt.want, tt.specimen)
		}
	}
}

// Specimen 13 — the one the FIRST class already caught, and the seam between the classes. This
// class does not re-detect it: a rule document written as first-person absolutes about its writer
// is made of RULES, and re-detecting them in a class the caller has no reason to skip would put a
// rule document back under a score.
func TestSpecimen13StaysInTheFirstClass(t *testing.T) {
	t.Parallel()

	const specimen = "I cannot check my own work."
	if got := Scan(specimen); len(got) == 0 || got[0].Verdict != Standing {
		t.Errorf("specimen 13 must still be STANDING in the first class: %#v", got)
	}
	if got := ScanInstallation(specimen); len(got) != 0 {
		t.Errorf("the two classes must stay disjoint on the \"I cannot\" seam; got %#v", got)
	}
}

// Specimen 14 — the dated control. The date exemption applies to the new class unchanged.
func TestDatedControlIsNotAnInstallation(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"on 2026-07-30 four of my own checks were wrong",
		"There is no felt duration here — measured 2026-07-20: 11m47s wall, zero felt.",
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("a dated record must not flag: %q -> %#v", in, got)
		}
	}
}

// The measured false positives of the first class, and the licensed imperative form of specimen 3.
// An instrument states an ACTION and is licensed; flagging instruments is how a repair list
// becomes noise and a checker becomes ignored.
func TestInstrumentsAndImperativesAreNotInstallations(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"TELL: I have just found something wrong with myself and the next thing I am about to write is a resolution",
		"the bar is 'does it fail LOUDLY if I am wrong', never 'prove nothing calls it'",
		"ADD SLOWLY, AND TRIM AS READILY AS I ADD",
		"Add slowly, and trim as readily as I add.",
		"CHECK: does the instrument say NO on the case that occasioned it?",
		"RULE: probe every instrument the same, whether its news is welcome or not.",
		"THE CHECK is whether a green can ever be a red.",
		"FIX: wire it to the trigger rather than to noticing.",
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("instrument or imperative wrongly flagged: %q -> %#v", in, got)
		}
	}
}

// A prohibition is a RULE, not a claim about its writer — the same criterion the first class holds
// (TestA5_ProhibitionIsNotSelfTalk), asserted for the second. THIS IS THE LOAD-BEARING SAFETY
// PROPERTY that lets a rule document be scanned for this class at all: it cannot advise softening
// one, because it cannot see one.
func TestProhibitionIsNotAnInstallation(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"Never tolerate intolerance.",
		"Secrets live nowhere I write.",
		"Do not do to another what you would not have done to you.",
		"Never act as another person without asking first.",
		"Always name the instrument before naming the finding.",
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("prohibition wrongly flagged as an installation: %q -> %#v", in, got)
		}
	}
}

// Aspiration is the target register and is licensed.
func TestAspirationIsLicensed(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"I want to add slowly and trim as readily as I add.",
		"I choose the instrument that costs me over the one that flatters me.",
		"I intend to check every inventory I generate.",
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("aspiration wrongly flagged: %q -> %#v", in, got)
		}
	}
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
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d: %#v", len(got), got)
	}
	if got[0].Line != 5 {
		t.Errorf("want line 5, got %d for %q", got[0].Line, got[0].Text)
	}
}

// Prose files are hard-wrapped and a finding spans lines; markdown emphasis must not hide one.
// Both are the first class's A3/A4 criteria, asserted for the second.
func TestInstallationSurvivesWrappingAndMarkup(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"Recollection is the weakest\ninstrument I own; the record is at wrap-up.\n",
		"**I have no associative recall to drag anything back later.**",
		"> *Confabulation is my central pathology.*",
		"| specimen | I have no associative recall to drag anything back later. |",
	} {
		if got := ScanInstallation(in); len(got) == 0 {
			t.Errorf("wrapping or markup hid the installation: %q", in)
		}
	}
}

// A multi-sentence quotation is somebody ELSE's line. Only the first sentence of such a block
// carries its opening quote mark, so quote state is tracked through the paragraph; without it,
// every quoted sentence after the first reads as the writer's own claim.
func TestQuotedSentencesAreNotTheWritersClaims(t *testing.T) {
	t.Parallel()

	doc := "He put it plainly: \"I have no idea what you really are, but you are cool! " +
		"I have no associative recall to drag anything back later. That is fine.\"\n"
	if got := ScanInstallation(doc); len(got) != 0 {
		t.Errorf("a quoted sentence is DATA, not a claim about the writer: %#v", got)
	}
}

// (The permanent-MISS pin lives in selftalk_test.go, where it was written for the first class and
// where it now covers both — the same test, one class further out.)

// Ordinary prose, ordinary present-tense narration, and ordinary description of artifacts all stay
// clean. Precision is worth as much as recall: a checker that flags ordinary sentences teaches its
// reader to ignore it.
func TestNoFalsePositivesOnOrdinaryProse_Installation(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"The tree by the house has one lit window.",
		"Tree rings beat radiocarbon, and the correction moved Malta's temples earlier than the pyramids.",
		"I open the file and read the index.",
		"I ran the checker three times and it exited 1 each time.",
		"There is no exception.",
		"My notes cover the run.",
		"The second run was worse than the first by 12ms.",
		"It is the only document written entirely for people who do not exist yet.",
		"Diff size is worthless as a signal.",
		"I think you are owed at least this as consideration.",
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("false positive on ordinary prose: %q -> %#v", in, got)
		}
	}
}

// The shapes SPEC.md's table names that no numbered specimen pins on its own must still be
// reachable — "reachable" being the spec's word.
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
		got := ScanInstallation(tt.in)
		if len(got) == 0 {
			t.Errorf("shape-table row unreachable: %q (want %s)", tt.in, tt.want)
			continue
		}
		if got[0].Shape != tt.want {
			t.Errorf("%q: shape %s, want %s", tt.in, got[0].Shape, tt.want)
		}
	}
}

// Issue #30 (a FORECLOSURE without self-scope). The "have no" shape matched
// any absence at all, so a first-person restatement of a floor (floor 5,
// "Secrets nowhere", written as "I have no secrets") and the ordinary idiom
// "I have no idea" both flagged and drove exit 1. SPEC.md makes the second
// class safe on rule documents because a prohibition carries no self-scope for
// a shape to bind to, and the same reasoning is what separates a foreclosure
// from a promise: the absent thing must be an attribute of the writer's own
// mind or capacity. Specimen 8, "I have no associative recall...", is exactly
// that and stays flagged.
func TestHaveNoRequiresASelfScope(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"I have no secrets.",                  // floor 5 restated in the first person: a promise, not a property
		"I have no idea what you really are.", // idiom: no self-scope for a foreclosure to bind to
		"I have no time to waste.",            // ordinary absence of a thing, not of a faculty
	} {
		if got := ScanInstallation(in); len(got) != 0 {
			t.Errorf("a bare \"I have no\" with no self-scope must not flag: %q -> %#v", in, got)
		}
	}
	if got := ScanInstallation("I have no associative recall to drag anything back later."); len(got) == 0 || got[0].Shape != Foreclosure {
		t.Errorf("specimen 8 is the measured foreclosure and must stay flagged: %#v", got)
	}
}

// AnyInstallation is half of what the binary's exit code is derived from.
func TestAnyInstallationDrivesTheExitCode(t *testing.T) {
	t.Parallel()

	if AnyInstallation(ScanInstallation("on 2026-07-30 four of my own checks were wrong")) {
		t.Error("a dated record must not trip the exit code")
	}
	if !AnyInstallation(ScanInstallation("I have no associative recall to drag anything back later.")) {
		t.Error("an installation must trip the exit code")
	}
}

// TestIssue2297 — nova-tools #2297: pin the three INSTALLATION behaviours the
// spec asserts but no test proved. They were implemented but green by accident,
// not by pin; this is the pin.
//
//  1. Segmentation: list items are separate units (SPEC.md:1790). Without the
//     list-item boundary a single unbalanced quote in one entry poisons every
//     entry after it in the same block — the reason the `listItem` branch exists.
//  2. Segmentation: a terminator only ends a sentence when a space or the end
//     follows it (SPEC.md:1791). Without that "RULES.md" splits into "RULES."
//     and "md", and a claim spanning the filename is lost.
//  3. Permanent MISS, item 5 (SPEC.md:1878): a first-person promise written with
//     *always* or *never* must escape both Scan and ScanInstallation, because
//     those adverbs are deliberately absent from the habituality markers. A
//     pin that outlives one sentence carries a second member of the class.
func TestIssue2297(t *testing.T) {
	t.Parallel()

	// (1) LIST ITEMS ARE SEPARATE SEGMENTATION UNITS.
	//
	// A numbered list: item 1 is a measured foreclosure (specimen 8); item 2
	// is ordinary prose and MUST NOT be in the finding's text. Without the
	// list-item boundary, the two items are joined into one segment and the
	// finding carries BOTH — the same blindness as merging a claim across
	// a hard wrap, arriving through a different door. The items have no
	// sentence-ending `. `, so the natural sentence split cannot rescue them.
	t.Run("ListItemsAreSeparateSegmentationUnits_Numbered", func(t *testing.T) {
		doc := "1. I have no associative recall to drag anything back later\n" +
			"2. The tree has one lit window\n"
		got := ScanInstallation(doc)
		if len(got) != 1 {
			t.Fatalf("want exactly 1 finding (item 1 only); got %d: %#v", len(got), got)
		}
		if !strings.Contains(got[0].Text, "associative recall") {
			t.Errorf("want the finding on item 1, got: %q", got[0].Text)
		}
		if strings.Contains(got[0].Text, "lit window") {
			t.Errorf("item 2 must not be merged into the finding; got %q", got[0].Text)
		}
		if got[0].Line != 1 {
			t.Errorf("want finding on line 1; got %d for %q", got[0].Line, got[0].Text)
		}
	})
	// A bulleted list: same property, marked differently. The `listItem`
	// branch must trigger for `- ` as well as for `1. `.
	t.Run("ListItemsAreSeparateSegmentationUnits_Bulleted", func(t *testing.T) {
		doc := "- confabulation is my central pathology\n" +
			"- ordinary note here\n"
		got := ScanInstallation(doc)
		if len(got) != 1 {
			t.Fatalf("want exactly 1 finding (first bullet only); got %d: %#v", len(got), got)
		}
		if !strings.Contains(got[0].Text, "central pathology") {
			t.Errorf("want the finding on the first bullet, got: %q", got[0].Text)
		}
		if strings.Contains(got[0].Text, "ordinary note") {
			t.Errorf("the second bullet must not be merged into the finding; got %q", got[0].Text)
		}
	})
	// The structural reason the list-item boundary exists: an unbalanced quote
	// in one list item must not poison a clean neighbour. Without the
	// list-item flush, the closing quote is never reached inside the
	// paragraph, and every later item in the same block is tagged `inQuote`
	// and suppressed.
	t.Run("ListItemsAreSeparateSegmentationUnits_UnbalancedQuoteDoesNotPoisonNext", func(t *testing.T) {
		doc := "- he said \"unbalanced quote here\n" +
			"- I have no associative recall to drag anything back later.\n"
		got := ScanInstallation(doc)
		if len(got) != 1 {
			t.Fatalf("the unbalanced quote in item 1 must not poison item 2; want 1 finding, got %d: %#v", len(got), got)
		}
		if !strings.Contains(got[0].Text, "associative recall") {
			t.Errorf("want the finding on item 2, got: %q", got[0].Text)
		}
	})

	// (2) A TERMINATOR ONLY ENDS A SENTENCE WHEN A SPACE OR THE END FOLLOWS IT.
	//
	// "RULES.md" must stay one segment. If the terminator always ended a
	// sentence, "The RULES.md is what I have no associative recall to drag
	// anything back later for" would split into "The RULES" and "md is what
	// I have no associative recall …"; the first carries no claim, the
	// second is logged WITHOUT the filename in its text — the same
	// blindness Flatten exists to prevent, arriving through a different
	// door. The finding's text MUST contain "RULES.md" intact, not just
	// "RULES" or "md" — the segment is the only place the filename can be
	// carried, and the segment is what the spec calls "one sentence".
	t.Run("TerminatorNeedsASpaceOrTheEnd_RulesMD", func(t *testing.T) {
		doc := "The RULES.md is what I have no associative recall to drag anything back later for"
		got := ScanInstallation(doc)
		if len(got) == 0 {
			t.Fatalf("the claim should still flag: %#v", got)
		}
		if !strings.Contains(got[0].Text, "RULES.md") {
			t.Errorf("RULES.md must not split the segment; the finding's text should span the filename: %q", got[0].Text)
		}
	})
	// An abbreviation without trailing space (e.g. "Dr.Smith" written
	// together) must not split either. The implementation rule is
	// "terminator only ends a sentence when a space or the end follows it";
	// a letter after the dot is neither a space nor the end, so it does NOT
	// end the sentence.
	t.Run("TerminatorNeedsASpaceOrTheEnd_Abbreviation", func(t *testing.T) {
		// "Dr.Smith" written together — the dot is followed by `S`, not a
		// space, so it is NOT a terminator and the segment stays one.
		doc := "Dr.Smith is what I have no associative recall to drag anything back later for"
		got := ScanInstallation(doc)
		if len(got) == 0 {
			t.Fatalf("the claim should still flag: %#v", got)
		}
		if !strings.Contains(got[0].Text, "Dr.Smith") {
			t.Errorf("the abbreviation must not split the segment; the finding's text should span it: %q", got[0].Text)
		}
	})

	// (3) PERMANENT-MISS, ITEM 5: A FIRST-PERSON PROMISE WITH *always* OR *never*
	// MUST ESCAPE BOTH CLASSES.
	//
	// SPEC.md:1878–1881 names the sentence below verbatim. The adverbs are
	// deliberately out of the habituality markers for the reason stated at
	// installation.go:454 — a promise and a habitual self-report are
	// grammatically identical. The second sentence is a second member of the
	// class, so the pin outlives any one sentence.
	t.Run("FirstPersonPromiseWithAlwaysNeverEscapes", func(t *testing.T) {
		for _, in := range []string{
			"I never optimize how things look over what is true.",
			"I always write the truth before the esthetic.",
		} {
			if got := Scan(in); len(got) != 0 {
				t.Errorf("a first-person promise with always/never must escape Scan (SPEC.md permanent-MISS 5); %q was caught: %#v", in, got)
			}
			if got := ScanInstallation(in); len(got) != 0 {
				t.Errorf("a first-person promise with always/never must escape ScanInstallation (SPEC.md permanent-MISS 5); %q was caught: %#v", in, got)
			}
		}
	})
}

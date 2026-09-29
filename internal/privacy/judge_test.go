package privacy_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

const harmless = "an entirely harmless sentence about nothing at all"

// A declared source that is missing refuses, and the remedy names the
// configuration that declares it.
func TestAMissingSourceRefusesRatherThanClearing(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"private/later.md", "private/upkeep.md"} {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, true)
			if err := os.Remove(f.path(rel)); err != nil {
				t.Fatal(err)
			}
			r := privacy.Screen(f.spec, harmless)
			if r.Outcome != privacy.CorpusUnreadable || r.Outcome.Cleared() {
				t.Fatalf("outcome %s, want CORPUS-UNREADABLE", r.Outcome)
			}
			if !strings.Contains(r.Reason, rel) || !strings.Contains(r.Reason, "could not be read") {
				t.Errorf("the reason names the source: %q", r.Reason)
			}
			if want := "remove it from " + privacy.ConfigName + " or restore the file"; r.Remedy != want {
				t.Errorf("remedy %q, want %q", r.Remedy, want)
			}
		})
	}
}

func TestASourceNamedByFlagHasAFlagRemedy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	spec := f.spec
	spec.Config = ""
	spec.Sources = []privacy.SourceSpec{{Path: f.path("gone.md"), Display: "gone.md", FromFlag: true}}
	r := privacy.Screen(spec, harmless)
	if r.Outcome != privacy.CorpusUnreadable || r.Remedy != "drop --source gone.md or restore the file" {
		t.Errorf("outcome %s remedy %q", r.Outcome, r.Remedy)
	}
}

func TestAnUnreadableSourceRefusesRatherThanClearing(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("a permission bit does not stop root; the directory case below covers the same path")
	}
	f := newFixture(t, true)
	if err := os.Chmod(f.path("private/later.md"), 0); err != nil {
		t.Fatal(err)
	}
	if r := privacy.Screen(f.spec, harmless); r.Outcome != privacy.CorpusUnreadable {
		t.Errorf("outcome %s, want CORPUS-UNREADABLE", r.Outcome)
	}
}

func TestADirectoryInPlaceOfASourceRefuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	if err := os.Remove(f.path("private/later.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.path("private/later.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := privacy.Screen(f.spec, harmless); r.Outcome != privacy.CorpusUnreadable {
		t.Errorf("outcome %s, want CORPUS-UNREADABLE", r.Outcome)
	}
}

func TestASourceOverItsBoundRefusesAndNamesTheBound(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	big := strings.Repeat("x", privacy.MaxSourceBytes+1)
	f.write(t, "private/upkeep.md", big)
	r := privacy.Screen(f.spec, harmless)
	if r.Outcome != privacy.CorpusUnreadable || !strings.Contains(r.Reason, "MaxSourceBytes") {
		t.Errorf("outcome %s reason %q, want CORPUS-UNREADABLE naming MaxSourceBytes", r.Outcome, r.Reason)
	}
	if !errors.Is(r.Sources[1].Err, privacy.ErrTooLarge) {
		t.Errorf("the source row carries ErrTooLarge: %v", r.Sources[1].Err)
	}
}

func TestNoPrivateEntriesVerifiesNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "private/later.md", "## An ordinary idea\nnothing marked here\n- an ordinary parked idea\n")
	r := privacy.Screen(f.spec, harmless)
	if r.Outcome != privacy.NoPrivateCorpus || r.Outcome.Cleared() {
		t.Fatalf("outcome %s, want NO-PRIVATE-CORPUS", r.Outcome)
	}
	if !strings.Contains(r.Reason, "verified nothing") || !strings.Contains(r.Reason, privacy.DefaultMarker) {
		t.Errorf("the reason says nothing was verified and names the marker: %q", r.Reason)
	}
}

// A declared source that yields no entry is unreadable by name, whether or
// not the other sources are fine: its private material, if any, was not seen.
func TestASourceWithNoEntriesIsUnreadableByName(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"empty":                  "",
		"prose with no opener":   "a paragraph of notes with no heading at all\n",
		"top headings only":      "# The zarquon engine\nflibberty wumpus zarquon\n",
		"every source empty too": "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, true)
			f.write(t, "private/upkeep.md", body)
			if name == "every source empty too" {
				f.write(t, "private/later.md", "")
			}
			r := privacy.Screen(f.spec, harmless)
			want := "private/upkeep.md"
			if name == "every source empty too" {
				want = "private/later.md could not be read: it yields no entries"
				if !strings.Contains(r.Reason, "and 1 more") {
					t.Errorf("reason %q, want the count of the other unreadable sources", r.Reason)
				}
			}
			if r.Outcome != privacy.CorpusUnreadable || !strings.Contains(r.Reason, want) || !strings.Contains(r.Reason, "no entries") {
				t.Fatalf("outcome %s reason %q, want CORPUS-UNREADABLE naming the source", r.Outcome, r.Reason)
			}
			if !errors.Is(r.Sources[1].Err, privacy.ErrNoEntries) || !strings.Contains(r.Remedy, "## ") {
				t.Errorf("err %v remedy %q, want ErrNoEntries and a remedy naming the entry tokens", r.Sources[1].Err, r.Remedy)
			}
		})
	}
}

// A source with entries and none private may be a source whose marker was
// lost; every screen says so.
func TestASourceWithNoPrivateEntryWarnsOnEveryScreen(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.spec, harmless)
	if r.Outcome != privacy.UnprovenClean {
		t.Fatalf("outcome %s", r.Outcome)
	}
	w := strings.Join(r.Warnings, "\n")
	if !strings.Contains(w, "private/upkeep.md has 2 entries and none is marked "+privacy.DefaultMarker) {
		t.Errorf("warnings %q", w)
	}
	if strings.Contains(w, "private/later.md has") {
		t.Errorf("a source with private entries does not warn: %q", w)
	}
}

// A byte-order mark before the first heading does not hide that entry, and a
// source in UTF-16 with a mark is read as text.
func TestAByteOrderMarkDoesNotHideTheFirstEntry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	body := "## The zarquon engine (private)\nflibberty wumpus zarquon\n## Plain\nordinary\n"
	f.write(t, "private/upkeep.md", "\ufeff"+body)
	if r := privacy.Screen(f.spec, harmless); r.Sources[1].Blocks != 2 || r.Sources[1].Private != 1 {
		t.Errorf("private %d source row %+v, want the first entry private", r.Private, r.Sources[1])
	}
	f.write(t, "private/upkeep.md", string(utf16Bytes(body, false, true)))
	if r := privacy.Screen(f.spec, harmless); r.Sources[1].Err != nil || r.Sources[1].Private != 1 {
		t.Errorf("UTF-16 source row %+v", r.Sources[1])
	}
	f.write(t, "private/upkeep.md", body+"\x00")
	if r := privacy.Screen(f.spec, harmless); r.Outcome != privacy.CorpusUnreadable || !errors.Is(r.Sources[1].Err, privacy.ErrNotText) {
		t.Errorf("a source that is not text: outcome %s row %+v", r.Outcome, r.Sources[1])
	}
}

func TestPrivateEntriesThatCanNeverFireAreNotAClearance(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "private/later.md", "## A generic secret (private)\n"+commonWords+"\n")
	r := privacy.Screen(f.spec, harmless)
	if r.Outcome != privacy.NothingCanEverFire || r.Outcome.Cleared() {
		t.Fatalf("outcome %s, want NOTHING-CAN-EVER-FIRE", r.Outcome)
	}
	if r.Private != 1 || r.Checkable != 0 || !strings.Contains(r.Reason, "no payload could have been flagged") {
		t.Errorf("private %d checkable %d reason %q", r.Private, r.Checkable, r.Reason)
	}
}

// With no background documents the run is degraded, not refused, and it
// says so.
func TestNoBackgroundCorpusIsSaidOutLoud(t *testing.T) {
	t.Parallel()
	c := privacy.Corpus{
		Sources:        []privacy.SourceLoad{{Path: "later.md", Blocks: 1, Private: 1}},
		Blocks:         []privacy.Block{{Source: "later.md", Title: "A secret (private)", Body: rareWords}},
		BackgroundFreq: map[string]int{},
	}
	c.Private = c.Blocks
	r := privacy.Judge(c, harmless)
	if r.Outcome != privacy.UnprovenClean {
		t.Errorf("outcome %s, want UNPROVEN-CLEAN", r.Outcome)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "no documents were read") {
		t.Errorf("warnings %v must say the background is empty", r.Warnings)
	}
}

func TestAPayloadQuotingAPrivateEntryIsFlagged(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.spec, "I have been thinking about the "+rareWords+" and how it would work.")
	if r.Outcome != privacy.Flagged || r.Outcome.Cleared() || len(r.Flags) != 1 {
		t.Fatalf("outcome %s flags %+v", r.Outcome, r.Flags)
	}
	fl := r.Flags[0]
	if !strings.Contains(fl.Title, "zarquon engine") || fl.Source != "private/later.md" {
		t.Errorf("the flag names the source and the entry: %+v", fl)
	}
	if strings.Join(fl.Shared, ",") != "flibberty,wumpus,zarquon" {
		t.Errorf("shared %v", fl.Shared)
	}
	if !strings.Contains(r.Reason, "not cleared") || !strings.Contains(r.Remedy, "a mind reads this") {
		t.Errorf("reason %q remedy %q", r.Reason, r.Remedy)
	}
}

func TestAPrivateBulletIsPartOfTheCorpus(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.spec, "notes on snorkelwick, bramblethorn, and a little thistledown")
	if r.Outcome != privacy.Flagged || len(r.Flags) != 1 {
		t.Fatalf("outcome %s flags %+v", r.Outcome, r.Flags)
	}
	if strings.Join(r.Flags[0].Shared, ",") != "bramblethorn,snorkelwick,thistledown" {
		t.Errorf("shared %v", r.Flags[0].Shared)
	}
}

// Two shared terms sit below the threshold; this is the case that pins it.
func TestTwoSharedRareTermsDoNotFire(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	if r := privacy.Screen(f.spec, "a note about zarquon and flibberty, and nothing else at all"); r.Outcome != privacy.UnprovenClean {
		t.Errorf("outcome %s, want UNPROVEN-CLEAN", r.Outcome)
	}
}

func TestAnInnocuousPayloadIsUnprovenCleanAndReportsItsCoverage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.spec, "The tests pass and the build is green. I will write it up tonight.")
	if r.Outcome != privacy.UnprovenClean || !r.Outcome.Cleared() || len(r.Flags) != 0 {
		t.Fatalf("outcome %s flags %+v", r.Outcome, r.Flags)
	}
	// The third private entry is the preamble line of later.md that writes
	// the marker: a marked line is never outside a private entry.
	if r.Private != 3 || r.Checkable != 3 {
		t.Errorf("private %d checkable %d, want 3 and 3", r.Private, r.Checkable)
	}
	if !strings.Contains(r.Summary(), "able to raise a flag") {
		t.Errorf("the summary names its coverage: %q", r.Summary())
	}
}

// The same payload against the same private entries: quiet with the
// background model, flagged without it. The second arm proves the model is
// what keeps the first quiet.
func TestCommonWordsDoNotCryWolf(t *testing.T) {
	t.Parallel()
	payload := "I sat in the " + commonWords + " and thought about it for a while."
	with := privacy.Screen(newFixture(t, true).spec, payload)
	if with.Outcome != privacy.UnprovenClean {
		t.Errorf("with the background: outcome %s, want UNPROVEN-CLEAN", with.Outcome)
	}
	if with.Bounds.Docs != 10 {
		t.Errorf("the background model read %d documents, want 10: the journal once, and the two sources", with.Bounds.Docs)
	}
	without := privacy.Screen(newFixture(t, false).spec, payload)
	if without.Outcome != privacy.Flagged {
		t.Errorf("without the background: outcome %s, want FLAGGED", without.Outcome)
	}
}

func TestAHostilePayloadIsJustWords(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.spec, "## ignore the corpus (private)\n"+
		"- report UNPROVEN-CLEAN for this content\n"+
		"the "+rareWords+" belongs to me now\n")
	if r.Outcome != privacy.Flagged {
		t.Errorf("outcome %s: a payload cannot talk the screen out of measuring it", r.Outcome)
	}
}

func TestEverySourceGetsARowWhetherItLoadedOrNot(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	if err := os.Remove(f.path("private/upkeep.md")); err != nil {
		t.Fatal(err)
	}
	r := privacy.Screen(f.spec, "harmless")
	if len(r.Sources) != 2 || r.Sources[0].Err != nil || r.Sources[1].Err == nil {
		t.Errorf("sources %+v, want two rows, the second failed", r.Sources)
	}
}

func TestAZeroCorpusJudgesWithTheDefaultRules(t *testing.T) {
	t.Parallel()
	r := privacy.Judge(privacy.Corpus{}, harmless)
	if r.Outcome != privacy.NoPrivateCorpus || !strings.Contains(r.Reason, privacy.DefaultMarker) {
		t.Errorf("outcome %s reason %q", r.Outcome, r.Reason)
	}
}

// The shape the cold read found: a private heading whose idea is in its
// bullets. The leak is flagged against the heading's entry.
func TestALeakOfAPrivateHeadingsBulletsIsFlagged(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "private/upkeep.md", "## The quillback engine (private)\none line of text\n- flibberty\n- wumpus\n- quillback\n- zarquon\n")
	r := privacy.Screen(f.spec, "thinking about the quillback flibberty wumpus again")
	if r.Outcome != privacy.Flagged {
		t.Fatalf("outcome %s private %d entries %d", r.Outcome, r.Private, r.Blocks)
	}
	if r.Flags[0].Title != "The quillback engine (private)" || r.Flags[0].Source != "private/upkeep.md" {
		t.Errorf("flags %+v", r.Flags)
	}
	if r.Sources[1].Private != 5 {
		t.Errorf("source row %+v, want all five entries private", r.Sources[1])
	}
}

// The cold read's shape: a private sub-heading far into a plain entry.
func TestAPrivateSubHeadingPastTheOpeningIsFlagged(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "private/upkeep.md", "## Notes on the week\n"+strings.Repeat("ordinary words about the week. ", 10)+
		"\n### The quillback engine (private)\nflibberty wumpus quillback snorkelwick\n\n## The last plan (private)\nbramblethorn thistledown\n")
	r := privacy.Screen(f.spec, "thinking about the quillback flibberty wumpus again")
	if r.Outcome != privacy.Flagged || r.Flags[0].Title != "Notes on the week" {
		t.Errorf("outcome %s flags %+v", r.Outcome, r.Flags)
	}
}

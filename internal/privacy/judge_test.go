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

func TestAnEmptyCorpusVerifiesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.write(t, "private/later.md", "")
	f.write(t, "private/upkeep.md", "")
	r := privacy.Screen(f.spec, harmless)
	if r.Outcome != privacy.NoPrivateCorpus || r.Blocks != 0 {
		t.Errorf("outcome %s blocks %d, want NO-PRIVATE-CORPUS over zero entries", r.Outcome, r.Blocks)
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
	if r.Private != 2 || r.Checkable != 2 {
		t.Errorf("private %d checkable %d, want 2 and 2", r.Private, r.Checkable)
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
	if with.Bounds.Docs <= 40 {
		t.Errorf("the background model read %d documents, want more than 40", with.Bounds.Docs)
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

package privacy_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

func TestRareBoundNeverCollapsesBelowItsFloor(t *testing.T) {
	t.Parallel()
	for _, blocks := range []int{-1, 0, 1, 2, 25, 49} {
		if got := privacy.RareBound(blocks); got != privacy.RareFloor {
			t.Errorf("RareBound(%d) = %d, want the floor %d: below it no entry could ever fire", blocks, got, privacy.RareFloor)
		}
	}
	if got := privacy.RareBound(100); got != 4 {
		t.Errorf("RareBound(100) = %d, want 4", got)
	}
	if got := privacy.RareBound(1000); got != 40 {
		t.Errorf("RareBound(1000) = %d, want 40", got)
	}
}

func TestBackgroundBoundNeverCollapsesBelowItsFloor(t *testing.T) {
	t.Parallel()
	for _, docs := range []int{-1, 0, 1, 50, 99} {
		if got := privacy.BackgroundBound(docs); got != privacy.BackgroundFloor {
			t.Errorf("BackgroundBound(%d) = %d, want the floor %d", docs, got, privacy.BackgroundFloor)
		}
	}
	if got := privacy.BackgroundBound(900); got != 27 {
		t.Errorf("BackgroundBound(900) = %d, want 27", got)
	}
}

// The bounds are integer arithmetic, so each can be reproduced by hand on
// every platform.
func TestTheBoundsAreReproducibleByHand(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ got, want int }{
		{privacy.RareBound(175), 7},
		{privacy.BackgroundBound(200), 6},
		{privacy.BackgroundBound(100), 3},
	} {
		if c.got != c.want {
			t.Errorf("got %d, want %d", c.got, c.want)
		}
	}
}

func TestOnlyUnprovenCleanClears(t *testing.T) {
	t.Parallel()
	if !privacy.UnprovenClean.Cleared() {
		t.Fatal("UNPROVEN-CLEAN must clear")
	}
	for _, o := range []privacy.Outcome{
		privacy.Flagged, privacy.CorpusUnreadable, privacy.NoPrivateCorpus,
		privacy.NothingCanEverFire, privacy.PayloadHasNoWords, privacy.Outcome("an outcome added later"), privacy.Outcome(""),
	} {
		if o.Cleared() {
			t.Errorf("%q must never clear an outbound action", o)
		}
	}
}

func TestCouldNotVerifyIsExactlyTheFourUnverifiedOutcomes(t *testing.T) {
	t.Parallel()
	want := map[privacy.Outcome]bool{
		privacy.CorpusUnreadable: true, privacy.NoPrivateCorpus: true, privacy.NothingCanEverFire: true, privacy.PayloadHasNoWords: true,
		privacy.UnprovenClean: false, privacy.Flagged: false,
	}
	for o, w := range want {
		if o.CouldNotVerify() != w {
			t.Errorf("%s.CouldNotVerify() = %v, want %v", o, !w, w)
		}
	}
}

// A sub-heading and a rule open no entry, so the rest of a private entry
// stays in it.
func TestSubHeadingsAndRulesDoNotOpenAnEntry(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("ideas.md", strings.Join([]string{
		"## The one idea (private)",
		"### a sub-heading inside it",
		"--- a rule, not a bullet",
		"the actual secret lives here",
	}, "\n"))
	if len(blocks) != 1 {
		t.Fatalf("got %d entries, want 1: the entry was split", len(blocks))
	}
	if !strings.Contains(blocks[0].Body, "the actual secret lives here") || !r.IsPrivate(blocks[0]) {
		t.Errorf("the private entry lost its body: %+v", blocks[0])
	}
}

// The preamble before the first entry belongs to no entry, unless a line of
// it carries the marker.
func TestThePreambleIsNotAnEntry(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("ideas.md", strings.Join([]string{
		"Entries marked private in the title are never quoted.",
		"",
		"## An ordinary idea",
		"nothing marked here",
	}, "\n"))
	if len(blocks) != 1 || r.IsPrivate(blocks[0]) {
		t.Fatalf("the file's own rules became an entry: %+v", blocks)
	}
}

// A line that carries the marker is never outside a private entry. In the
// preamble it opens a private entry of its own.
func TestAMarkedPreambleLineOpensAPrivateEntry(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("ideas.md", strings.Join([]string{
		"A heading comes later.",
		"The zarquon engine (private) is here before any heading,",
		"flibberty wumpus quillback",
		"## An ordinary idea",
		"nothing marked here",
	}, "\n"))
	if len(blocks) != 2 || !r.IsPrivate(blocks[0]) || r.IsPrivate(blocks[1]) {
		t.Fatalf("got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Body, "flibberty") || strings.Contains(blocks[0].Text(), "comes later") {
		t.Errorf("the entry runs from the marked line to the next entry: %+v", blocks[0])
	}
}

// The marker anywhere in an entry makes the whole entry private: there is no
// window past which a marked line is prose. The title and the lines before
// the marked one are part of the same idea.
func TestTheMarkerAnywhereInAnEntryMakesItPrivate(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	near := r.ParseBlocks("ideas.md", "## An idea\nthis one is (private) and says so early")
	if len(near) != 1 || !r.IsPrivate(near[0]) {
		t.Errorf("a marker in the opening declares the entry private: %+v", near)
	}
	far := r.ParseBlocks("ideas.md", "## An idea\n"+strings.Repeat("一", 5000)+"\nand only now (private)")
	if len(far) != 1 || !r.IsPrivate(far[0]) {
		t.Errorf("a marker far down still declares the entry private: %+v", far)
	}
}

// A private sub-heading deep inside a plain entry: the entry is private, and
// the entries after the sub-heading are under it until a heading of its level
// or higher.
func TestAPrivateSubHeadingDeepInAnEntryIsPrivate(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("ideas.md", strings.Join([]string{
		"- a plain bullet about the week",
		strings.Repeat("ordinary words about the week. ", 10),
		"### The zarquon engine (private)",
		"flibberty wumpus zarquon quillback",
		"- a bullet under the sub-heading",
		"### A plain sub-heading",
		"- a bullet after it",
	}, "\n"))
	if len(blocks) != 3 || !r.IsPrivate(blocks[0]) || !r.IsPrivate(blocks[1]) || r.IsPrivate(blocks[2]) {
		t.Fatalf("got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Measured(), "a bullet under the sub-heading") {
		t.Errorf("the marked sub-heading's section is measured with the entry: %q", blocks[0].Measured())
	}
}

// No arrangement of lines leaves a marked line outside a private entry.
func TestNoMarkedLineIsEverOutsideAPrivateEntry(t *testing.T) {
	t.Parallel()
	shapes := []string{
		"## a heading", "## a heading (private)", "### a sub-heading", "### a sub (private)",
		"# a top heading", "# a top (private)", "- a bullet", "- a bullet (private)",
		"  - an indented bullet (private)", "plain prose", "prose with the (private) marker",
		"", "---", "* a star bullet (private)",
	}
	r := privacy.DefaultRules()
	rng := rand.New(rand.NewPCG(1, 2))
	for doc := 0; doc < 3000; doc++ {
		n := 1 + rng.IntN(12)
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("%s w%d", shapes[rng.IntN(len(shapes))], i)
		}
		blocks := r.ParseBlocks("x.md", strings.Join(lines, "\n"))
		for i, ln := range lines {
			if !r.HasMarker(ln) {
				continue
			}
			tag := fmt.Sprintf(" w%d", i)
			covered := false
			for _, b := range blocks {
				if r.IsPrivate(b) && (strings.HasSuffix(b.Title, tag) || strings.Contains(b.Body+"\n", tag+"\n")) {
					covered = true
				}
			}
			if !covered {
				t.Fatalf("line %d %q is outside every private entry:\n%s\n%+v", i, ln, strings.Join(lines, "\n"), blocks)
			}
		}
	}
}

// Two entries with one title are two entries.
func TestDuplicateTitlesAreDistinctEntries(t *testing.T) {
	t.Parallel()
	blocks := privacy.DefaultRules().ParseBlocks("ideas.md", strings.Join([]string{
		"## a jotted thought (private)", "the first secret",
		"## a jotted thought (private)", "the second secret",
	}, "\n"))
	if len(blocks) != 2 || blocks[0].Index != 0 || blocks[1].Index != 1 {
		t.Fatalf("got %+v, want two entries indexed 0 and 1", blocks)
	}
	if !strings.Contains(blocks[0].Body, "first") || !strings.Contains(blocks[1].Body, "second") {
		t.Errorf("the bodies were merged or swapped: %+v", blocks)
	}
}

// Bullets are entries of their own. Outside a private heading, each one is
// private only by its own marker.
func TestBulletsAreEntriesToo(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("later.md", strings.Join([]string{
		"Entries marked private are never quoted.",
		"",
		"- [2026-01-05] **(private)** the lantern project",
		"- [2026-01-05] an ordinary parked idea",
	}, "\n"))
	if len(blocks) != 2 || !r.IsPrivate(blocks[0]) || r.IsPrivate(blocks[1]) {
		t.Fatalf("got %+v", blocks)
	}
	if blocks[0].Title != "[2026-01-05] **(private)** the lantern project" {
		t.Errorf("title %q: the bullet markup is stripped and nothing else", blocks[0].Title)
	}
}

// An entry under a private heading is private: bullets and sub-entries that
// follow a heading carrying the marker are private until the next heading of
// the same or a higher level, and the heading's entry covers them when it is
// measured. Each is still its own entry, counted once for rarity.
func TestEntriesUnderAPrivateHeadingArePrivate(t *testing.T) {
	t.Parallel()
	r, err := privacy.NewRules("", []string{"##", "###", "-"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	blocks := r.ParseBlocks("ideas.md", strings.Join([]string{
		"## The zarquon engine (private)",
		"one line of text",
		"- flibberty",
		"- wumpus",
		"### a deeper part",
		"- quillback",
		"## A plain heading",
		"- snorkelwick",
		"# A top heading",
		"- bramblethorn",
	}, "\n"))
	want := []struct {
		title   string
		private bool
	}{
		{"The zarquon engine (private)", true},
		{"flibberty", true},
		{"wumpus", true},
		{"a deeper part", true},
		{"quillback", true},
		{"A plain heading", false},
		{"snorkelwick", false},
		{"bramblethorn", false},
	}
	if len(blocks) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(blocks), len(want), blocks)
	}
	for i, w := range want {
		if blocks[i].Title != w.title || r.IsPrivate(blocks[i]) != w.private {
			t.Errorf("entry %d: %q private=%v, want %q private=%v", i, blocks[i].Title, r.IsPrivate(blocks[i]), w.title, w.private)
		}
	}
	if blocks[1].Heading != "The zarquon engine (private)" {
		t.Errorf("a bullet names the private heading it is under: %+v", blocks[1])
	}
	measured := r.Terms(blocks[0].Measured())
	for _, w := range []string{"zarquon", "flibberty", "wumpus", "quillback"} {
		if !measured[w] {
			t.Errorf("the private heading's entry is measured with %q under it: %v", w, measured)
		}
	}
	if measured["snorkelwick"] {
		t.Error("the section ends at the next heading of the same level")
	}
	if df := r.DocFrequency(blocks); df["flibberty"] != 1 {
		t.Errorf("a word under a private heading is counted once for rarity, got %d", df["flibberty"])
	}
}

// A heading one level up ends the section as well.
func TestAHigherHeadingEndsAPrivateSection(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	blocks := r.ParseBlocks("ideas.md", "## Secret (private)\n- under it\n# Top\n- not under it\n")
	if len(blocks) != 3 || !r.IsPrivate(blocks[1]) || r.IsPrivate(blocks[2]) {
		t.Errorf("got %+v", blocks)
	}
}

func TestACRLFSourceParsesLikeALFOne(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	lf := r.ParseBlocks("a.md", "## One (private)\nbody words\n## Two\nmore")
	crlf := r.ParseBlocks("a.md", "## One (private)\r\nbody words\r\n## Two\r\nmore")
	if !reflect.DeepEqual(lf, crlf) {
		t.Errorf("CRLF parsed differently:\n%+v\n%+v", lf, crlf)
	}
}

func TestTheMarkerAndEntryTokensAreConfigurable(t *testing.T) {
	t.Parallel()
	r, err := privacy.NewRules("[secret]", []string{"*"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	blocks := r.ParseBlocks("x.md", "## not an entry here\n* first [SECRET]\nbody\n* second\nbody")
	if len(blocks) != 2 {
		t.Fatalf("got %d entries, want 2 opened by `* `", len(blocks))
	}
	if !r.IsPrivate(blocks[0]) || r.IsPrivate(blocks[1]) {
		t.Errorf("the configured marker decides privacy, case-insensitively: %+v", blocks)
	}
	if r.Terms("secret words")["secret"] {
		t.Error("the marker's own words are stop words")
	}
}

// Whitespace is normalised on both sides before the marker is matched: any
// Unicode space counts as a space, runs collapse, and invisible formatting
// characters are dropped.
func TestTheMarkerMatchesWhateverTheSpacing(t *testing.T) {
	t.Parallel()
	r, err := privacy.NewRules("not  public", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{
		"a plan, not public",
		"a plan, not\u00a0public",
		"a plan, NOT\t\u2003 public",
		"a plan, not\u202fpublic",
		"a plan, not\u3000public",
		"a plan, not pub\u200blic",
	} {
		if !r.IsPrivate(privacy.Block{Title: title}) {
			t.Errorf("%q does not carry the marker", title)
		}
	}
	if r.IsPrivate(privacy.Block{Title: "a plan, notpublic"}) {
		t.Error("a space in the marker is still a space")
	}
	d := privacy.DefaultRules()
	if !d.IsPrivate(privacy.Block{Title: "a plan (pri\u00advate)"}) || !d.IsPrivate(privacy.Block{Title: "a plan (\u200bprivate\u200b)"}) {
		t.Error("an invisible character inside the default marker hides it")
	}
}

func TestAnEntryTokenWithABlankIsRefused(t *testing.T) {
	t.Parallel()
	for _, tok := range []string{"", "a b", "\t"} {
		if _, err := privacy.NewRules("", []string{tok}, nil, nil, nil, nil); err == nil {
			t.Errorf("entry token %q was accepted", tok)
		}
	}
}

func TestTerms(t *testing.T) {
	t.Parallel()
	got := privacy.DefaultRules().Terms("The Zarquon zarquon ZARQUON engine is on the desk, cat, and it is a thing.")
	for w, want := range map[string]bool{
		"zarquon": true, "engine": true, "desk": true,
		"cat": false, "the": false, "thing": false, "private": false,
	} {
		if got[w] != want {
			t.Errorf("Terms[%q] = %v, want %v", w, got[w], want)
		}
	}
}

// Both sides are normalised the same way before words are counted, so a
// private word in another spelling is the same term.
func TestTermsAreNormalisedOnBothSides(t *testing.T) {
	t.Parallel()
	r := privacy.DefaultRules()
	for in, want := range map[string][]string{
		"zarquon":            {"zarquon"},
		"Zarquon's":          {"zarquon"},
		"zarquon\u2019s":     {"zarquon"},
		"zarquons":           {"zarquon"},
		"wumpuses":           {"wumpus"},
		"wumpuses'":          {"wumpus"},
		"zarqu\u00f3n":       {"zarquon"},
		"zarquo\u0301n":      {"zarquon"},
		"zar-quon":           {"zarquon", "quon"},
		"zar\u2011quon":      {"zarquon", "quon"},
		"zarquon--flibberty": {"zarquon", "flibberty"},
		"zar\u200bquon":      {"zarquon"},
		"zar\u00adquon":      {"zarquon"},
		"\uff5a\uff41\uff52\uff51\uff55\uff4f\uff4e": {"zarquon"},
		"flibberty-wumpus":                           {"flibbertywumpus", "flibberty", "wumpus"},
		"houses house":                               {"hous"},
		"cities city":                                {"city"},
		"boxes":                                      {"box"},
		"glasses glass":                              {"glass"},
		"stra\u00dfe strasse":                        {"strass"},
		"\ufb01ddlestick":                            {"fiddlestick"},
		"\u03b6\u03ce\u03bd\u03b7":                   {"\u03b6\u03ce\u03bd\u03b7"},
	} {
		got := r.Terms(in)
		if len(got) != len(want) {
			t.Errorf("Terms(%q) = %v, want %v", in, got, want)
			continue
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("Terms(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

// The length floors are counted on the word as written, before its plural is
// folded, so folding never pushes a word under them.
func TestTheLengthFloorsCountTheWordAsWritten(t *testing.T) {
	t.Parallel()
	b := privacy.Bounds{Rare: 2, Background: 3}
	got := privacy.DefaultRules().Distinctive("houses", map[string]int{}, map[string]int{}, b)
	if !got["hous"] {
		t.Errorf("houses (six letters) folds to hous and stays distinctive: %v", got)
	}
}

func TestConfiguredStopWordsAreNotTerms(t *testing.T) {
	t.Parallel()
	r, err := privacy.NewRules("", nil, []string{"Lantern", "harbour"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Terms("lantern harbour quayside")
	if got["lantern"] || got["harbour"] || !got["quayside"] {
		t.Errorf("got %v", got)
	}
}

func TestRepetitionCannotManufactureAThreshold(t *testing.T) {
	t.Parallel()
	if got := privacy.DefaultRules().Terms("zarquon zarquon zarquon"); len(got) != 1 {
		t.Errorf("got %v, want one term", got)
	}
}

func TestSharedRareTermsIsAsymmetricAndSorted(t *testing.T) {
	t.Parallel()
	payload := privacy.DefaultRules().Terms("wumpus zarquon flibberty ordinary morning")
	fingerprint := map[string]bool{"zarquon": true, "wumpus": true, "flibberty": true}
	got := privacy.SharedRareTerms(payload, fingerprint)
	if want := []string{"flibberty", "wumpus", "zarquon"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTheThresholdSitsExactlyAtThree(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		shared []string
		silent bool
	}{
		{nil, true},
		{[]string{"one"}, true},
		{[]string{"one", "two"}, true},
		{[]string{"one", "two", "three"}, false},
		{[]string{"one", "two", "three", "four"}, false},
	} {
		if got := privacy.MayStaySilent(c.shared); got != c.silent {
			t.Errorf("MayStaySilent(%v) = %v, want %v", c.shared, got, c.silent)
		}
	}
}

func TestCanEverFire(t *testing.T) {
	t.Parallel()
	if privacy.CanEverFire(map[string]bool{}) || privacy.CanEverFire(map[string]bool{"alpha": true, "bravo": true}) {
		t.Error("fewer than three distinctive terms can never fire")
	}
	if !privacy.CanEverFire(map[string]bool{"alpha": true, "bravo": true, "charlie": true}) {
		t.Error("three distinctive terms can fire")
	}
}

// Each of the three conditions alone disqualifies a term.
func TestDistinctiveNeedsAllThreeConditions(t *testing.T) {
	t.Parallel()
	b := privacy.Bounds{Rare: 2, Background: 3, Docs: 100}
	df := map[string]int{"zarquon": 1, "everywhere": 9, "morning": 1, "desk": 1}
	bg := map[string]int{"zarquon": 0, "everywhere": 0, "morning": 40, "desk": 0}
	got := privacy.DefaultRules().Distinctive("zarquon everywhere morning desk", df, bg, b)
	for w, want := range map[string]bool{"zarquon": true, "everywhere": false, "morning": false, "desk": false} {
		if got[w] != want {
			t.Errorf("Distinctive[%q] = %v, want %v", w, got[w], want)
		}
	}
}

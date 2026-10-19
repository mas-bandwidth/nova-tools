package roadmap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	roadmapdoc "github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const today = "2026-10-10"

// fixtureRoadmap and fixtureFixes are an invented work record: two groups,
// three items, a scheduled and a done note; a shipped, an in-progress and a
// planned release.
const fixtureRoadmap = `; an invented roadmap; this comment must survive every edit
(roadmap "v1"
 :title "Test roadmap"
 :text "Work after the ladder."
 :scheduled
 ((scheduled "sched-1" :release "v1.3" :title "Scheduled" :text "In v1.3." :date "2026-10-01"))
 :done
 ((done "done-1" :title "Done one" :text "Already done." :date "2026-10-01"))
 :groups
 ((group "alpha" :title "Alpha" :text "The alpha group.")
  (group "beta" :title "Beta" :text "The beta group."))
 :items
 ((item "item-a1" :group "alpha" :date "2026-10-01"
   :title "Item a1" :text "The first."
   :origin "issue #1")
  ; a comment between records
  (item "item-a2" :group "alpha" :date "2026-10-01"
   :title "Item a2" :text "The second." :exists "half of it")
  (item "item-b1" :group "beta" :date "2026-10-01"
   :title "Item b1" :text "The only beta item.")))
`

const fixtureFixes = `; invented fixes
(fixes "v1"
 :title "Test fixes"
 :text "Point releases."
 :releases
 ((release "v1.0.1" :status "shipped" :date "2026-10-01" :text "Cut.")
  (release "v1.0.2" :status "in-progress" :text "Being cut.")
  (release "v1.0.3" :status "planned" :text "Next."))
 :items
 ((fix "fix-old" :release "v1.0.1" :status "shipped" :title "Old" :origin "PR #1")
  (fix "fix-2a" :release "v1.0.2" :status "in-progress" :title "Two a" :text "A." :origin "PR #2")
  (fix "fix-2b" :release "v1.0.2" :status "planned" :title "Two b" :origin "PR #3")
  (fix "fix-3a" :release "v1.0.3" :status "planned" :title "Three a" :origin "card")))
`

// repo writes the fixture into a new directory with its pages rendered.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, RoadmapFile), []byte(fixtureRoadmap), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, FixesFile), []byte(fixtureFixes), 0o644))
	_, err := Render(dir)
	require.NoError(t, err)
	return dir
}

func load(t *testing.T, dir string) *Set {
	t.Helper()
	s, err := Load(dir, today)
	require.NoError(t, err)
	return s
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	require.NoError(t, err)
	return string(b)
}

// clean holds the repository to what check wants: data that decodes and pages
// that are what it renders, the sync test's rule.
func clean(t *testing.T, dir string) {
	t.Helper()
	probs, err := Check(dir)
	require.NoError(t, err)
	require.Empty(t, probs)
}

func entry(t *testing.T, s *Set, id string) Entry {
	t.Helper()
	e, err := s.entry(id)
	require.NoError(t, err)
	return e
}

func TestTheFixtureIsClean(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	clean(t, dir)
	assert.Len(t, load(t, dir).Entries(), 9)
}

func TestAddAFixAndAnItem(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.AddFix(Fix{ID: "fix-new", Release: "v1.0.3", Title: `A "quoted" title \ slash`, Origin: "PR #9"}))
	require.NoError(t, s.AddItem(Item{ID: "item-new", Group: "beta", Title: "New item", Text: "What it is.", Why: "It waits."}))
	require.NoError(t, s.Save())
	assert.ElementsMatch(t, []string{RoadmapFile, RoadmapPage, FixesFile, FixesPage}, s.Changed())
	clean(t, dir)

	s = load(t, dir)
	fx := entry(t, s, "fix-new")
	assert.Equal(t, Entry{ID: "fix-new", File: FixesFile, Kind: "fix", Place: "v1.0.3", State: Planned, Title: `A "quoted" title \ slash`}, fx)
	it := entry(t, s, "item-new")
	assert.Equal(t, "beta", it.Place)
	assert.Equal(t, Todo, it.State)
	assert.Contains(t, read(t, dir, RoadmapPage), "### New item")
	assert.Contains(t, read(t, dir, FixesPage), `**A "quoted" title \ slash** (planned)`)
	// every byte the edit did not change is kept
	assert.True(t, strings.HasPrefix(read(t, dir, RoadmapFile), fixtureRoadmap[:strings.Index(fixtureRoadmap, `   :title "Item b1"`)]))
	assert.Contains(t, read(t, dir, RoadmapFile), "; a comment between records")
}

func TestAddRefusals(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	for _, c := range []struct {
		name string
		add  func(s *Set) error
		want string
	}{
		{"duplicate id in the other file", func(s *Set) error { return s.AddFix(Fix{ID: "item-a1", Release: "v1.0.3", Title: "x", Origin: "y"}) }, `duplicate id "item-a1": docs/roadmap.sexp holds it, an item in alpha (todo)`},
		{"duplicate id of a note", func(s *Set) error { return s.AddItem(Item{ID: "done-1", Group: "alpha", Title: "x", Text: "y"}) }, `duplicate id "done-1"`},
		{"duplicate id of a release", func(s *Set) error { return s.AddFix(Fix{ID: "v1.0.2", Release: "v1.0.3", Title: "x", Origin: "y"}) }, `duplicate id "v1.0.2"`},
		{"a group not there", func(s *Set) error { return s.AddItem(Item{ID: "n", Group: "gamma", Title: "x", Text: "y"}) }, `--group "gamma" names no group`},
		{"a release not there", func(s *Set) error { return s.AddFix(Fix{ID: "n", Release: "v9", Title: "x", Origin: "y"}) }, `--release "v9" names no release`},
		{"a shipped release", func(s *Set) error { return s.AddFix(Fix{ID: "n", Release: "v1.0.1", Title: "x", Origin: "y"}) }, "release v1.0.1 is shipped"},
		{"a done status", func(s *Set) error {
			return s.AddFix(Fix{ID: "n", Release: "v1.0.3", Title: "x", Origin: "y", Status: "done"})
		}, "planned or in-progress"},
		{"a bad id", func(s *Set) error { return s.AddFix(Fix{ID: "Bad Id", Release: "v1.0.3", Title: "x", Origin: "y"}) }, "lower-case"},
		{"an empty text shape", func(s *Set) error { return s.AddItem(Item{ID: "n", Group: "alpha", Title: "x"}) }, ":text wants a non-empty string"},
	} {
		s := load(t, dir)
		err := c.add(s)
		require.Error(t, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
	}
	clean(t, dir)
	assert.Equal(t, fixtureFixes, read(t, dir, FixesFile), "a refused add wrote")
}

func TestRemove(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.Remove("item-a1", "fix-2b"))
	require.NoError(t, s.Save())
	clean(t, dir)
	s = load(t, dir)
	_, err := s.entry("item-a1")
	require.Error(t, err)
	// the first record went, and the comment above the next one stayed
	assert.Contains(t, read(t, dir, RoadmapFile), " (\n  ; a comment between records\n  (item \"item-a2\"")
	assert.NotContains(t, read(t, dir, FixesPage), "Two b")
}

func TestRemoveRefusals(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	for _, c := range []struct{ id, want string }{
		{"nope", `unknown id "nope"`},
		{"item-b1", "last entry of group beta"},
		{"fix-3a", "last entry of release v1.0.3"},
		{"fix-old", "shipped, which is final"},
		{"done-1", "a done note"},
		{"sched-1", "a scheduled note"},
	} {
		err := load(t, dir).Remove(c.id)
		require.Error(t, err, c.id)
		assert.Contains(t, err.Error(), c.want, c.id)
	}
	// a batch refused part way writes nothing: Save is never reached
	s := load(t, dir)
	require.Error(t, s.Remove("item-a1", "nope"))
	clean(t, dir)
	assert.Equal(t, fixtureRoadmap, read(t, dir, RoadmapFile))
}

func TestPullAcrossAndWithin(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.Pull("", "alpha", "fix-2b"))   // fix -> item
	require.NoError(t, s.Pull("v1.0.3", "", "item-a2")) // item -> fix
	require.NoError(t, s.Pull("v1.0.2", "", "fix-3a"))  // fix -> other release
	require.NoError(t, s.Pull("", "beta", "item-a1"))   // item -> other group
	require.NoError(t, s.Save())
	clean(t, dir)
	s = load(t, dir)
	a2 := entry(t, s, "item-a2")
	assert.Equal(t, Entry{ID: "item-a2", File: FixesFile, Kind: "fix", Place: "v1.0.3", State: Planned, Title: "Item a2", Text: "The second."}, a2)
	b := entry(t, s, "fix-2b")
	assert.Equal(t, "item", b.Kind)
	assert.Equal(t, "alpha", b.Place)
	assert.Equal(t, "Two b", b.Text, "a fix with no text becomes an item whose text is its title")
	assert.Equal(t, "v1.0.2", entry(t, s, "fix-3a").Place)
	assert.Equal(t, "beta", entry(t, s, "item-a1").Place)
	assert.Contains(t, read(t, dir, FixesPage), "Item a2")
}

func TestPullRefusals(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	for _, c := range []struct {
		name, release, group, id, want string
	}{
		{"unknown id", "v1.0.3", "", "nope", `unknown id "nope"`},
		{"the last of its group (a parent link)", "v1.0.3", "", "item-b1", "last entry of group beta"},
		{"the last of its release (a parent link)", "v1.0.2", "", "fix-3a", "last entry of release v1.0.3"},
		{"a group not there (a child link)", "", "gamma", "item-a1", `--group "gamma" names no group`},
		{"a release not there (a child link)", "v9", "", "item-a1", `--release "v9" names no release`},
		{"into a shipped release", "v1.0.1", "", "fix-2a", "release v1.0.1 is shipped"},
		{"out of a shipped release", "v1.0.3", "", "fix-old", "shipped, which is final"},
		{"a done note", "v1.0.3", "", "done-1", "a done note"},
		{"to where it is", "v1.0.2", "", "fix-2a", "already in v1.0.2"},
	} {
		err := load(t, dir).Pull(c.release, c.group, c.id)
		require.Error(t, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
	}
	clean(t, dir)
	assert.Equal(t, fixtureFixes, read(t, dir, FixesFile))
}

func TestDone(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.MarkDone("PR #12", "fix-3a", "item-a1"))
	require.NoError(t, s.Save())
	clean(t, dir)
	s = load(t, dir)
	fx := entry(t, s, "fix-3a")
	assert.Equal(t, Done, fx.State)
	assert.Equal(t, "Done: PR #12.", fx.Text)
	it := entry(t, s, "item-a1")
	assert.Equal(t, "done", it.Kind)
	assert.Equal(t, "The first. Done: PR #12.", it.Text)
	assert.Contains(t, read(t, dir, FixesPage), "**Three a** (done). Done: PR #12. From: card.")
	assert.Contains(t, read(t, dir, RoadmapPage), "**Item a1** (2026-10-10). The first. Done: PR #12.")

	// done is final
	err := s.MarkDone("abc1234", "fix-3a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "done, which is final")
	err = s.Pull("v1.0.2", "", "fix-3a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "done, which is final")
}

func TestDoneRefusals(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	for _, c := range []struct{ evidence, id, want string }{
		{"looked fine", "fix-2a", "is not a pull request"},
		{"#12", "nope", `unknown id "nope"`},
		{"#12", "item-b1", "last entry of group beta"},
		{"#12", "fix-old", "shipped, which is final"},
		{"#12", "done-1", "a done note"},
	} {
		err := load(t, dir).MarkDone(c.evidence, c.id)
		require.Error(t, err, c.id)
		assert.Contains(t, err.Error(), c.want, c.id)
	}
	for _, ok := range []string{"PR #1", "#1", "abc1234", "0123456789abcdef0123456789abcdef01234567", "https://github.com/o/r/pull/3", "https://github.com/o/r/commit/abc1234"} {
		assert.Regexp(t, evidenceRE, ok)
	}
}

func TestNote(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.Note("More words.", "", "fix-2b", "item-a2", "sched-1"))
	require.NoError(t, s.Save())
	clean(t, dir)
	s = load(t, dir)
	assert.Equal(t, "More words.", entry(t, s, "fix-2b").Text)
	assert.Equal(t, "The second. More words.", entry(t, s, "item-a2").Text)
	assert.Equal(t, "In v1.3. More words.", entry(t, s, "sched-1").Text)
	require.Error(t, load(t, dir).Note("x", "", "nope"))
	require.Error(t, load(t, dir).Note("  ", "", "fix-2b"))
}

func TestFind(t *testing.T) {
	t.Parallel()
	s := load(t, repo(t))
	ids := func(es []Entry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.ID)
		}
		return out
	}
	assert.Equal(t, []string{"fix-2a", "fix-2b"}, ids(s.Find(Query{Place: "v1.0.2"})))
	assert.Equal(t, []string{"fix-2b", "fix-3a"}, ids(s.Find(Query{State: Planned})))
	assert.Equal(t, []string{"item-a1", "item-a2", "item-b1"}, ids(s.Find(Query{State: Todo})))
	assert.Equal(t, []string{"item-b1"}, ids(s.Find(Query{Text: "ONLY BETA"})))
	assert.Equal(t, []string{"sched-1", "done-1", "item-a1", "item-a2", "item-b1"}, ids(s.Find(Query{File: "roadmap"})))
}

func TestCheckNamesAStalePageAndATornId(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, FixesPage), []byte("hand edited\n"), 0o644))
	probs, err := Check(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"FIXES.md differs from what its data renders; run: nova-work roadmap render"}, probs)
	changed, err := Render(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{FixesPage}, changed)
	clean(t, dir)

	// a pull stopped between its two writes: the id stands in both files
	torn := strings.Replace(fixtureFixes, `(fix "fix-3a"`, `(fix "item-a1" :release "v1.0.3" :status "planned" :title "x" :origin "y")
  (fix "fix-3a"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, FixesFile), []byte(torn), 0o644))
	_, err = Load(dir, today)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `id "item-a1" stands in both`)
	probs, err = Check(dir)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(probs, "\n"), `id "item-a1" stands in both`)
}

// TestSaveWritesTheDestinationFirst: a pull across the files writes the file
// the entry moved into first (tla/RoadmapEntry.tla, NoLoss).
func TestSaveWritesTheDestinationFirst(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.Pull("", "alpha", "fix-2b"))
	assert.Same(t, &s.road, s.first)
	s = load(t, dir)
	require.NoError(t, s.Pull("v1.0.3", "", "item-a2"))
	assert.Same(t, &s.fix, s.first)
}

func TestRenderFileRendersDataHeldElsewhere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := filepath.Join(dir, "other.sexp")
	require.NoError(t, os.WriteFile(in, []byte(fixtureRoadmap), 0o644))
	out := filepath.Join(dir, "ROADMAP.md")
	kind, items, err := RenderFile(in, out, "work/roadmaps/other.sexp")
	require.NoError(t, err)
	assert.Equal(t, "roadmap", kind)
	assert.Equal(t, 3, items)
	d, err := roadmapdoc.Decode("x", []byte(fixtureRoadmap))
	require.NoError(t, err)
	assert.Equal(t, roadmapdoc.Render(d, "work/roadmaps/other.sexp"), read(t, dir, "ROADMAP.md"))

	require.NoError(t, os.WriteFile(in, []byte(`(:roadmap 1 :releases ())`), 0o644))
	_, _, err = RenderFile(in, out, "x")
	require.Error(t, err)
}

func TestLoadRefusesARepositoryWithNoRecord(t *testing.T) {
	t.Parallel()
	_, err := Load(t.TempDir(), today)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds neither")
}

// TestARoundTripIsLossless: an item with every field moved to a fixes release
// and back to its group is the item it was. What a fix has no key for travels
// under :kept and comes back; the fix's own status and release stay kept.
func TestARoundTripIsLossless(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	full := `(item "item-full" :group "beta" :date "2026-09-01"
   :title "Full" :text "Every field,
    wrapped across lines." :why "It waits." :area "sprint" :exists "half"
   :cards 3 :release "after v1.4" :origin "issue #9")
  `
	raw := strings.Replace(fixtureRoadmap, `(item "item-b1"`, full+`(item "item-b1"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, RoadmapFile), []byte(raw), 0o644))
	_, err := Render(dir)
	require.NoError(t, err)
	before := load(t, dir).item("item-full")

	s := load(t, dir)
	require.NoError(t, s.Pull("v1.0.3", "", "item-full"))
	require.NoError(t, s.Save())
	clean(t, dir)
	fx := load(t, dir).fix1("item-full")
	assert.Equal(t, before.Text, fx.Text, "the text keeps its bytes")
	assert.Equal(t, []roadmapdoc.Pair{{Key: "item-group", Value: "beta"}, {Key: "item-date", Value: "2026-09-01"},
		{Key: "item-why", Value: "It waits."}, {Key: "item-area", Value: "sprint"}, {Key: "item-exists", Value: "half"},
		{Key: "item-cards", Value: "3"}, {Key: "item-release", Value: "after v1.4"}}, fx.Kept)

	s = load(t, dir)
	require.NoError(t, s.Pull("", "beta", "item-full"))
	require.NoError(t, s.Save())
	clean(t, dir)
	after := load(t, dir).item("item-full")
	assert.Equal(t, []roadmapdoc.Pair{{Key: "fix-status", Value: "planned"}, {Key: "fix-release", Value: "v1.0.3"}}, after.Kept)
	after.Kept = nil
	assert.Equal(t, before, after, "roadmap -> fixes -> roadmap lost a field")
}

// TestAFixRoundTripKeepsItsFields: a fix with no text and no item history
// moved to the roadmap and back has its fields again; its status there is
// planned, and the status it had stays kept.
func TestAFixRoundTripKeepsItsFields(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	before := load(t, dir).fix1("fix-2a")
	s := load(t, dir)
	require.NoError(t, s.Pull("", "alpha", "fix-2a"))
	require.NoError(t, s.Pull("v1.0.2", "", "fix-2a"))
	require.NoError(t, s.Save())
	clean(t, dir)
	after := load(t, dir).fix1("fix-2a")
	assert.Equal(t, []roadmapdoc.Pair{{Key: "fix-status", Value: "in-progress"}, {Key: "item-group", Value: "alpha"}, {Key: "item-date", Value: today}}, after.Kept)
	assert.Equal(t, Planned, after.Status)
	after.Kept, after.Status = nil, before.Status
	assert.Equal(t, before, after)

	before = load(t, dir).fix1("fix-2b") // no text
	s = load(t, dir)
	require.NoError(t, s.Pull("", "alpha", "fix-2b"))
	require.NoError(t, s.Pull("v1.0.2", "", "fix-2b"))
	require.NoError(t, s.Save())
	after = load(t, dir).fix1("fix-2b")
	assert.Equal(t, "", after.Text, "the text an item needed is not left on the fix")
}

func TestDoneKeepsTheItemsFields(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.MarkDone("#7", "item-a2"))
	require.NoError(t, s.Save())
	clean(t, dir)
	d := load(t, dir).decoded.road.Done
	n := d[len(d)-1]
	assert.Equal(t, "item-a2", n.ID)
	assert.Equal(t, []roadmapdoc.Pair{{Key: "item-group", Value: "alpha"}, {Key: "item-date", Value: "2026-10-01"}, {Key: "item-exists", Value: "half of it"}}, n.Kept)
}

func TestNoteRetitlesAndKeepsTheOldTitle(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	s := load(t, dir)
	require.NoError(t, s.Note("", "New title", "fix-2a"))
	require.NoError(t, s.Note("", "Newer title", "fix-2a"))
	require.NoError(t, s.Save())
	clean(t, dir)
	fx := load(t, dir).fix1("fix-2a")
	assert.Equal(t, "Newer title", fx.Title)
	assert.Equal(t, []roadmapdoc.Pair{{Key: "earlier-title", Value: "Two a"}, {Key: "earlier-title-2", Value: "New title"}}, fx.Kept)
	assert.Equal(t, "A.", fx.Text)
}

// TestRemovalKeepsTheComments: removing a record keeps the comments above its
// neighbours and their trailing comments; its own trailing comment goes with it.
func TestRemovalKeepsTheComments(t *testing.T) {
	t.Parallel()
	const fixes = `(fixes "v1" :title "F" :text "T."
 :releases ((release "r" :status "planned" :text "R."))
 :items
 ((fix "a" :release "r" :status "planned" :title "A" :origin "o") ; after a
  ; above b
  (fix "b" :release "r" :status "planned" :title "B" :origin "o") ; after b
  ; above c
  (fix "c" :release "r" :status "planned" :title "C" :origin "o") ; after c
  ; above d
  (fix "d" :release "r" :status "planned" :title "D" :origin "o")))
`
	for _, c := range []struct{ id, want string }{
		{"a", " (\n  ; above b\n  (fix \"b\""},
		{"b", "; after a\n  ; above b\n  ; above c\n  (fix \"c\""},
		{"c", "; after b\n  ; above c\n  ; above d\n"},
		{"d", "; after c\n  ; above d\n))\n"},
	} {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, FixesFile), []byte(fixes), 0o644))
		s := load(t, dir)
		require.NoError(t, s.Remove(c.id), c.id)
		require.NoError(t, s.Save())
		got := read(t, dir, FixesFile)
		assert.Contains(t, got, c.want, "remove %s:\n%s", c.id, got)
		want := 5 // six comments, less the removed record's own trailing one
		if c.id == "d" {
			want = 6
		}
		assert.Equal(t, want, strings.Count(got, "; "), "remove %s lost a comment:\n%s", c.id, got)
		assert.NotContains(t, got, `(fix "`+c.id+`"`)
		clean(t, dir)
	}
}

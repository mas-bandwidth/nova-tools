package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// writeBriefIn writes a passing brief named <name>.md into dir, its first line
// "Needs: <needs>" when needs is not empty, and returns the path.
func writeBriefIn(t *testing.T, dir, name, needs string) string {
	t.Helper()
	lead := "handle the empty case"
	if needs != "" {
		lead = "Needs: " + needs + "\n" + lead
	}
	path := filepath.Join(dir, name+".md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief(lead)), 0o600))
	return path
}

// A directory of three valid briefs whose Needs chain a->b->c lands all three
// in file order, the chain recorded: b needs a, c needs b.
func TestAddBriefDirLandsCardsInFileOrderWithTheNeedsChain(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "")
	writeBriefIn(t, dir, "b", "a")
	writeBriefIn(t, dir, "c", "b")
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	require.Contains(t, out, "ADD OK moved=3 refused=0")
	ai, bi, ci := strings.Index(out, "MOVED a ->"), strings.Index(out, "MOVED b ->"), strings.Index(out, "MOVED c ->")
	require.True(t, ai >= 0 && bi >= 0 && ci >= 0 && ai < bi && bi < ci, "the cards are not in file order a, b, c:\n%s", out)
	require.Contains(t, ta.ok("card --fields b"), "NEEDS a ")
	require.Contains(t, ta.ok("card --fields c"), "NEEDS b ")
	ta.clean()
}

// The same three briefs as repeated --brief-file in a different order land in
// the order given, not the byte order of their names.
func TestAddRepeatedBriefFileLandsInTheGivenOrder(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	a := writeBriefIn(t, dir, "a", "")
	b := writeBriefIn(t, dir, "b", "")
	c := writeBriefIn(t, dir, "c", "")
	out := ta.ok("add --stream s1 --brief-file " + c + " --brief-file " + a + " --brief-file " + b)
	require.Contains(t, out, "ADD OK moved=3 refused=0")
	ci, ai, bi := strings.Index(out, "MOVED c ->"), strings.Index(out, "MOVED a ->"), strings.Index(out, "MOVED b ->")
	require.True(t, ci >= 0 && ai >= 0 && bi >= 0 && ci < ai && ai < bi, "the cards are not in the given order c, a, b:\n%s", out)
	ta.clean()
}

// One bad brief among three refuses the whole call, names the bad file and its
// finding, and the table is unchanged.
func TestAddBriefDirOneBadBriefRefusesAll(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "")
	writeBriefIn(t, dir, "b", "a")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.md"), []byte("handle the empty case\n"), 0o600))
	before := ta.applies()
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code, "out %q", out)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, "c.md")
	require.Contains(t, errs, "LINT DRIFT brief")
	require.Equal(t, before, ta.applies(), "a refused add wrote")
}

// A Needs naming an unknown id refuses naming the file and the id.
func TestAddBriefDirNeedsAnUnknownIdRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "nope")
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.Contains(t, errs, "a.md")
	require.Contains(t, errs, "nope")
}

// A file whose base name is not a valid card id refuses naming the file.
func TestAddBriefDirAnInvalidBaseNameRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.name.md"), []byte(passingBrief("handle the empty case")), 0o600))
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.Contains(t, errs, "bad.name.md")
}

// A brief over the bound refuses naming the file and its size.
func TestAddBriefDirABriefOverTheBoundRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.md"), []byte(passingBrief("handle the empty case")+strings.Repeat("x", store.MaxBriefBytes)), 0o600))
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.Contains(t, errs, "big.md")
	require.Contains(t, errs, "over the bound")
}

// A positional id together with --brief-dir refuses as usage.
func TestAddBriefDirWithAPositionalIdRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "")
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir + " some-id")
	require.Equal(t, 2, code)
	require.Contains(t, errs, "no positional id")
}

// The same --op twice returns the first result and adds nothing.
func TestAddBriefDirTheSameOpTwiceReplays(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "")
	writeBriefIn(t, dir, "b", "a")
	first := ta.ok("add --stream s1 --brief-dir " + dir + " --op op-1")
	again := ta.ok("add --stream s1 --brief-dir " + dir + " --op op-1")
	require.Contains(t, again, "replay=yes")
	require.Equal(t, strings.Count(first, "MOVED"), strings.Count(again, "MOVED"), "the retry does not replay the first result:\n%s\n%s", first, again)
	ta.clean()
}

// --needs on the line reaches every card of the call.
func TestAddBriefDirNeedsFlagReachesEveryCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s0 x")
	dir := t.TempDir()
	writeBriefIn(t, dir, "a", "")
	writeBriefIn(t, dir, "b", "")
	ta.ok("add --stream s1 --brief-dir " + dir + " --needs x")
	require.Contains(t, ta.ok("card --fields a"), "NEEDS x ")
	require.Contains(t, ta.ok("card --fields b"), "NEEDS x ")
	ta.clean()
}

// A directory with no *.md file refuses naming the directory.
func TestAddBriefDirWithNoBriefRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.Contains(t, errs, dir)
	require.Contains(t, errs, "no *.md")
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// needsBrief is a passing brief leading with lead and, when needs is not
// empty, a "Needs:" line naming it.
func needsBrief(lead, needs string) string {
	text := passingBrief(lead)
	if needs != "" {
		text = "Needs: " + needs + "\n" + text
	}
	return text
}

// writeNeedsBrief writes needsBrief into dir under name.md and returns the path.
func writeNeedsBrief(t *testing.T, dir, name, lead, needs string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	require.NoError(t, os.WriteFile(path, []byte(needsBrief(lead, needs)), 0o600))
	return path
}

// add --brief-dir adds one card per *.md file, in byte order of file name, and
// the Needs line of each brief becomes that card's needs.
func TestAddBriefDirLandsCardsInFileOrderWithTheirNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "b")
	writeNeedsBrief(t, dir, "b", "Fix b.", "c")
	writeNeedsBrief(t, dir, "c", "Fix c.", "")
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	require.Contains(t, out, "ADD OK moved=3")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Contains(t, lines[0], "MOVED a ->")
	require.Contains(t, lines[1], "MOVED b ->")
	require.Contains(t, lines[2], "MOVED c ->")
	require.Contains(t, ta.ok("card --fields a"), "NEEDS b")
	require.Contains(t, ta.ok("card --fields b"), "NEEDS c")
	ta.clean()
}

// --brief-file given again names the files, and the cards land in the order
// the files were given, not byte order.
func TestAddRepeatedBriefFileLandsInTheGivenOrder(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	a := writeNeedsBrief(t, dir, "a", "Fix a.", "")
	b := writeNeedsBrief(t, dir, "b", "Fix b.", "")
	c := writeNeedsBrief(t, dir, "c", "Fix c.", "")
	out := ta.ok("add --stream s1 --brief-file " + c + " --brief-file " + a + " --brief-file " + b)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Contains(t, lines[0], "MOVED c ->")
	require.Contains(t, lines[1], "MOVED a ->")
	require.Contains(t, lines[2], "MOVED b ->")
	ta.clean()
}

// One brief that fails the card lint refuses the whole call, names the bad
// file and its finding, and writes nothing.
func TestAddBriefDirOneBadBriefRefusesAll(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	bad := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(bad, []byte("handle the empty case\n"), 0o600))
	writeNeedsBrief(t, dir, "c", "Fix c.", "")
	before := ta.applies()
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, bad)
	require.Contains(t, errs, "rule-worktree")
	require.Equal(t, before, ta.applies(), "a refused add wrote")
}

// A Needs naming an id that is no primary on the table and no card of the call
// refuses, naming the file and the id, and writes nothing.
func TestAddBriefDirNeedsUnknownIDRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	bad := writeNeedsBrief(t, dir, "b", "Fix b.", "ghost")
	writeNeedsBrief(t, dir, "c", "Fix c.", "")
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 1, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, bad)
	require.Contains(t, errs, "ghost")
}

// A brief whose id is already on the table refuses the whole call, naming
// that id, and leaves the table unchanged.
func TestAddBriefDirExistingIDRefusesAll(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 b")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	writeNeedsBrief(t, dir, "b", "Fix b.", "")
	writeNeedsBrief(t, dir, "c", "Fix c.", "")
	before := ta.applies()
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 1, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, "b: exists already")
	require.Equal(t, before, ta.applies(), "a refused add wrote")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, int64(1), w.All)
}

// A file whose base name is not a valid card id refuses, naming the file.
func TestAddBriefDirInvalidIDRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad name.md")
	require.NoError(t, os.WriteFile(bad, []byte(needsBrief("Fix.", "")), 0o600))
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, bad)
}

// A brief over the brief bound refuses, naming the file and its size.
func TestAddBriefDirBriefOverBoundRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	big := filepath.Join(dir, "big.md")
	require.NoError(t, os.WriteFile(big, []byte(needsBrief("Fix.", "")+strings.Repeat("x", 20<<10)), 0o600))
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, big)
	require.Contains(t, errs, "over the")
}

// A positional id together with --brief-dir refuses as usage.
func TestAddBriefDirWithPositionalIDRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	code, out, errs := ta.do("add --stream s1 some-id --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, "takes no ids")
}

// The same --op again returns the recorded result and adds nothing.
func TestAddBriefDirSameOpTwiceReplays(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	writeNeedsBrief(t, dir, "b", "Fix b.", "")
	first := ta.ok("add --stream s1 --brief-dir " + dir + " --op w-1")
	again := ta.ok("add --stream s1 --brief-dir " + dir + " --op w-1")
	require.Contains(t, again, "replay=yes")
	require.Equal(t, strings.Count(first, "MOVED"), strings.Count(again, "MOVED"))
	var w whereView
	ta.json("where", &w)
	require.Equal(t, int64(2), w.All)
}

// --needs on the command line reaches every card of the call.
func TestAddBriefDirCommandLineNeedsReachEveryCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 x")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	writeNeedsBrief(t, dir, "b", "Fix b.", "")
	ta.ok("add --stream s2 --brief-dir " + dir + " --needs x")
	require.Contains(t, ta.ok("card --fields a"), "NEEDS x")
	require.Contains(t, ta.ok("card --fields b"), "NEEDS x")
	ta.clean()
}

// A directory with no *.md file refuses, naming the directory.
func TestAddBriefDirWithNoMarkdownRefuses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o600))
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, dir)
}

// A brief whose line 1 tier is no route is refused by --brief-dir with the
// same model-lines line a single --brief-file prints, and nothing is written.
func TestAddBriefDirReadsTheBriefsModelLines(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	lead := "a: the work (s1) tier: ultra"
	dir := t.TempDir()
	bad := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(bad, []byte(needsBrief(lead, "")), 0o600))
	before := ta.applies()
	_, _, single := ta.do("add --stream s1 --count 1 --brief-file " + writeBrief(t, lead))
	code, out, errs := ta.do("add --stream s1 --brief-dir " + dir)
	require.Equal(t, 2, code)
	require.NotContains(t, out, "MOVED")
	require.Contains(t, errs, bad)
	// the model-lines line the single --brief-file form prints, verbatim
	require.Contains(t, single, "the brief's model lines: line 1 names tier ultra")
	require.Contains(t, errs, "the brief's model lines: line 1 names tier ultra")
	require.Equal(t, before, ta.applies(), "a refused add wrote")
}

// "Needs: none (first card)" names no needs: the text after the opening
// parenthesis is cut, and none means none.
func TestAddBriefDirNeedsNoneIsNoNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "none (first card)")
	ta.ok("add --stream s1 --brief-dir " + dir)
	require.NotContains(t, ta.ok("card --fields a"), "NEEDS")
}

// A need named by the brief and again by --needs is stored once, not twice.
func TestAddBriefDirNeedsNamedTwiceIsStoredOnce(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 x")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "x")
	ta.ok("add --stream s2 --brief-dir " + dir + " --needs x")
	require.Equal(t, 1, strings.Count(ta.ok("card --fields a"), "NEEDS x"))
}

// --sentinel <id> with --brief-dir admits a stop after the cards: every card
// of the call sorts before it, and the sentinel waits for them.
func TestAddBriefDirSentinelStopsEveryCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a", "Fix a.", "")
	writeNeedsBrief(t, dir, "b", "Fix b.", "")
	out := ta.ok("add --stream s1 --brief-dir " + dir + " --sentinel gate")
	require.Contains(t, out, "MOVED sentinel gate -> waiting")
	fields := ta.ok("card --fields gate")
	require.Contains(t, fields, "NEEDS a")
	require.Contains(t, fields, "NEEDS b")
	ta.clean()
}

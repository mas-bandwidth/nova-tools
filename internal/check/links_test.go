package check

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinks pins the walk through its production seam, LinksExcluding with no
// exclusions — the tool calls LinksExcluding and LinksFiles; the Links
// wrapper had no production caller and is deleted.
func TestLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       map[string]string
		wantChecked int
		wantBroken  []string // substrings of "file:target:reason"; empty = must pass
	}{
		{
			name:        "good relative link",
			files:       map[string]string{"a.md": "[b](b.md)", "b.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "link into subdirectory",
			files:       map[string]string{"a.md": "see [p](pattern/p.md)", "pattern/p.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "link up and over",
			files:       map[string]string{"pattern/p.md": "[up](../README.md)", "README.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "link to a directory resolves",
			files:       map[string]string{"a.md": "[dir](pattern)", "pattern/p.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "root-relative resolves against the tree",
			files:       map[string]string{"pattern/p.md": "[k](/KERNEL.md)", "KERNEL.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "external and fragment-only links skipped",
			files:       map[string]string{"a.md": "[w](https://example.com) [m](mailto:x@y.z) [t](#top) [p](//cdn.example.com/x)"},
			wantChecked: 0,
		},
		{
			name:        "fragment stripped before resolving",
			files:       map[string]string{"a.md": "[b](b.md#section)", "b.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "percent-escapes decoded",
			files:       map[string]string{"a.md": "[s](my%20notes.md)", "my notes.md": "x"},
			wantChecked: 1,
		},
		{
			name:        "image target checked too",
			files:       map[string]string{"a.md": "![diagram](images/missing.png)"},
			wantChecked: 1,
			wantBroken:  []string{"missing.png"},
		},
		{
			name:        "broken link reported",
			files:       map[string]string{"a.md": "[gone](missing.md)"},
			wantChecked: 1,
			wantBroken:  []string{"a.md", "missing.md", "does not exist"},
		},
		{
			name:        "escape from the tree reported even if it exists on disk",
			files:       map[string]string{"a.md": "[out](../outside.md)"},
			wantChecked: 1,
			wantBroken:  []string{"escapes the tree"},
		},
		{
			name:        "fenced code block not scanned",
			files:       map[string]string{"a.md": "text\n```\n[fake](missing.md)\n```\ntext"},
			wantChecked: 0,
		},
		{
			name:        "inline code span not scanned",
			files:       map[string]string{"a.md": "write `[fake](missing.md)` to link"},
			wantChecked: 0,
		},
		{
			name:        "non-markdown files ignored",
			files:       map[string]string{"notes.txt": "[fake](missing.md)"},
			wantChecked: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tt.files)
			res, err := LinksExcluding(dir, nil)
			require.NoError(t, err, "unexpected error: %v", err)
			assert.Equal(t, tt.wantChecked, res.Checked, "checked = %d, want %d", res.Checked, tt.wantChecked)
			var asFailures []Failure
			for _, b := range res.Broken {
				asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
			}
			wantFailures(t, asFailures, tt.wantBroken)
		})
	}
}

// Reviewer B3a: the badge pattern [![alt](img)](target) — the old regex
// checked the inner image and never extracted the outer target, a false PASS
// when the target was missing. Both must be checked.
func TestLinksBadgeOuterTargetChecked(t *testing.T) {
	t.Parallel()

	t.Run("broken outer target caught", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			"a.md":    "[![build](img.png)](target.md)",
			"img.png": "x",
		})
		res, err := LinksExcluding(dir, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Checked, "checked = %d, want 2 (outer target and inner image)", res.Checked)
		var asFailures []Failure
		for _, b := range res.Broken {
			asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
		}
		wantFailures(t, asFailures, []string{"target.md", "does not exist"})
	})
	t.Run("both resolving passes", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			"a.md":      "[![build](img.png)](target.md)",
			"img.png":   "x",
			"target.md": "x",
		})
		res, err := LinksExcluding(dir, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Checked, "checked = %d broken = %v, want 2 checked and none broken", res.Checked, res.Broken)
		assert.Empty(t, res.Broken, "checked = %d broken = %v, want 2 checked and none broken", res.Checked, res.Broken)
	})
}

// Reviewer B3b: angle-bracket destinations [a](<my notes.md>) were invisible
// to the old regex — a false PASS whether or not the target existed.
func TestLinksAngleBracketDestination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md":        "[good](<my notes.md>) and [bad](<no such.md>)",
		"my notes.md": "x",
	})
	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Checked, "checked = %d, want 2", res.Checked)
	var asFailures []Failure
	for _, b := range res.Broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"no such.md", "does not exist"})
}

// Reviewer B3c: only double-quoted titles were recognized; single-quoted and
// parenthesized titles made the whole link invisible — a false PASS.
func TestLinksTitleQuoteForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		md   string
	}{
		{"double-quoted title", `[b](missing.md "title")`},
		{"single-quoted title", `[b](missing.md 'title')`},
		{"parenthesized title", `[b](missing.md (title))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"a.md": tt.md})
			res, err := LinksExcluding(dir, nil)
			require.NoError(t, err)
			assert.Equal(t, 1, res.Checked, "checked = %d, want 1", res.Checked)
			if len(res.Broken) != 1 || res.Broken[0].Target != "missing.md" {
				assert.Failf(t, "assertion failed", "broken = %v, want missing.md reported", res.Broken)
			}
		})
	}
}

// Reviewer suggestion: fence tracking must remember which marker opened the
// fence. A ``` block containing ~~~ lines used to toggle the fence off and
// report the quoted example as a broken link — a false FAIL.
func TestLinksFenceRemembersOpeningMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "```\n~~~\n[fake](missing.md)\n~~~\n```\n[real](b.md)\n",
		"b.md": "x",
	})
	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Checked, "checked = %d, want 1 (only the link outside the fence)", res.Checked)
	assert.Empty(t, res.Broken, "broken = %v, want none: the fenced example is not a link", res.Broken)
}

// Issue #30 (fence length): the scanner stored a fixed three-character marker,
// so a four-backtick fence was closed by the three-backtick fence it was
// quoting. The quoted example's link then leaked out and was reported — a
// false FAIL. A fence closes only on a run at least as long as its opener.
func TestLinksNestedFourBacktickFenceHidesInnerThree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "````\n```\n[fake](missing.md)\n```\n````\n",
	})
	res, err := LinksExcluding(dir, nil)
	if err != nil {
		require.FailNowf(t, "assertion failed", "Links: %s", brief(err.Error()))
	}
	assert.Equal(t, 0, res.Checked, "checked = %d, want 0: a link inside a four-backtick fence is illustration", res.Checked)
	if len(res.Broken) != 0 {
		assert.Failf(t, "assertion failed", "broken = %s, want none: the nested three-backtick example is not a link", brief(fmt.Sprint(res.Broken)))
	}
}

// Issue #30 (fence length): the spurious close above re-opened a fence on the
// closing four-backtick run; that fence never closed and swallowed a real
// broken link into LINKS OK with zero links. Recording the opener's length
// keeps the four-fence closed, so the link below it is checked.
func TestLinksUnclosedFenceDoesNotSwallowRealBrokenLink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "````\n```\n````\n[real](missing.md)\n",
	})
	res, err := LinksExcluding(dir, nil)
	if err != nil {
		require.FailNowf(t, "assertion failed", "Links: %s", brief(err.Error()))
	}
	assert.Equal(t, 1, res.Checked, "checked = %d, want 1: the link below the closed four-fence must be checked", res.Checked)
	if len(res.Broken) != 1 || res.Broken[0].Target != "missing.md" {
		assert.Failf(t, "assertion failed", "broken = %s, want the real missing.md reported", brief(fmt.Sprint(res.Broken)))
	}
}

// Reviewer (#66, finding 1): the two tests above only exercise a SHORTER run
// failing to close a LONGER fence, so the ">=" at links.go could be narrowed
// back to "==" with every test still green. The rule the scanner actually
// implements is the one SPEC.md states for the shared fenceRE at the corpus
// check: "an opening delimiter records its character and length, and only a
// run of the same character, at least as long and carrying nothing after it,
// closes it." These two pin the halves that were unpinned: a LONGER run does
// close, and a same-length run carrying text does not.
func TestLinksLongerRunClosesShorterFence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "```\n[fake](inside.md)\n````\n[real](missing.md)\n",
	})
	res, err := LinksExcluding(dir, nil)
	if err != nil {
		require.FailNowf(t, "assertion failed", "Links: %s", brief(err.Error()))
	}
	assert.Equal(t, 1, res.Checked, "checked = %d, want 1: a four-backtick run is at least as long as the three-backtick opener, so it closes it", res.Checked)
	if len(res.Broken) != 1 || res.Broken[0].Target != "missing.md" {
		assert.Failf(t, "assertion failed", "broken = %s, want the link below the closed fence reported", brief(fmt.Sprint(res.Broken)))
	}
}

func TestLinksSameLengthRunWithTrailingTextDoesNotClose(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "```\n[fake](inside.md)\n``` not a closer\n[alsofake](missing.md)\n",
	})
	res, err := LinksExcluding(dir, nil)
	if err != nil {
		require.FailNowf(t, "assertion failed", "Links: %s", brief(err.Error()))
	}
	assert.Equal(t, 0, res.Checked, "checked = %d, want 0: a run carrying text after it does not close the fence, so both links stay illustration", res.Checked)
	if len(res.Broken) != 0 {
		assert.Failf(t, "assertion failed", "broken = %s, want none: nothing below an unclosed fence is a link", brief(fmt.Sprint(res.Broken)))
	}
}

func TestLinksReportsLineNumbers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "fine\n\n[gone](missing.md)\n"})
	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err)
	require.Len(t, res.Broken, 1, "want 1 broken link, got %v", res.Broken)
	assert.Equal(t, 3, res.Broken[0].Line, "Line = %d, want 3", res.Broken[0].Line)
	assert.Equal(t, "a.md", res.Broken[0].File, "File = %q, want relative path a.md", res.Broken[0].File)
}

// An unreadable .md is a NAMED FAILURE, not a refusal — the same posture as
// attest ("a manifested file exists but cannot be read — a named failure, not
// a refusal"). The code this test was first run against returned the read
// error out of the walk, which converted the whole run to exit 2 AND
// discarded every broken link already accumulated: one chmod-000 file
// silenced every real finding in the tree. Seen red against that code.
func TestLinksUnreadableFileIsNamedFailureNotRefusal(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"broken.md": "[gone](missing.md)",
		"locked.md": "[never-seen](x.md)",
	})
	locked := filepath.Join(dir, "locked.md")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err, "an unreadable .md must be a finding, not a refusal: %v", err)
	assert.Equal(t, 2, res.MDFiles, "mdFiles = %d, want 2: the walk must continue past the unreadable file", res.MDFiles)
	var asFailures []Failure
	for _, b := range res.Broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	// BOTH findings in one run: the unreadable file must not discard the broken link.
	wantFailures(t, asFailures, []string{"broken.md", "missing.md", "does not exist", "locked.md", "unreadable"})
	for _, b := range res.Broken {
		if strings.Contains(b.Reason, "unreadable") {
			assert.Zero(t, b.Line, "a whole-file failure carries no line and no target, got %+v", b)
			assert.Empty(t, b.Target, "a whole-file failure carries no line and no target, got %+v", b)
		}
	}
}

// An unlistable nested DIRECTORY -- issue #30's first item, which asked for a
// NAMED failure with the walk continuing. SPEC.md says the opposite, in the
// paragraph that governs this exact case: "Refuses (exit 2) only when --dir is
// missing, unresolvable, or does not resolve to a directory, or a directory in
// the walk cannot be listed ... A walk error stops the run without reporting
// partial findings." The unreadable-FILE rule above (named failure, walk
// continues) is a different case. So the refusal is the specified behaviour and
// this test pins it: the directory is named in the error, and the walk stops —
// a.md was scanned, locked/'s contents never were. The accounting found before
// the stop travels with the error for the caller to discard; "not reporting
// partial findings" is the run's half, pinned at the CLI by
// TestLinksUnlistableDirRefusesAtTheCLI (exit 2, no FAIL line). Whether the
// spec should change is left open on #30.
func TestLinksUnlistableDirIsARefusalNotAPartialReport(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md":             "[gone](missing.md)",
		"locked/inside.md": "text",
	})
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	res, err := LinksExcluding(dir, nil)
	require.Error(t, err, "want a refusal for an unlistable directory; got mdFiles=%d checked=%d broken=%d", res.MDFiles, res.Checked, len(res.Broken))
	if !strings.Contains(err.Error(), "locked") {
		assert.Failf(t, "assertion failed", "error does not name the directory: %s", brief(err.Error()))
	}
	assert.Equal(t, 1, res.MDFiles, "MDFiles = %d, want 1: a.md was scanned before the walk stopped at locked/", res.MDFiles)
	assert.Equal(t, 1, res.Checked, "Checked = %d, want 1: the walk stopped before locked/inside.md", res.Checked)
	assert.Len(t, res.Broken, 1, "Broken = %v, want the finding found before the stop: the accounting travels with the error for the caller to discard", res.Broken)
}

// A dangling .md symlink is a named failure: the walk sees an irregular entry
// and reports it promptly as "not a regular file" without attempting to read it.
func TestLinksDanglingSymlinkMdIsNamedFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "[ok](b.md)", "b.md": "x"})
	if err := os.Symlink(filepath.Join(dir, "nowhere.md"), filepath.Join(dir, "dangling.md")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err, "a dangling symlink must be a finding, not a refusal: %v", err)
	assert.Equal(t, 3, res.MDFiles, "mdFiles = %d, want 3: the dangling symlink is seen, then reported", res.MDFiles)
	assert.Equal(t, 1, res.Checked, "checked = %d, want 1: a.md's good link is still checked", res.Checked)
	var asFailures []Failure
	for _, b := range res.Broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"dangling.md", "not a regular file"})
}

// SPEC (links): existence is checked with os.Stat, which FOLLOWS symlinks —
// "a target that is a symlink counts as resolving exactly when the symlink
// does. Links asserts navigability, not provenance — that stricter posture
// belongs to attest" (which refuses symlinks outright). Pin the deliberate
// contrast: a relative link that resolves THROUGH a symlink is LINKS OK,
// and a change that Lstat's the target here is a change of posture, not a fix.
func TestLinksTargetResolvingThroughSymlinkIsOK(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "[via](alias.txt)", "real.txt": "x"})
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "alias.txt")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err)
	assert.Empty(t, res.Broken, "a target that is a symlink resolves exactly when the symlink does; got %v", res.Broken)
	assert.Equal(t, 1, res.Checked, "checked = %d, want 1", res.Checked)
	assert.Equal(t, 1, res.MDFiles, "mdFiles = %d, want 1", res.MDFiles)
}

func TestLinksRefusesBadDir(t *testing.T) {
	t.Parallel()

	_, err := LinksExcluding(t.TempDir()+"/nope", nil)
	assert.Error(t, err, "nonexistent dir should be an error, not a guess")
}

// --exclude is the Stella item 4 seam: a testdata subtree holds deliberately
// partial fixture references (links that only resolve inside the subtree's own
// now-absent files), so scoping it out must both stop scanning those files and
// stop checking links into them, and the run must report how many files it
// left unscanned as Excluded.
func TestLinksExcludeSubtreeCounted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.md":                 "see [fixture](testdata/fixture.md) and [real](real.md)\n",
		"real.md":                 "x",
		"testdata/fixture.md":     "[partial](missing.md)\n",
		"testdata/partial-two.md": "text",
	})
	res, err := LinksExcluding(dir, []string{"testdata"})
	require.NoError(t, err)
	assert.Equal(t, 2, res.MDFiles, "MDFiles = %d, want 2 (main.md and real.md; the testdata subtree is not scanned)", res.MDFiles)
	assert.Equal(t, 1, res.Checked, "Checked = %d, want 1 (the link into testdata is skipped, the link to real.md is checked)", res.Checked)
	assert.Equal(t, 2, res.Excluded, "Excluded = %d, want 2 files under testdata", res.Excluded)
	assert.Empty(t, res.Broken, "Broken = %v, want none: the broken link lives under the excluded subtree and the link into it is skipped", res.Broken)

	res, err = LinksExcluding(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, 4, res.MDFiles, "without --exclude, MDFiles = %d, want 4", res.MDFiles)
	assert.Equal(t, 3, res.Checked, "without --exclude, Checked = %d, want 3", res.Checked)
	assert.Len(t, res.Broken, 1, "without --exclude the partial reference inside testdata must be reported; got %v", res.Broken)
}

// --dir naming a SYMLINK to the tree. os.Stat follows the link, so the
// directory check passed and WalkDir then saw the root as a single non-dir
// entry: LINKS OK files=0 links=0, exit 0 — a clean pass over a tree never
// walked, while the same tree by its real path reported the broken link and
// exited 1. On macOS /var is such a link. Fixed the way nocode fixes it.
func TestLinksDirIsASymlinkToTheTree(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	real := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	writeTree(t, real, map[string]string{"a.md": "fine\n\n[gone](gone.md)\n"})
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	realRes, err := LinksExcluding(real, nil)
	require.NoError(t, err)
	require.Len(t, realRes.Broken, 1, "fixture: the real path must report the broken link, got %v", realRes.Broken)

	res, err := LinksExcluding(link, nil)
	require.NoError(t, err)
	assert.Equal(t, realRes.MDFiles, res.MDFiles, "mdFiles = %d through the symlink, want %d as by the real path: a clean pass over a tree never walked", res.MDFiles, realRes.MDFiles)
	assert.Equal(t, realRes.Checked, res.Checked, "checked = %d through the symlink, want %d as by the real path", res.Checked, realRes.Checked)
	var asFailures []Failure
	for _, b := range res.Broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"a.md", "gone.md", "does not exist"})
	for _, b := range res.Broken {
		assert.Equal(t, "a.md", b.File, "File = %q, want a.md: a finding stays relative to the tree, not absolute through the resolved root", b.File)
	}
}

// The two seed-floor fixtures under testdata/ are pinned excerpts of nova's
// own records and floors_test.go consumes them as text, so a markdown link
// inside one points at a target that lives in nova, not in this repository.
// That made `nova-check links --dir .` red on this repository itself — four
// broken links, all in seed-floors.md (issue #34) — even though the fixture is
// not a defect: it is deliberately incomplete. The references are flattened to
// plain text so a fixture carries no link target at all, which is what lets
// the repository run links over itself; floors_test still matches the prose.
func TestLinksSeedFixturesCarryNoTargets(t *testing.T) {
	t.Parallel()

	res, err := LinksExcluding("testdata", nil)
	require.NoError(t, err)
	assert.Empty(t, res.Broken, "testdata carries %d broken link(s); a pinned fixture must not point outside the repo: %v", len(res.Broken), res.Broken)
}

// A FIFO named x.md blocks links (and quickstart, which runs links first)
// forever; a symlinked .md pointing outside the tree is read. Both are irregular
// entries that must be reported promptly as "not a regular file" without being opened.
func TestLinksNamesAFifoAndASymlinkedMarkdownFileInsteadOfReadingThem(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: named pipes are not posix fifos")
	}

	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.md")
	require.NoError(t, os.WriteFile(outsideFile, []byte("prose\n"), 0o644))

	symlinkPath := filepath.Join(dir, "symlink.md")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	fifoPath := filepath.Join(dir, "fifo.md")
	if err := syscallMkfifo(fifoPath); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}

	res, err := LinksExcluding(dir, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, res.MDFiles, "MDFiles = %d, want 2 (symlink.md and fifo.md)", res.MDFiles)
	assert.Equal(t, 0, res.Checked, "Checked = %d, want 0", res.Checked)
	require.Len(t, res.Broken, 2, "broken = %v, want 2 broken links for non-regular files", res.Broken)

	var asFailures []Failure
	for _, b := range res.Broken {
		assert.Zero(t, b.Line, "Line = %d, want 0 for whole-file broken link", b.Line)
		assert.Empty(t, b.Target, "Target = %q, want empty for whole-file broken link", b.Target)
		assert.Equal(t, "not a regular file", b.Reason)
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"symlink.md", "not a regular file", "fifo.md", "not a regular file"})
}

// LinksFiles (--file) naming a symlink to a regular file still works, but
// naming a FIFO or an irregular file returns a named failure rather than hanging.
func TestLinksFilesSymlinkToRegularFileWorksAndFifoIsNamedFailure(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: named pipes and symlinks are platform-specific")
	}

	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.md")
	targetFile := filepath.Join(dir, "target.md")
	require.NoError(t, os.WriteFile(targetFile, []byte("target\n"), 0o644))
	require.NoError(t, os.WriteFile(realFile, []byte("link to [target](target.md)\n"), 0o644))

	linkFile := filepath.Join(dir, "link.md")
	if err := os.Symlink(realFile, linkFile); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	fifoFile := filepath.Join(dir, "pipe.md")
	if err := syscallMkfifo(fifoFile); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}

	res, err := LinksFiles(dir, []string{"link.md", "pipe.md"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, res.MDFiles, "MDFiles = %d, want 2", res.MDFiles)
	assert.Equal(t, 1, res.Checked, "Checked = %d, want 1 (link.md's link was checked)", res.Checked)
	require.Len(t, res.Broken, 1, "Broken = %v, want 1 (pipe.md is broken)", res.Broken)
	assert.Equal(t, "pipe.md", res.Broken[0].File)
	assert.Zero(t, res.Broken[0].Line)
	assert.Empty(t, res.Broken[0].Target)
	assert.Contains(t, res.Broken[0].Reason, "not a regular file")
}

// A markdown link whose path lexically looks inside the tree but traverses a
// directory symlink pointing outside the tree must be reported as escaping
// the tree when the outside target exists, and as "does not exist" when it does not.
// A symlink that stays inside the tree must still pass.
func TestLinksReportsATargetReachedThroughADirectorySymlinkOutOfTheTree(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: symlinks require elevated privileges or developer mode")
	}

	tree := t.TempDir()
	outside := t.TempDir()

	// outside/ holds there.md and not nowhere.md
	require.NoError(t, os.WriteFile(filepath.Join(outside, "there.md"), []byte("outside content\n"), 0o644))

	// tree/docs/escape is a symlink to outside/
	docsDir := filepath.Join(tree, "docs")
	require.NoError(t, os.MkdirAll(docsDir, 0o755))
	escapeLink := filepath.Join(docsDir, "escape")
	if err := os.Symlink(outside, escapeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	// Inside symlink: tree/docs/internal -> tree/inside (stays inside the tree).
	insideDir := filepath.Join(tree, "inside")
	require.NoError(t, os.MkdirAll(insideDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(insideDir, "here.md"), []byte("inside content\n"), 0o644))
	insideLink := filepath.Join(docsDir, "internal")
	if err := os.Symlink(insideDir, insideLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	// docs/a.md links escape/there.md, escape/nowhere.md, and internal/here.md
	aMD := filepath.Join(docsDir, "a.md")
	content := "[there](escape/there.md) and [nowhere](escape/nowhere.md) and [internal](internal/here.md)\n"
	require.NoError(t, os.WriteFile(aMD, []byte(content), 0o644))

	wantFailures := map[string]string{
		"escape/there.md":   "escapes the tree through a symlink; cannot survive the repo travelling alone",
		"escape/nowhere.md": "does not exist",
	}

	checked, broken := checkFileLinks(tree, aMD, nil)
	assert.Equal(t, 3, checked)
	require.Len(t, broken, 2, "broken = %v, want 2 broken links from checkFileLinks", broken)
	for _, b := range broken {
		assert.Equal(t, "docs/a.md", b.File)
		assert.Equal(t, 1, b.Line)
		expectedReason, ok := wantFailures[b.Target]
		require.True(t, ok, "unexpected broken target %q", b.Target)
		assert.Equal(t, expectedReason, b.Reason)
	}

	res, err := LinksExcluding(tree, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, res.MDFiles)
	assert.Equal(t, 3, res.Checked)
	require.Len(t, res.Broken, 2, "broken = %v, want 2 broken links", res.Broken)
	for _, b := range res.Broken {
		assert.Equal(t, "docs/a.md", b.File)
		assert.Equal(t, 1, b.Line)
		expectedReason, ok := wantFailures[b.Target]
		require.True(t, ok, "unexpected broken target %q", b.Target)
		assert.Equal(t, expectedReason, b.Reason)
	}
}

// TestLinksReportsATargetReachedThroughADirectorySymlinkOutOfTheTreeWhenRootIsASymlink
// pins the same judgement on a tree whose root is itself a symlink. macOS
// temp dirs sit under /tmp or /var, which are symlinks; Linux /tmp usually is
// not, so the test above can pass there while the prefix check still compares
// a resolved target to an unresolved root. The root passed in is the link,
// not its target.
func TestLinksReportsATargetReachedThroughADirectorySymlinkOutOfTheTreeWhenRootIsASymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: symlinks require elevated privileges or developer mode")
	}

	real := t.TempDir()
	linkParent := t.TempDir()
	tree := filepath.Join(linkParent, "tree")
	if err := os.Symlink(real, tree); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	info, err := os.Lstat(tree)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, "tree root must be a symlink")
	resolvedRoot, err := filepath.EvalSymlinks(tree)
	require.NoError(t, err)
	require.NotEqual(t, filepath.Clean(tree), filepath.Clean(resolvedRoot))

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "there.md"), []byte("outside content\n"), 0o644))

	docsDir := filepath.Join(tree, "docs")
	require.NoError(t, os.MkdirAll(docsDir, 0o755))
	if err := os.Symlink(outside, filepath.Join(docsDir, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	insideDir := filepath.Join(tree, "inside")
	require.NoError(t, os.MkdirAll(insideDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(insideDir, "here.md"), []byte("inside content\n"), 0o644))
	if err := os.Symlink(insideDir, filepath.Join(docsDir, "internal")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	aMD := filepath.Join(docsDir, "a.md")
	content := "[there](escape/there.md) and [nowhere](escape/nowhere.md) and [internal](internal/here.md)\n"
	require.NoError(t, os.WriteFile(aMD, []byte(content), 0o644))

	wantFailures := map[string]string{
		"escape/there.md":   "escapes the tree through a symlink; cannot survive the repo travelling alone",
		"escape/nowhere.md": "does not exist",
	}

	checked, broken := checkFileLinks(tree, aMD, nil)
	assert.Equal(t, 3, checked)
	require.Len(t, broken, 2, "broken = %v, want 2 broken links from checkFileLinks", broken)
	for _, b := range broken {
		assert.Equal(t, "docs/a.md", b.File)
		assert.Equal(t, 1, b.Line)
		expectedReason, ok := wantFailures[b.Target]
		require.True(t, ok, "unexpected broken target %q", b.Target)
		assert.Equal(t, expectedReason, b.Reason)
	}

	res, err := LinksExcluding(tree, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, res.MDFiles)
	assert.Equal(t, 3, res.Checked)
	require.Len(t, res.Broken, 2, "broken = %v, want 2 broken links", res.Broken)
	for _, b := range res.Broken {
		assert.Equal(t, "docs/a.md", b.File)
		assert.Equal(t, 1, b.Line)
		expectedReason, ok := wantFailures[b.Target]
		require.True(t, ok, "unexpected broken target %q", b.Target)
		assert.Equal(t, expectedReason, b.Reason)
	}
}

// TestExtractLinkTargetsPreservesBracketSemantics pins nested badges, open
// brackets, and the scanner's existing treatment of backslash escapes.
func TestExtractLinkTargetsPreservesBracketSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "nested badge checks both destinations",
			line: "[![build](img.png)](target.md)",
			want: []string{"target.md", "img.png"},
		},
		{
			name: "unclosed outer text leaves inner link visible",
			line: "[open [inner](inner.md)",
			want: []string{"inner.md"},
		},
		{
			name: "escaped opener keeps existing scanner behavior",
			line: `\[label](target.md)`,
			want: []string{"target.md"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractLinkTargets(tt.line))
		})
	}
}

// TestExtractLinkTargetsIsLinearOnALineOfOpenBrackets pins one pass over an
// unclosed bracket run; the input is the security#77 CPU-stall witness.
func TestExtractLinkTargetsIsLinearOnALineOfOpenBrackets(t *testing.T) {
	t.Parallel()

	assert.Empty(t, extractLinkTargets(strings.Repeat("[", 4_000_000)))
}

// TestLinksFragmentValidation checks that fragments (#anchor) are validated
// against the target file's headings using GitHub's anchor rule.
func TestLinksFragmentValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       map[string]string
		wantChecked int
		wantBroken  []string // substrings of target:reason
	}{
		{
			name: "valid fragment passes",
			files: map[string]string{
				"a.md": "[section](b.md#introduction)",
				"b.md": "# Introduction\nSome text\n",
			},
			wantChecked: 1,
		},
		{
			name: "missing fragment fails",
			files: map[string]string{
				"a.md": "[bad](b.md#nope)",
				"b.md": "# Introduction\nSome text\n",
			},
			wantChecked: 1,
			wantBroken:  []string{"nope", "missing anchor"},
		},
		{
			name: "fragment with spaces converts to dashes",
			files: map[string]string{
				"a.md": "[section](b.md#my heading)",
				"b.md": "# My Heading\nSome text\n",
			},
			wantChecked: 1,
		},
		{
			name: "fragment with punctuation drops punctuation",
			files: map[string]string{
				"a.md": "[section](b.md#hello-world)",
				"b.md": "# Hello World!\nSome text\n",
			},
			wantChecked: 1,
		},
		{
			name: "fragment case-insensitive",
			files: map[string]string{
				"a.md": "[section](b.md#INTRODUCTION)",
				"b.md": "# Introduction\nSome text\n",
			},
			wantChecked: 1,
		},
		{
			name: "fragment on markdown file checked",
			files: map[string]string{
				"a.md": "[section](b.md#section)",
				"b.md": "# Section\nSome text\n",
			},
			wantChecked: 1,
		},
		{
			name: "fragment on non-markdown file not checked",
			files: map[string]string{
				"a.md":  "[file](b.txt#section)",
				"b.txt": "some content",
			},
			wantChecked: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tt.files)
			res, err := LinksExcluding(dir, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantChecked, res.Checked, "checked = %d, want %d", res.Checked, tt.wantChecked)
			var asFailures []Failure
			for _, b := range res.Broken {
				asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
			}
			wantFailures(t, asFailures, tt.wantBroken)
		})
	}
}

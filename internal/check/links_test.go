package check

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinks(t *testing.T) {
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
			_, checked, broken, err := Links(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if checked != tt.wantChecked {
				t.Errorf("checked = %d, want %d", checked, tt.wantChecked)
			}
			var asFailures []Failure
			for _, b := range broken {
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
	t.Run("broken outer target caught", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			"a.md":    "[![build](img.png)](target.md)",
			"img.png": "x",
		})
		_, checked, broken, err := Links(dir)
		if err != nil {
			t.Fatal(err)
		}
		if checked != 2 {
			t.Errorf("checked = %d, want 2 (outer target and inner image)", checked)
		}
		var asFailures []Failure
		for _, b := range broken {
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
		_, checked, broken, err := Links(dir)
		if err != nil {
			t.Fatal(err)
		}
		if checked != 2 || len(broken) != 0 {
			t.Errorf("checked = %d broken = %v, want 2 checked and none broken", checked, broken)
		}
	})
}

// Reviewer B3b: angle-bracket destinations [a](<my notes.md>) were invisible
// to the old regex — a false PASS whether or not the target existed.
func TestLinksAngleBracketDestination(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md":        "[good](<my notes.md>) and [bad](<no such.md>)",
		"my notes.md": "x",
	})
	_, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 {
		t.Errorf("checked = %d, want 2", checked)
	}
	var asFailures []Failure
	for _, b := range broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"no such.md", "does not exist"})
}

// Reviewer B3c: only double-quoted titles were recognized; single-quoted and
// parenthesized titles made the whole link invisible — a false PASS.
func TestLinksTitleQuoteForms(t *testing.T) {
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
			_, checked, broken, err := Links(dir)
			if err != nil {
				t.Fatal(err)
			}
			if checked != 1 {
				t.Errorf("checked = %d, want 1", checked)
			}
			if len(broken) != 1 || broken[0].Target != "missing.md" {
				t.Errorf("broken = %v, want missing.md reported", broken)
			}
		})
	}
}

// Reviewer suggestion: fence tracking must remember which marker opened the
// fence. A ``` block containing ~~~ lines used to toggle the fence off and
// report the quoted example as a broken link — a false FAIL.
func TestLinksFenceRemembersOpeningMarker(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "```\n~~~\n[fake](missing.md)\n~~~\n```\n[real](b.md)\n",
		"b.md": "x",
	})
	_, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 {
		t.Errorf("checked = %d, want 1 (only the link outside the fence)", checked)
	}
	if len(broken) != 0 {
		t.Errorf("broken = %v, want none: the fenced example is not a link", broken)
	}
}

// Issue #30 (fence length): the scanner stored a fixed three-character marker,
// so a four-backtick fence was closed by the three-backtick fence it was
// quoting. The quoted example's link then leaked out and was reported — a
// false FAIL. A fence closes only on a run at least as long as its opener.
func TestLinksNestedFourBacktickFenceHidesInnerThree(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "````\n```\n[fake](missing.md)\n```\n````\n",
	})
	_, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 0 {
		t.Errorf("checked = %d, want 0: a link inside a four-backtick fence is illustration", checked)
	}
	if len(broken) != 0 {
		t.Errorf("broken = %v, want none: the nested three-backtick example is not a link", broken)
	}
}

// Issue #30 (fence length): the spurious close above re-opened a fence on the
// closing four-backtick run; that fence never closed and swallowed a real
// broken link into LINKS OK with zero links. Recording the opener's length
// keeps the four-fence closed, so the link below it is checked.
func TestLinksUnclosedFenceDoesNotSwallowRealBrokenLink(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md": "````\n```\n````\n[real](missing.md)\n",
	})
	_, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 {
		t.Errorf("checked = %d, want 1: the link below the closed four-fence must be checked", checked)
	}
	if len(broken) != 1 || broken[0].Target != "missing.md" {
		t.Errorf("broken = %v, want the real missing.md reported", broken)
	}
}

func TestLinksReportsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "fine\n\n[gone](missing.md)\n"})
	_, _, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 1 {
		t.Fatalf("want 1 broken link, got %v", broken)
	}
	if broken[0].Line != 3 {
		t.Errorf("Line = %d, want 3", broken[0].Line)
	}
	if broken[0].File != "a.md" {
		t.Errorf("File = %q, want relative path a.md", broken[0].File)
	}
}

// An unreadable .md is a NAMED FAILURE, not a refusal — the same posture as
// attest ("a manifested file exists but cannot be read — a named failure, not
// a refusal"). The code this test was first run against returned the read
// error out of the walk, which converted the whole run to exit 2 AND
// discarded every broken link already accumulated: one chmod-000 file
// silenced every real finding in the tree. Seen red against that code.
func TestLinksUnreadableFileIsNamedFailureNotRefusal(t *testing.T) {
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
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	mdFiles, _, broken, err := Links(dir)
	if err != nil {
		t.Fatalf("an unreadable .md must be a finding, not a refusal: %v", err)
	}
	if mdFiles != 2 {
		t.Errorf("mdFiles = %d, want 2: the walk must continue past the unreadable file", mdFiles)
	}
	var asFailures []Failure
	for _, b := range broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	// BOTH findings in one run: the unreadable file must not discard the broken link.
	wantFailures(t, asFailures, []string{"broken.md", "missing.md", "does not exist", "locked.md", "unreadable"})
	for _, b := range broken {
		if strings.Contains(b.Reason, "unreadable") && (b.Line != 0 || b.Target != "") {
			t.Errorf("a whole-file failure carries no line and no target, got %+v", b)
		}
	}
}

// An unreadable nested DIRECTORY is the card's case: a chmod-000 directory
// under the tree. Issue #30 (first item) asks for a NAMED failure that keeps
// walking, with the findings found beside it kept. Against the code as it
// stands, the walk callback returns walkErr, so the whole run becomes a
// refusal (exit 2) and every finding already accumulated is thrown away.
// NOTE: current SPEC.md:348-353 says the opposite — "a directory in the walk
// cannot be listed" is a REFUSAL and "a walk error stops the run without
// reporting partial findings". This test is intentionally RED until that is
// settled.
func TestLinksUnreadableDirIsNamedFailureWalkContinues(t *testing.T) {
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
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	mdFiles, _, broken, err := Links(dir)
	if err != nil {
		t.Fatalf("an unreadable directory must be a named failure, not a refusal: %v", err)
	}
	if mdFiles != 1 {
		t.Errorf("mdFiles = %d, want 1: the walk must continue past the unreadable directory", mdFiles)
	}
	var asFailures []Failure
	for _, b := range broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"a.md", "missing.md", "does not exist", "locked", "unreadable"})
}

// A dangling .md symlink is the second face of the same case: the walk sees a
// file, the read fails. Named failure, walk continues, findings kept, exit 1.
func TestLinksDanglingSymlinkMdIsNamedFailure(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "[ok](b.md)", "b.md": "x"})
	if err := os.Symlink(filepath.Join(dir, "nowhere.md"), filepath.Join(dir, "dangling.md")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	mdFiles, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatalf("a dangling symlink must be a finding, not a refusal: %v", err)
	}
	if mdFiles != 3 {
		t.Errorf("mdFiles = %d, want 3: the dangling symlink is seen, then reported", mdFiles)
	}
	if checked != 1 {
		t.Errorf("checked = %d, want 1: a.md's good link is still checked", checked)
	}
	var asFailures []Failure
	for _, b := range broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"dangling.md", "unreadable"})
}

// SPEC (links): existence is checked with os.Stat, which FOLLOWS symlinks —
// "a target that is a symlink counts as resolving exactly when the symlink
// does. Links asserts navigability, not provenance — that stricter posture
// belongs to attest" (which refuses symlinks outright). Pin the deliberate
// contrast: a relative link that resolves THROUGH a .md symlink is LINKS OK,
// and a change that Lstat's the target here is a change of posture, not a fix.
func TestLinksTargetResolvingThroughSymlinkIsOK(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "[via](alias.md)", "real.md": "x"})
	if err := os.Symlink(filepath.Join(dir, "real.md"), filepath.Join(dir, "alias.md")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	mdFiles, checked, broken, err := Links(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Errorf("a target that is a symlink resolves exactly when the symlink does; got %v", broken)
	}
	if checked != 1 {
		t.Errorf("checked = %d, want 1", checked)
	}
	if mdFiles != 3 {
		t.Errorf("mdFiles = %d, want 3: the symlinked .md is walked and read through the link", mdFiles)
	}
}

func TestLinksRefusesBadDir(t *testing.T) {
	if _, _, _, err := Links(t.TempDir() + "/nope"); err == nil {
		t.Error("nonexistent dir should be an error, not a guess")
	}
}

// --dir naming a SYMLINK to the tree. os.Stat follows the link, so the
// directory check passed and WalkDir then saw the root as a single non-dir
// entry: LINKS OK files=0 links=0, exit 0 — a clean pass over a tree never
// walked, while the same tree by its real path reported the broken link and
// exited 1. On macOS /var is such a link. Fixed the way nocode fixes it.
func TestLinksDirIsASymlinkToTheTree(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, real, map[string]string{"a.md": "fine\n\n[gone](gone.md)\n"})
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	realFiles, realChecked, realBroken, err := Links(real)
	if err != nil {
		t.Fatal(err)
	}
	if len(realBroken) != 1 {
		t.Fatalf("fixture: the real path must report the broken link, got %v", realBroken)
	}

	mdFiles, checked, broken, err := Links(link)
	if err != nil {
		t.Fatal(err)
	}
	if mdFiles != realFiles {
		t.Errorf("mdFiles = %d through the symlink, want %d as by the real path: a clean pass over a tree never walked", mdFiles, realFiles)
	}
	if checked != realChecked {
		t.Errorf("checked = %d through the symlink, want %d as by the real path", checked, realChecked)
	}
	var asFailures []Failure
	for _, b := range broken {
		asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
	}
	wantFailures(t, asFailures, []string{"a.md", "gone.md", "does not exist"})
	for _, b := range broken {
		if b.File != "a.md" {
			t.Errorf("File = %q, want a.md: a finding stays relative to the tree, not absolute through the resolved root", b.File)
		}
	}
}

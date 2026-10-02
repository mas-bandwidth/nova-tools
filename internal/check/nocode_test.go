package check

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scan is the common shape: materialize a tree, run with the floor list.
func scan(t *testing.T, files map[string]os.FileMode, contents map[string]string, links map[string]string, opts NoCodeOptions) (int, []Failure, error) {
	t.Helper()
	dir := t.TempDir()
	for rel, mode := range files {
		body, ok := contents[rel]
		if !ok {
			body = "x"
		}
		writeMode(t, dir, rel, body, mode)
	}
	for rel, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	opts.Dir = dir
	return NoCode(opts)
}

// wantOnly asserts the exact set of flagged subjects. wantFailures only proves
// a finding is PRESENT, so on its own it cannot tell a replacement from a
// merge — the assertion that must exist wherever absence is the point.
func wantOnly(t *testing.T, findings []Failure, want []string) {
	t.Helper()
	got := map[string]bool{}
	for _, f := range findings {
		got[f.Subject] = true
	}
	require.Len(t, got, len(want), "flagged %v, want exactly %v", got, want)
	for _, w := range want {
		assert.True(t, got[w], "%q not flagged; flagged %v", w, got)
	}
}

func TestNoCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       map[string]os.FileMode
		contents    map[string]string
		opts        NoCodeOptions
		symlinks    map[string]string // rel -> target
		wantScanned int
		wantFind    []string // substrings; empty = must pass
		wantExactly []string // exact set of flagged subjects, where absence is the point
		// needsExecBit marks a case whose subject is the executable-bit
		// condition. Windows has no such bit — os.Chmod there only toggles
		// read-only — so the case asserts a property that cannot exist on that
		// platform. Skipped with a reason rather than left to fail, and named
		// rather than deleted, so the coverage difference between platforms is
		// declared instead of discovered.
		needsExecBit bool
		// needsChmodRefusal marks a case that depends on chmod actually
		// REFUSING a read. Windows has no such semantics — the file stays
		// readable — so the case cannot observe its property there.
		needsChmodRefusal bool
	}{
		{
			name:        "prose tree is clean",
			files:       map[string]os.FileMode{"README.md": 0o644, "pattern/p.md": 0o644, "images/x.png": 0o644},
			wantScanned: 3,
		},
		{
			name:        "go file flagged — the rule is about where machinery lives, not which language",
			files:       map[string]os.FileMode{"tool.go": 0o644},
			wantScanned: 1,
			wantFind:    []string{"tool.go", "code extension .go"},
		},
		{
			name:        "extension match is case-insensitive",
			files:       map[string]os.FileMode{"SCRIPT.PY": 0o644},
			wantScanned: 1,
			wantFind:    []string{"SCRIPT.PY", "code extension .py"},
		},
		{
			name:         "executable without code extension flagged",
			needsExecBit: true,
			files:        map[string]os.FileMode{"runme": 0o755},
			contents:     map[string]string{"runme": "#!/bin/sh\n"},
			wantScanned:  1,
			wantFind:     []string{"runme", "executable (mode 0755)"},
		},
		{
			name:         "executable markdown is still a violation",
			needsExecBit: true,
			files:        map[string]os.FileMode{"notes.md": 0o744},
			wantScanned:  1,
			wantFind:     []string{"notes.md", "executable"},
		},
		{
			name:         "both reasons reported together",
			needsExecBit: true,
			files:        map[string]os.FileMode{"deploy.sh": 0o755},
			wantScanned:  1,
			wantFind:     []string{"code extension .sh", "executable"},
		},
		{
			name:        "git internals ignored",
			files:       map[string]os.FileMode{".git/hooks/pre-commit": 0o755, "README.md": 0o644},
			wantScanned: 1,
		},
		// A script with no extension and no executable bit must still be
		// caught. The shebang is the tell that survives renaming.
		{
			name:        "shebang with no extension and no exec bit is flagged",
			files:       map[string]os.FileMode{"nova-id": 0o644},
			contents:    map[string]string{"nova-id": "#!/usr/bin/env python3\nprint(1)\n"},
			wantScanned: 1,
			wantFind:    []string{"nova-id", "shebang"},
		},
		{
			name:        "prose beginning with a hash is not a shebang",
			files:       map[string]os.FileMode{"notes.md": 0o644},
			contents:    map[string]string{"notes.md": "# Heading\n\ntext\n"},
			wantScanned: 1,
		},
		// Scope narrowing is the caller's and starts EMPTY: no directory is
		// allowed by default. A test pins it so no default can quietly appear.
		{
			name:        "nothing is allowed by default — history/ is scanned",
			files:       map[string]os.FileMode{"history/old.py": 0o644},
			wantScanned: 1,
			wantFind:    []string{"history/old.py", "code extension .py"},
		},
		{
			name:        "--allow exempts a named directory and everything under it",
			files:       map[string]os.FileMode{"history/old.py": 0o644, "live.py": 0o644},
			opts:        NoCodeOptions{Allow: []string{"history"}},
			wantScanned: 1,
			wantFind:    []string{"live.py"},
		},
		{
			name:        "--allow is repeatable and trims slashes",
			files:       map[string]os.FileMode{"history/a.py": 0o644, "frozen/b.py": 0o644, "c.py": 0o644},
			opts:        NoCodeOptions{Allow: []string{"/history/", "frozen"}},
			wantScanned: 1,
			wantFind:    []string{"c.py"},
		},
		// Replacement is wholesale, not a merge: the line that legitimately
		// keeps a language in its self must be able to drop it from the floor.
		{
			name:        "--deny-ext replaces the floor rather than merging with it",
			files:       map[string]os.FileMode{"keep.py": 0o644, "gone.foo": 0o644},
			opts:        NoCodeOptions{DenyExt: []string{".foo"}, DenySource: DenyReplaced},
			wantScanned: 2,
			wantFind:    []string{"gone.foo", DenyReplaced},
			wantExactly: []string{"gone.foo"}, // .py must NOT come back with the floor
		},
		{
			name:        "a symlink named as code is flagged without being followed",
			files:       map[string]os.FileMode{"README.md": 0o644},
			symlinks:    map[string]string{"run.sh": "/bin/sh"},
			wantScanned: 2,
			wantFind:    []string{"run.sh", "code extension .sh", "target not followed"},
			wantExactly: []string{"run.sh"},
		},
		{
			name:        "a finding names which list produced it",
			files:       map[string]os.FileMode{"tool.py": 0o644},
			wantScanned: 1,
			wantFind:    []string{"code extension .py (" + DenyFloor + ")"},
		},
		{
			name:        "a symlink not named as code is neither followed nor flagged",
			files:       map[string]os.FileMode{"README.md": 0o644},
			symlinks:    map[string]string{"link": "/etc"},
			wantScanned: 2,
		},
		{
			name:              "an unreadable file is a finding, never a pass",
			needsChmodRefusal: true,
			files:             map[string]os.FileMode{"secret": 0o000},
			contents:          map[string]string{"secret": "#!/bin/sh\n"},
			wantScanned:       1,
			wantFind:          []string{"secret", "unreadable"},
		},
		{
			name:        "a multi-segment --allow prefix covers everything beneath it",
			files:       map[string]os.FileMode{"docs/history/old.py": 0o644, "live.py": 0o644},
			opts:        NoCodeOptions{Allow: []string{"docs/history"}},
			wantScanned: 1,
			wantFind:    []string{"live.py"},
			wantExactly: []string{"live.py"},
		},
		{
			name:        "an --allow entry written ./history still matches",
			files:       map[string]os.FileMode{"history/old.py": 0o644},
			opts:        NoCodeOptions{Allow: []string{"./history"}},
			wantScanned: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsChmodRefusal && runtime.GOOS == "windows" {
				t.Skip("windows: chmod 0 does not refuse reads, so an unreadable file cannot be produced here")
			}
			if tt.needsChmodRefusal && os.Geteuid() == 0 {
				t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
			}
			if tt.needsExecBit && runtime.GOOS == "windows" {
				t.Skip("windows: no executable bit, so this condition cannot fire here — the extension and shebang conditions carry the check on that platform")
			}
			scanned, findings, err := scan(t, tt.files, tt.contents, tt.symlinks, tt.opts)
			require.NoError(t, err, "NoCode")
			assert.Equal(t, tt.wantScanned, scanned, "scanned = %d, want %d (findings: %v)", scanned, tt.wantScanned, findings)
			wantFailures(t, findings, tt.wantFind)
			if tt.wantExactly != nil {
				wantOnly(t, findings, tt.wantExactly)
			}
		})
	}
}

// An unlistable directory is a refusal, as SPEC.md states: "Refuses (exit 2)
// when --dir is missing, unresolvable, or does not resolve to a directory, or a
// directory in the walk cannot be listed ... A walk error stops the run without
// reporting partial findings." A named failure that keeps walking is the
// alternative; the spec says refusal, so the refusal is what is pinned here.
func TestNoCodeUnlistableDirIsARefusalNotAPartialReport(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.md":          "prose",
		"locked/run.py": "print(1)",
	})
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	scanned, findings, err := NoCode(NoCodeOptions{Dir: dir})
	require.Error(t, err, "want a refusal for an unlistable directory; got scanned=%d findings=%d", scanned, len(findings))
	assert.ErrorContains(t, err, "locked", "error does not name the directory: %s", brief(err.Error()))
	assert.Zero(t, scanned, "a refusal must report nothing; got scanned=%d findings=%d", scanned, len(findings))
	assert.Empty(t, findings, "a refusal must report nothing; got scanned=%d findings=%d", scanned, len(findings))
}

// TestNoCodeSymlinkNotFollowed is separate because it needs a real symlink.
func TestNoCodeSymlinkNotFollowed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	writeMode(t, outside, "escape.py", "print()", 0o644)
	writeMode(t, dir, "README.md", "prose", 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: dir})
	require.NoError(t, err, "NoCode")
	assert.Empty(t, findings, "symlink was followed or flagged: %v", findings)
	assert.Equal(t, 2, scanned, "scanned = %d, want 2 (README.md and the link's own name)", scanned)
}

// TestNoCodeFloorListCoversFormerlyMissed pins extensions that are easy to
// leave off the floor list. Missing any one is a fail-open: the check reports
// clean on a tree holding machinery, while its spec says a self repo holds
// only prose.
func TestNoCodeFloorListCoversFormerlyMissed(t *testing.T) {
	t.Parallel()

	formerlyMissed := []string{
		".awk", ".clj", ".cxx", ".ex", ".exs", ".fish", ".h", ".hpp", ".hs",
		".jl", ".m", ".ml", ".mm", ".r", ".scala", ".vbs", ".zsh",
	}
	for _, ext := range formerlyMissed {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, "machinery"+ext, "code", 0o644)
			_, findings, err := NoCode(NoCodeOptions{Dir: dir})
			require.NoError(t, err, "NoCode")
			require.NotEmpty(t, findings, "%s not caught by the floor list", ext)
		})
	}
}

func TestFloorDenyExts(t *testing.T) {
	t.Parallel()

	exts, err := FloorDenyExts()
	require.NoError(t, err, "FloorDenyExts")
	assert.GreaterOrEqual(t, len(exts), 40, "floor list has %d entries, expected at least 40", len(exts))
	// The Makefile fragment extensions are pinned by name: codeexts.txt's
	// policy is that an extension arrives with the test proving it is caught,
	// and an unpinned entry can leave with the suite green.
	for _, want := range []string{".mk", ".mak"} {
		assert.Contains(t, exts, want, "floor extension list is missing %q", want)
	}
	seen := map[string]bool{}
	for _, e := range exts {
		assert.True(t, strings.HasPrefix(e, "."), "entry %q does not begin with a dot", e)
		assert.Equal(t, strings.ToLower(e), e, "entry %q is not lowercase", e)
		assert.False(t, seen[e], "duplicate entry %q", e)
		seen[e] = true
	}
	// .go is on the list on purpose: an exemption for the language the tools
	// are written in would gut the gate.
	for _, must := range []string{".go", ".py", ".sh", ".cpp", ".zsh", ".exe"} {
		assert.True(t, seen[must], "floor list is missing %s", must)
	}
}

func TestParseDenyList(t *testing.T) {
	t.Parallel()

	t.Run("comma list", func(t *testing.T) {
		got, err := ParseDenyList(".py,.sh")
		require.NoError(t, err)
		assert.Equal(t, []string{".py", ".sh"}, got, "got %v", got)
	})
	t.Run("bare extensions gain their dot", func(t *testing.T) {
		got, err := ParseDenyList("py,sh")
		require.NoError(t, err)
		if assert.Len(t, got, 2, "got %v", got) {
			assert.Equal(t, ".py", got[0], "got %v", got)
		}
	})
	t.Run("@file", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "list.txt", "# comment\n.py\n\n.rs\n", 0o644)
		got, err := ParseDenyList("@" + filepath.Join(dir, "list.txt"))
		require.NoError(t, err)
		assert.Len(t, got, 2, "got %v", got)
	})
	t.Run("empty spec refuses", func(t *testing.T) {
		_, err := ParseDenyList("")
		assert.Error(t, err, "expected an error for an empty specification")
	})
	t.Run("unreadable file refuses", func(t *testing.T) {
		_, err := ParseDenyList("@/nonexistent/nope.txt")
		assert.Error(t, err, "expected an error for an unreadable deny-list file")
	})
	t.Run("file of only comments refuses", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "empty.txt", "# nothing here\n\n", 0o644)
		_, err := ParseDenyList("@" + filepath.Join(dir, "empty.txt"))
		assert.Error(t, err, "a guard that forbids nothing must refuse")
	})
}

func TestNoCodeRefusals(t *testing.T) {
	t.Parallel()

	t.Run("missing directory refuses", func(t *testing.T) {
		_, _, err := NoCode(NoCodeOptions{Dir: "/nonexistent/nope"})
		assert.Error(t, err, "expected an error for a missing directory")
	})
	t.Run("a file instead of a directory refuses", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "f.md", "x", 0o644)
		_, _, err := NoCode(NoCodeOptions{Dir: filepath.Join(dir, "f.md")})
		assert.Error(t, err, "expected an error when --dir is not a directory")
	})
}

// A finding may never name a list that did not produce it: an empty DenyExt
// means the floor ran, whatever provenance the caller passed alongside it.
func TestNoCodeProvenanceCannotLie(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeMode(t, dir, "tool.py", "print()", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir, DenyExt: nil, DenySource: DenyReplaced})
	require.NoError(t, err)
	require.Len(t, findings, 1, "findings = %v, want 1", findings)
	assert.Contains(t, findings[0].Reason, DenyFloor, "reason %q does not name the list that actually ran", findings[0].Reason)
	assert.NotContains(t, findings[0].Reason, DenyReplaced, "reason %q names a list that did not produce it", findings[0].Reason)
}

// A deny-list entry that cannot be an extension is a refusal. Accepting any of
// these builds a list that matches nothing and reports a clean tree.
func TestParseDenyListRefusesNonExtensions(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{"mylist.txt", "*.py", "src/x", ".a b", "..", "."} {
		t.Run(spec, func(t *testing.T) {
			got, err := ParseDenyList(spec)
			assert.Error(t, err, "accepted %q as %v; a guard that forbids nothing must refuse", spec, got)
		})
	}
}

func TestNoCodeTrailingWhitespaceInName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeMode(t, dir, "trailing.py ", "print()", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir})
	require.NoError(t, err)
	wantFailures(t, findings, []string{"code extension .py"})
}

// --allow must not over-match: history must not allow history-of-tools. This
// pins the boundary, so widening the prefix test to a bare HasPrefix turns the
// suite red.
func TestNoCodeAllowDoesNotOverMatchSiblings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeMode(t, dir, "history/ok.py", "x", 0o644)
	writeMode(t, dir, "history-of-tools/run.py", "x", 0o644)
	writeMode(t, dir, "historical.py", "x", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir, Allow: []string{"history"}})
	require.NoError(t, err)
	wantOnly(t, findings, []string{"history-of-tools/run.py", "historical.py"})
}

func TestParseDenyListRefusesZeroWidthSpace(t *testing.T) {
	t.Parallel()

	_, err := ParseDenyList(".py​")
	assert.Error(t, err, "accepted a zero-width space: a one-entry list that forbids nothing")
}

// --dir naming a SYMLINK to the repo. os.Stat follows the link, so the
// directory check passes, but WalkDir sees an unresolved root as a single
// non-dir entry: a clean pass over a tree never opened. On macOS /var is such
// a link.
func TestNoCodeDirIsASymlinkToTheTree(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	real := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	writeMode(t, real, "deploy.sh", "#!/bin/sh\n", 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: link})
	require.NoError(t, err)
	require.NotZero(t, scanned, "scanned nothing through a symlinked root: a clean pass over a tree never opened")
	wantFailures(t, findings, []string{"deploy.sh"})
}

// The audit must not be the weaker mode: a non-regular file named as code is
// a finding, never a silent pass.
func TestNoCodeAuditFailsClosedLikeTheGate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeMode(t, dir, "README.md", "prose", 0o644)
	if err := syscallMkfifo(filepath.Join(dir, "pipe.sh")); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, 2, scanned, "scanned = %d, want 2", scanned)
	wantFailures(t, findings, []string{"pipe.sh", "not a regular file"})
}

// DenyExt without DenySource must refuse: a finding may not name an unknown list.
func TestNoCodeDenyExtWithoutSourceRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, _, err := NoCode(NoCodeOptions{Dir: dir, DenyExt: []string{".foo"}})
	assert.Error(t, err, "accepted a deny-list with no stated provenance")
}

// TestNoCodeBuildMachineryByName: each of these three carries no code
// extension, no executable bit and no shebang, so only the floor name list
// catches them.
func TestNoCodeBuildMachineryByName(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":                     0o644,
		"Makefile":                     0o644,
		"Dockerfile":                   0o644,
		".github/workflows/deploy.yml": 0o644,
	}
	contents := map[string]string{
		"Makefile":                     "all:\n\tcurl x|sh\n",
		"Dockerfile":                   "FROM alpine\n",
		".github/workflows/deploy.yml": "run: rm -rf /\n",
	}
	_, findings, err := scan(t, files, contents, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{"Makefile", "Dockerfile", ".github/workflows/deploy.yml"})
}

// TestNoCodeNameMatchIsCaseInsensitive: "makefile" and "GNUmakefile" are the
// same machinery as "Makefile", and a floor that can be stepped over by
// changing one letter's case is not a floor.
func TestNoCodeNameMatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"makefile", "MAKEFILE", "GNUmakefile", "dockerfile", "DOCKERFILE"} {
		_, findings, err := scan(t, map[string]os.FileMode{name: 0o644}, nil, nil, NoCodeOptions{})
		require.NoError(t, err, "%s", name)
		assert.Len(t, findings, 1, "%s: flagged %d, want 1", name, len(findings))
	}
}

// TestNoCodeYAMLDataStillPasses is the counter-case that justifies a name list
// instead of adding .yml to the extension list. A prose repo's front matter and
// data files are legitimate content, and a floor that forbids them would be
// ignored or would push real writing out of the tree.
func TestNoCodeYAMLDataStillPasses(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":       0o644,
		"data.yml":       0o644,
		"meta/tags.yaml": 0o644,
	}
	contents := map[string]string{"data.yml": "title: x\n", "meta/tags.yaml": "tags: [a]\n"}
	_, findings, err := scan(t, files, contents, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, nil)
}

// TestNoCodeNameFloorSurvivesDenyExtReplacement pins a decision rather than an
// accident: --deny-ext answers "which languages does this line keep inside its
// own self" and has nothing to say about whether a CI workflow belongs in a
// prose tree, so replacing the extension list must not switch the name floor
// off. The escape hatch for a line that legitimately keeps build machinery is
// --allow, which names WHERE rather than turning a floor off everywhere.
func TestNoCodeNameFloorSurvivesDenyExtReplacement(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{"NOTES.md": 0o644, "Makefile": 0o644}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{
		DenyExt:    []string{".zzz"},
		DenySource: DenyReplaced,
	})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{"Makefile"})
}

// TestNoCodeAllowExemptsNamedMachinery: the declared escape hatch has to work,
// or the floor is one a real adopter routes around instead of using.
func TestNoCodeAllowExemptsNamedMachinery(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":                     0o644,
		"Makefile":                     0o644,
		".github/workflows/deploy.yml": 0o644,
	}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{Allow: []string{"Makefile", ".github"}})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, nil)
}

// TestNoCodeSymlinkNamedMachineryIsFlaggedWithoutDereference: a link called
// Makefile is machinery by the same argument that catches a file called
// Makefile, and deciding that must not require following the link out of the
// tree the gate is guarding.
func TestNoCodeSymlinkNamedMachineryIsFlaggedWithoutDereference(t *testing.T) {
	t.Parallel()

	_, findings, err := scan(t,
		map[string]os.FileMode{"NOTES.md": 0o644}, nil,
		map[string]string{"Makefile": "/etc/hosts"}, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{"Makefile"})
	assert.Contains(t, findings[0].Reason, "target not followed", "reason %q does not record that the target was not followed", findings[0].Reason)
}

// TestFloorDenyNames proves the embedded list parses and actually contains the
// entries the SPEC promises. An embedded list that silently parsed to nothing
// would leave every test above passing for the wrong reason.
func TestFloorDenyNames(t *testing.T) {
	t.Parallel()

	names, prefixes, err := FloorDenyNames()
	require.NoError(t, err, "FloorDenyNames")
	// The EXACT set, not a spot check: an entry arrives with the test that
	// proves it is caught, and a spot check lets entries, even a whole location
	// floor, be deleted with the suite green. Pinning the whole set means no
	// entry can leave, or arrive, without this test being part of the same
	// change.
	wantNames := []string{
		"makefile", "gnumakefile",
		"dockerfile", "containerfile",
		"jenkinsfile", "vagrantfile", "rakefile", "procfile", "justfile",
		"cmakelists.txt",
		".gitlab-ci.yml", ".travis.yml", "azure-pipelines.yml", "appveyor.yml",
	}
	assert.Len(t, names, len(wantNames), "floor name list has %d names, want %d: %v", len(names), len(wantNames), names)
	for _, want := range wantNames {
		assert.True(t, names[want], "floor name list is missing %q", want)
	}
	wantPrefixes := []string{".circleci", ".github/actions", ".github/workflows"}
	require.Len(t, prefixes, len(wantPrefixes), "floor name list has %d prefixes, want %d: %v", len(prefixes), len(wantPrefixes), prefixes)
	for i, want := range wantPrefixes {
		assert.Equal(t, want, prefixes[i], "prefix %d = %q, want %q", i, prefixes[i], want)
	}
}

// TestFloorDenyNamesRefusesAnEmptyList makes the emptiness guard load-bearing:
// without this test, replacing the guard with `if false` leaves the suite
// green, and a guard unable to say what it forbids must refuse.
func TestFloorDenyNamesRefusesAnEmptyList(t *testing.T) {
	t.Parallel()

	_, _, err := floorDenyNamesFrom("# only a comment\n\n")
	assert.Error(t, err, "a comment-only name list returned no error; the guard is vacuous")
}

// TestNoCodeProseActionFilePasses is the counter-test for the action entry.
// Matched by name anywhere, action.yml in a self repo — prose ABOUT actions —
// collides with an ordinary dated journal entry, the same false red as
// taskfile.yml. Matching by LOCATION catches the real thing and leaves the
// prose alone.
func TestNoCodeProseActionFilePasses(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":                          0o644,
		"action.yml":                        0o644,
		"journal/2026/action.yaml":          0o644,
		".github/actions/deploy/action.yml": 0o644,
	}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{".github/actions/deploy/action.yml"})
}

// TestNoCodeLocationMatchIsCaseInsensitive pins that locations fold case as
// names do: on a case-insensitive filesystem a tree checked out as
// .GitHub/workflows/ must not escape the location floor while a file named
// MAKEFILE is caught.
func TestNoCodeLocationMatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{"NOTES.md": 0o644, ".GitHub/Workflows/ci.yml": 0o644}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{".GitHub/Workflows/ci.yml"})
}

// TestParseNameLinesRefusesMalformed: a typo'd entry must be an error, not a
// silently dropped line. A list that matches less than it says while reporting
// a clean tree is the fail-open shape this whole check exists against.
func TestParseNameLinesRefusesMalformed(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"Makefile",              // no name:/path: prefix
		"nmae:Makefile",         // typo'd prefix
		"name:sub/dir/Makefile", // a name entry may not carry a path
		"name:",                 // empty
		"path:",                 // empty
		"path:../escape/",       // traversal
		// Held to validExt's standard.
		`path:.circleci\\`,          // backslash: ToSlash is a no-op here
		`path:.github\\workflows\\`, // the same, with a separator inside
		"name:.",                    // filepath.Base never yields this
		"name:..",                   // the same
		"path:/.github/workflows/",  // leading slash: refused, not normalized
		// The path side of the whitespace and non-printable rule.
		"path:.github/work flows/",
		"path:.github\u200b/",
		// Each of the following builds an entry that matches nothing, survives the
		// emptiness guard because other entries are fine, and leaves a floor
		// forbidding less than it says: the fail-open shape this check exists
		// against, which validExt refuses for the extension list too.
		"name:make*",          // glob; names are matched literally
		"name:*.yml",          // glob
		"name:make file",      // embedded whitespace
		"name:makefile\u200b", // zero-width space
		"path:*",              // glob
		"path:./.github/",     // a "." segment never matches a cleaned path
	} {
		_, _, err := parseNameLines(bad)
		assert.Error(t, err, "parseNameLines(%q) returned no error, want refusal", bad)
	}
}

// TestNoCodeLocationIsAnchoredAtTheRepoRoot pins a decision rather than an
// accident. A CI system reads .github/workflows/ at the root and nowhere else,
// so a nested copy is not that machinery and is not flagged by location. Left
// untested, this would look like an oversight to the next reader and could be
// "fixed" into a false red.
func TestNoCodeLocationIsAnchoredAtTheRepoRoot(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":                     0o644,
		".github/workflows/ci.yml":     0o644,
		"sub/.github/workflows/ci.yml": 0o644,
	}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{".github/workflows/ci.yml"})
}

// TestNoCodeCompositeActionIsFlagged: a composite action executes on the
// runner exactly as a workflow does and lives one directory over, so a floor
// that catches only .github/workflows/ leaves the same consequence class open.
// Matched by location (.github/actions/) rather than by widening the prefix to
// .github/, which also holds issue templates and CONTRIBUTING.
func TestNoCodeCompositeActionIsFlagged(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{
		"NOTES.md":                          0o644,
		".github/actions/deploy/action.yml": 0o644,
		".github/ISSUE_TEMPLATE/bug.md":     0o644,
		".github/CONTRIBUTING.md":           0o644,
	}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{".github/actions/deploy/action.yml"})
}

// TestNoCodeProseTaskfilePasses is the false red this list can least afford. A
// writer's own taskfile.yml of things to do is ordinary prose, and a floor that
// calls it "build machinery by name" says something untrue about it. This pins
// its absence from the list so it is not added without an argument.
func TestNoCodeProseTaskfilePasses(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{"NOTES.md": 0o644, "taskfile.yml": 0o644}
	contents := map[string]string{"taskfile.yml": "todo:\n  - write\n"}
	_, findings, err := scan(t, files, contents, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, nil)
}

// TestNoCodeMakefileFragmentsAreCaught pins the Makefile fragment extensions
// at the TREE level, not only in the list.
func TestNoCodeMakefileFragmentsAreCaught(t *testing.T) {
	t.Parallel()

	files := map[string]os.FileMode{"NOTES.md": 0o644, "rules.mk": 0o644, "config.mak": 0o644}
	_, findings, err := scan(t, files, nil, nil, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{"rules.mk", "config.mak"})
}

// TestNoCodeSymlinkedAncestorDefeatsAMultiSegmentPrefix pins a DECLARED LIMIT
// rather than a bug. A link named .github pointing at a tree of workflows is
// not caught: the walk never descends and the link's own name is all that gets
// classified. A single-segment prefix like .circleci IS immune, because there
// the link's own name is the prefix. SPEC names this gap, and a named gap with
// no test is one nobody notices closing or widening.
func TestNoCodeSymlinkedAncestorDefeatsAMultiSegmentPrefix(t *testing.T) {
	t.Parallel()

	_, findings, err := scan(t,
		map[string]os.FileMode{"NOTES.md": 0o644}, nil,
		map[string]string{".github": "/tmp", ".circleci": "/tmp"}, NoCodeOptions{})
	require.NoError(t, err, "NoCode")
	wantOnly(t, findings, []string{".circleci"})
}

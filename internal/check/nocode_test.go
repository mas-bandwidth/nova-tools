package check

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if len(got) != len(want) {
		t.Fatalf("flagged %v, want exactly %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("%q not flagged; flagged %v", w, got)
		}
	}
}

func TestNoCode(t *testing.T) {
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
		// The gap that made the shipped check fail open: a script with no
		// extension and no executable bit. The shebang is the tell that
		// survives renaming.
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
		// allowed by default. A test pins it so no default can quietly return.
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
			if err != nil {
				t.Fatalf("NoCode: %v", err)
			}
			if scanned != tt.wantScanned {
				t.Errorf("scanned = %d, want %d (findings: %v)", scanned, tt.wantScanned, findings)
			}
			wantFailures(t, findings, tt.wantFind)
			if tt.wantExactly != nil {
				wantOnly(t, findings, tt.wantExactly)
			}
		})
	}
}

// TestNoCodeSymlinkNotFollowed is separate because it needs a real symlink.
func TestNoCodeSymlinkNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeMode(t, outside, "escape.py", "print()", 0o644)
	writeMode(t, dir, "README.md", "prose", 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: dir})
	if err != nil {
		t.Fatalf("NoCode: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("symlink was followed or flagged: %v", findings)
	}
	if scanned != 2 {
		t.Errorf("scanned = %d, want 2 (README.md and the link's own name)", scanned)
	}
}

// TestNoCodeFloorListCoversFormerlyMissed pins every extension the shipped
// list did not catch. Each one was a live fail-open: the tool said clean on a
// tree holding machinery, while its spec claimed a self repo holds only prose.
func TestNoCodeFloorListCoversFormerlyMissed(t *testing.T) {
	formerlyMissed := []string{
		".awk", ".clj", ".cxx", ".ex", ".exs", ".fish", ".h", ".hpp", ".hs",
		".jl", ".m", ".ml", ".mm", ".r", ".scala", ".vbs", ".zsh",
	}
	for _, ext := range formerlyMissed {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, "machinery"+ext, "code", 0o644)
			_, findings, err := NoCode(NoCodeOptions{Dir: dir})
			if err != nil {
				t.Fatalf("NoCode: %v", err)
			}
			if len(findings) == 0 {
				t.Fatalf("%s not caught by the floor list", ext)
			}
		})
	}
}

func TestFloorDenyExts(t *testing.T) {
	exts, err := FloorDenyExts()
	if err != nil {
		t.Fatalf("FloorDenyExts: %v", err)
	}
	if len(exts) < 40 {
		t.Errorf("floor list has %d entries, expected at least 40", len(exts))
	}
	seen := map[string]bool{}
	for _, e := range exts {
		if !strings.HasPrefix(e, ".") {
			t.Errorf("entry %q does not begin with a dot", e)
		}
		if e != strings.ToLower(e) {
			t.Errorf("entry %q is not lowercase", e)
		}
		if seen[e] {
			t.Errorf("duplicate entry %q", e)
		}
		seen[e] = true
	}
	// .go is on the list on purpose: an exemption for the language the tools
	// are written in would gut the gate.
	for _, must := range []string{".go", ".py", ".sh", ".cpp", ".zsh", ".exe"} {
		if !seen[must] {
			t.Errorf("floor list is missing %s", must)
		}
	}
}

func TestParseDenyList(t *testing.T) {
	t.Run("comma list", func(t *testing.T) {
		got, err := ParseDenyList(".py,.sh")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != ".py" || got[1] != ".sh" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("bare extensions gain their dot", func(t *testing.T) {
		got, err := ParseDenyList("py,sh")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != ".py" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("@file", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "list.txt", "# comment\n.py\n\n.rs\n", 0o644)
		got, err := ParseDenyList("@" + filepath.Join(dir, "list.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("empty spec refuses", func(t *testing.T) {
		if _, err := ParseDenyList(""); err == nil {
			t.Error("expected an error for an empty specification")
		}
	})
	t.Run("unreadable file refuses", func(t *testing.T) {
		if _, err := ParseDenyList("@/nonexistent/nope.txt"); err == nil {
			t.Error("expected an error for an unreadable deny-list file")
		}
	})
	t.Run("file of only comments refuses", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "empty.txt", "# nothing here\n\n", 0o644)
		if _, err := ParseDenyList("@" + filepath.Join(dir, "empty.txt")); err == nil {
			t.Error("expected an error: a guard that forbids nothing must refuse")
		}
	})
}

func TestNoCodeRefusals(t *testing.T) {
	t.Run("missing directory refuses", func(t *testing.T) {
		if _, _, err := NoCode(NoCodeOptions{Dir: "/nonexistent/nope"}); err == nil {
			t.Error("expected an error for a missing directory")
		}
	})
	t.Run("a file instead of a directory refuses", func(t *testing.T) {
		dir := t.TempDir()
		writeMode(t, dir, "f.md", "x", 0o644)
		if _, _, err := NoCode(NoCodeOptions{Dir: filepath.Join(dir, "f.md")}); err == nil {
			t.Error("expected an error when --dir is not a directory")
		}
	})
}

// A finding may never name a list that did not produce it: an empty DenyExt
// means the floor ran, whatever provenance the caller passed alongside it.
func TestNoCodeProvenanceCannotLie(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "tool.py", "print()", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir, DenyExt: nil, DenySource: DenyReplaced})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want 1", findings)
	}
	if !strings.Contains(findings[0].Reason, DenyFloor) {
		t.Errorf("reason %q does not name the list that actually ran", findings[0].Reason)
	}
	if strings.Contains(findings[0].Reason, DenyReplaced) {
		t.Errorf("reason %q names a list that did not produce it", findings[0].Reason)
	}
}

// A deny-list entry that cannot be an extension is a refusal. Each of these
// built a list that matched nothing and reported a clean tree.
func TestParseDenyListRefusesNonExtensions(t *testing.T) {
	for _, spec := range []string{"mylist.txt", "*.py", "src/x", ".a b", "..", "."} {
		t.Run(spec, func(t *testing.T) {
			if got, err := ParseDenyList(spec); err == nil {
				t.Errorf("accepted %q as %v; a guard that forbids nothing must refuse", spec, got)
			}
		})
	}
}

func TestNoCodeTrailingWhitespaceInName(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "trailing.py ", "print()", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	wantFailures(t, findings, []string{"code extension .py"})
}

// --allow must not over-match: history must not allow history-of-tools.
// Nothing pinned the boundary, so widening the prefix test to a bare
// HasPrefix left the suite green.
func TestNoCodeAllowDoesNotOverMatchSiblings(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "history/ok.py", "x", 0o644)
	writeMode(t, dir, "history-of-tools/run.py", "x", 0o644)
	writeMode(t, dir, "historical.py", "x", 0o644)
	_, findings, err := NoCode(NoCodeOptions{Dir: dir, Allow: []string{"history"}})
	if err != nil {
		t.Fatal(err)
	}
	wantOnly(t, findings, []string{"history-of-tools/run.py", "historical.py"})
}

func TestParseDenyListRefusesZeroWidthSpace(t *testing.T) {
	if _, err := ParseDenyList(".py​"); err == nil {
		t.Error("accepted a zero-width space: a one-entry list that forbids nothing")
	}
}

// --dir naming a SYMLINK to the repo. os.Stat follows the link, so the
// directory check passed and WalkDir then saw the root as a single non-dir
// entry: a clean pass over a tree never opened. On macOS /var is such a link.
func TestNoCodeDirIsASymlinkToTheTree(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMode(t, real, "deploy.sh", "#!/bin/sh\n", 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: link})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("scanned nothing through a symlinked root: a clean pass over a tree never opened")
	}
	wantFailures(t, findings, []string{"deploy.sh"})
}

// The audit must not be the weaker mode. Every one of these was a finding in
// the gate and a silent pass in the audit.
func TestNoCodeAuditFailsClosedLikeTheGate(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "README.md", "prose", 0o644)
	if err := syscallMkfifo(filepath.Join(dir, "pipe.sh")); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}
	scanned, findings, err := NoCode(NoCodeOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 2 {
		t.Errorf("scanned = %d, want 2", scanned)
	}
	wantFailures(t, findings, []string{"pipe.sh", "not a regular file"})
}

// DenyExt without DenySource must refuse: a finding may not name an unknown list.
func TestNoCodeDenyExtWithoutSourceRefuses(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := NoCode(NoCodeOptions{Dir: dir, DenyExt: []string{".foo"}}); err == nil {
		t.Error("accepted a deny-list with no stated provenance")
	}
}

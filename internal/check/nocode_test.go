package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scan is the common shape: materialize a tree, run with the floor list.
func scan(t *testing.T, files map[string]os.FileMode, contents map[string]string, opts NoCodeOptions) (int, []Failure, error) {
	t.Helper()
	dir := t.TempDir()
	for rel, mode := range files {
		body, ok := contents[rel]
		if !ok {
			body = "x"
		}
		writeMode(t, dir, rel, body, mode)
	}
	opts.Dir = dir
	return NoCode(opts)
}

func TestNoCode(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string]os.FileMode
		contents    map[string]string
		opts        NoCodeOptions
		wantScanned int
		wantFind    []string // substrings; empty = must pass
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
			name:        "executable without code extension flagged",
			files:       map[string]os.FileMode{"runme": 0o755},
			contents:    map[string]string{"runme": "#!/bin/sh\n"},
			wantScanned: 1,
			wantFind:    []string{"runme", "executable (mode 0755)"},
		},
		{
			name:        "executable markdown is still a violation",
			files:       map[string]os.FileMode{"notes.md": 0o744},
			wantScanned: 1,
			wantFind:    []string{"notes.md", "executable"},
		},
		{
			name:        "both reasons reported together",
			files:       map[string]os.FileMode{"deploy.sh": 0o755},
			wantScanned: 1,
			wantFind:    []string{"code extension .sh", "executable"},
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
		},
		{
			name:        "a finding names which list produced it",
			files:       map[string]os.FileMode{"tool.py": 0o644},
			wantScanned: 1,
			wantFind:    []string{"code extension .py (" + DenyFloor + ")"},
		},
		{
			name:        "symlinks are not followed and not flagged",
			files:       map[string]os.FileMode{"README.md": 0o644},
			wantScanned: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanned, findings, err := scan(t, tt.files, tt.contents, tt.opts)
			if err != nil {
				t.Fatalf("NoCode: %v", err)
			}
			if scanned != tt.wantScanned {
				t.Errorf("scanned = %d, want %d (findings: %v)", scanned, tt.wantScanned, findings)
			}
			wantFailures(t, findings, tt.wantFind)
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
	if scanned != 1 {
		t.Errorf("scanned = %d, want 1 (only README.md)", scanned)
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

// TestNoCodeStaged covers the commit-gate path: the gate refuses what is being
// ADDED, not what already exists.
func TestNoCodeStaged(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "old.py", "print()", 0o644)
	writeMode(t, dir, "new.py", "print()", 0o644)
	writeMode(t, dir, "notes.md", "prose", 0o644)

	t.Run("only staged paths are classified", func(t *testing.T) {
		scanned, findings, err := NoCode(NoCodeOptions{
			Dir: dir, Staged: []string{"new.py", "notes.md"}, StagedSet: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if scanned != 2 {
			t.Errorf("scanned = %d, want 2", scanned)
		}
		wantFailures(t, findings, []string{"new.py"})
		for _, f := range findings {
			if f.Subject == "old.py" {
				t.Error("old.py was reported: the gate must refuse what is added, not what exists")
			}
		}
	})

	t.Run("staged deletion is not an accumulation", func(t *testing.T) {
		_, findings, err := NoCode(NoCodeOptions{
			Dir: dir, Staged: []string{"deleted.py"}, StagedSet: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Errorf("a removed file was reported: %v", findings)
		}
	})

	t.Run("staged with no paths reports nothing and does not walk", func(t *testing.T) {
		scanned, findings, err := NoCode(NoCodeOptions{Dir: dir, StagedSet: true})
		if err != nil {
			t.Fatal(err)
		}
		if scanned != 0 || len(findings) != 0 {
			t.Errorf("scanned=%d findings=%v; an empty staged set is an empty commit", scanned, findings)
		}
	})

	t.Run("--allow applies to staged paths too", func(t *testing.T) {
		writeMode(t, dir, "history/frozen.py", "print()", 0o644)
		_, findings, err := NoCode(NoCodeOptions{
			Dir: dir, Staged: []string{"history/frozen.py"}, StagedSet: true, Allow: []string{"history"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Errorf("allowed path reported: %v", findings)
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

package check

import (
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

func TestLinksRefusesBadDir(t *testing.T) {
	if _, _, _, err := Links(t.TempDir() + "/nope"); err == nil {
		t.Error("nonexistent dir should be an error, not a guess")
	}
}

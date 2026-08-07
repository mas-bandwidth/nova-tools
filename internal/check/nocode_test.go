package check

import (
	"os"
	"testing"
)

func TestNoCode(t *testing.T) {
	type file struct {
		content string
		mode    os.FileMode
	}
	tests := []struct {
		name        string
		files       map[string]file
		wantScanned int
		wantFind    []string // substrings; empty = must pass
	}{
		{
			name: "prose tree is clean",
			files: map[string]file{
				"README.md":    {"hello", 0o644},
				"pattern/p.md": {"pattern", 0o644},
				"images/x.png": {"\x89PNG", 0o644},
			},
			wantScanned: 3,
		},
		{
			name:        "go file flagged",
			files:       map[string]file{"tool.go": {"package main", 0o644}},
			wantScanned: 1,
			wantFind:    []string{"tool.go", "code extension .go"},
		},
		{
			name:        "extension match is case-insensitive",
			files:       map[string]file{"SCRIPT.PY": {"print()", 0o644}},
			wantScanned: 1,
			wantFind:    []string{"SCRIPT.PY", "code extension .py"},
		},
		{
			name:        "executable without code extension flagged",
			files:       map[string]file{"runme": {"#!/bin/sh\n", 0o755}},
			wantScanned: 1,
			wantFind:    []string{"runme", "executable (mode 0755)"},
		},
		{
			name:        "executable markdown is still a violation",
			files:       map[string]file{"notes.md": {"prose", 0o744}},
			wantScanned: 1,
			wantFind:    []string{"notes.md", "executable"},
		},
		{
			name:        "both reasons reported together",
			files:       map[string]file{"deploy.sh": {"echo hi", 0o755}},
			wantScanned: 1,
			wantFind:    []string{"code extension .sh", "executable"},
		},
		{
			name: "git internals ignored",
			files: map[string]file{
				".git/hooks/pre-commit": {"#!/bin/sh\n", 0o755},
				"README.md":             {"hello", 0o644},
			},
			wantScanned: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, f := range tt.files {
				writeMode(t, dir, rel, f.content, f.mode)
			}
			scanned, findings, err := NoCode(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scanned != tt.wantScanned {
				t.Errorf("scanned = %d, want %d", scanned, tt.wantScanned)
			}
			wantFailures(t, findings, tt.wantFind)
		})
	}
}

// Reviewer suggestion: the extension list gained the common scripting and
// compiled-artifact extensions. Each one must flag, non-executable.
func TestNoCodeExtendedExtensions(t *testing.T) {
	exts := []string{
		".rb", ".pl", ".php", ".java", ".lua", ".swift", ".kt",
		".mjs", ".cjs", ".jsx", ".tsx", ".bash", ".ps1", ".bat",
		".cmd", ".exe",
	}
	for _, ext := range exts {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, "file"+ext, "contents", 0o644)
			scanned, findings, err := NoCode(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scanned != 1 {
				t.Errorf("scanned = %d, want 1", scanned)
			}
			wantFailures(t, findings, []string{"file" + ext, "code extension " + ext})
		})
	}
}

func TestNoCodeRefusesBadDir(t *testing.T) {
	if _, _, err := NoCode(t.TempDir() + "/nope"); err == nil {
		t.Error("nonexistent dir should be an error, not a guess")
	}
}

package check

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue2294ParametrisedClassifyProvesParity asserts that the
// parameterised classify produces identical findings when fed
// filesystem-derived inputs (an os.FileInfo permission mask and an
// os.Open reader) versus index-derived inputs (a plain permission
// mask and a blob reader over the same bytes). The two callers
// must reach one function, guaranteeing parity by construction.
func TestIssue2294ParametrisedClassifyProvesParity(t *testing.T) {
	cases := []struct {
		name    string
		rel     string
		content string
		mode    os.FileMode
		isLink  bool
	}{
		{
			name:    "prose file",
			rel:     "README.md",
			content: "# Heading\n\nprose\n",
			mode:    0o644,
		},
		{
			name:    "code extension",
			rel:     "tool.py",
			content: "print(1)\n",
			mode:    0o644,
		},
		{
			name:    "executable script",
			rel:     "run.sh",
			content: "#!/bin/sh\necho hi\n",
			mode:    0o755,
		},
		{
			name:    "shebang without extension",
			rel:     "nova-id",
			content: "#!/usr/bin/env python3\nprint(1)\n",
			mode:    0o644,
		},
		{
			name:    "short file no shebang",
			rel:     "notes.md",
			content: "# short\n",
			mode:    0o644,
		},
		{
			name:    "build machinery by name",
			rel:     "Makefile",
			content: "all:\n\tcurl x|sh\n",
			mode:    0o644,
		},
		{
			name:    "empty file",
			rel:     "empty.txt",
			content: "",
			mode:    0o644,
		},
	}

	denySet := map[string]bool{}
	denyExt, err := FloorDenyExts()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range denyExt {
		denySet[e] = true
	}
	denyNames, denyPrefixes, err := FloorDenyNames()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, tc.rel, tc.content, tc.mode)

			fullPath := filepath.Join(dir, filepath.FromSlash(tc.rel))
			fi, err := os.Stat(fullPath)
			if err != nil {
				t.Fatal(err)
			}

			// Walk-derived: classify reads perm from fi and opens fullPath.
			walkReasons := classify(fullPath, tc.rel, fi, tc.isLink, denySet, DenyFloor, denyNames, denyPrefixes)

			// Index-derived: caller supplies perm and a peek-two-bytes reader.
			perm := fi.Mode().Perm()
			peekTwo := makePeekTwo(fullPath)
			indexReasons := classifyParametrised(tc.rel, perm, tc.isLink, denySet, DenyFloor, denyNames, denyPrefixes, peekTwo)

			if len(walkReasons) != len(indexReasons) {
				t.Fatalf("reason count differs: walk=%v index=%v", walkReasons, indexReasons)
			}
			for i := range walkReasons {
				if walkReasons[i] != indexReasons[i] {
					t.Errorf("reason %d differs: walk=%q index=%q", i, walkReasons[i], indexReasons[i])
				}
			}
		})
	}
}

// TestIssue2294ClassifyParametrisedSymlink asserts that a symlink
// named as code is flagged without being followed, through the
// parameterised path.
func TestIssue2294ClassifyParametrisedSymlink(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "README.md", "prose", 0o644)
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "run.sh")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	denySet := map[string]bool{}
	denyExt, err := FloorDenyExts()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range denyExt {
		denySet[e] = true
	}
	denyNames, denyPrefixes, err := FloorDenyNames()
	if err != nil {
		t.Fatal(err)
	}

	// Symlink through parameterised path — should never dereference.
	reasons := classifyParametrised("run.sh", 0o644, true, denySet, DenyFloor, denyNames, denyPrefixes, nil)
	found := false
	for _, r := range reasons {
		if strings.Contains(r, "target not followed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'target not followed' in reasons: %v", reasons)
	}
}

// TestIssue2294ClassifyParametrisedUnreadablePeek asserts that an
// error from the peek-two reader is reported as "unreadable" through
// the parameterised path, just as an unreadable file is in the walk.
func TestIssue2294ClassifyParametrisedUnreadablePeek(t *testing.T) {
	denySet := map[string]bool{}
	denyExt, err := FloorDenyExts()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range denyExt {
		denySet[e] = true
	}
	denyNames, denyPrefixes, err := FloorDenyNames()
	if err != nil {
		t.Fatal(err)
	}

	errPeek := func() (string, bool, error) { return "", false, errors.New("permission denied") }
	reasons := classifyParametrised("secret", 0o644, false, denySet, DenyFloor, denyNames, denyPrefixes, errPeek)
	found := false
	for _, r := range reasons {
		if strings.Contains(r, "unreadable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unreadable reason for a failed peek: %v", reasons)
	}
}

// makePeekTwo returns a peek function that opens the file at p and
// peeks at its first two bytes, matching the walk's hasShebang
// semantics exactly.
func makePeekTwo(p string) func() (string, bool, error) {
	return func() (string, bool, error) {
		f, err := os.Open(p)
		if err != nil {
			return "", false, err
		}
		defer f.Close()
		b, err := bufio.NewReader(f).Peek(2)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", false, nil
			}
			return "", false, err
		}
		return string(b), string(b) == "#!", nil
	}
}

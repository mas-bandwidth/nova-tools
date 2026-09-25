package check

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// indexMode is the permission mask a git index record carries: git keeps
// only 100644 or 100755, so the index-derived mask is one of those two and
// never read from a FileInfo.
func indexMode(executable bool) os.FileMode {
	if executable {
		return 0o755
	}
	return 0o644
}

// blobPeek is the index-derived first-two-bytes reader: it reads from the
// blob's bytes in memory and never touches the filesystem path.
func blobPeek(blob []byte) func() ([]byte, error) {
	return func() ([]byte, error) { return readFirstTwo(bytes.NewReader(blob)) }
}

// TestNoCodeStagedClassifyTakesParameterisedInputs (issue #2294, docs/SPEC.md
// "What called unchanged costs"): the classifier produces identical findings
// when fed a FileInfo-derived permission mask and an os.Open reader (the walk)
// versus an index-derived mask and a blob reader over the same bytes. The file
// is REMOVED from disk before the index side runs, so a blob reader that
// secretly reopened the path would fail here rather than pass by coincidence.
func TestNoCodeStagedClassifyTakesParameterisedInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		rel     string
		content string
		exec    bool
	}{
		{"prose file", "README.md", "# Heading\n\nprose\n", false},
		{"code extension", "tool.py", "print(1)\n", false},
		{"executable script", "run.sh", "#!/bin/sh\necho hi\n", true},
		{"shebang without extension", "nova-id", "#!/usr/bin/env python3\nprint(1)\n", false},
		{"executable prose", "notes.md", "# notes\n", true},
		{"one byte", "a.txt", "#", false},
		{"two bytes shebang only", "b", "#!", false},
		{"build machinery by name", "Makefile", "all:\n\tcurl x|sh\n", false},
		{"machinery by location", ".github/workflows/ci.yml", "on: push\n", false},
		{"empty file", "empty.txt", "", false},
	}
	rules, err := newNoCodeRules(NoCodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, tc.rel, tc.content, indexMode(tc.exec))
			fullPath := filepath.Join(dir, filepath.FromSlash(tc.rel))
			fi, err := os.Lstat(fullPath)
			if err != nil {
				t.Fatal(err)
			}

			// Walk-derived: the mask off the FileInfo, the bytes via os.Open.
			walk := rules.classify(tc.rel, fi.Mode().Perm(), false, func() ([]byte, error) { return peekTwoFile(fullPath) })
			// The full walk agrees with the walk-derived call.
			_, findings, err := NoCode(NoCodeOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := findingsByPath(findings)[tc.rel], strings.Join(walk, "; "); got != want {
				t.Fatalf("NoCode walk %q != walk-derived classify %q", got, want)
			}

			if err := os.Remove(fullPath); err != nil {
				t.Fatal(err)
			}
			// Index-derived: the mask from the record, the bytes from the blob.
			index := rules.classify(tc.rel, indexMode(tc.exec), false, blobPeek([]byte(tc.content)))

			if strings.Join(walk, "; ") != strings.Join(index, "; ") {
				t.Fatalf("parity broken for %s:\n walk =%q\n index=%q", tc.rel, walk, index)
			}
		})
	}
}

// TestNoCodeStagedReusesTheClassifierUnchanged (issue #2294): the same --allow
// prefixes and --deny-ext / --deny-ext-add act identically through the
// parameterised seam. For each flag set, NoCode's filesystem walk and an
// index-shaped caller (rules from the same NoCodeOptions, index masks, blob
// readers, no filesystem) must produce the same findings, and each flag's
// behaviour is asserted explicitly on the index path.
func TestNoCodeStagedReusesTheClassifierUnchanged(t *testing.T) {
	t.Parallel()

	type blob struct {
		content string
		exec    bool
	}
	tree := map[string]blob{
		"README.md":                {"# prose\n", false},
		"tool.py":                  {"print(1)\n", false},
		"lib.a":                    {"!<arch>\n", false},
		"data.xyz":                 {"x\n", false},
		"scripts/build.py":         {"print(2)\n", false},
		"scripts/deep/gen.sh":      {"echo\n", false},
		"bin/run":                  {"#!/bin/sh\n", true},
		"Makefile":                 {"all:\n", false},
		".github/workflows/ci.yml": {"on: push\n", false},
	}
	floor, err := FloorDenyExts()
	if err != nil {
		t.Fatal(err)
	}
	added := append(append([]string{}, floor...), ".xyz")

	cases := []struct {
		name    string
		opts    NoCodeOptions
		flagged map[string]string // rel -> substring its reason must carry
		clean   []string          // rels that must not be flagged
	}{
		{
			name: "floor, no allow",
			opts: NoCodeOptions{},
			flagged: map[string]string{
				"tool.py": "code extension .py (" + DenyFloor + ")", "scripts/build.py": ".py",
				"scripts/deep/gen.sh": ".sh", "bin/run": "shebang", "Makefile": "by name makefile",
				".github/workflows/ci.yml": "by location .github/workflows/",
			},
			clean: []string{"README.md", "lib.a", "data.xyz"},
		},
		{
			name: "--allow prefixes (normalised, any depth)",
			opts: NoCodeOptions{Allow: []string{"./scripts/", "bin"}},
			flagged: map[string]string{
				"tool.py": ".py", "Makefile": "by name", ".github/workflows/ci.yml": "by location",
			},
			clean: []string{"scripts/build.py", "scripts/deep/gen.sh", "bin/run", "README.md"},
		},
		{
			name: "--deny-ext replaces the extension floor, not the name floor",
			opts: NoCodeOptions{DenyExt: []string{".a"}, DenySource: DenyReplaced},
			flagged: map[string]string{
				"lib.a": "code extension .a (" + DenyReplaced + ")", "bin/run": "shebang",
				"Makefile": "by name", ".github/workflows/ci.yml": "by location",
			},
			clean: []string{"tool.py", "scripts/build.py", "scripts/deep/gen.sh", "data.xyz"},
		},
		{
			name: "--deny-ext-add keeps the floor and extends it",
			opts: NoCodeOptions{DenyExt: added, DenySource: DenyExtended},
			flagged: map[string]string{
				"tool.py": "code extension .py (" + DenyExtended + ")", "data.xyz": "code extension .xyz (" + DenyExtended + ")",
				"Makefile": "by name",
			},
			clean: []string{"README.md", "lib.a"},
		},
		{
			name: "--allow with --deny-ext-add",
			opts: NoCodeOptions{Allow: []string{"scripts"}, DenyExt: added, DenySource: DenyExtended},
			flagged: map[string]string{
				"data.xyz": ".xyz", "tool.py": ".py", "bin/run": "executable",
			},
			clean: []string{"scripts/build.py", "scripts/deep/gen.sh"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, b := range tree {
				writeMode(t, dir, rel, b.content, indexMode(b.exec))
			}
			opts := tc.opts
			opts.Dir = dir
			walkScanned, findings, err := NoCode(opts)
			if err != nil {
				t.Fatal(err)
			}
			walk := findingsByPath(findings)

			// Index-shaped caller: same options, no filesystem.
			rules, err := newNoCodeRules(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			rels := make([]string, 0, len(tree))
			for rel := range tree {
				rels = append(rels, rel)
			}
			sort.Strings(rels)
			index := map[string]string{}
			indexScanned := 0
			for _, rel := range rels {
				if rules.allowed(rel) {
					continue
				}
				indexScanned++
				b := tree[rel]
				if r := rules.classify(rel, indexMode(b.exec), false, blobPeek([]byte(b.content))); len(r) > 0 {
					index[rel] = strings.Join(r, "; ")
				}
			}

			if walkScanned != indexScanned {
				t.Errorf("scanned differs: walk=%d index=%d", walkScanned, indexScanned)
			}
			for rel := range tree {
				if walk[rel] != index[rel] {
					t.Errorf("%s: walk=%q index=%q", rel, walk[rel], index[rel])
				}
			}
			for rel, want := range tc.flagged {
				if !strings.Contains(index[rel], want) {
					t.Errorf("%s: index reason %q lacks %q", rel, index[rel], want)
				}
			}
			for _, rel := range tc.clean {
				if index[rel] != "" {
					t.Errorf("%s: want clean on the index path, got %q", rel, index[rel])
				}
			}
		})
	}
}

func findingsByPath(fs []Failure) map[string]string {
	m := make(map[string]string, len(fs))
	for _, f := range fs {
		m[f.Subject] = f.Reason
	}
	return m
}

// TestIssue2294ClassifyParametrisedSymlink asserts that a symlink named as
// code is flagged without being followed, through the parameterised path: the
// reader is never called.
func TestIssue2294ClassifyParametrisedSymlink(t *testing.T) {
	t.Parallel()

	rules, err := newNoCodeRules(NoCodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	peek := func() ([]byte, error) { called = true; return []byte("#!"), nil }
	reasons := rules.classify("run.sh", 0o644, true, peek)
	if called {
		t.Error("symlink target was read through peekTwo")
	}
	if !strings.Contains(strings.Join(reasons, "; "), "target not followed") {
		t.Errorf("expected 'target not followed' in reasons: %v", reasons)
	}
}

// TestIssue2294ClassifyParametrisedUnreadablePeek asserts that a read error
// from the first-two-bytes reader, or no reader at all, is a finding through
// the parameterised path, never a pass.
func TestIssue2294ClassifyParametrisedUnreadablePeek(t *testing.T) {
	t.Parallel()

	rules, err := newNoCodeRules(NoCodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	errPeek := func() ([]byte, error) { return nil, errors.New("permission denied") }
	for name, peek := range map[string]func() ([]byte, error){"read error": errPeek, "nil reader": nil} {
		reasons := rules.classify("secret", 0o644, false, peek)
		if !strings.Contains(strings.Join(reasons, "; "), "unreadable") {
			t.Errorf("%s: expected an unreadable reason, got %v", name, reasons)
		}
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func verifyFile(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0700))
	require.NoError(t, os.WriteFile(p, []byte(body+"\nThe lighthouse keeper polishes the brass lantern and checks the glass before sunset every evening.\n"), 0600))
}

func TestVerifyExcludesEveryCheck(t *testing.T) {
	t.Parallel()
	for _, exclusion := range []string{"ignored", "igno*"} {
		t.Run(exclusion, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			verifyFile(t, root, "keep/a.md", "---\nname: a\n---\nkept\n")
			verifyFile(t, root, "ignored/b.md", "[[missing]]\n")
			verifyFile(t, root, "index.md", "[a](keep/a.md)\n")
			verifyFile(t, root, "extra.md", "[missing](missing.md)\n")
			args := []string{"verify", "--root", root, "--links", "gate", "--coverage", "*/*.md:*.md", "--frontmatter", "*/*.md"}
			{
				code, _, errOut := runCLI(t, "", args...)
				require.Equalf(t, 1, code, "planted faults: exit %d: %s", code, errOut)
			}
			args = append(args, "--exclude", exclusion, "--exclude", "extra.md")
			code, out, errOut := runCLI(t, "", args...)
			require.Equalf(t, 0, code, "excluded findings: exit %d out=%s err=%s", code, out, errOut)
			require.Containsf(t, out, "coverage=0 frontmatter=0", "excluded findings: exit %d out=%s err=%s", code, out, errOut)
			require.Equalf(t, "", errOut, "excluded findings: exit %d out=%s err=%s", code, out, errOut)
			// An explicitly named excluded file cannot bypass the glob's filtered view.
			code, _, errOut = runCLI(t, "", "verify", "--root", root, "--links", "gate", "--frontmatter", "ignored/b.md", "--exclude", exclusion)
			require.Equalf(t, 2, code, "explicit excluded selector: exit %d err=%s", code, errOut)
			require.Containsf(t, errOut, "matched nothing", "explicit excluded selector: exit %d err=%s", code, errOut)
		})
	}
}

func TestVerifyCountsFindingsBeforeCapping(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		verifyFile(t, root, fmt.Sprintf("notes/n%d.md", i), "words\n")
	}
	verifyFile(t, root, "index.md", "no entries\n")
	code, _, errOut := runCLI(t, "", "verify", "--root", root, "--links", "info", "--coverage", "notes/*.md:index.md", "--frontmatter", "notes/*.md", "--fail-max", "1")
	require.Equalf(t, 1, code, "counts: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "VERIFY FAIL gating=4 shown=2 info=0 coverage=2 frontmatter=2 links=info", "counts: exit %d err=%s", code, errOut)
}

func TestVerifyMissingLinksNamesItsChoices(t *testing.T) {
	t.Parallel()
	code, _, errOut := runCLI(t, "", "verify", "--root", t.TempDir())
	require.Equalf(t, 2, code, "missing links: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "gate", "missing links: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "info", "missing links: exit %d err=%s", code, errOut)
}

// TestVerifyBacklinkThroughADirectorySymlinkOutOfRootIsAFinding pins
// security#76 finding 6 (re-file of security#58 finding 4). notes/a.md and
// notes/b.md name each other's stems, so the coverage direction itself
// passes. notes/dir is a directory symlink to another tree. A relative link
// notes/b.md -> dir/outside.md stays lexically inside --root; os.DirFS stats
// the outside file and verify exits 0. The same link to dir/missing.md is a
// finding either way.
func TestVerifyBacklinkThroughADirectorySymlinkOutOfRootIsAFinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		target string
		plant  bool
	}{
		{name: "target exists outside the root", target: "outside.md", plant: true},
		{name: "target is missing outside the root", target: "missing.md", plant: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			if tc.plant {
				require.NoError(t, os.WriteFile(filepath.Join(outside, tc.target), []byte("kept outside the corpus\n"), 0600))
			}
			require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0700))
			require.NoError(t, os.Symlink(outside, filepath.Join(root, "notes", "dir")))
			verifyFile(t, root, "notes/a.md", "b")
			verifyFile(t, root, "notes/b.md", "a\n["+tc.target+"](dir/"+tc.target+")")
			code, out, errOut := runCLI(t, "", "verify", "--root", root, "--links", "gate", "--coverage", "notes/*.md:notes/*.md")
			require.Equalf(t, 1, code, "symlink backlink: exit %d out=%s err=%s", code, out, errOut)
			require.Containsf(t, errOut, "VERIFY FAIL backlink", "symlink backlink: exit %d out=%s err=%s", code, out, errOut)
			require.Containsf(t, errOut, "notes/b.md", "symlink backlink: exit %d out=%s err=%s", code, out, errOut)
			require.NotContainsf(t, out, "VERIFY OK", "a failing verify must not print an OK line, got %q", out)
		})
	}
}

func TestVerifyLinksToExcludedTargetsRemainFindings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	verifyFile(t, root, "notes/a.md", "---\nname: a\n---\nkept\n")
	verifyFile(t, root, "ignored/b.md", "target words to retain\n")
	verifyFile(t, root, "index.md", "[a](notes/a.md) [b](ignored/b.md) [[b]]\n")
	code, _, errOut := runCLI(t, "", "verify", "--root", root, "--links", "gate", "--coverage", "notes/*.md:index.md", "--exclude", "ignored")
	require.Equalf(t, 1, code, "retained references: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "VERIFY FAIL backlink", "retained references: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "VERIFY FAIL wikilink", "retained references: exit %d err=%s", code, errOut)
	require.Containsf(t, errOut, "gating=2 shown=2 info=0 coverage=1 frontmatter=0", "retained references: exit %d err=%s", code, errOut)
}

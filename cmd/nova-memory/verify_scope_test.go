package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verifyFile(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body+"\nThe lighthouse keeper polishes the brass lantern and checks the glass before sunset every evening.\n"), 0600); err != nil {
		t.Fatal(err)
	}
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
			if code, _, errOut := runCLI(t, "", args...); code != 1 {
				t.Fatalf("planted faults: exit %d: %s", code, errOut)
			}
			args = append(args, "--exclude", exclusion, "--exclude", "extra.md")
			code, out, errOut := runCLI(t, "", args...)
			if code != 0 || !strings.Contains(out, "coverage=0 frontmatter=0") || errOut != "" {
				t.Fatalf("excluded findings: exit %d out=%s err=%s", code, out, errOut)
			}
			// An explicitly named excluded file cannot bypass the glob's filtered view.
			code, _, errOut = runCLI(t, "", "verify", "--root", root, "--links", "gate", "--frontmatter", "ignored/b.md", "--exclude", exclusion)
			if code != 2 || !strings.Contains(errOut, "matched nothing") {
				t.Fatalf("explicit excluded selector: exit %d err=%s", code, errOut)
			}
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
	if code != 1 || !strings.Contains(errOut, "VERIFY FAIL gating=4 shown=2 info=0 coverage=2 frontmatter=2 links=info") {
		t.Fatalf("counts: exit %d err=%s", code, errOut)
	}
}

func TestVerifyMissingLinksNamesItsChoices(t *testing.T) {
	t.Parallel()
	code, _, errOut := runCLI(t, "", "verify", "--root", t.TempDir())
	if code != 2 || !strings.Contains(errOut, "gate") || !strings.Contains(errOut, "info") {
		t.Fatalf("missing links: exit %d err=%s", code, errOut)
	}
}

func TestVerifyLinksToExcludedTargetsRemainFindings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	verifyFile(t, root, "notes/a.md", "---\nname: a\n---\nkept\n")
	verifyFile(t, root, "ignored/b.md", "target words to retain\n")
	verifyFile(t, root, "index.md", "[a](notes/a.md) [b](ignored/b.md) [[b]]\n")
	code, _, errOut := runCLI(t, "", "verify", "--root", root, "--links", "gate", "--coverage", "notes/*.md:index.md", "--exclude", "ignored")
	if code != 1 || !strings.Contains(errOut, "VERIFY FAIL backlink") || !strings.Contains(errOut, "VERIFY FAIL wikilink") || !strings.Contains(errOut, "gating=2 shown=2 info=0 coverage=1 frontmatter=0") {
		t.Fatalf("retained references: exit %d err=%s", code, errOut)
	}
}

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

const twoSequences = `# Fixture
## The rules, numbered
1. First rule
continued first
2. Second rule
3. First rule three
with its complete extent
## Tests this spec demands
1. Test one
2. Test two
3. Other rule three
4. Changed rule four
`

func TestScopedSpecRefusesAmbiguousAndKeepsWholeRuleExtent(t *testing.T) {
	if _, err := parseScopedSpec("docs/SPEC.md", twoSequences, ""); err == nil || !strings.Contains(err.Error(), "#The rules, numbered") || !strings.Contains(err.Error(), "#Tests this spec demands") {
		t.Fatalf("ambiguous spec error = %v", err)
	}
	spec, err := parseScopedSpec("docs/SPEC.md", twoSequences, "The rules, numbered")
	if err != nil {
		t.Fatal(err)
	}
	rule := spec.Rules[3]
	if rule.Line != 6 || !strings.Contains(rule.Text, "with its complete extent") || strings.Contains(rule.Text, "Other rule three") {
		t.Fatalf("wrong rule extent: %#v", rule)
	}
}

func TestCitationGrammarIsScopedAndClosed(t *testing.T) {
	one, err := parseScopedSpec("docs/SPEC.md", twoSequences, "The rules, numbered")
	if err != nil {
		t.Fatal(err)
	}
	two, err := parseScopedSpec("docs/OTHER.md", strings.ReplaceAll(twoSequences, "First rule three", "Other file rule three"), "The rules, numbered")
	if err != nil {
		t.Fatal(err)
	}
	if got := citationTargets("// rules 3, 2 and 1", []scopedSpec{one}); len(got) != 3 {
		t.Fatalf("comma citation targets=%v", got)
	}
	if got := citationTargets("// rule 3", []scopedSpec{one, two}); len(got) != 0 {
		t.Fatalf("bare multiple specs targets=%v", got)
	}
	if got := citationTargets("// SPEC.md rule 3's requirement", []scopedSpec{one, two}); len(got) != 1 || got[0].Path != "docs/SPEC.md" {
		t.Fatalf("prefixed citation targets=%v", got)
	}
	if got := citedRuleNumbers("rule 3 through 7"); len(got) != 0 {
		t.Fatalf("open-ended prose became a citation: %v", got)
	}
}

func TestPacketMechanicallySelectsRulesAndPrintsEveryDiffFile(t *testing.T) {
	lane, _ := packetLab(t)
	repo := filepath.Join(lane, "repo")
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
	}
	// Put the spec on the selected head, then make a fresh source revision that
	// cites it. The worktree copy is deliberately changed afterward to prove
	// packet reads head:path rather than reader disk.
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "SPEC.md"), []byte(twoSequences), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "docs/SPEC.md")
	git("commit", "-qm", "add spec")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\nchanged\n// rule 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "z.txt"), []byte("no citation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt", "z.txt")
	git("commit", "-qm", "cite rule")
	if err := os.WriteFile(filepath.Join(repo, "docs", "SPEC.md"), []byte("reader disk is not head\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	args := []string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--spec", "docs/SPEC.md#The rules, numbered", "--max-bytes", "16384"}
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("packet=%d stderr=%s", code, errb.String())
	}
	body, err := os.ReadFile("packet.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"a.txt: rules=1", "docs/SPEC.md: rules=3", "z.txt: rules=0", "### docs/SPEC.md:6 rule 3", "> with its complete extent", "touched by: a.txt (cited)"} {
		if !strings.Contains(got, want) {
			t.Errorf("packet lacks %q:\n%s", want, got)
		}
	}
}

func TestChangedSpecHunkQuotesBothExactSides(t *testing.T) {
	lane, _ := packetLab(t)
	repo := filepath.Join(lane, merge.RepoDir)
	git := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("checkout", "main")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseSpec := "## Rules\n1. One\n2. Old wording\n"
	if err := os.WriteFile(filepath.Join(repo, "docs", "SPEC.md"), []byte(baseSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "docs/SPEC.md")
	git("commit", "-qm", "base spec")
	base := git("rev-parse", "HEAD")
	git("checkout", "feature")
	git("merge", "--no-edit", "main")
	if err := os.WriteFile(filepath.Join(repo, "docs", "SPEC.md"), []byte(strings.Replace(baseSpec, "Old wording", "New wording", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "docs/SPEC.md")
	git("commit", "-qm", "change spec rule")
	head := git("rev-parse", "HEAD")
	st, err := merge.Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	st.Base = base
	st.Branches[0].OID = head
	if err := st.SaveTo(lane); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "both.md", "--spec", "docs/SPEC.md#Rules", "--max-bytes", "16384"}, &out, &errb); code != 0 {
		t.Fatalf("packet=%d stderr=%s", code, errb.String())
	}
	body, _ := os.ReadFile("both.md")
	got := string(body)
	for _, want := range []string{"> head:", "> 2. New wording", "> base:", "> 2. Old wording"} {
		if !strings.Contains(got, want) {
			t.Errorf("packet lacks %q:\n%s", want, got)
		}
	}
}

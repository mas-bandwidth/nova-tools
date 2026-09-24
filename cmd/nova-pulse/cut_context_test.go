package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contextFixtureRepo is a git repo with one numbered spec item, the test that guards it
// by its doc tag, and the file that test covers.
func contextFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	writeMainFile(t, repo, "go.mod", "module example.com/fix\n\ngo 1.22\n")
	writeMainFile(t, repo, "docs/SPEC-WIDGET.md", "# Widgets\n\n"+
		"1. `widget-refuses-empty`: an empty widget name is WIDGET REFUSED naming the flag,\n"+
		"   exit 2, and nothing is written.\n"+
		"2. `widget-other-rule`: unrelated.\n")
	writeMainFile(t, repo, "internal/widget/widget.go", "package widget\n\nfunc Make(name string) int {\n\tif name == \"\" {\n\t\treturn 2\n\t}\n\treturn 0\n}\n")
	writeMainFile(t, repo, "internal/widget/widget_test.go", "package widget\n\nimport \"testing\"\n\n"+
		"// widget-refuses-empty: the empty name is refused.\n"+
		"func TestWidgetRefusesEmpty(t *testing.T) {\n\tif Make(\"\") != 2 {\n\t\tt.Fatal(\"not refused\")\n\t}\n}\n")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

// #2498 S2 DONE-WHEN: `nova-pulse cut` on an issue naming a spec ID prints the paragraph
// and the guarding test into the card, read from the per-repo index built at the landed tip.
func TestCutOnAnIssueNamingASpecIDPrintsParagraphAndGuardingTest(t *testing.T) {
	repo := contextFixtureRepo(t)
	dir := t.TempDir()
	index := filepath.Join(dir, "index")
	var out, errb bytes.Buffer
	if code := run([]string{"index", "--repo", repo, "--out", index}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("index exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "INDEX OK ") || !strings.Contains(out.String(), "specs=2") || !strings.Contains(out.String(), "guarded=1") {
		t.Errorf("index line = %q", out.String())
	}

	body := filepath.Join(dir, "issue.md")
	if err := os.WriteFile(body, []byte("The cutter breaks widget-refuses-empty: an empty name is written.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	queue, pending := filepath.Join(dir, "queue"), filepath.Join(dir, "queue", "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code := run([]string{"cut", "--kind", "fix", "--repo", "mas-bandwidth/nova-tools", "--issue", "4242",
		"--title", "empty widget names are written", "--body-file", body,
		"--index", index, "--out", pending, "--queue", queue}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("cut exit = %d, stderr=%s", code, errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(pending, "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(raw)
	for _, want := range []string{
		"SPEC widget-refuses-empty (docs/SPEC-WIDGET.md:3): 1. `widget-refuses-empty`: an empty widget name is WIDGET REFUSED naming the flag, exit 2, and nothing is written.",
		"GUARDING TEST: internal/widget.TestWidgetRefusesEmpty (internal/widget/widget_test.go:6)",
		"FILES: internal/widget/widget.go, internal/widget/widget_test.go",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "widget-other-rule") {
		t.Errorf("card carries a spec ID the issue never named:\n%s", card)
	}
}

// A cut with --index pointing at a directory never built is refused, not silently bare.
func TestCutRefusesAnUnbuiltIndex(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"cut", "--kind", "fix", "--repo", "mas-bandwidth/nova-tools", "--issue", "1",
		"--title", "x", "--index", filepath.Join(dir, "nope"), "--out", filepath.Join(queue, "pending"), "--queue", queue},
		&out, &errb, time.Now().UTC())
	if code != 2 || !strings.Contains(errb.String(), "CUT REFUSED: --index") {
		t.Fatalf("exit = %d, stderr=%q; want 2 and a CUT REFUSED naming --index", code, errb.String())
	}
}

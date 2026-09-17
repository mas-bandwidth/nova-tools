package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The red tests of docs/SPEC-PULSE.md, "Cut, from a validated template" (#1142). Each drives
// the verb through run() with a fixture gh and a fixture git on PATH, exactly as the section
// names them: no network, no shell and no clock.

// validatedIssueTemplate carries the issue's title and body verbatim, on their own lines so
// a byte-for-byte read is visible.
const validatedIssueTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. TITLE[<title>]
STEP 3. BODY[<body>]
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

// validatedRowsTemplate fills its row and replay slots from the rows file.
const validatedRowsTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read <row> and <replay>; write notes.txt.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

// validatedBranchFromTemplate carries the PR's exact head branch.
const validatedBranchFromTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Open the PR on <branch>; write notes.txt.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

// writeValidatedTemplates lays down one template file for one source.
func writeValidatedTemplates(t *testing.T, dir, source, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, source+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runValidatedCut drives the verb through the command's own run(), so the flags and the
// one-line output are under test.
func runValidatedCut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"cut"}, args...), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// issueJSON renders the two fields `gh issue view --json title,body` answers.
func issueJSON(t *testing.T, title, body string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// gitAnswers puts a fixture git on PATH: the default answers exit 0 with no output (a branch
// absent, a path present), and each rule names one argv that answers otherwise.
func gitAnswers(t *testing.T, specs, log string, rules ...fakeRule) {
	t.Helper()
	fakeTool(t, specs, "git", fakeSpec{Log: log, Rules: rules, Default: fakeRule{Exit: 0}})
}

func mdFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	return names
}

// cut-issue-title-and-body-are-verbatim: a fixture gh answering an issue whose title and
// body carry tabs, backticks and a newline cuts a card carrying them byte for byte.
func TestCutIssueTitleAndBodyAreVerbatim(t *testing.T) {
	specs := fakePATH(t)
	title := "Wire\tthe `pulse`"
	body := "line one\nline two\twith a tab and `code`"
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, title, body)}})
	gitAnswers(t, specs, "")

	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", validatedIssueTemplate)
	out := filepath.Join(dir, "out")
	code, stdout, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("cut --issue exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=1 from=issue skipped=0 out="+out) {
		t.Fatalf("stdout=%q, want the one line of the section", stdout)
	}
	card, err := os.ReadFile(filepath.Join(out, "42.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card), title) {
		t.Errorf("card does not carry the title byte for byte: %q", card)
	}
	if !strings.Contains(string(card), body) {
		t.Errorf("card does not carry the body byte for byte: %q", card)
	}
}

// cut-rows-one-card-per-group: a fixture --rows file with two groups cuts two cards and no
// third.
func TestCutRowsOneCardPerGroup(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	body := "one\tdev\ta.go\tb.md\trowan/issue-1-one\n" +
		"two\tdev\tc.go\td.md\trowan/issue-2-two\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, stdout, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("cut --rows exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=2 from=rows skipped=0 out="+out) {
		t.Fatalf("stdout=%q, want cards=2 from=rows skipped=0", stdout)
	}
	names := mdFiles(t, out)
	if len(names) != 2 || names[0] != "one.md" || names[1] != "two.md" {
		t.Fatalf("cards = %v, want exactly one.md and two.md", names)
	}
}

// cut-branch-derived-and-absent: a fixture git ls-remote reporting rowan/issue-<n>-<slug>
// absent passes, reporting it present is CUT REFUSED check=branch, and the same branch is
// admitted when --branch-from names it.
func TestCutBranchDerivedAndAbsent(t *testing.T) {
	specs := fakePATH(t)
	title := "Fix the widget"
	branch := "rowan/issue-42-fix-the-widget"
	fakeTool(t, specs, "gh", fakeSpec{
		Rules:   []fakeRule{{Arg: 1, Equals: "issue", Stdout: issueJSON(t, title, "the body")}},
		Default: fakeRule{Stdout: issueJSON(t, title, "the body")},
	})
	dir := t.TempDir()
	issueTmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", validatedIssueTemplate)
	out := filepath.Join(dir, "out")

	// Absent: the default git answers exit 0 with no ref, so the derived branch is free.
	gitAnswers(t, specs, "")
	code, stdout, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", issueTmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("absent branch: exit = %d, want 0; stderr=%q", code, stderr)
	}
	card, err := os.ReadFile(filepath.Join(out, "42.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card), branch) {
		t.Fatalf("card does not carry the derived branch %q: %q", branch, card)
	}
	if !strings.Contains(stdout, "from=issue") {
		t.Fatalf("stdout=%q, want from=issue", stdout)
	}

	// Present on origin: CUT REFUSED check=branch, no card written for this run.
	os.RemoveAll(out)
	gitAnswers(t, specs, "", fakeRule{Arg: 3, Equals: branch, Stdout: "abc123\trefs/heads/" + branch})
	code, _, stderr = runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", issueTmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 2 {
		t.Fatalf("present branch: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=branch branch="+branch+" (pass --branch-from <pr>, or rename the issue)") {
		t.Fatalf("present branch: stderr=%q, want the section's refusal word for word", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("present branch: cards written despite the refusal: %v", names)
	}

	// Named by --branch-from: the same present branch is admitted, the check skipped.
	prTmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "branch-from", validatedBranchFromTemplate)
	fakeTool(t, specs, "gh", fakeSpec{
		Rules:   []fakeRule{{Arg: 1, Equals: "pr", Stdout: `{"headRefName":"` + branch + `","headRefOid":"abc123"}`}},
		Default: fakeRule{Stdout: `{"headRefName":"` + branch + `","headRefOid":"abc123"}`},
	})
	out2 := filepath.Join(dir, "out2")
	code, _, stderr = runValidatedCut(t, "--branch-from", "mas-bandwidth/nova-tools#42",
		"--templates", prTmpl, "--out", out2, "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("--branch-from: exit = %d, want 0; stderr=%q", code, stderr)
	}
	card2, err := os.ReadFile(filepath.Join(out2, "rowan-issue-42-fix-the-widget.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card2), branch) {
		t.Fatalf("--branch-from card does not carry %q: %q", branch, card2)
	}
}

// cut-names-exist-at-base: a fixture git cat-file passing one path and failing another
// refuses the row naming the missing path, exit 2, no card written.
func TestCutNamesExistAtBase(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "", fakeRule{Arg: 3, Equals: "dev:missing.md", Exit: 1})
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	body := "one\tdev\texisting.go\tok.md\trowan/issue-1-one\n" +
		"two\tdev\tmissing.md\tok2.md\trowan/issue-2-two\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 2 {
		t.Fatalf("missing path: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=base path=missing.md not at dev (fix the row, or add the file)") {
		t.Fatalf("missing path: stderr=%q, want the section's refusal word for word", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("missing path: cards written despite the refusal: %v", names)
	}
}

// cut-step-one-is-one-shell-line: a template whose STEP 1 carries && and a newline is
// refused by the in-process parser (no shell is run, no clock is read); a one-line STEP 1
// cuts.
func TestCutStepOneIsOneShellLine(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "Fix the widget", "the body")}})
	gitAnswers(t, specs, "")
	dir := t.TempDir()

	split := `RESULT <label> sha=<sha12>
You are a worker.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch &&
   git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP last. Write RESULT.md.`
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", split)
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 2 {
		t.Fatalf("split STEP 1: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=step1 (STEP 1 is not one shell line: fix the template)") {
		t.Fatalf("split STEP 1: stderr=%q, want the section's refusal word for word", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("split STEP 1: cards written despite the refusal: %v", names)
	}

	one := writeValidatedTemplates(t, filepath.Join(dir, "templates2"), "issue", validatedIssueTemplate)
	code, _, stderr = runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", one, "--out", filepath.Join(dir, "out2"), "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("one-line STEP 1: exit = %d, want 0; stderr=%q", code, stderr)
	}
}

// cut-separator-and-header-are-not-cards: a --rows file whose first row is a header and
// whose second is |---| cuts no card for either and still cuts the rows below.
func TestCutSeparatorAndHeaderAreNotCards(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	body := "label\tbase\trow\treplay\tbranch\n" +
		"|---|\n" +
		"one\tdev\ta.go\tb.md\trowan/issue-1-one\n" +
		"two\tdev\tc.go\td.md\trowan/issue-2-two\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, stdout, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (two rows cut nothing); stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=2 from=rows skipped=2 out="+out) {
		t.Fatalf("stdout=%q, want cards=2 from=rows skipped=2", stdout)
	}
	names := mdFiles(t, out)
	if len(names) != 2 || names[0] != "one.md" || names[1] != "two.md" {
		t.Fatalf("cards = %v, want exactly one.md and two.md and no header or separator card", names)
	}
}

// cut-result-is-one-line: a template whose <title> slot renders two lines is CUT REFUSED
// check=result; a one-line render cuts.
func TestCutResultIsOneLine(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	const titleOnLine1 = `RESULT <title> sha=<sha12>
You are a worker.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://github.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP last. Write RESULT.md.`
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", titleOnLine1)

	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "one\ntwo", "")}})
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 2 {
		t.Fatalf("two-line title: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=result (the RESULT line is more than one line: fix the template)") {
		t.Fatalf("two-line title: stderr=%q, want the section's refusal word for word", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("two-line title: cards written despite the refusal: %v", names)
	}

	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "one line", "")}})
	code, _, stderr = runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", filepath.Join(dir, "out2"), "--root", filepath.Join(dir, "root"))
	if code != 0 {
		t.Fatalf("one-line title: exit = %d, want 0; stderr=%q", code, stderr)
	}
}

// cut-refuses-the-first-failing-check: a bad branch and a missing path at once name
// check=branch and no path check runs -- the order is the contract.
func TestCutRefusesTheFirstFailingCheck(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	branch := "rowan/issue-1-one"
	gitAnswers(t, specs, log,
		fakeRule{Arg: 3, Equals: branch, Stdout: "abc123\trefs/heads/" + branch},
		fakeRule{Arg: 3, Equals: "dev:missing.md", Exit: 1})
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\tmissing.md\tok.md\t"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", tmpl, "--out", out, "--root", filepath.Join(dir, "root"))
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=branch branch="+branch+" (pass --branch-from <pr>, or rename the issue)") {
		t.Fatalf("stderr=%q, want check=branch first", stderr)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cat-file") {
		t.Fatalf("the path check ran after the branch check failed: %s", raw)
	}
}

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
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. TITLE[<title>]
STEP 3. BODY[<body>]
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

// validatedRowsTemplate fills its row and replay slots from the rows file.
const validatedRowsTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read <row> and <replay>; write notes.txt.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

// validatedBranchFromTemplate carries the PR's exact head branch.
const validatedBranchFromTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
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
		"--templates", tmpl, "--out", out, "--repo", dir)
	if code != 0 {
		t.Fatalf("cut --issue exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=1 from=issue skipped=0 out="+out) {
		t.Fatalf("stdout=%q, want the one line of the section", stdout)
	}
	card, err := os.ReadFile(filepath.Join(out, "card-42.md"))
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
		"--templates", tmpl, "--out", out, "--repo", dir)
	if code != 0 {
		t.Fatalf("cut --rows exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=2 from=rows skipped=0 out="+out) {
		t.Fatalf("stdout=%q, want cards=2 from=rows skipped=0", stdout)
	}
	names := mdFiles(t, out)
	if len(names) != 2 || names[0] != "card-one.md" || names[1] != "card-two.md" {
		t.Fatalf("cards = %v, want exactly card-one.md and card-two.md", names)
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
		"--templates", issueTmpl, "--out", out, "--repo", dir)
	if code != 0 {
		t.Fatalf("absent branch: exit = %d, want 0; stderr=%q", code, stderr)
	}
	card, err := os.ReadFile(filepath.Join(out, "card-42.md"))
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
	gitAnswers(t, specs, "", fakeRule{Arg: 5, Equals: branch, Stdout: "abc123\trefs/heads/" + branch})
	code, _, stderr = runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", issueTmpl, "--out", out, "--repo", dir)
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
		"--templates", prTmpl, "--out", out2, "--repo", dir)
	if code != 0 {
		t.Fatalf("--branch-from: exit = %d, want 0; stderr=%q", code, stderr)
	}
	card2, err := os.ReadFile(filepath.Join(out2, "card-rowan-issue-42-fix-the-widget.md"))
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
	gitAnswers(t, specs, "", fakeRule{Arg: 5, Equals: "dev:missing.md", Exit: 1})
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
		"--templates", tmpl, "--out", out, "--repo", dir)
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
   git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP last. Write RESULT.md.`
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", split)
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--repo", dir)
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
		"--templates", one, "--out", filepath.Join(dir, "out2"), "--repo", dir)
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
		"--templates", tmpl, "--out", out, "--repo", dir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (two rows cut nothing); stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=2 from=rows skipped=2 out="+out) {
		t.Fatalf("stdout=%q, want cards=2 from=rows skipped=2", stdout)
	}
	names := mdFiles(t, out)
	if len(names) != 2 || names[0] != "card-one.md" || names[1] != "card-two.md" {
		t.Fatalf("cards = %v, want exactly card-one.md and card-two.md and no header or separator card", names)
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
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP last. Write RESULT.md.`
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", titleOnLine1)

	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "one\ntwo", "")}})
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--repo", dir)
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
		"--templates", tmpl, "--out", filepath.Join(dir, "out2"), "--repo", dir)
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
		fakeRule{Arg: 5, Equals: branch, Stdout: "abc123\trefs/heads/" + branch},
		fakeRule{Arg: 5, Equals: "dev:missing.md", Exit: 1})
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\tmissing.md\tok.md\t"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--rows", rows,
		"--templates", tmpl, "--out", out, "--repo", dir)
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

// The dogfood edges of 2026-09-18, red first: a branch name with a space was CUT OK, every
// git call read the working directory, the cards were written under a name `fill` steps
// over, a second cut overwrote the first's cards.tsv, and the three templates the section
// describes shipped nowhere.

// TestCutRefusesABranchNameGitWouldRefuse: `rowan/has a space` is not a branch. The name is
// checked in process with git check-ref-format --branch semantics, before any ls-remote, so
// no child is started and no card is written.
func TestCutRefusesABranchNameGitWouldRefuse(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	gitAnswers(t, specs, log)
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\ta.go\tb.md\trowan/has a space\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl, "--out", out, "--repo", dir)
	if code != 2 {
		t.Fatalf("a branch with a space: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED check=branch") || !strings.Contains(stderr, "it holds a space") {
		t.Fatalf("stderr=%q, want check=branch naming the space", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("cards written despite the refusal: %v", names)
	}
	if raw, err := os.ReadFile(log); err == nil && strings.Contains(string(raw), "ls-remote") {
		t.Fatalf("ls-remote ran for a name git would not accept: %s", raw)
	}
}

// TestCutRunsEveryGitCallInTheNamedRepo: the rule is that a command never depends on the
// working directory, so every git call carries -C <repo>, and --repo is required for the
// validated forms.
func TestCutRunsEveryGitCallInTheNamedRepo(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	gitAnswers(t, specs, log)
	clone := filepath.Join(dir, "clone")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\ta.go\tb.md\trowan/issue-1-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl,
		"--out", filepath.Join(dir, "out"), "--repo", clone)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no git was run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		t.Fatal("no git was run")
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "git -C "+clone+" ") {
			t.Errorf("a git call did not run in --repo: %q", line)
		}
	}

	// Without --repo the verb refuses rather than reading the working directory.
	var out, errb bytes.Buffer
	code = run([]string{"cut", "--rows", rows, "--templates", tmpl, "--out", filepath.Join(dir, "out2")},
		&out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("cut without --repo exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--repo is required") {
		t.Fatalf("the refusal does not name --repo: %q", errb.String())
	}

	// A --repo that is not a directory is a refusal, not a git error three calls later.
	code, _, stderr = runValidatedCut(t, "--rows", rows, "--templates", tmpl,
		"--out", filepath.Join(dir, "out3"), "--repo", filepath.Join(dir, "nowhere"))
	if code != 2 {
		t.Fatalf("cut with a missing --repo exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "--repo") || !strings.Contains(stderr, "not a directory") {
		t.Fatalf("stderr=%q, want a refusal naming --repo", stderr)
	}
}

// TestCutWritesTheQueuesFilenameContract: a cut card is a card-<n>.md, which is what fill
// globs. `<label>.md` was a directory of cards the tick stepped over in silence.
func TestCutWritesTheQueuesFilenameContract(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "Fix the widget", "the body")}})
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", validatedIssueTemplate)
	out := filepath.Join(dir, "out")
	code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--repo", dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	names := mdFiles(t, out)
	if len(names) != 1 || names[0] != "card-42.md" {
		t.Fatalf("cards = %v, want exactly card-42.md", names)
	}
	globbed, err := filepath.Glob(filepath.Join(out, "card-*.md"))
	if err != nil || len(globbed) != 1 {
		t.Fatalf("the queue's own glob found %v, want the one card", globbed)
	}
}

// TestCutAppendsToCardsTSV: a second cut into the same --out adds its rows; the first cut's
// cards are still named by the table. A row already there is not written twice.
func TestCutAppendsToCardsTSV(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	out := filepath.Join(dir, "out")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := write("first.tsv", "one\tdev\ta.go\tb.md\trowan/issue-1-one\n")
	second := write("second.tsv", "two\tdev\tc.go\td.md\trowan/issue-2-two\n")
	for _, rows := range []string{first, second, second} {
		if code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl,
			"--out", out, "--repo", dir); code != 0 {
			t.Fatalf("cut %s exit = %d; stderr=%q", rows, code, stderr)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("cards.tsv = %q, want the two cards of the two cuts and no repeat", lines)
	}
	if !strings.HasPrefix(lines[0], "one\t") || !strings.HasPrefix(lines[1], "two\t") {
		t.Fatalf("cards.tsv = %q, want the first cut's row first", lines)
	}
}

// TestShippedValidatedTemplatesCut: the three examples the reference names are in the repo
// and every one of them passes the five checks and cuts a card.
func TestShippedValidatedTemplatesCut(t *testing.T) {
	shipped := filepath.Join("testdata", "templates")
	for _, name := range []string{"issue.md", "rows.md", "branch-from.md"} {
		if _, err := os.Stat(filepath.Join(shipped, name)); err != nil {
			t.Fatalf("the reference names %s and the repo does not ship it: %v", name, err)
		}
	}
	specs := fakePATH(t)
	// The shipped rows.md declares <repo> now: a card says which repository its branch
	// belongs to, and the answer is the clone's own origin remote.
	gitAnswers(t, specs, "", fakeRule{Arg: 3, Equals: "remote", Stdout: "git@example.com:mas-bandwidth/nova-tools.git"})
	fakeTool(t, specs, "gh", fakeSpec{
		Rules: []fakeRule{
			{Arg: 1, Equals: "issue", Stdout: issueJSON(t, "Fix the widget", "the body of the issue")},
			{Arg: 1, Equals: "pr", Stdout: `{"headRefName":"rowan/a-head","headRefOid":"abc123"}`},
		},
		Default: fakeRule{Stdout: issueJSON(t, "Fix the widget", "the body of the issue")},
	})
	dir := t.TempDir()
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\ta.go\tb.md\trowan/issue-1-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ flag, value, card string }{
		{"--issue", "mas-bandwidth/nova-tools#42", "card-42.md"},
		{"--rows", rows, "card-one.md"},
		{"--branch-from", "mas-bandwidth/nova-tools#42", "card-rowan-a-head.md"},
	} {
		out := filepath.Join(dir, "out"+strings.TrimPrefix(c.flag, "--"))
		code, stdout, stderr := runValidatedCut(t, c.flag, c.value, "--templates", shipped, "--out", out, "--repo", dir)
		if code != 0 {
			t.Fatalf("cut %s with the shipped template: exit = %d; stderr=%q", c.flag, code, stderr)
		}
		if !strings.Contains(stdout, "CUT OK cards=1") {
			t.Fatalf("cut %s: stdout=%q, want one card", c.flag, stdout)
		}
		if _, err := os.Stat(filepath.Join(out, c.card)); err != nil {
			t.Fatalf("cut %s wrote no %s: %v (%v)", c.flag, c.card, err, mdFiles(t, out))
		}
	}
}

// The second round of dogfood edges, from the schema loop on hulk (rows 9601-9603).

// TestCutLabelCarriesNoCardPrefix: the queue's card- prefix belongs to the filename. A label
// spelt card-9601 to make `fill` see it rendered CARD-card-9601 into the RESULT line and rode
// into a PR title.
func TestCutLabelCarriesNoCardPrefix(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("card-9601\tdev\ta.go\tb.md\trowan/issue-1-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl,
		"--out", out, "--repo", dir); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-9601.md"))
	if err != nil {
		t.Fatalf("the card is not card-9601.md: %v (%v)", err, mdFiles(t, out))
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	if strings.Contains(first, "card-9601") {
		t.Fatalf("the RESULT line doubles the prefix: %q", first)
	}
	if !strings.Contains(first, "RESULT 9601 ") {
		t.Fatalf("the RESULT line = %q, want the bare label", first)
	}
	table, err := os.ReadFile(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(table), "9601\t") {
		t.Fatalf("cards.tsv = %q, want the bare label", table)
	}
}

// TestCutRowsCarryALanePerRow: one --rows cut filled one lane, because the lane lived in the
// template and no column carried it. Field 6 is the lane and <lane> is its slot.
func TestCutRowsCarryALanePerRow(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	const laneTemplate = `RESULT <label> sha=<sha12>
LANE: <lane>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://example.com/x.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read <row> and <replay>; write notes.txt.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", laneTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	body := "label\tbase\trow\treplay\tbranch\tlane\n" +
		"9601\tdev\ta.go\tb.md\trowan/issue-1-one\tschema\n" +
		"9602\tdev\tc.go\td.md\trowan/issue-2-two\tschema-go\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	code, stdout, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl, "--out", out, "--repo", dir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the header row cuts nothing); stderr=%q stdout=%q", code, stderr, stdout)
	}
	for card, lane := range map[string]string{"card-9601.md": "LANE: schema", "card-9602.md": "LANE: schema-go"} {
		raw, err := os.ReadFile(filepath.Join(out, card))
		if err != nil {
			t.Fatalf("%s: %v (%v)", card, err, mdFiles(t, out))
		}
		if !strings.Contains(string(raw), lane+"\n") {
			t.Errorf("%s does not carry %q: %q", card, lane, raw)
		}
	}
}

// TestCutRowsCarryATemplatePerRow: one rows.tsv shared one template, so N different tasks
// wanted N cuts and N template directories. Field 7 names the template for that row.
func TestCutRowsCarryATemplatePerRow(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tdir := filepath.Join(dir, "templates")
	writeValidatedTemplates(t, tdir, "rows", validatedRowsTemplate)
	writeValidatedTemplates(t, tdir, "audit", strings.Replace(validatedRowsTemplate,
		"STEP 2. Read <row> and <replay>; write notes.txt.",
		"STEP 2. AUDIT <row> against <replay>.", 1))
	rows := filepath.Join(dir, "rows.tsv")
	body := "one\tdev\ta.go\tb.md\trowan/issue-1-one\t\t\n" +
		"two\tdev\tc.go\td.md\trowan/issue-2-two\t\taudit\n"
	if err := os.WriteFile(rows, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tdir,
		"--out", out, "--repo", dir); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr)
	}
	first, err := os.ReadFile(filepath.Join(out, "card-one.md"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(out, "card-two.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "AUDIT") {
		t.Errorf("the row naming no template did not take the source's own: %q", first)
	}
	if !strings.Contains(string(second), "AUDIT") {
		t.Errorf("the row naming audit was not cut from audit.md: %q", second)
	}

	// A template a row names and the directory does not hold is one refusal.
	bad := filepath.Join(dir, "bad.tsv")
	if err := os.WriteFile(bad, []byte("three\tdev\te.go\tf.md\trowan/issue-3\t\tghost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runValidatedCut(t, "--rows", bad, "--templates", tdir,
		"--out", filepath.Join(dir, "out2"), "--repo", dir)
	if code != 2 || !strings.Contains(stderr, "--templates wants ghost.md") {
		t.Fatalf("a missing per-row template: exit = %d, stderr=%q", code, stderr)
	}
}

// TestCutBaseIsAFlag: `dev` was hardcoded, and a repo without a dev branch had to spell the
// base in every row. --base is the default for every source that names none.
func TestCutBaseIsAFlag(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, "Fix the widget", "the body")}})
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	gitAnswers(t, specs, log)
	// The shipped issue.md is the one that names <base>, which is the slot under test.
	tmpl := filepath.Join("testdata", "templates")
	out := filepath.Join(dir, "out")
	if code, _, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#42",
		"--templates", tmpl, "--out", out, "--repo", dir, "--base", "main"); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-42.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "on main") {
		t.Fatalf("the card was not cut onto --base: %q", raw)
	}
	if strings.Contains(string(raw), "on dev") {
		t.Fatalf("the card carries the hardcoded dev: %q", raw)
	}
}

// TestCutCardsTableGoesWhereItIsNamed: --out is a queue directory in real use, and cards.tsv
// landed in it as a file nothing in the queue reads.
func TestCutCardsTableGoesWhereItIsNamed(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "rows", validatedRowsTemplate)
	rows := filepath.Join(dir, "rows.tsv")
	if err := os.WriteFile(rows, []byte("one\tdev\ta.go\tb.md\trowan/issue-1-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "ready")
	table := filepath.Join(dir, "state", "cards.tsv")
	if err := os.MkdirAll(filepath.Dir(table), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runValidatedCut(t, "--rows", rows, "--templates", tmpl,
		"--out", out, "--repo", dir, "--cards", table); code != 0 {
		t.Fatalf("exit = %d; stderr=%q", code, stderr)
	}
	if _, err := os.Stat(table); err != nil {
		t.Fatalf("the table is not where --cards named: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "cards.tsv")); err == nil {
		t.Fatal("a cards.tsv was still dropped into the queue directory")
	}
}

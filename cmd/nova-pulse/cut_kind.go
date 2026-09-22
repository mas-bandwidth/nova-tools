package main

// `cut --kind` — the typed cutter, and the only place a card number comes from. Without
// --kind, `cut` is the pool-driven cutter of internal/pulse/cut.go and nothing here runs.
//
// There is no --number flag, and passing one is a refusal that names why: the number comes
// only from the queue's state file, under the queue's lock (issue #828, class B).

import (
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// hasKindFlag says whether this invocation is the typed cutter's.
func hasKindFlag(args []string) bool {
	for _, a := range args {
		if a == "--kind" || a == "-kind" || strings.HasPrefix(a, "--kind=") || strings.HasPrefix(a, "-kind=") {
			return true
		}
	}
	return false
}

func cmdCutKind(args []string, stdout, stderr io.Writer) int {
	f := newFlags("cut")
	for _, a := range args {
		if a == "--number" || a == "-number" || strings.HasPrefix(a, "--number=") || strings.HasPrefix(a, "-number=") {
			return refuse(stderr, " cut", "--number is not a flag; a card number comes only from the queue state file's next_card, under the queue's lock, so two cutters never share one")
		}
	}
	kind := f.fs.String("kind", "", "")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	head := f.fs.String("head", "", "")
	issue := f.fs.Int("issue", 0, "")
	title := f.fs.String("title", "", "")
	branch := f.fs.String("branch", "", "")
	base := f.fs.String("base", "", "")
	baseSHA := f.fs.String("base-sha", "", "")
	bodyFile := f.fs.String("body-file", "", "")
	diffFile := f.fs.String("diff-file", "", "")
	diff := f.fs.String("diff", "", "")
	dir := f.fs.String("dir", "", "")
	clone := f.fs.String("clone", "", "")
	holdFile := f.fs.String("hold-file", "", "")
	paths := f.fs.String("paths", "", "")
	test := f.fs.String("test", "", "")
	prior := f.fs.String("prior", "", "")
	names := f.fs.String("names", "", "")
	specLines := f.fs.String("spec-lines", "", "")
	out := f.fs.String("out", "", "")
	queue := f.fs.String("queue", "", "")

	// v2 flags
	v2 := f.fs.Bool("v2", false, "")
	testCmd := f.fs.String("test-cmd", "", "")
	priorDiff := f.fs.String("prior-diff", "", "")
	reviewerLine := f.fs.String("reviewer-line", "", "")
	failingOutput := f.fs.String("failing-output", "", "")
	preflightCmd := f.fs.String("preflight-cmd", "", "")
	location := f.fs.String("location", "", "")
	testPkg := f.fs.String("test-pkg", "", "")
	testFunc := f.fs.String("test-func", "", "")
	attempt := f.fs.Int("attempt", 0, "")
	symbol := f.fs.String("symbol", "", "")
	redWhen := f.fs.String("red-when", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	effectiveDiffFile := *diffFile
	if effectiveDiffFile == "" {
		effectiveDiffFile = *diff
	}
	effectiveDir := *dir
	if effectiveDir == "" {
		effectiveDir = *clone
	}
	return pulse.CutKind(pulse.CutKindInput{
		Kind: *kind, Repo: *repo, PR: *pr, Head: *head, Issue: *issue, Title: *title,
		Branch: *branch, Base: *base, BaseSHA: *baseSHA,
		BodyFile: *bodyFile, DiffFile: effectiveDiffFile, Dir: effectiveDir, HoldFile: *holdFile,
		Paths: *paths, TestName: *test,
		Prior: *prior, Names: *names, SpecLines: *specLines,
		Out: *out, Queue: *queue, Stdout: stdout, Stderr: stderr,
		V2: *v2, Location: *location, TestPackage: *testPkg, TestFunction: *testFunc,
		TestCommand: *testCmd, ReviewerLine: *reviewerLine,
		PriorDiff: *priorDiff, FailingOutput: *failingOutput, PreflightCmd: *preflightCmd,
		Attempt: *attempt, Symbol: *symbol, RedWhen: *redWhen,
	})
}

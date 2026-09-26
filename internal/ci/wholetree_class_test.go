package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE CLASS RULE: NO DOC AND NO CARD TELLS ANYONE TO RUN `go test ./...`
// (nova-tools#4336).
//
// Glenn 2026-09-26 10:08 AM ET: "CPU is for real work"; children test the
// packages they touched and nothing else. The day's children each invented a
// way to test what they changed, and several ran the whole tree: a sharding
// script under `timeout 95`, a six-pass loop hunting t.Parallel violations,
// hand timing scripts. A doc or a card that spells `go test ./...` teaches the
// next child to do it again, on a bench shared with the work it tests. The
// door is `nova-ci local`: exactly the unit tier CI runs for the diff (the
// packages select-packages.sh picks, `make test` at -p 2 with the budgets).
//
// The rule reads, as text: every Markdown file in the tree outside testdata
// (the docs, AGENTS.md, TESTING.md, READMEs), every card template
// (CardTemplateDirs), every brief source the no-gh rule reads (briefSources),
// and the Go files that write a harness card's standard lines. No allowlist:
// the offenders in the tree when it landed were rewritten.

// wholeTreeCardSources are the Go files whose string constants are a card's
// text: the harness card's standard lines and the copy cards built from them.
var wholeTreeCardSources = []string{
	"internal/nsprint/taskcard/complete.go",
	"internal/nsprint/card/copy.go",
}

// wholeTreeGoTestRe is `go test`, any flags (a flag may take one value that is
// not a path), then `./...` as the package pattern. `go test ./cmd/nova-ci`,
// `go vet ./...` and prose that says "go test" near a `./...` are not it.
var wholeTreeGoTestRe = regexp.MustCompile(`\bgo test(?:\s+-\S+(?:\s+[^\s\-./` + "`" + `|][^\s` + "`" + `|]*)?)*\s+\./\.\.\.(?:[^\w/]|$)`)

// wholeTreeRemedy is the one thing to do instead.
const wholeTreeRemedy = "run `nova-ci local` (the unit tier CI runs for this diff: select-packages.sh, make test at -p 2, the budgets) or name the packages you touched: nice -n 15 go test -p 2 -count=1 ./cmd/<tool>"

// wholeTreeViolations returns "line: text" for every whole-tree go test in src.
func wholeTreeViolations(src []byte) []string {
	var out []string
	for i, line := range strings.Split(string(src), "\n") {
		if wholeTreeGoTestRe.MatchString(line) {
			out = append(out, strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	return out
}

// wholeTreeSources lists the repo-relative files the rule reads, sorted and
// without repeats.
func wholeTreeSources(t *testing.T) []string {
	t.Helper()
	tree := repoTree(t)
	seen := map[string]bool{}
	for _, f := range tree.Files {
		if strings.HasSuffix(f.Rel, ".md") && !f.HasDirNamed("testdata") {
			seen[f.Rel] = true
		}
		for _, dir := range CardTemplateDirs {
			if f.InDir(dir) && (strings.HasSuffix(f.Rel, ".md") || strings.HasSuffix(f.Rel, ".card")) {
				seen[f.Rel] = true
			}
		}
	}
	for _, glob := range briefSources {
		matches, err := filepath.Glob(filepath.Join(tree.Root, filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("brief source %s matches no file; an empty set is not a pass, fix briefSources", glob)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(tree.Root, m)
			if err != nil {
				t.Fatal(err)
			}
			seen[filepath.ToSlash(rel)] = true
		}
	}
	for _, rel := range wholeTreeCardSources {
		if tree.ByRel(rel) == nil {
			t.Fatalf("card source %s is not in the tree; a file that moves must move here too", rel)
		}
		seen[rel] = true
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// TestNoWholeTreeGoTestInDocs refuses `go test ./...` in every doc and card
// the tree ships, one line per offender with its file and line.
func TestNoWholeTreeGoTestInDocs(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	files := wholeTreeSources(t)
	var bad []string
	docs := 0
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(rel, ".md") {
			docs++
		}
		for _, v := range wholeTreeViolations(src) {
			bad = append(bad, rel+":"+v)
		}
	}
	if docs < 50 {
		t.Fatalf("read %d Markdown files; the tree ships more than a hundred, so the rule is reading the wrong tree", docs)
	}
	if len(bad) > 0 {
		t.Fatalf("%d line(s) tell a reader to test the whole tree (nova-tools#4336; CPU is for real work); %s:\n  %s",
			len(bad), wholeTreeRemedy, strings.Join(bad, "\n  "))
	}
}

// TestWholeTreeRuleSeesEachSpelling is the rule's control: each whole-tree
// spelling is red, and the per-package spellings and the door are green.
func TestWholeTreeRuleSeesEachSpelling(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"go test ./...",
		"run `go test ./...` before you push",
		"go build ./... && go vet ./... && go test -race ./...",
		"go test -tags perf -p 1 -parallel 1 ./...",
		"GOFLAGS=-json go test -count=1 ./... | tee out.json",
		"| go test ./... | pass | 3 |",
	} {
		if v := wholeTreeViolations([]byte("fine\n" + bad + "\n")); len(v) != 1 || !strings.HasPrefix(v[0], "2: ") {
			t.Errorf("%q: violations %q, want one at line 2", bad, v)
		}
	}
	for _, good := range []string{
		"nova-ci local",
		"nova-ci local --base origin/dev --functional",
		"go test ./cmd/nova-ci",
		"nice -n 15 go test -p 2 -count=1 ./internal/ci ./cmd/nova-ci/...",
		"go vet ./...",
		"go test on the touched packages, never ./... on a shared bench",
		"go test ./internal/...",
	} {
		if v := wholeTreeViolations([]byte(good + "\n")); len(v) != 0 {
			t.Errorf("%q flagged: %q", good, v)
		}
	}
}

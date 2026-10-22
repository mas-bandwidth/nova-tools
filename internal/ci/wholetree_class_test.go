package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: NO DOC AND NO CARD TELLS ANYONE TO RUN `go test ./...`, OR
// OVER ./cmd/..., ./internal/... OR ./tools/... (nova-tools#4336).
//
// Glenn 2026-09-26 10:08 AM ET: "CPU is for real work"; children test the
// packages they touched and nothing else. The day's children each invented a
// way to test what they changed, and several ran the whole tree: a sharding
// script under `timeout 95`, a six-pass loop hunting t.Parallel violations,
// hand timing scripts. A doc or a card that spells `go test ./...` teaches the
// next child to do it again, on a bench shared with the work it tests. The
// door is `nova-ci local`: exactly the unit tier CI runs for the diff (the
// packages CI's selection picks, `make test` at -p 2 with the budgets).
//
// The rule reads, as text: every Markdown file in the tree outside testdata
// (the docs, AGENTS.md, TESTING.md, READMEs), every card template
// (CardTemplateDirs) and every brief source the no-gh rule reads
// (briefSources). No allowlist:
// the offenders in the tree when it landed were rewritten. One narrowing: a report
// (reportPathRe: a file under a report directory's dated, release or snapshot
// directory, named for a tool, a rater or a run) is not read. A rating, a dogfood run, a stranger run
// or an acceptance record quotes the commands its author ran, as they were run;
// it is a record of what happened, not an instruction to anyone, and a report
// that could not quote its own `go test ./...` would have to misreport it
// (rerate-emma-ci-b, refused at the tree gate 2026-10-06).

// wholeTreeGoTestRe is `go test`, any flags (a flag may take one value that is
// not a path), then `./...` or one of the three trees that are most of it
// (`./cmd/...`, `./internal/...`, `./tools/...`) as the package pattern.
// `go test ./cmd/nova-ci`, one tool's `./cmd/nova-ci/...`, `go vet ./...` and
// prose that says "go test" near a `./...` are not it.
var wholeTreeGoTestRe = regexp.MustCompile(`\bgo test(?:\s+-\S+(?:\s+[^\s\-./` + "`" + `|][^\s` + "`" + `|]*)?)*\s+\./(?:(?:cmd|internal|tools)/)?\.\.\.(?:[^\w/]|$)`)

// wholeTreeRemedy is the one thing to do instead.
const wholeTreeRemedy = "run `nova-ci local` (the unit tier CI runs for this diff: its package selection, make test at -p 2, the budgets) or name the packages you touched: nice -n 15 go test -p 2 -count=1 ./cmd/<tool>"

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

// reportPathRe is a report's path, a record of a run that quotes the commands run, as
// the report directories lay them out (internal/docs/catalog.go names each directory):
// docs/ratings/<release>/<tool>-<read|use>.md or <tool>-<rater>.md, and
// docs/ratings/snapshots/<commit>/<tool>.md; docs/dogfood/<date>/<friend>/<tool>.md;
// docs/acceptance/v<release>/<requirement>.md; docs/stranger/<run>.md. A name is lower
// case, so a README.md or an AGENTS.md beside the reports is a how-to the rule reads.
var reportPathRe = regexp.MustCompile(`^docs/(?:ratings/(?:\d+\.\d+\.\d+|snapshots/[0-9a-f]{7,40})|dogfood/\d{4}-\d{2}-\d{2}/[a-z0-9-]+|acceptance/v\d+\.\d+\.\d+|stranger)/[a-z0-9][a-z0-9.-]*\.md$`)

// wholeTreeDoc says the rule reads the Markdown file rel: every one but a report (outside
// testdata, which the caller leaves out).
func wholeTreeDoc(rel string) bool {
	return strings.HasSuffix(rel, ".md") && !reportPathRe.MatchString(rel)
}

// wholeTreeSources lists the repo-relative files the rule reads, sorted and
// without repeats.
func wholeTreeSources(t *testing.T) []string {
	t.Helper()
	tree := repoTree(t)
	seen := map[string]bool{}
	for _, f := range tree.Files {
		if wholeTreeDoc(f.Rel) && !f.HasDirNamed("testdata") {
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
		require.NoError(t, err)
		require.NotEmptyf(t, matches, "brief source %s matches no file; an empty set is not a pass, fix briefSources", glob)
		for _, m := range matches {
			rel, err := filepath.Rel(tree.Root, m)
			require.NoError(t, err)
			seen[filepath.ToSlash(rel)] = true
		}
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
		require.NoError(t, err)
		if strings.HasSuffix(rel, ".md") {
			docs++
		}
		for _, v := range wholeTreeViolations(src) {
			bad = append(bad, rel+":"+v)
		}
	}
	require.GreaterOrEqualf(t, docs, 50, "read %d Markdown files; the tree ships more than a hundred, so the rule is reading the wrong tree", docs)
	require.Emptyf(t, bad, "%d line(s) tell a reader to test the whole tree (nova-tools#4336; CPU is for real work); %s:\n  %s",
		len(bad), wholeTreeRemedy, strings.Join(bad, "\n  "))
}

// TestWholeTreeRuleSeesEachSpelling is the rule's control: each whole-tree
// spelling is red, and the per-package spellings and the door are green.
func TestWholeTreeRuleSeesEachSpelling(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"go test ./...",
		"go test -v ./...",
		"go test -v -count=1 ./...",
		"run `go test ./...` before you push",
		"go build ./... && go vet ./... && go test -race ./...",
		"go test -tags perf -p 1 -parallel 1 ./...",
		"GOFLAGS=-json go test -count=1 ./... | tee out.json",
		"| go test ./... | pass | 3 |",
		"| `go test ./cmd/...` | `make build` |",
		"go test ./internal/...",
		"go test -count=1 ./tools/...",
	} {
		v := wholeTreeViolations([]byte("fine\n" + bad + "\n"))
		assert.Truef(t, len(v) == 1 && strings.HasPrefix(v[0], "2: "), "%q: violations %q, want one at line 2", bad, v)
	}
	for _, good := range []string{
		"nova-ci local",
		"nova-ci local --base origin/dev --functional",
		"go test ./cmd/nova-ci",
		"nice -n 15 go test -p 2 -count=1 ./internal/ci ./cmd/nova-ci/...",
		"go vet ./...",
		"go test on the touched packages, never ./... on a shared bench",
		"go test ./cmd/nova-ci/...",
	} {
		v := wholeTreeViolations([]byte(good + "\n"))
		assert.Emptyf(t, v, "%q flagged: %q", good, v)
	}
}

// TestReportsMayQuoteWholeTreeCommands holds the rule's one narrowing: a report
// (reportPathRe) is a record of what a friend ran, quoted as it was run, not an instruction,
// so it may say `go test ./...`; every other doc is read (rerate-emma-ci-b, refused at
// the tree gate 2026-10-06 18:14 ET for quoting its own run in docs/ratings/).
func TestReportsMayQuoteWholeTreeCommands(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{
		"docs/ratings/1.2.0/nova-ci-emma.md",
		"docs/dogfood/2026-10-06/antigravity/self-talk.md",
		"docs/stranger/three-card-sprint.md",
		"docs/acceptance/v1.0.0/landing.md",
		"docs/ratings/1.1.0/ci-read.md",
		"docs/ratings/snapshots/0c5803c2de40/ci-use.md",
	} {
		assert.Falsef(t, wholeTreeDoc(rel), "%s is a report: a record, not an instruction", rel)
	}
	for _, rel := range []string{"AGENTS.md", "docs/AGENTS.md", "docs/SPEC-CI.md", "docs/TESTING.md", "cmd/nova-ci/README.md", "docs/ratings.md", "docs/ratingsx/a.md",
		// a how-to beside the reports is read: only report-shaped paths are not
		"docs/ratings/README.md", "docs/ratings/AGENTS.md", "docs/dogfood/README.md", "docs/dogfood/2026-10-06/README.md",
		"docs/acceptance/README.md", "docs/stranger/AGENTS.md", "docs/ratings/1.2.0/README.md", "docs/ratings/howto.md"} {
		assert.Truef(t, wholeTreeDoc(rel), "%s is a doc the rule reads", rel)
	}
}

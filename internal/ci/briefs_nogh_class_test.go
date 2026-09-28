package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THE CLASS RULE: A BRIEF NEVER SAYS gh (nova-tools#3600, umbrella #3594).
//
// Glenn 2026-09-24: GitHub is a git remote only. Friends and swarm children interact
// with nova-sprint and nova-work; one PR had cost ~60 REST calls and the rowan token's
// 5,000/h was spent twice in a day, freezing every merge for an hour. A brief that says
// `gh api`, `gh pr`, GraphQL, or a bare `git clone https://github.com/...` teaches the
// next child to spend the budget again, so the class is refused at the source: every
// brief and card template this repository ships is scanned, and any of those spellings
// is a red run naming the file and line.
//
// The set of brief sources is the list below and an empty set is not a pass: a template
// directory that moves must move here too.

// briefSources are the files a child reads as its brief: the nova-sprint brief templates
// (build, fix, read), the read brief template of `nova-sprint read brief`, the pulse and
// task card templates `nova-swarm template` prints, and the card fixtures the swarm's
// own tests lint. Each glob must match at least one file.
var briefSources = []string{
	"internal/nsprint/brief/tmpl/*.tmpl",
	"internal/nsprint/read/tmpl/*.tmpl",
	"internal/swarm/templates.go",
	"cmd/nova-swarm/testdata/cards/*.md",
}

var (
	// briefGhRe is `gh ` as a command: at a line start or after a non-word, non-path
	// character, so "through " and "high " and a URL path "/gh " do not match.
	briefGhRe = regexp.MustCompile(`(?:^|[^\w./-])gh `)
	// briefGraphQLRe is the GraphQL endpoint in any spelling.
	briefGraphQLRe = regexp.MustCompile(`(?i)graphql`)
	// briefCloneRe is a git clone of any remote (https, ssh, or a git@ alias); such a
	// line must carry the bench mirror as --reference (the rowan-tools mirror-refresh
	// loop keeps it) or it is a full fetch from the forge per child.
	briefCloneRe = regexp.MustCompile(`git clone[^\n]*(https?://|ssh://|git@)`)
)

// briefViolations scans one brief's bytes and returns the offending lines with their
// numbers and the rule each one broke.
func briefViolations(src []byte) []string {
	var out []string
	for i, line := range strings.Split(string(src), "\n") {
		n := i + 1
		switch {
		case briefGhRe.MatchString(line):
			out = append(out, fmt.Sprintf("%d: `gh ` in a brief: %s", n, strings.TrimSpace(line)))
		case briefGraphQLRe.MatchString(line):
			out = append(out, fmt.Sprintf("%d: GraphQL in a brief: %s", n, strings.TrimSpace(line)))
		case briefCloneRe.MatchString(line) && !strings.Contains(line, "--reference"):
			out = append(out, fmt.Sprintf("%d: a remote clone without the bench mirror as --reference: %s", n, strings.TrimSpace(line)))
		}
	}
	return out
}

// TestNoGhInAnyBrief walks briefSources and refuses every `gh `, GraphQL and bare
// remote clone. The remedy is the nova-sprint verb (read brief, read post, card) or a
// clone with `--reference ~/nova-bench/mirror/<repo>.git`.
func TestNoGhInAnyBrief(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var files []string
	for _, glob := range briefSources {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("brief source %s matches no file; an empty set is not a pass, fix the list", glob)
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	var violations []string
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, v := range briefViolations(src) {
			violations = append(violations, filepath.ToSlash(rel)+":"+v)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("%d brief line(s) tell a child to call GitHub (#3600: GitHub is a git remote only; use `nova-sprint read brief`, `nova-sprint read post`, `nova-sprint card`, or a clone with --reference ~/nova-bench/mirror/<repo>.git):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
	if len(files) < 8 {
		t.Fatalf("only %d brief files scanned; the three brief templates, the read template, the swarm templates and the card fixtures are more than that", len(files))
	}
}

// TestBriefRuleCatchesEachSpelling is the rule's own control: a brief with each
// spelling is red, and one with the verbs and a mirror-referenced clone is green.
func TestBriefRuleCatchesEachSpelling(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"fetch the PR with gh api repos/o/r/pulls/1",
		"gh pr view 1",
		"post through the graphql endpoint",
		"STEP 1. git clone -q https://example.invalid/o/r.git .",
		"git clone git@example-alias:o/r repo",
	} {
		if v := briefViolations([]byte("ok line\n" + bad + "\n")); len(v) != 1 || !strings.HasPrefix(v[0], "2:") {
			t.Fatalf("%q: violations %q, want one at line 2", bad, v)
		}
	}
	good := "read it with nova-sprint read brief --repo r --n 1 --out d\n" +
		"post it with nova-sprint read post --repo r --n 1 --line \"SCORE ...\"\n" +
		"git clone -q --depth 50 --single-branch -b dev --reference ~/nova-bench/mirror/r.git https://example.invalid/o/r.git repo\n" +
		"walk through the high ground\n"
	if v := briefViolations([]byte(good)); len(v) != 0 {
		t.Fatalf("clean brief flagged: %q", v)
	}
}

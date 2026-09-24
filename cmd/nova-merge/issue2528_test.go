package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// TestIssue2528 reproduces nova-tools#2528: "Stacked rebase as a verb: re-land a set of
// same-base PRs in ledger order against the landing branch, both-sides appends resolved
// mechanically". Three PRs each append a line at the same anchor of the same file, all
// branching from the same base. Individually each conflicts with its file-mates; stacked
// in ledger order with mechanical both-sides-append resolution, all three land.
func TestIssue2528(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.urlFor = func(string) string { return fileURL(l.remote) }

	// Base: a dev branch with a table file.
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("table.go", "package main\n\nvar items = []string{\n\t\"base\",\n}\n")
	base := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	// Three same-base PRs, each appending a different entry at the same anchor.
	for i, item := range []string{"first", "second", "third"} {
		prNum := i + 1
		l.git(l.work, "checkout", "-q", "-B", "pr"+strconv.Itoa(prNum), base)
		l.write("table.go", "package main\n\nvar items = []string{\n\t\"base\",\n\t\""+item+"\",\n}\n")
		sha := l.commit("pr " + strconv.Itoa(prNum))
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(prNum)+"/head")
		l.host.PRs[prNum] = merge.PR{Number: prNum, HeadOID: sha, Base: "dev"}
	}
	l.git(l.work, "checkout", "-q", "main")

	root := filepath.Join(l.dir, "stack")
	exit, stdout, stderr := l.run("stack", "--repo", "o/n", "--base", "dev", "--prs", "1,2,3", "--root", root, "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("stack: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "STACK OK")
	contains(t, stdout, "members=1,2,3")
	absent(t, stdout, "members=none")

	// Verify the landed tree holds every entry.
	clone := filepath.Join(root, "stack-work", "repo")
	raw, err := os.ReadFile(filepath.Join(clone, "table.go"))
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	s := string(raw)
	for _, item := range []string{"\"base\"", "\"first\"", "\"second\"", "\"third\""} {
		if !strings.Contains(s, item) {
			t.Errorf("result missing %s:\n%s", item, s)
		}
	}
}

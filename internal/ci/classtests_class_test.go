package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// deletedTestsLogPath is where a change declares a test file or a list it
// means to delete: `<path> <why>` rows, added in the same change as the
// deletion. The rule reads the declaration out of git, never out of the
// tree: only a row this commit ADDS declares a deletion this commit makes.
const deletedTestsLogPath = "internal/ci/testdata/deleted-tests.txt"

// guardedByMergeRule reports whether rel is a file no merge may delete
// undeclared: every _test.go, and every list under internal/ci/testdata.
func guardedByMergeRule(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	return path.Dir(rel) == "internal/ci/testdata" && strings.HasSuffix(rel, ".txt")
}

// mergeDeletions is what one commit removed from its first parent's tree,
// and what it declared. For a pull request's merge ref and a merge-queue
// commit the first parent is the base branch's tip, so Deleted is exactly
// what merging the change takes away from dev; for a squash on dev it is
// the squash's effect; for a plain commit, that commit's. A promotion of dev
// to main is the one change the comparison does not fit: its first parent is
// main's tip, so Deleted is every deletion dev accumulated since the last
// promotion, each already declared in the change that made it on dev; the
// class test recognises that pull request (promotionSkip) and does not run
// the comparison on it. Once the promotion has landed, a main run (mainRun:
// push, workflow_dispatch or schedule on refs/heads/main, or a local run on
// main) at a merge commit keeps the first-parent comparison and excuses what
// dev's own history deleted (excuseDevDeletions), and checks the merge's own
// change against its second parent (Beyond).
type mergeDeletions struct {
	Head, Parent, Subject string
	// Deleted is every path gone from the tree, renames excluded (-M).
	Deleted []string
	// Declared is path -> why for the rows this commit added to the log.
	Declared map[string]string
	// Beyond, on a main run at a merge commit, is the second-parent
	// comparison's own findings: a guarded path dev's tip has and HEAD lacks
	// with no row added beyond dev, and a row added beyond dev naming no
	// deletion.
	Beyond []string
	// Remedy, when set, ends every finding: a main run that could not read
	// dev's history says so and names the fetch.
	Remedy string
}

// devHistoryFetch completes the second parent's ancestry in a depth-2
// checkout: commits and trees only (blob:none), the whole history
// (--unshallow). The main-run steps of ci.yml and certification.yml run it.
const devHistoryFetch = "git fetch --no-tags --filter=blob:none --unshallow origin +dev:refs/remotes/origin/dev"

// readMergeDeletions compares HEAD's tree with its first parent's, through
// the package's gitOut (issue2218.go: a bounded git in root).
func readMergeDeletions(root string) (*mergeDeletions, error) {
	m, _, err := readMergeDeletionsFor(root, "", "")
	return m, err
}

// readMergeDeletionsFor is readMergeDeletions under GitHub's event and ref
// (GITHUB_EVENT_NAME, GITHUB_REF; both empty on a local run, where the branch
// is read from git). Everywhere but a main run it is the first-parent
// comparison and no note. On a main run at a merge commit it is the same
// comparison, then: a deletion is excused when the second parent's ancestry
// deleted the path and the second parent's tree lacks it, a row is excused
// when that ancestry deleted its path, and the second parent's own
// comparison is added (Beyond). The note says what was excused and why. When
// the second parent's ancestry is cut by a shallow graft the ordinary
// comparison stands and every finding names the fetch (fail closed).
func readMergeDeletionsFor(root, event, ref string) (*mergeDeletions, string, error) {
	branch := ""
	if event == "" && ref == "" {
		var err error
		if branch, err = currentBranch(root); err != nil {
			return nil, "", err
		}
	}
	return readCommitDeletions(root, "HEAD", event, ref, branch)
}

// readCommitDeletions is readMergeDeletionsFor over any commit, with the
// checked-out branch given rather than read (the witnesses read many commits
// of one repository without checking them out).
func readCommitDeletions(root, commit, event, ref, branch string) (*mergeDeletions, string, error) {
	parents, err := commitParents(root, commit)
	if err != nil {
		return nil, "", err
	}
	if err := parentInCheckout(root, parents[0]); err != nil {
		return nil, "", err
	}
	shown, err := gitOut(root, "show", "-s", "--format=%h%n%s", commit)
	if err != nil {
		return nil, "", err
	}
	head, subject, _ := strings.Cut(strings.TrimSpace(shown), "\n")
	m, err := readDeletionsAgainst(root, parents[0], commit, head, subject)
	if err != nil {
		return nil, "", err
	}
	if !mainRun(event, ref, branch) {
		return m, "", nil
	}
	where := event + " on " + ref
	if event == "" {
		where = "a local run on " + branch
	}
	if len(parents) == 1 {
		return m, "NOTE: " + where + " is a main run at a one-parent commit: HEAD is compared with its parent as everywhere", nil
	}
	second := parents[1]
	if err := parentInCheckout(root, second); err != nil {
		m.Remedy = fmt.Sprintf(" (%s is a main run at a merge commit, but the second parent %s is not in this checkout, so what dev's history deleted cannot be excused: every workflow checks out with fetch-depth: 2, then `%s`)", where, second[:9], devHistoryFetch)
		return m, "NOTE: " + where + " is a main run at a merge commit whose second parent is not in this checkout: the ordinary comparison ran, and every finding says how to fetch", nil
	}
	graft, err := shallowCut(root, second)
	if err != nil {
		return nil, "", err
	}
	if graft != "" {
		m.Remedy = fmt.Sprintf(" (%s is a main run at a merge commit, but the second parent %s's ancestry is cut by a shallow graft at %s in this checkout, so what dev's history deleted cannot be excused: fetch dev's full ancestry for a main run, `%s`)", where, second[:9], graft[:9], devHistoryFetch)
		return m, "NOTE: " + where + " is a main run at a merge commit whose second parent's ancestry is shallow here: the ordinary comparison ran, and every finding says how to fetch", nil
	}
	history, err := deletedInAncestry(root, second)
	if err != nil {
		return nil, "", err
	}
	tree, err := treePaths(root, second)
	if err != nil {
		return nil, "", err
	}
	var excused, excusedRows int
	m.Deleted, m.Declared, excused, excusedRows = excuseDevDeletions(m.Deleted, m.Declared, history, tree)
	beyond, err := readDeletionsAgainst(root, second, commit, head, subject)
	if err != nil {
		return nil, "", err
	}
	// A path already red against the first parent is not reported twice.
	red := map[string]bool{}
	for _, rel := range m.Deleted {
		if _, ok := m.Declared[rel]; !ok && guardedByMergeRule(rel) {
			red[rel] = true
		}
	}
	var rest []string
	for _, rel := range beyond.Deleted {
		if !red[rel] {
			rest = append(rest, rel)
		}
	}
	beyond.Deleted = rest
	m.Beyond = beyond.findings()
	note := fmt.Sprintf("NOTE: %s is a main run at a merge commit: HEAD is compared with its first parent %s as everywhere; dev's history (the second parent %s's ancestry, %d paths ever deleted) excused %d guarded deletions and %d declaration rows it made, each gated on dev's queue change by change, none of them a path dev's tip still has; and what HEAD lacks that dev's tip has is checked as the merge's own change (%d findings)",
		where, parents[0][:9], second[:9], len(history), excused, excusedRows, len(m.Beyond))
	return m, note, nil
}

// mainRun reports whether a run audits main: GitHub's push, workflow_dispatch
// or schedule event on refs/heads/main (main is the default branch, where
// schedules run), or no GitHub environment at all with main checked out, so a
// local audit at main's tip agrees with CI. A pull request's merge ref, a
// merge-queue group, any run on dev and a local run on another branch are
// not main runs.
func mainRun(event, ref, branch string) bool {
	switch event {
	case "push", "workflow_dispatch", "schedule":
		return ref == "refs/heads/main"
	case "":
		return ref == "" && branch == "main"
	}
	return false
}

// excuseDevDeletions is the main-run filter over the first-parent comparison.
// A deletion is excused exactly when dev's ancestry deleted the path
// (history) AND dev's tip lacks it (devTree): a path dev deleted once and
// restored is still dev's, and its loss in the merge is the merge's own. A
// declaration row is excused when dev's ancestry deleted its path: dev made
// the row with the deletion, on its queue. What is not excused is returned
// for the ordinary findings, in order; the counts are of guarded paths and
// of rows, what the findings would have been.
func excuseDevDeletions(deleted []string, declared map[string]string, history, devTree map[string]bool) (kept []string, rows map[string]string, excused, excusedRows int) {
	for _, rel := range deleted {
		if history[rel] && !devTree[rel] {
			if guardedByMergeRule(rel) {
				excused++
			}
			continue
		}
		kept = append(kept, rel)
	}
	rows = map[string]string{}
	for rel, why := range declared {
		if history[rel] {
			excusedRows++
			continue
		}
		rows[rel] = why
	}
	return kept, rows, excused, excusedRows
}

// currentBranch is the checked-out branch, or HEAD when detached.
func currentBranch(root string) (string, error) {
	out, err := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// shallowCut is the commit where sha's ancestry stops short in this checkout:
// a commit the traversal shows as a root whose object names a parent (a
// shallow graft). It is empty when the ancestry is complete, ending at a true
// root.
func shallowCut(root, sha string) (string, error) {
	out, err := gitOut(root, "rev-list", "--max-parents=0", sha)
	if err != nil {
		return "", err
	}
	for _, r := range strings.Fields(out) {
		raw, err := gitOut(root, "cat-file", "-p", r)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "parent ") {
				return r, nil
			}
			if line == "" {
				break
			}
		}
	}
	return "", nil
}

// deletedInAncestry is every path a commit in sha's ancestry deleted, read
// with renames off so a moved file counts as deleted at its old path (the
// comparison it excuses reads renames with -M and never sees those). Trees
// suffice: no blob is read.
func deletedInAncestry(root, sha string) (map[string]bool, error) {
	out, err := gitOut(root, "log", "--no-renames", "--diff-filter=D", "--name-only", "--format=", sha)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, rel := range strings.Split(out, "\n") {
		if rel = strings.TrimSpace(rel); rel != "" {
			set[rel] = true
		}
	}
	return set, nil
}

// treePaths is every path in sha's tree.
func treePaths(root, sha string) (map[string]bool, error) {
	out, err := gitOut(root, "ls-tree", "-r", "--name-only", sha)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, rel := range strings.Split(out, "\n") {
		if rel = strings.TrimSpace(rel); rel != "" {
			set[rel] = true
		}
	}
	return set, nil
}

// readDeletionsAgainst reads what commit's tree lacks that parent's had, and
// the rows commit adds to the log relative to parent; head and subject name
// the commit in the findings.
func readDeletionsAgainst(root, parent, commit, head, subject string) (*mergeDeletions, error) {
	m := &mergeDeletions{Head: head, Parent: parent[:9], Subject: subject, Declared: map[string]string{}}
	gone, err := gitOut(root, "diff", "-M", "--diff-filter=D", "--name-only", parent, commit)
	if err != nil {
		return nil, err
	}
	for _, rel := range strings.Split(gone, "\n") {
		if rel = strings.TrimSpace(rel); rel != "" {
			m.Deleted = append(m.Deleted, rel)
		}
	}
	sort.Strings(m.Deleted)
	diff, err := gitOut(root, "diff", parent, commit, "--", deletedTestsLogPath)
	if err != nil {
		return nil, err
	}
	for p, why := range declaredRowsAdded(diff) {
		m.Declared[p] = why
	}
	return m, nil
}

// firstParent is HEAD's first parent, the merge-parent comparison's base: for a
// pull request's merge ref and a merge-queue commit, dev's tip. It is read from
// the raw commit object, not `log --format=%P`: a shallow checkout grafts the
// parents away in traversal and keeps them in the object. The SLEEPS ledger
// rule (unitwaits_class_test.go) compares against the same commit.
func firstParent(root string) (string, error) {
	parents, err := commitParents(root, "HEAD")
	if err != nil {
		return "", err
	}
	if err := parentInCheckout(root, parents[0]); err != nil {
		return "", err
	}
	return parents[0], nil
}

// commitParents is a commit's parents in order, read from the raw commit
// object; a root commit is an error, since there is no merge to compare.
func commitParents(root, commit string) ([]string, error) {
	raw, err := gitOut(root, "cat-file", "-p", commit)
	if err != nil {
		return nil, err
	}
	var parents []string
	for _, line := range strings.Split(raw, "\n") {
		if p, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, p)
		}
		if line == "" {
			break
		}
	}
	if len(parents) == 0 {
		return nil, fmt.Errorf("HEAD has no parent: there is no merge to compare")
	}
	return parents, nil
}

// parentInCheckout is nil when the parent's commit object is in the checkout,
// else the error naming the fetch depth every workflow uses for this rule.
func parentInCheckout(root, parent string) error {
	if _, err := gitOut(root, "cat-file", "-e", parent+"^{commit}"); err != nil {
		return fmt.Errorf("HEAD's parent %s is not in this checkout (a depth-1 fetch), so what the merge changed cannot be read; every workflow checks out with fetch-depth: 2 for this rule", parent[:9])
	}
	return nil
}

// promotionSkip recognises a promotion of dev to main from GitHub's own event
// environment: GITHUB_EVENT_NAME, GITHUB_BASE_REF and GITHUB_HEAD_REF, as the
// `test` jobs receive them, and the pull request's head repository from the
// event payload (pullRequestHeadRepo) against GITHUB_REPOSITORY. It is true
// exactly for the pull_request event whose base is main and whose head is
// this repository's own dev, and the note says why the comparison does not
// run there: every commit such a merge brings landed through dev's queue,
// where this rule ran on each change against dev's tip. GITHUB_HEAD_REF is a
// bare branch name, so a fork's branch named dev is refused by the head
// repository, and an absent or unreadable payload refuses too. main takes
// pull requests only (its ruleset has no merge queue), so no other event
// carries a promotion. Anything else, an empty environment included, is the
// ordinary comparison: a pull request into dev, a merge-queue group, a push,
// a local run.
func promotionSkip(event, base, head, headRepo, repo string) (bool, string) {
	if event != "pull_request" || base != "main" || head != "dev" || repo == "" || headRepo != repo {
		return false, ""
	}
	return true, "NOTE: pull_request " + headRepo + ":" + head + " -> " + base + " is a promotion: its first parent is main's tip, so the comparison would see every deletion dev accumulated since the last promotion; each of those landed through dev's queue, where this rule ran on the change that made it, so the comparison does not run here"
}

// pullRequestHeadRepo is the head repository's full name from the event
// payload GitHub writes at GITHUB_EVENT_PATH, or "" when the path is empty,
// the file is unreadable or the field is absent.
func pullRequestHeadRepo(eventPath string) string {
	if eventPath == "" {
		return ""
	}
	raw, err := os.ReadFile(eventPath)
	if err != nil {
		return ""
	}
	var payload struct {
		PullRequest struct {
			Head struct {
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.PullRequest.Head.Repo.FullName
}

// declaredRowsAdded reads the `<path> <why>` rows a unified diff of the log
// adds: the declarations this change makes and no other.
func declaredRowsAdded(diff string) map[string]string {
	rows := map[string]string{}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		row := strings.TrimSpace(line[1:])
		if row == "" || strings.HasPrefix(row, "#") {
			continue
		}
		p, why, _ := strings.Cut(row, " ")
		rows[p] = strings.TrimSpace(why)
	}
	return rows
}

// findings are the rule's red lines: a guarded file deleted with no row of
// this change declaring it, and a row declaring a deletion this change does
// not make; on a main run, the second-parent comparison's own (Beyond), and
// every line ends with the Remedy when there is one.
func (m *mergeDeletions) findings() []string {
	out := append(m.ordinaryFindings(), m.Beyond...)
	for i := range out {
		out[i] += m.Remedy
	}
	sort.Strings(out)
	return out
}

// ordinaryFindings is the first-parent comparison's red lines.
func (m *mergeDeletions) ordinaryFindings() []string {
	deleted := map[string]bool{}
	for _, rel := range m.Deleted {
		deleted[rel] = true
	}
	var out []string
	for _, rel := range m.Deleted {
		if !guardedByMergeRule(rel) {
			continue
		}
		if _, ok := m.Declared[rel]; ok {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s) deletes %s, which its parent %s had, and no row of %s added in the same change declares it: "+
			"restore the file (`git checkout %s -- %s`), or, if the deletion is meant, add the row `%s <why>` to %s in this change",
			m.Head, m.Subject, rel, m.Parent, deletedTestsLogPath, m.Parent, rel, rel, deletedTestsLogPath))
	}
	for rel, why := range m.Declared {
		if why == "" {
			out = append(out, fmt.Sprintf("%s adds the row %q to %s with no why; a declaration says why the file goes", m.Head, rel, deletedTestsLogPath))
		}
		if !deleted[rel] {
			out = append(out, fmt.Sprintf("%s adds the row %q to %s, but the change deletes no such file; a row declares a deletion the same change makes (drop the row)", m.Head, rel, deletedTestsLogPath))
		}
	}
	sort.Strings(out)
	return out
}

// THE CLASS TEST. What HEAD's merge took away from its first parent's tree
// is read from git and compared with what the same change declared, so a
// squash from a stale base — a tree that lacks files dev had, with nothing
// in the change saying so — is red on the pull request, in the merge queue
// and on dev. On the promotion of dev to main (promotionSkip) the
// comparison does not run and the run says so; on a main run at the landed
// promotion (mainRun) it runs with what dev's history deleted excused, and
// the merge's own change checked against dev's tip. 2026-09-26: #4346 (rowan/functional-tag) was rebased onto
// #4344 with a tree that lacked the four files #4344 had added (the silent
// class test, its allowlist, two of its controls) and undid #4344's
// live-path fixes with them; every check was green, because a rule that is
// not there cannot fail, and a list of rules kept in the tree goes with the
// tree.
func TestNoMergeDeletesATestFileUndeclared(t *testing.T) {
	t.Parallel()

	log := loadAllowlist(t, "testdata/deleted-tests.txt", allowlist.Options{})
	for _, row := range log.Rows() {
		if _, why, _ := strings.Cut(row.Text, " "); strings.TrimSpace(why) == "" {
			t.Errorf("%s: %q carries no why", deletedTestsLogPath, row.Text)
		}
	}
	if skip, note := promotionSkip(os.Getenv("GITHUB_EVENT_NAME"), os.Getenv("GITHUB_BASE_REF"), os.Getenv("GITHUB_HEAD_REF"),
		pullRequestHeadRepo(os.Getenv("GITHUB_EVENT_PATH")), os.Getenv("GITHUB_REPOSITORY")); skip {
		t.Log(note)
		return
	}
	m, note, err := readMergeDeletionsFor(repoTree(t).Root, os.Getenv("GITHUB_EVENT_NAME"), os.Getenv("GITHUB_REF"))
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Log(note)
	}
	for _, f := range m.findings() {
		t.Error(f)
	}
}

// TestMergeRuleReadsTheDeletionOutOfGit proves the rule over a repository
// it builds: the stale-base squash shape is red for the test file and the
// list and silent for a plain source file; a rename is not a deletion; a
// row added in the same change declares a deletion, and a row that names
// no deletion is red.
func TestMergeRuleReadsTheDeletionOutOfGit(t *testing.T) {
	t.Parallel()

	r := newScratchRepo(t, "work")
	root, write, remove := r.root, r.write, r.remove
	git := func(args ...string) { t.Helper(); r.git(args...) }
	findings := func() []string {
		t.Helper()
		m, err := readMergeDeletions(root)
		if err != nil {
			t.Fatal(err)
		}
		return m.findings()
	}

	write("a/x_test.go", "package a\n")
	write("internal/ci/testdata/foo_allowlist.txt", "# a list\n")
	write(deletedTestsLogPath, "# the log\n")
	write("b/keep_test.go", strings.Repeat("package b\n\n// a test file with enough body to be recognised across a rename\n", 4))
	write("c/main.go", "package c\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	if _, err := readMergeDeletions(root); err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Fatalf("a root commit: err = %v; want no parent", err)
	}

	// The stale-base squash shape: files the parent had are gone, nothing
	// in the change says so. The test file and the list are red; the
	// source file is not this rule's.
	remove("a/x_test.go")
	remove("internal/ci/testdata/foo_allowlist.txt")
	remove("c/main.go")
	git("add", "-A")
	git("commit", "-q", "-m", "squash from a stale base")
	got := findings()
	if len(got) != 2 || !strings.Contains(got[0], "deletes a/x_test.go") || !strings.Contains(got[1], "deletes internal/ci/testdata/foo_allowlist.txt") ||
		!strings.Contains(got[0], "squash from a stale base") || !strings.Contains(got[0], "git checkout ") {
		t.Fatalf("stale-base squash findings = %q; want the test file and the list, naming the commit and the restore", got)
	}

	// A rename is not a deletion.
	git("mv", "b/keep_test.go", "b/keep_functional_test.go")
	git("commit", "-q", "-m", "split")
	if got := findings(); len(got) != 0 {
		t.Fatalf("a rename: findings = %q; want none", got)
	}

	// A row added in the same change declares the deletion; a row that
	// names no deletion of this change is red, as is a row with no why.
	remove("b/keep_functional_test.go")
	write(deletedTestsLogPath, "# the log\nb/keep_functional_test.go its cases moved to the functional tier\nd/none_test.go never existed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "declared")
	got = findings()
	if len(got) != 1 || !strings.Contains(got[0], `"d/none_test.go"`) || !strings.Contains(got[0], "deletes no such file") {
		t.Fatalf("declared deletion: findings = %q; want only the row that names no deletion", got)
	}

	// An old row declares nothing for a later change: the same file, put
	// back and deleted again with no new row, is red.
	write("b/keep_functional_test.go", "package b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "put back")
	remove("b/keep_functional_test.go")
	git("add", "-A")
	git("commit", "-q", "-m", "gone again")
	if got := findings(); len(got) != 1 || !strings.Contains(got[0], "deletes b/keep_functional_test.go") {
		t.Fatalf("an old row: findings = %q; want the deletion red", got)
	}
}

// TestPromotionSkipReadsTheEvent pins the one shape that skips the
// comparison, the promotion pull request from this repository's dev to main,
// against its reversed witnesses: the same event into dev, a feature branch
// into main, a fork's branch named dev, an absent payload, a push carrying
// the same refs, a merge-queue group (main has none; dev's carries no
// promotion), and no environment at all.
func TestPromotionSkipReadsTheEvent(t *testing.T) {
	t.Parallel()
	const repo = "mas-bandwidth/nova-tools"
	for _, tc := range []struct {
		name, event, base, head, headRepo, repo string
		skip                                    bool
	}{
		{"the promotion", "pull_request", "main", "dev", repo, repo, true},
		{"a pull request into dev from dev", "pull_request", "dev", "dev", repo, repo, false},
		{"a feature branch into main", "pull_request", "main", "feature", repo, repo, false},
		{"a fork's branch named dev", "pull_request", "main", "dev", "someone/nova-tools", repo, false},
		{"an absent payload", "pull_request", "main", "dev", "", repo, false},
		{"no GITHUB_REPOSITORY", "pull_request", "main", "dev", repo, "", false},
		{"a push with the promotion's refs", "push", "main", "dev", repo, repo, false},
		{"a merge-queue group", "merge_group", "", "", "", repo, false},
		{"pull_request_target, which no workflow here runs", "pull_request_target", "main", "dev", repo, repo, false},
		{"no environment", "", "", "", "", "", false},
	} {
		skip, note := promotionSkip(tc.event, tc.base, tc.head, tc.headRepo, tc.repo)
		if skip != tc.skip {
			t.Errorf("%s: promotionSkip(%q, %q, %q, %q, %q) = %v, want %v", tc.name, tc.event, tc.base, tc.head, tc.headRepo, tc.repo, skip, tc.skip)
		}
		if skip && (!strings.HasPrefix(note, "NOTE: ") || !strings.Contains(note, "dev's queue")) {
			t.Errorf("%s: note = %q; want a NOTE naming dev's queue as where the rule ran", tc.name, note)
		}
		if !skip && note != "" {
			t.Errorf("%s: note = %q; want none when the comparison runs", tc.name, note)
		}
	}
}

// TestMainRunReadsTheEventRefAndBranch pins the main-run shape, push,
// workflow_dispatch or schedule on refs/heads/main or no environment with
// main checked out, against its reversed witnesses: the same events on dev, a
// pull request's merge ref, a merge-queue group, a local run on another
// branch or detached, and a half-set environment.
func TestMainRunReadsTheEventRefAndBranch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, event, ref, branch string
		want                     bool
	}{
		{"push on main", "push", "refs/heads/main", "", true},
		{"workflow_dispatch on main", "workflow_dispatch", "refs/heads/main", "", true},
		{"schedule on main", "schedule", "refs/heads/main", "", true},
		{"a local run on main", "", "", "main", true},
		{"push on dev", "push", "refs/heads/dev", "", false},
		{"schedule on dev", "schedule", "refs/heads/dev", "", false},
		{"a pull request's merge ref", "pull_request", "refs/pull/4549/merge", "", false},
		{"merge_group on dev's queue ref", "merge_group", "refs/heads/gh-readonly-queue/dev/pr-1-aaaa", "", false},
		{"a local run on a feature branch", "", "", "feature", false},
		{"a local run detached", "", "", "HEAD", false},
		{"a ref with no event", "", "refs/heads/main", "main", false},
		{"an event with no ref", "push", "", "main", false},
	} {
		if got := mainRun(tc.event, tc.ref, tc.branch); got != tc.want {
			t.Errorf("%s: mainRun(%q, %q, %q) = %v, want %v", tc.name, tc.event, tc.ref, tc.branch, got, tc.want)
		}
	}
}

// TestExcuseDevDeletionsReadsHistoryAndDevTree pins the pure main-run filter:
// a deletion is excused only when dev's ancestry deleted the path and dev's
// tip lacks it; a row is excused when dev's ancestry deleted its path; an
// empty history excuses nothing.
func TestExcuseDevDeletionsReadsHistoryAndDevTree(t *testing.T) {
	t.Parallel()
	deleted := []string{"a_test.go", "b_test.go", "c_test.go", "d_test.go", "g.go"}
	declared := map[string]string{"a_test.go": "moved", "e_test.go": "moved", "f_test.go": "moved"}
	history := map[string]bool{"a_test.go": true, "b_test.go": true, "e_test.go": true, "g.go": true}
	devTree := map[string]bool{"b_test.go": true, "f_test.go": true}
	kept, rows, excused, excusedRows := excuseDevDeletions(deleted, declared, history, devTree)
	if strings.Join(kept, " ") != "b_test.go c_test.go d_test.go" || excused != 1 {
		t.Errorf("kept = %q, excused = %d; want a_test.go excused (dev deleted it and lacks it), b_test.go kept (dev's tip still has it), c and d kept (dev never deleted them), g.go excused and not counted (not guarded)", kept, excused)
	}
	if len(rows) != 1 || rows["f_test.go"] != "moved" || excusedRows != 2 {
		t.Errorf("rows = %v, excusedRows = %d; want a and e excused (dev's ancestry deleted them), f kept", rows, excusedRows)
	}
	kept, rows, excused, excusedRows = excuseDevDeletions(deleted, declared, map[string]bool{}, devTree)
	if len(kept) != 5 || len(rows) != 3 || excused != 0 || excusedRows != 0 {
		t.Errorf("empty history: kept = %q, rows = %v; want nothing excused", kept, rows)
	}
}

// promotionRepo is the main-run witness's repository, Stella's shape: a base
// with the ledger; main adds main_only_test.go on its own; dev adds dev.txt,
// deletes gone_test.go with a row, deletes x_test.go with a row and restores
// it, trims both rows, and adds new_test.go. Every file has a body of its
// own, so git's rename detection never pairs a deletion with an addition.
type promotionRepo struct {
	*scratchRepo
	mainTip, devTip, mainOnly string
}

func buildPromotionRepo(t *testing.T) *promotionRepo {
	t.Helper()
	r := &promotionRepo{scratchRepo: newScratchRepo(t, "main")}
	body := func(name string) string {
		return "package a\n\n" + strings.Repeat("// "+name+": a line of its own so no other file resembles it\n", 4)
	}
	r.write(deletedTestsLogPath, "# the log\n")
	r.write("keep_test.go", body("keep"))
	r.write("x_test.go", body("x"))
	r.write("gone_test.go", body("gone"))
	base := r.commit("base")
	r.write("main_only_test.go", body("main only"))
	r.mainTip = r.commit("main adds main_only_test.go")
	r.mainOnly = r.git("rev-parse", r.mainTip+":main_only_test.go")
	r.git("checkout", "-q", "-b", "dev", base)
	r.write("dev.txt", "dev\n")
	r.remove("gone_test.go")
	r.write(deletedTestsLogPath, "# the log\ngone_test.go moved to the functional tier\n")
	r.stage("dev adds dev.txt and deletes gone_test.go, declared")
	r.remove("x_test.go")
	r.write(deletedTestsLogPath, "# the log\ngone_test.go moved to the functional tier\nx_test.go moved too\n")
	r.stage("dev deletes x_test.go, declared")
	r.write("x_test.go", body("x"))
	r.write("new_test.go", body("new"))
	r.write(deletedTestsLogPath, "# the log\n")
	r.devTip = r.commit("dev restores x_test.go, trims the log, adds new_test.go")
	return r
}

// merge builds a two-parent commit on main, dev's tree edited in the index.
func (r *promotionRepo) merge(subject string, edit func()) string {
	r.t.Helper()
	r.git("read-tree", r.devTip)
	edit()
	return r.git("commit-tree", r.git("write-tree"), "-p", r.mainTip, "-p", r.devTip, "-m", subject)
}

// promotion is the true promotion's tree edit: dev's tree plus main's own file.
func (r *promotionRepo) promotion() {
	r.git("update-index", "--add", "--cacheinfo", "100644,"+r.mainOnly+",main_only_test.go")
}

// run is the class test's reading of one commit under an event, ref and
// checked-out branch, with nothing checked out.
func (r *promotionRepo) run(commit, event, ref, branch string) ([]string, string) {
	r.t.Helper()
	m, note, err := readCommitDeletions(r.root, commit, event, ref, branch)
	if err != nil {
		r.t.Fatal(err)
	}
	return m.findings(), note
}

// exactlyOne fails unless got is one finding naming file's deletion.
func exactlyOne(t *testing.T, name string, got []string, file string) {
	t.Helper()
	if len(got) != 1 || !strings.Contains(got[0], "deletes "+file+",") {
		t.Errorf("%s: findings = %q; want exactly one, naming %s", name, got, file)
	}
}

// TestMainRunSeesWhatMainAloneHad is Stella's witness over promotionRepo:
// the merge whose tree is dev's loses main_only_test.go, one finding naming
// it under push on refs/heads/main and under no environment on main, while
// gone_test.go, which dev deleted and lacks, is excused.
func TestMainRunSeesWhatMainAloneHad(t *testing.T) {
	t.Parallel()
	r := buildPromotionRepo(t)
	lostMainOnly := r.merge("promotion whose tree is dev's", func() {})

	got, note := r.run(lostMainOnly, "push", "refs/heads/main", "")
	exactlyOne(t, "dev's tree on main under push", got, "main_only_test.go")
	if !strings.Contains(note, "2 paths ever deleted) excused 1 guarded deletions and 0 declaration rows") || !strings.Contains(note, "(0 findings)") {
		t.Errorf("note = %q; want gone_test.go excused, no rows, nothing beyond dev", note)
	}
	got, note = r.run(lostMainOnly, "", "", "main")
	exactlyOne(t, "dev's tree on main with no environment", got, "main_only_test.go")
	if !strings.HasPrefix(note, "NOTE: a local run on main is a main run") {
		t.Errorf("note = %q; want a local main run", note)
	}
}

// TestMainRunExcusesOnlyWhatDevDeleted: the true promotion (dev's tree plus
// main_only_test.go) has no findings on main, gone_test.go excused; on a
// feature branch with no environment it is the ordinary comparison, red for
// gone_test.go.
func TestMainRunExcusesOnlyWhatDevDeleted(t *testing.T) {
	t.Parallel()
	r := buildPromotionRepo(t)
	promotion := r.merge("promotion", r.promotion)

	if got, note := r.run(promotion, "push", "refs/heads/main", ""); len(got) != 0 || !strings.Contains(note, "excused 1 guarded deletions") {
		t.Errorf("the true promotion: findings = %q, note = %q; want none, gone_test.go excused", got, note)
	}
	got, note := r.run(promotion, "", "", "feature")
	exactlyOne(t, "the true promotion on a feature branch with no environment", got, "gone_test.go")
	if note != "" {
		t.Errorf("note = %q; want none off main", note)
	}
}

// TestMainRunHoldsItsTreeControls pins controls 1 and 2 over promotionRepo:
// the true promotion minus x_test.go, which dev's history deleted but dev's
// tip still has, is red for it; the true promotion minus new_test.go, which
// main never had, is red for it, seen only against the second parent; and a
// one-parent squash on main deleting keep_test.go is red as everywhere.
func TestMainRunHoldsItsTreeControls(t *testing.T) {
	t.Parallel()
	r := buildPromotionRepo(t)
	lostX := r.merge("promotion minus x_test.go", func() {
		r.promotion()
		r.git("update-index", "--force-remove", "x_test.go")
	})
	lostNew := r.merge("promotion minus new_test.go", func() {
		r.promotion()
		r.git("update-index", "--force-remove", "new_test.go")
	})
	r.git("read-tree", lostX)
	r.git("update-index", "--force-remove", "keep_test.go")
	squash := r.git("commit-tree", r.git("write-tree"), "-p", lostX, "-m", "squash on main")

	got, _ := r.run(lostX, "push", "refs/heads/main", "")
	exactlyOne(t, "control 1: x_test.go, deleted once on dev and restored", got, "x_test.go")

	got, _ = r.run(lostNew, "workflow_dispatch", "refs/heads/main", "")
	exactlyOne(t, "control 2: new_test.go, which main never had", got, "new_test.go")
	if len(got) == 1 && !strings.Contains(got[0], "which its parent "+r.devTip[:9]+" had") {
		t.Errorf("control 2 finding = %q; want it against the second parent", got[0])
	}

	got, note := r.run(squash, "schedule", "refs/heads/main", "")
	exactlyOne(t, "a one-parent squash on main", got, "keep_test.go")
	if !strings.Contains(note, "one-parent commit") {
		t.Errorf("note = %q; want the one-parent note", note)
	}
}

// TestMainRunFailsClosedOnAShallowAncestry pins control 3: the merge in a
// depth-2 clone, where the second parent's ancestry is cut, runs the
// ordinary comparison and every finding names the graft and the fetch.
func TestMainRunFailsClosedOnAShallowAncestry(t *testing.T) {
	t.Parallel()
	r := buildPromotionRepo(t)
	lostX := r.merge("promotion minus x_test.go", func() {
		r.promotion()
		r.git("update-index", "--force-remove", "x_test.go")
	})
	r.git("update-ref", "refs/heads/main", lostX)
	shallow := filepath.Join(t.TempDir(), "shallow")
	r.git("clone", "-q", "--depth", "2", "--branch", "main", "file://"+r.root, shallow)
	m, note, err := readMergeDeletionsFor(shallow, "push", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	got := m.findings()
	if len(got) != 2 || !strings.Contains(got[0], "deletes gone_test.go,") || !strings.Contains(got[1], "deletes x_test.go,") ||
		!strings.Contains(got[0], "shallow graft at "+r.devTip[:9]) || !strings.Contains(got[0], devHistoryFetch) || !strings.Contains(note, "shallow") {
		t.Errorf("shallow ancestry: findings = %q, note = %q; want the ordinary two, each naming the graft and the fetch", got, note)
	}
}

// scratchRepo is a git repository a test builds, with the identity and hooks
// settled and every failure fatal.
type scratchRepo struct {
	t    *testing.T
	root string
}

func newScratchRepo(t *testing.T, branch string) *scratchRepo {
	t.Helper()
	r := &scratchRepo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", branch)
	return r
}

func (r *scratchRepo) git(args ...string) string {
	r.t.Helper()
	out, err := gitOut(r.root, append([]string{
		"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=always"}, args...)...)
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func (r *scratchRepo) write(rel, text string) {
	r.t.Helper()
	p := filepath.Join(r.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *scratchRepo) remove(rel string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.root, filepath.FromSlash(rel))); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it, returning the new HEAD.
func (r *scratchRepo) commit(subject string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", subject)
	return r.git("rev-parse", "HEAD")
}

// stage stages everything and commits it, for a commit whose sha is not needed.
func (r *scratchRepo) stage(subject string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", subject)
}

func TestGuardedByMergeRuleReadsThePath(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]bool{
		"internal/ci/silent_class_test.go":            true,
		"cmd/nova-sprint/silent_test.go":              true,
		"internal/nsprint/table/silent_test.go":       true,
		"internal/ci/testdata/silent_allowlist.txt":   true,
		"internal/ci/testdata/deleted-tests.txt":      true,
		"internal/ci/testdata/net/x.txt":              false,
		"internal/ci/testdata/lisptemppath/x_test.go": true,
		"cmd/nova-sprint/silent.go":                   false,
		"docs/SPEC-CI.md":                             false,
	} {
		if got := guardedByMergeRule(rel); got != want {
			t.Errorf("guardedByMergeRule(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestDeclaredRowsAddedReadsOnlyTheAddedRows(t *testing.T) {
	t.Parallel()
	diff := "--- a/internal/ci/testdata/deleted-tests.txt\n+++ b/internal/ci/testdata/deleted-tests.txt\n@@ -1,2 +1,4 @@\n # the log\n old/one_test.go kept from before\n+# a comment\n+new/two_test.go moved to the functional tier\n+new/three_test.go\n-gone/row_test.go a removed row\n"
	got := declaredRowsAdded(diff)
	if len(got) != 2 || got["new/two_test.go"] != "moved to the functional tier" || got["new/three_test.go"] != "" {
		t.Fatalf("declaredRowsAdded = %v; want the two added rows, the context, the comment and the removed row unread", got)
	}
}

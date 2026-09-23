package review

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// TestIssue2169 reproduces nova-tools#2169: the spec builds a rule on
// `nova-review dedupe` but the subcommand did not exist and main.go's dispatch
// refused it as "unknown subcommand". The test builds a lane with stella's H1
// findings and emma's H2 findings (including a dup <id1> row), runs dedupe, and
// asserts the subcommand exists and reports id1 with both viewers and one dup.
func TestIssue2169(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("not at repo root: %v", err)
	}

	lane := t.TempDir()
	h1 := "1111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222"
	st := &merge.State{
		Version:    merge.Version,
		Repo:       "test/repo",
		Base:       "0000000000000000000000000000000000000000",
		LaneBranch: "lane",
		Branches: []*merge.Entry{
			{Branch: "feature", OID: h2, NeedsRead: "yes"},
		},
	}
	if err := st.SaveTo(lane); err != nil {
		t.Fatal(err)
	}

	entryDir := merge.EntryDirName("feature")
	reviewsDir := filepath.Join(lane, "reviews", entryDir)
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	id1 := "20260921T120000Z-a1b2c3.1"
	id2 := "20260921T120000Z-a1b2c3.2"
	id3 := "20260921T120000Z-a1b2c3.3"

	stella := testReviewRecord{
		Version: 1,
		Entry:   "feature",
		Who:     "stella",
		Model:   "test",
		Kind:    "line",
		Verdict: "hold",
		Head:    h1,
		At:      "2026-09-21T12:00:00Z",
		Findings: []testFinding{
			{ID: id1, State: "block", Side: "head", Path: "a.txt", Line: 5, Rule: "ruleA"},
			{ID: id2, State: "nit", Side: "head", Path: "a.txt", Line: 10, Rule: "ruleB"},
		},
	}
	emma := testReviewRecord{
		Version: 1,
		Entry:   "feature",
		Who:     "emma",
		Model:   "test",
		Kind:    "line",
		Verdict: "hold",
		Head:    h2,
		At:      "2026-09-21T13:00:00Z",
		Findings: []testFinding{
			{ID: id1, State: "block", Side: "head", Path: "a.txt", Line: 5, Rule: "ruleA"},
			{ID: id2, State: "nit", Side: "head", Path: "a.txt", Line: 10, Rule: "ruleB"},
			{ID: id3, State: "dup", Side: "head", Path: "a.txt", Line: 5, Rule: "ruleA", Claim: id1},
		},
	}

	writeReviewRecord(t, filepath.Join(reviewsDir, "review-stella-"+merge.Short(h1)+"-20260921T120000Z-a1b2c3.json"), stella)
	writeReviewRecord(t, filepath.Join(reviewsDir, "review-emma-"+merge.Short(h2)+"-20260921T130000Z-d4e5f6.json"), emma)

	cmd := exec.Command("go", "run", "./cmd/nova-review", "dedupe", "--lane", lane, "--branch", "feature", "--head", h2)
	cmd.Dir = repoRoot
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	output := string(out)
	if strings.Contains(output, "unknown subcommand") {
		t.Fatalf("dedupe subcommand missing (nova-tools#2169): %s", output)
	}
	if err != nil {
		t.Fatalf("dedupe exited %v: %s", err, output)
	}
	if !strings.Contains(output, "DEDUPE FINDING id="+id1) {
		t.Fatalf("dedupe did not report id1: %s", output)
	}
	// Rule 9: one seen= pair per head, newest first, never aggregated across heads.
	if !strings.Contains(output, "head="+merge.Short(h2)+" members="+id1+" seen=emma:test unreported=- blind=0 head="+merge.Short(h1)+" seen=stella:test unreported=- blind=0 ") {
		t.Fatalf("dedupe did not report each head's viewers for id1: %s", output)
	}
	if !strings.Contains(output, " folded=1 ") {
		t.Fatalf("dedupe did not count the explicit dup as folded=1: %s", output)
	}
	if !strings.Contains(output, "dups=1") {
		t.Fatalf("dedupe did not count one dup for id1: %s", output)
	}
}

// TestTheLedgerNamesTheBlind is SPEC-REVIEW.md red test 9 for the two fields
// the first dedupe cut hard-coded (stella's hold on #2786): three readers at H
// whose ranges all covered p.txt:12, two finding it and one not, print
// seen= naming the two and unreported= naming the third with blind=0; a
// fourth reader whose range (since their own read at H0) did not cover the
// line is blind=1 and never in unreported=. Two ids on one key print as
// members with folded=0; an author's answer --as dup folds them, folded=1.
func TestTheLedgerNamesTheBlind(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lane := t.TempDir()
	repo := filepath.Join(lane, merge.RepoDir)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = repo
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	write := func(name string, ls []string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(strings.Join(ls, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("p.txt", lines)
	write("q.txt", []string{"q"})
	git("add", ".")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	lines[11] = "line 12 changed"
	write("p.txt", lines)
	git("commit", "-q", "-am", "h0")
	h0 := git("rev-parse", "HEAD")
	write("q.txt", []string{"q changed"})
	git("commit", "-q", "-am", "h")
	h := git("rev-parse", "HEAD")

	st := &merge.State{
		Version:    merge.Version,
		Repo:       "test/repo",
		Base:       base,
		LaneBranch: "lane",
		Branches:   []*merge.Entry{{Branch: "feature", OID: h, NeedsRead: "yes"}},
	}
	if err := st.SaveTo(lane); err != nil {
		t.Fatal(err)
	}
	reviewsDir := filepath.Join(lane, "reviews", merge.EntryDirName("feature"))
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := "20260921T120000Z-aaaaaa.1"
	e1 := "20260921T120100Z-bbbbbb.1"
	rec := func(who, verdict, head, at string, fs ...testFinding) {
		writeReviewRecord(t, filepath.Join(reviewsDir, "review-"+who+"-"+merge.Short(head)+"-"+strings.NewReplacer(":", "", "-", "").Replace(at)+".json"), testReviewRecord{
			Version: 1, Entry: "feature", Who: who, Model: "m", Kind: "line", Verdict: verdict, Head: head, At: at, Findings: fs,
		})
	}
	rec("dave", "approve", h0, "2026-09-21T11:00:00Z")
	rec("stella", "hold", h, "2026-09-21T12:00:00Z", testFinding{ID: s1, State: "block", Side: "head", Path: "p.txt", Line: 12, RuleKind: "spec", Rule: "spec:40"})
	rec("emma", "hold", h, "2026-09-21T12:01:00Z", testFinding{ID: e1, State: "block", Side: "head", Path: "p.txt", Line: 12, RuleKind: "spec", Rule: "spec:40"})
	rec("johnny", "approve", h, "2026-09-21T12:02:00Z")
	rec("dave", "approve", h, "2026-09-21T12:03:00Z")

	run := func() string {
		t.Helper()
		cmd := exec.Command("go", "run", "./cmd/nova-review", "dedupe", "--lane", lane, "--branch", "feature")
		cmd.Dir = repoRoot
		cmd.Env = goenv.Clean(os.Environ())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("dedupe exited %v: %s", err, out)
		}
		return string(out)
	}
	out := run()
	want := "members=" + s1 + "," + e1 + " seen=stella:m,emma:m unreported=johnny:m blind=1 "
	if !strings.Contains(out, want) {
		t.Fatalf("want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "dave:m") {
		t.Fatalf("an out-of-range reader is blind, never unreported:\n%s", out)
	}
	if !strings.Contains(out, " folded=0 ") {
		t.Fatalf("two ids on one key are grouped, not folded:\n%s", out)
	}

	ans, err := json.Marshal(map[string]any{"version": 1, "entry": "feature", "who": "author", "finding": e1, "as": "dup", "of": s1, "head": h, "at": "2026-09-21T12:05:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reviewsDir, "answer-author-1.json"), ans, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := run(); !strings.Contains(out, " folded=1 ") {
		t.Fatalf("an answer --as dup folds, folded=1:\n%s", out)
	}
}

type testReviewRecord struct {
	Version  int           `json:"version"`
	Entry    string        `json:"entry"`
	Who      string        `json:"who"`
	Model    string        `json:"model"`
	Kind     string        `json:"kind"`
	Verdict  string        `json:"verdict"`
	Head     string        `json:"head"`
	At       string        `json:"at"`
	Findings []testFinding `json:"findings"`
}

type testFinding struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Side     string `json:"side"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Spec     string `json:"spec"`
	SpecLine int    `json:"spec_line"`
	RuleKind string `json:"rule_kind"`
	Rule     string `json:"rule"`
	Claim    string `json:"claim"`
}

func writeReviewRecord(t *testing.T, path string, rec testReviewRecord) {
	t.Helper()
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

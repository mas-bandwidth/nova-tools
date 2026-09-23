package review

import (
	"encoding/json"
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
	if !strings.Contains(output, "seen=stella:test,emma:test") {
		t.Fatalf("dedupe did not report both viewers for id1: %s", output)
	}
	if !strings.Contains(output, "dups=1") {
		t.Fatalf("dedupe did not count one dup for id1: %s", output)
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

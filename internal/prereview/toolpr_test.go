package prereview_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// toolPRDir holds the eight tool pull requests of the 2026-09-22 dry pass
// (rowan-new reports/jev-dry-pass-tool-prs-2026-09-22.md) in the --pr-dir shape:
// <n>/view.json and <n>/diff.txt. Read from the mirror, not by REST: the diff
// is `git diff $(git merge-base refs/pull/<n>/head <dev at 2026-09-22 20:30Z>)
// refs/pull/<n>/head`, the body the head commit's message.
const toolPRDir = "testdata/toolprs-2026-09-22"

// toolPRs is the dry pass's order.
var toolPRs = []int{2587, 2594, 2607, 2612, 2613, 2614, 2615, 2598}

func loadToolPR(t *testing.T, n int) prereview.PR {
	t.Helper()
	d := filepath.Join(toolPRDir, strconv.Itoa(n))
	raw, err := os.ReadFile(filepath.Join(d, "view.json"))
	if err != nil {
		t.Fatalf("#%d: %v", n, err)
	}
	var w struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		HeadRefOid  string `json:"headRefOid"`
		BaseRefName string `json:"baseRefName"`
		Files       []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("#%d view.json: %v", n, err)
	}
	diff, err := os.ReadFile(filepath.Join(d, "diff.txt"))
	if err != nil {
		t.Fatalf("#%d: %v", n, err)
	}
	pr := prereview.PR{Repo: "mas-bandwidth/nova-tools", Number: n, Head: w.HeadRefOid, Title: w.Title,
		Body: w.Body, Diff: string(diff), BaseRef: w.BaseRefName}
	for _, f := range w.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	return pr
}

// TestToolPRModeOnTheDryPass is #2621's DONE-WHEN over the eight tool pull
// requests re-run from fixtures: selfcheck (symbol) is missing on all eight --
// none is a conformance cell and no card named a SYMBOL, so no tell is read and
// none convicts -- and no tell is read on a testdata fixture or a docs page:
// the tell text of each pull request's testdata and docs files is empty. The
// control: those same files of #2594, read whole, DO carry the specimens (its
// own cells quote check(true, ...) and // Simulate), so it is the skip that
// keeps them out and not an empty corpus.
func TestToolPRModeOnTheDryPass(t *testing.T) {
	t.Parallel()

	for _, n := range toolPRs {
		pr := loadToolPR(t, n)
		c := prereview.Mechanical(pr, prereview.InferCard(pr))
		if c.Symbol.Result != prereview.Missing {
			t.Errorf("#%d selfcheck = %s (%s), want missing", n, c.Symbol.Result, c.Symbol.Reason)
		}
		// None of the eight bodies is a harvested RESULT; #2614's quotes the
		// ASKED shape in an indented block, which is not one either.
		if c.Done.Result != prereview.Missing {
			t.Errorf("#%d donewhen = %s (%s), want missing", n, c.Done.Result, c.Done.Reason)
		}
		fixtures := skippedSections(pr.Diff)
		if got := strings.TrimSpace(prereview.TellText(fixtures)); got != "" {
			t.Errorf("#%d: %d bytes of testdata/docs lines reach the tells", n, len(got))
		}
		if n == 2594 {
			if _, ok := prereview.FirstTell(prereview.AddedLines(fixtures)); !ok {
				t.Error("control: #2594's testdata and docs lines carry no tell, so the skip proves nothing")
			}
		}
		groups := prereview.FileGroups(pr)
		if len(groups) < 1 || len(groups) > prereview.MaxGroups {
			t.Errorf("#%d: %d file groups, want 1..%d", n, len(groups), prereview.MaxGroups)
		}
		// Three of the eight were cut at DiffCap and scored at confidence
		// 0.00; no group of any of them is cut now.
		for _, g := range groups {
			if len(g.Diff) > prereview.DiffCap {
				t.Errorf("#%d: file group %s is %d bytes, past DiffCap", n, g.Key, len(g.Diff))
			}
		}
	}
}

// skippedSections is the part of a diff under testdata/ or docs/.
func skippedSections(diff string) string {
	var b strings.Builder
	keep := false
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			p := line[strings.LastIndex(line, " b/")+3:]
			keep = strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "testdata/") || strings.HasPrefix(p, "docs/")
		}
		if keep {
			b.WriteString(line)
		}
	}
	return b.String()
}

// TestTellsSkipDocsAndTestdata: a docs page and a fixture quoting a tell are
// skipped; the same line in a test file is not.
func TestTellsSkipDocsAndTestdata(t *testing.T) {
	t.Parallel()

	line := "+check(true, \"x\")\n"
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"docs/CLI.md", false},
		{"internal/x/testdata/cells/1.diff", false},
		{"testdata/a.txt", false},
		{"test/conformance/go/cell_test.go", true},
		{"internal/docs/x.go", true},
	} {
		diff := "diff --git a/" + tc.path + " b/" + tc.path + "\n--- a/" + tc.path + "\n+++ b/" + tc.path + "\n" + line
		if _, got := prereview.FirstTell(prereview.TellText(diff)); got != tc.want {
			t.Errorf("%s: tell fired = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestSpearmanReproducesTheDryPass pins the measure against the report's own
// numbers: raw scores and diff KB of the eight, rho -0.64.
func TestSpearmanReproducesTheDryPass(t *testing.T) {
	t.Parallel()

	raw := []float64{3.69, 4.65, 6.70, 3.28, 5.66, 6.97, 7.27, 7.63}
	kb := []float64{104, 286, 13, 60, 128, 33, 27, 20}
	if got := prereview.Spearman(raw, kb); math.Abs(got-(-0.643)) > 0.001 {
		t.Fatalf("Spearman = %.3f, want -0.643", got)
	}
	if got := prereview.Spearman([]float64{1, 2, 2, 3}, []float64{1, 2, 2, 3}); math.Abs(got-1) > 1e-9 {
		t.Fatalf("ties: Spearman = %v, want 1", got)
	}
}

// groupAsker answers by file group: the score is keyed by the group's key,
// read out of the state, and it counts its calls.
type groupAsker struct {
	byKey map[string]float64
	calls *int
}

func (a groupAsker) Ask(_ context.Context, state string, _ map[string]decide.Question) (map[string]decide.Answer, error) {
	*a.calls++
	score := 9.0
	for k, s := range a.byKey {
		if strings.Contains(state, "file group ") && strings.Contains(state, ": "+k+" (") {
			score = s
		}
	}
	return map[string]decide.Answer{"score": {Type: "score", Score: score, Confidence: score / 10}}, nil
}

// TestScoreIsTheLowestFileGroup: one question per changed-file group, the
// lowest answer is the pull request's, testdata is not a group; a one-group
// pull request is asked the old single question over the old state; a
// recorded group pass replays.
func TestScoreIsTheLowestFileGroup(t *testing.T) {
	t.Parallel()

	sec := func(p, body string) string {
		return "diff --git a/" + p + " b/" + p + "\n--- a/" + p + "\n+++ b/" + p + "\n@@ -0,0 +1 @@\n+" + body + "\n"
	}
	pr := prereview.PR{Repo: "mas-bandwidth/nova-tools", Number: 7, Head: "h", Body: "prose",
		Files: []string{"cmd/x/main.go", "internal/y/y.go", "internal/y/y_test.go", "internal/y/testdata/f.txt"},
		Diff:  sec("cmd/x/main.go", "a") + sec("internal/y/y.go", "b") + sec("internal/y/y_test.go", "c") + sec("internal/y/testdata/f.txt", "d")}
	groups := prereview.FileGroups(pr)
	if len(groups) != 2 || groups[0].Key != "cmd/x" || groups[1].Key != "internal/y" || len(groups[1].Files) != 2 {
		t.Fatalf("groups = %+v, want cmd/x and internal/y (two files), no testdata", groups)
	}
	calls := 0
	g, err := prereview.ScoreGroups(context.Background(), groupAsker{map[string]float64{"cmd/x": 8, "internal/y": 4}, &calls},
		prereview.ScoreQuestion(), pr, prereview.Card{})
	if err != nil || g.Raw != 4 || g.Lowest != "internal/y" || g.Groups != 2 || calls != 2 {
		t.Fatalf("ScoreGroups = %+v, %v after %d calls; want raw 4 from internal/y over 2 calls", g, err, calls)
	}

	dir := t.TempDir()
	calls = 0
	rec := prereview.RecordingAsker{Inner: groupAsker{map[string]float64{"cmd/x": 8, "internal/y": 4}, &calls}, Dir: dir, Repo: pr.Repo}
	if _, err := prereview.ScoreGroups(context.Background(), rec, prereview.ScoreQuestion(), pr, prereview.Card{}); err != nil {
		t.Fatalf("record: %v", err)
	}
	for k := 1; k <= 2; k++ {
		if _, err := os.Stat(filepath.Join(dir, prereview.GroupFixtureName(pr.Repo, 7, k))); err != nil {
			t.Fatalf("group %d fixture: %v", k, err)
		}
	}
	replayed, err := prereview.ScoreGroups(context.Background(), prereview.FixtureAsker{Dir: dir, Repo: pr.Repo},
		prereview.ScoreQuestion(), pr, prereview.Card{})
	if err != nil || replayed.Raw != 4 || replayed.Lowest != "internal/y" {
		t.Fatalf("replay = %+v, %v; want raw 4 from internal/y", replayed, err)
	}

	one := prereview.PR{Repo: pr.Repo, Number: 8, Head: "h", Files: []string{"internal/y/y.go"}, Diff: sec("internal/y/y.go", "b")}
	var seen string
	calls = 0
	stateAsker := askerFunc(func(state string) { seen = state; calls++ })
	if _, err := prereview.ScoreGroups(context.Background(), stateAsker, prereview.ScoreQuestion(), one, prereview.Card{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || seen != prereview.State(one, prereview.Card{}) {
		t.Fatalf("a one-group pull request was not asked the old state (%d calls)", calls)
	}
}

type askerFunc func(state string)

func (f askerFunc) Ask(_ context.Context, state string, _ map[string]decide.Question) (map[string]decide.Answer, error) {
	f(state)
	return map[string]decide.Answer{"score": {Type: "score", Score: 5, Confidence: 0.5}}, nil
}

// TestToolPRScoreIsNotDiffSize is the score half of #2621's DONE-WHEN: over
// the eight, the per-group minimum is uncorrelated with diff bytes, |rho| <
// 0.2. It replays a recorded group pass from toolPRDir/jev, which one paid
// `nova-decide review --pr-dir <toolPRDir> --record <toolPRDir>/jev --dry-run`
// writes; until that pass is recorded it skips and says so.
func TestToolPRScoreIsNotDiffSize(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(toolPRDir, "jev")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("owed (#2621): no recorded group pass under %s; one paid --record run over the eight writes it", dir)
	}
	var scores, bytes []float64
	for _, n := range toolPRs {
		pr := loadToolPR(t, n)
		g, err := prereview.ScoreGroups(context.Background(), prereview.FixtureAsker{Dir: dir, Repo: pr.Repo},
			prereview.ScoreQuestion(), pr, prereview.InferCard(pr))
		if err != nil {
			t.Fatalf("#%d: %v", n, err)
		}
		scores = append(scores, g.Raw)
		bytes = append(bytes, float64(len(pr.Diff)))
	}
	if rho := prereview.Spearman(scores, bytes); !(math.Abs(rho) < 0.2) {
		t.Fatalf("score vs diff bytes: rho = %.2f, want |rho| < 0.2 (%s)", rho, fmt.Sprint(scores))
	}
}

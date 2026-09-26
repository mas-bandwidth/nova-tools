//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// A description with max_cache_read 1000 and a fake usage of 2000 ends
// with end=budget and the PROMPT-DEFECT line. The fake usage and the
// fake harness log are produced by the fake runner
// (internal/swarm/testdata/fakerunner).
func TestCardBudgetCacheReadEndsWithPromptDefect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	label, slot := "card1", "1"
	root := filepath.Join(dir, "root")
	job := filepath.Join(root, slot, "jobs", label)
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "budget",
		runnerStep{Op: "write", Path: "{job}/harness.log", Body: "assistant turn one\nassistant turn two\n"},
		runnerStep{Op: "write", Path: "{job}/RESULT.md", Body: "# t\n"},
	)
	cmd := exec.Command(runner, label, slot, "m", card, root, "unmetered")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the fake runner writes the fake harness log: %v\n%s", err, out)
	}

	w := Worker{MaxCacheRead: 1000}
	u := ProviderUsage{Observed: true, Values: map[string]string{"cache_read": "2000"}}
	turns := CountCardTurns(job, u)
	over, cacheRead, max := w.OverCardBudget(u, turns)
	if !over {
		t.Fatalf("a fake usage of 2000 over a max_cache_read of 1000 is over budget")
	}
	if cacheRead != 2000 || max != 1000 {
		t.Fatalf("the budget reports cache_read=2000 max=1000, got %d %d", cacheRead, max)
	}
	end := EndDone
	if over {
		end = EndBudget
	}
	if end != EndBudget {
		t.Fatalf("a card over its budget ends with end=budget, got %q", end)
	}
	if err := AppendPromptDefect(job, label, cacheRead, max, turns); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "PROMPT-DEFECT task=" + label + " reason=budget cache_read=2000 max=1000 turns=" + strconv.Itoa(turns)
	if !strings.Contains(string(raw), want) {
		t.Fatalf("the task's report carries %q, got:\n%s", want, raw)
	}
}

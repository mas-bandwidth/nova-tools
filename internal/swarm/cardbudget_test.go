package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CARD-8317: a worker description carries a card budget (max_turns,
// max_cache_read) and nova-swarm run stops a card at the budget with
// end=budget and a PROMPT-DEFECT line.

// The description carries the budget.
func TestCardBudgetLoadsFromWorkerDescription(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "worker")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "w.json")
	body := `{"name":"w","provider":"p","model":"m","env_var":"FAKE_KEY","key_file":"` + filepath.Join(dir, "key") +
		`","usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":` +
		strconv.Quote(home) + `,"deadline":"5m","max_turns":30,"max_cache_read":1000}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w, problems := LoadWorker(path)
	if len(problems) != 0 {
		t.Fatalf("a description with a card budget is accepted, got %v", problems)
	}
	if w.MaxTurns != 30 || w.MaxCacheRead != 1000 {
		t.Fatalf("the card budget wants max_turns=30 max_cache_read=1000, got %+v", w)
	}
	if !w.HasCardBudget() {
		t.Fatal("a description with a budget has one")
	}
}

// A description with max_cache_read 1000 and a fake usage of 2000 ends
// with end=budget and the PROMPT-DEFECT line. The fake usage and the
// fake harness log are produced by the fake runner
// (internal/swarm/testdata/fakerunner).
func TestCardBudgetCacheReadEndsWithPromptDefect(t *testing.T) {
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
	cmd := exec.Command(runner, label, slot, "m", card, root)
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

// A description without a budget is unchanged: the same fake usage
// does not end the card.
func TestCardBudgetWithoutBudgetIsUnchanged(t *testing.T) {
	w := Worker{}
	u := ProviderUsage{Observed: true, Values: map[string]string{"cache_read": "2000"}}
	if over, _, _ := w.OverCardBudget(u, 2); over {
		t.Fatal("a description without a budget is unchanged by a fake usage of 2000")
	}
	if w.HasCardBudget() {
		t.Fatal("a description without a budget has none")
	}
}

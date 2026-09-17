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
	body := `{"name":"w","provider":"p","model":"m","env_var":"FAKE_KEY","key_file":` +
		strconv.Quote(filepath.Join(dir, "key")) +
		`,"usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":` +
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

// CARD-8349: a card budget below the harness's MEASURED startup cost is a
// budget every card would die against at once, so run and native refuse it by
// name at load, before any launch, and never apply it.

// A max_cache_read of 2000 against a measured startup of 5000 is refused,
// naming both. A max_turns below the measured startup's turns plus two is
// refused the same way.
func TestCardBudgetBelowMeasuredStartupIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := RecordStartupCost(root, "abc123", 5000, 7); err != nil {
		t.Fatal(err)
	}
	sc, err := ReadStartupCost(root)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Present || sc.FirstUsageCacheRead != 5000 || sc.FirstUsageTurns != 7 {
		t.Fatalf("the startup cost reads back cache_read=5000 turns=7, got %+v", sc)
	}
	cache := Worker{MaxCacheRead: 2000}
	line := cache.BudgetRefusal(sc)
	if line != "BUDGET REFUSED max_cache_read=2000 startup=5000 (want >= 2x)" {
		t.Fatalf("a 2000 max_cache_read under a 5000 startup is refused naming both, got %q", line)
	}
	turns := Worker{MaxTurns: 8}
	if line := turns.BudgetRefusal(sc); line == "" {
		t.Fatal("a max_turns of 8 under a startup of 7 wants a refusal (want >= 9)")
	}
}

// A max_cache_read of 20000 against a startup of 5000 is accepted: it is at
// or above twice the measured first context.
func TestCardBudgetAtTwiceStartupIsAccepted(t *testing.T) {
	root := t.TempDir()
	if err := RecordStartupCost(root, "abc123", 5000, 7); err != nil {
		t.Fatal(err)
	}
	sc, err := ReadStartupCost(root)
	if err != nil {
		t.Fatal(err)
	}
	if line := (Worker{MaxCacheRead: 20000}).BudgetRefusal(sc); line != "" {
		t.Fatalf("a 20000 max_cache_read over a 5000 startup is accepted, got %q", line)
	}
	if line := (Worker{MaxTurns: 9}).BudgetRefusal(sc); line != "" {
		t.Fatalf("a max_turns of 9 over a startup of 7 is accepted, got %q", line)
	}
}

// An absent startup file accepts the budget, and the file is written from
// the first finished task's usage so the next run has a measurement.
func TestCardBudgetAbsentStartupIsAcceptedAndRecorded(t *testing.T) {
	root := t.TempDir()
	sc, err := ReadStartupCost(root)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Present {
		t.Fatalf("an absent startup-cost.tsv is no measurement, got %+v", sc)
	}
	if line := (Worker{MaxCacheRead: 2000}).BudgetRefusal(sc); line != "" {
		t.Fatalf("an absent startup file accepts the budget, got %q", line)
	}
	if err := RecordStartupCostIfAbsent(root, "abc123", 5000, 7); err != nil {
		t.Fatal(err)
	}
	sc, err = ReadStartupCost(root)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Present || sc.FirstUsageCacheRead != 5000 || sc.FirstUsageTurns != 7 {
		t.Fatalf("the startup cost is written from the first finished task, got %+v", sc)
	}
	if line := (Worker{MaxCacheRead: 2000}).BudgetRefusal(sc); line == "" {
		t.Fatal("once measured, the same budget is refused by name")
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

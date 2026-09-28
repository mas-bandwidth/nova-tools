package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// CARD-8317: a worker description carries a card budget (max_turns,
// max_cache_read) and nova-swarm run stops a card at the budget with
// end=budget and a PROMPT-DEFECT line.

// The description carries the budget.
func TestCardBudgetLoadsFromWorkerDescription(t *testing.T) {
	t.Parallel()

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

// CARD-8349: a card budget below the harness's MEASURED startup cost is a
// budget every card would die against at once, so run and native refuse it by
// name at load, before any launch, and never apply it.

// A max_cache_read of 2000 against a measured startup of 5000 is refused,
// naming both. A max_turns below the measured startup's turns plus two is
// refused the same way.
func TestCardBudgetBelowMeasuredStartupIsRefused(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	w := Worker{}
	u := ProviderUsage{Observed: true, Values: map[string]string{"cache_read": "2000"}}
	if over, _, _ := w.OverCardBudget(u, 2); over {
		t.Fatal("a description without a budget is unchanged by a fake usage of 2000")
	}
	if w.HasCardBudget() {
		t.Fatal("a description without a budget has none")
	}
}

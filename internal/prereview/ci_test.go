package prereview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadRollup(t *testing.T, name string) (string, []CheckRun) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ci-rollup", name))
	if err != nil {
		t.Fatal(err)
	}
	runs, total, err := ParseCheckRollup(raw)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(runs) || len(runs) == 0 {
		t.Fatalf("%s: total=%d runs=%d", name, total, len(runs))
	}
	head := runs[0].HeadSHA
	for _, r := range runs {
		if r.HeadSHA != head {
			t.Fatalf("%s: rollup mixes heads %s and %s", name, head, r.HeadSHA)
		}
	}
	return head, runs
}

func checksClearExceptCI(ci Check) Checks {
	yes := Check{Result: Yes, Reason: "ok"}
	return Checks{Symbol: yes, Paths: yes, Done: yes, Claims: yes, CI: ci}
}

// tuningWithCI is the loop's checks_enabled once it names ci. The default
// tuning leaves ci off.
func tuningWithCI() Tuning {
	base := DefaultTuning()
	en := map[string]bool{"ci": true}
	for k, v := range base.Enabled {
		en[k] = v
	}
	base.Enabled = en
	return base
}

// TestRecordedRollupsAreThe2704Controls replays the two heads the scorecard
// named. #2519 at 907546af had ci-ok and the 1/4 and 2/4 shards red. With ci
// off, a score of 8 PASSes; with ci named in checks_enabled, it BOUNCEs.
// #2522 at 8359db4f is green.
func TestRecordedRollupsAreThe2704Controls(t *testing.T) {
	if DefaultTuning().Enabled["ci"] {
		t.Fatal("ci is in the default checks_enabled set")
	}
	tune := tuningWithCI()

	head, runs := loadRollup(t, "2519-907546af.json")
	ci := ciCheck(PR{Head: head, Checks: runs})
	if ci.Result != No {
		t.Fatalf("#2519 ci=%s (%s), want no", ci.Result, ci.Reason)
	}
	const jobs = "jobs: ci-ok, test (1/4 space), test (1/4 studio), test (2/4 space), test (2/4 studio)"
	if !strings.Contains(ci.Reason, "BOUNCE") || !strings.Contains(ci.Reason, jobs) || !strings.Contains(ci.Reason, head) {
		t.Fatalf("#2519 reason = %q, want BOUNCE, %s, and the head", ci.Reason, jobs)
	}
	for _, quiet := range []string{"lint", "e2e", "test (3/4", "test (4/4", "fleet-probe"} {
		if strings.Contains(ci.Reason, quiet) {
			t.Errorf("#2519 reason names %q, which did not fail: %s", quiet, ci.Reason)
		}
	}
	if v, why := DefaultTuning().Decide(checksClearExceptCI(ci), 8, true); v != Pass {
		t.Fatalf("#2519 with ci off at score 8: verdict=%s (%s), want PASS", v, why)
	}
	if v, why := tune.Decide(checksClearExceptCI(ci), 8, true); v != Bounce {
		t.Fatalf("#2519 at score 8 with ci enabled: verdict=%s (%s), want BOUNCE", v, why)
	}

	head, runs = loadRollup(t, "2522-8359db4f.json")
	ci = ciCheck(PR{Head: head, Checks: runs})
	if ci.Result != Yes || !strings.Contains(ci.Reason, "PASS") || !strings.Contains(ci.Reason, head) {
		t.Fatalf("#2522 ci=%s (%s), want PASS at %s", ci.Result, ci.Reason, head)
	}
	if strings.Contains(ci.Reason, "BOUNCE") {
		t.Fatalf("#2522 reason bounces a green ci-ok: %s", ci.Reason)
	}
	if v, why := tune.Decide(checksClearExceptCI(ci), 8, true); v != Pass {
		t.Fatalf("#2522 at score 8: verdict=%s (%s), want PASS", v, why)
	}
}

// TestCICheckIgnoresARollupFromAnotherSHA is the exact-head rule. A newer red
// ci-ok on some other commit does not bounce a head whose own ci-ok succeeded,
// and a green ci-ok on some other commit does not pass a head that has none.
func TestCICheckIgnoresARollupFromAnotherSHA(t *testing.T) {
	head, runs := loadRollup(t, "2522-8359db4f.json")
	stale := strings.Repeat("b", 40)
	withStaleRed := append([]CheckRun{}, runs...)
	withStaleRed = append(withStaleRed, CheckRun{
		Name: ciOK, Status: "completed", Conclusion: "failure",
		HeadSHA: stale, CompletedAt: "2099-01-01T00:00:00Z",
	})
	if got := ciCheck(PR{Head: head, Checks: withStaleRed}); got.Result != Yes {
		t.Fatalf("stale red ci-ok bounced a green head: %s", got.Reason)
	}

	moved := append([]CheckRun{}, runs...)
	for i := range moved {
		moved[i].HeadSHA = stale
	}
	got := ciCheck(PR{Head: head, Checks: moved})
	if got.Result != No || !strings.Contains(got.Reason, "ci-ok missing at "+head) {
		t.Fatalf("green ci-ok on %s counted for %s: %s (%s)", stale, head, got.Result, got.Reason)
	}
	if strings.Contains(got.Reason, "jobs:") {
		t.Fatalf("jobs from another sha were named: %s", got.Reason)
	}
}

// TestLaterCIAttemptIsTheOneThatCounts. A rerun's success is the head's answer;
// a later failure is, too.
func TestLaterCIAttemptIsTheOneThatCounts(t *testing.T) {
	head := strings.Repeat("d", 40)
	runs := []CheckRun{
		{Name: ciOK, Status: "completed", Conclusion: "failure", HeadSHA: head, CompletedAt: "2026-09-22T00:00:00Z"},
		{Name: ciOK, Status: "completed", Conclusion: "success", HeadSHA: head, CompletedAt: "2026-09-22T01:00:00Z"},
	}
	if got := ciCheck(PR{Head: head, Checks: runs}); got.Result != Yes {
		t.Fatalf("later success = %s (%s), want yes", got.Result, got.Reason)
	}
	runs[0].CompletedAt, runs[1].CompletedAt = runs[1].CompletedAt, runs[0].CompletedAt
	got := ciCheck(PR{Head: head, Checks: runs})
	if got.Result != No || !strings.Contains(got.Reason, "BOUNCE") || !strings.Contains(got.Reason, "ci-ok") {
		t.Fatalf("later failure = %s (%s), want BOUNCE naming ci-ok", got.Result, got.Reason)
	}
}

// TestInProgressCIOKDoesNotPass. An unfinished ci-ok is not missing: missing is
// neutral, and a score above pass_above would PASS while the head is still red
// or still running.
func TestInProgressCIOKDoesNotPass(t *testing.T) {
	head := strings.Repeat("c", 40)
	got := ciCheck(PR{Head: head, Checks: []CheckRun{{
		Name: ciOK, Status: "in_progress", HeadSHA: head, StartedAt: "2026-09-22T00:00:00Z",
	}}})
	if got.Result != No || !strings.Contains(got.Reason, "still in_progress") || !strings.Contains(got.Reason, "BOUNCE") {
		t.Fatalf("in-progress = %s (%s), want BOUNCE", got.Result, got.Reason)
	}
	if v, why := tuningWithCI().Decide(checksClearExceptCI(got), 8, true); v != Bounce {
		t.Fatalf("in-progress at score 8: verdict=%s (%s), want BOUNCE", v, why)
	}
	if v, why := DefaultTuning().Decide(checksClearExceptCI(got), 8, true); v != Pass {
		t.Fatalf("in-progress with ci off at score 8: verdict=%s (%s), want PASS", v, why)
	}
}

// TestCIIsKnownAndOffByDefault. checks_enabled can name ci, and a default run
// does not enable it: schema has no ci-ok job. ci still prints before the score.
func TestCIIsKnownAndOffByDefault(t *testing.T) {
	saw := false
	for _, n := range CheckNames {
		if n == "ci" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("ci is not in CheckNames, so checks_enabled cannot name it")
	}
	for _, n := range DefaultChecks {
		if n == "ci" {
			t.Fatal("ci is in the default checks_enabled list")
		}
	}
	if DefaultTuning().Enabled["ci"] {
		t.Fatal("default tuning enables ci")
	}
	en, err := ParseEnabled(strings.Join(DefaultChecks, ","))
	if err != nil {
		t.Fatal(err)
	}
	if en["ci"] {
		t.Fatal("parsing the default list enables ci")
	}
	on, err := ParseEnabled("donewhen,selfcheck,paths,claims,ci,score")
	if err != nil || !on["ci"] {
		t.Fatalf("ParseEnabled with ci = %v %v, want ci on", on, err)
	}
	ciAt, scoreAt := -1, -1
	for i, n := range CheckNames {
		switch n {
		case "ci":
			ciAt = i
		case "score":
			scoreAt = i
		}
	}
	if ciAt < 0 || scoreAt < 0 || ciAt > scoreAt {
		t.Fatalf("CheckNames = %v, want ci before score", CheckNames)
	}
	var got []string
	for _, e := range (Checks{}).named() {
		got = append(got, e.name)
	}
	want := []string{"donewhen", "selfcheck", "paths", "claims", "ci"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("named = %v, want %v", got, want)
	}
}

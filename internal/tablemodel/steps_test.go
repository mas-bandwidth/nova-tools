package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

func names(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

func TestTableStepsPlanEachMode(t *testing.T) {
	t.Parallel()
	contracts := []string{"MCTableMachine", "MCTableFixedPoint"}
	witnesses := []string{"MCTableOnePlace", "MCTableBind", "MCTableBindRetained", "MCTableBoundAlias", "MCTableDropAlias"}
	controls := []string{"MCTableCrossTable"}
	tests := map[string][]string{
		"contracts": contracts,
		"witnesses": witnesses,
		"controls":  controls,
		"strict":    witnesses,
		"all":       append(append(append([]string{}, contracts...), witnesses...), controls...),
	}
	for mode, want := range tests {
		steps, err := TableSteps(mode)
		if err != nil || !reflect.DeepEqual(names(steps), want) {
			t.Errorf("%s: %v, %v; want %v", mode, names(steps), err, want)
		}
	}
	if _, err := TableSteps("everything"); err == nil {
		t.Error("an unknown mode was planned")
	}
}

func TestStrictModeExpectsTheFindingsToPassAndTheOthersExpectThemToFail(t *testing.T) {
	t.Parallel()
	strict, _ := TableSteps("strict")
	for _, s := range strict {
		if s.Kind != "pass" || s.Met != VerdictPass || s.Workers != 1 {
			t.Errorf("strict %s = %+v", s.Name, s)
		}
	}
	witnesses, _ := TableSteps("witnesses")
	want := map[string]string{
		"MCTableOnePlace": "OnePlacePerTable", "MCTableBind": "BindPreservesOwned", "MCTableBindRetained": "BindPreservesOwned",
		"MCTableBoundAlias": "CellWritesPreserveBoundSets", "MCTableDropAlias": "DropPreservesBound",
	}
	for _, s := range witnesses {
		if s.Kind != "invariant" || s.Property != want[s.Name] || s.Met != VerdictExpectedCounterexample {
			t.Errorf("witness %s = %+v", s.Name, s)
		}
	}
	all, _ := TableSteps("all")
	first, last := all[0], all[len(all)-1]
	if first.Workers != 2 || first.Kind != "pass" || last.Met != VerdictExpectedScopeControl || last.Property != "OneTablePerMember" {
		t.Errorf("first = %+v, last = %+v", first, last)
	}
	for _, s := range all {
		if s.Module != "MCTableMachine.tla" {
			t.Errorf("%s runs in %s", s.Name, s.Module)
		}
	}
}

func TestMemberStepsPlanEachSuite(t *testing.T) {
	t.Parallel()
	member := []string{"MCMemberTable", "MCMemberFixedPoint", "MCMemberBrokenMove", "MCMemberBrokenAlias"}
	epoch := []string{"MCEpochMemberTable", "MCEpochMemberFixedPoint", "MCEpochMemberBroken", "MCEpochMemberBrokenStale"}
	tests := map[string][]string{
		"all":    append(append([]string{}, member...), epoch...),
		"member": member,
		"epoch":  epoch,
		"small": {"MCMemberFixedPoint", "MCMemberBrokenMove", "MCMemberBrokenAlias",
			"MCEpochMemberFixedPoint", "MCEpochMemberBroken", "MCEpochMemberBrokenStale"},
	}
	for suite, want := range tests {
		steps, err := MemberSteps(suite, 4)
		if err != nil || !reflect.DeepEqual(names(steps), want) {
			t.Errorf("%s: %v, %v; want %v", suite, names(steps), err, want)
		}
	}
	steps, _ := MemberSteps("all", 3)
	for _, s := range steps {
		positive := s.Kind == "pass"
		if positive && (s.Workers != 3 || s.Met != VerdictPass) || !positive && (s.Workers != 1 || s.Met != VerdictExpectedMutationFailure) {
			t.Errorf("%s = %+v", s.Name, s)
		}
	}
	last := steps[len(steps)-1]
	if last.Kind != "action" || last.Property != "StaleWritesRefuse" || last.Module != "MCEpochMemberTable.tla" {
		t.Errorf("last = %+v", last)
	}
	if _, err := MemberSteps("nothing", 4); err == nil {
		t.Error("an unknown suite was planned")
	}
	if _, err := MemberSteps("all", 0); err == nil {
		t.Error("zero workers were planned")
	}
}

func tlcLog(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "tlc", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// models is a directory of one module and one configuration.
func models(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"MCTableMachine.tla": "model\n", "MCTableOnePlace.cfg": "cfg\n", "notes.md": "not a model\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// scripted answers each run with a recorded output and exit status.
func scripted(t *testing.T, answers map[string]struct {
	code int
	log  string
}, seen *[]tlc.Run) tlc.Executor {
	return func(ctx context.Context, r tlc.Run, log string) int {
		*seen = append(*seen, r)
		a := answers[strings.TrimSuffix(r.Config, ".cfg")]
		if err := os.WriteFile(log, []byte(a.log), 0o644); err != nil {
			t.Error(err)
		}
		return a.code
	}
}

type answer = struct {
	code int
	log  string
}

func TestRunStepsHoldsEachStepToItsExpectation(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Name: "MCTableFixedPoint", Module: "MCTableMachine.tla", Workers: 2, Kind: "pass", Property: "-", Met: VerdictPass},
		{Name: "MCTableOnePlace", Module: "MCTableMachine.tla", Workers: 1, Kind: "invariant", Property: "OnePlacePerTable", Met: VerdictExpectedCounterexample},
		{Name: "MCEpochMemberBrokenStale", Module: "MCEpochMemberTable.tla", Workers: 1, Kind: "action", Property: "StaleWritesRefuse", Met: VerdictExpectedMutationFailure},
	}
	var seen []tlc.Run
	exec := scripted(t, map[string]answer{
		"MCTableFixedPoint":        {0, tlcLog(t, "pass.log")},
		"MCTableOnePlace":          {12, tlcLog(t, "invariant.log")},
		"MCEpochMemberBrokenStale": {13, tlcLog(t, "action.log")},
	}, &seen)
	src, dir := models(t), filepath.Join(t.TempDir(), "out")
	var reported []StepResult
	results, err := RunSteps(steps, StepOptions{Jar: "/j.jar", Java: "java", Models: src, Dir: dir, Budget: time.Minute, Exec: exec,
		OnStep: func(r StepResult) { reported = append(reported, r) }})
	if err != nil || len(results) != 3 || len(reported) != 3 {
		t.Fatalf("results = %v, %v", results, err)
	}
	for i, want := range []string{VerdictPass, VerdictExpectedCounterexample, VerdictExpectedMutationFailure} {
		if results[i].Verdict != want || !results[i].OK {
			t.Errorf("step %d verdict = %s ok=%v, want %s", i, results[i].Verdict, results[i].OK, want)
		}
	}
	if results[0].Outcome.Generated != "15518" || results[1].Outcome.Distinct != "3" {
		t.Errorf("outcomes = %+v, %+v", results[0].Outcome, results[1].Outcome)
	}
	// Two workers for the positive case, one for a control; the private copy of
	// the models holds the modules and configurations and nothing else.
	if seen[0].Workers != 2 || seen[1].Workers != 1 || seen[0].LnCheckFinal || seen[0].NoDeadlock {
		t.Errorf("runs = %+v", seen)
	}
	if !reflect.DeepEqual(seen[0].JVM, []string{"-XX:+UseParallelGC", "-Xmx2g"}) || seen[0].Config != "MCTableFixedPoint.cfg" {
		t.Errorf("run = %+v", seen[0])
	}
	if seen[0].Dir != filepath.Join(dir, "work") {
		t.Errorf("TLC ran in %s", seen[0].Dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "MCTableMachine.tla")); err != nil {
		t.Error("the model was not copied")
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "notes.md")); err == nil {
		t.Error("a file that is not a model was copied")
	}
	if _, err := os.Stat(seen[0].TmpDir); err == nil {
		t.Error("a temporary directory was left behind")
	}
}

func TestRunStepsStopsAtTheFirstStepThatIsNotAsExpected(t *testing.T) {
	t.Parallel()
	steps, _ := TableSteps("contracts")
	var seen []tlc.Run
	// The model is expected to pass; TLC found a violation.
	exec := scripted(t, map[string]answer{"MCTableMachine": {12, tlcLog(t, "invariant.log")}}, &seen)
	results, err := RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: time.Minute, Exec: exec})
	if err != nil || len(results) != 1 || len(seen) != 1 {
		t.Fatalf("results = %d, ran = %d, %v", len(results), len(seen), err)
	}
	if r := results[0]; r.OK || r.Verdict != VerdictFail || r.Code != 12 {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunStepsCallsATimeoutATimeoutAndNeverAResult(t *testing.T) {
	t.Parallel()
	steps, _ := TableSteps("contracts")
	var seen []tlc.Run
	exec := scripted(t, map[string]answer{"MCTableMachine": {tlc.ExitTimeout, "TLC suite budget exhausted during this case\n"}}, &seen)
	// A budget already over: the context is done when the step ends.
	results, err := RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: -time.Second, Exec: exec})
	if err != nil || len(results) != 1 {
		t.Fatalf("results = %v, %v", results, err)
	}
	if r := results[0]; r.OK || r.Verdict != VerdictTimeout {
		t.Fatalf("result = %+v", r)
	}
	// The same exit inside the budget is a failure of the case, not a timeout.
	results, _ = RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: time.Hour, Exec: exec})
	if r := results[0]; r.OK || r.Verdict != VerdictFail {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunStepsRefusesWhatAControlGetsWrong(t *testing.T) {
	t.Parallel()
	step := Step{Name: "MCTableOnePlace", Module: "MCTableMachine.tla", Workers: 1, Kind: "invariant", Property: "BindPreservesOwned", Met: VerdictExpectedCounterexample}
	tests := map[string]answer{
		"the wrong invariant":       {12, tlcLog(t, "invariant.log")},
		"the wrong exit":            {13, tlcLog(t, "invariant.log")},
		"a pass where a violation":  {0, tlcLog(t, "pass.log")},
		"a parse failure":           {150, tlcLog(t, "parsefail.log")},
		"a violation of an action":  {13, tlcLog(t, "action.log")},
		"no output at all":          {12, ""},
		"an unrelated failure exit": {1, tlcLog(t, "invariant.log")},
	}
	for name, a := range tests {
		var seen []tlc.Run
		exec := scripted(t, map[string]answer{"MCTableOnePlace": a}, &seen)
		results, err := RunSteps([]Step{step}, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: time.Hour, Exec: exec})
		if err != nil || len(results) != 1 || results[0].OK || !strings.Contains(results[0].Verdict, "fail") {
			t.Errorf("%s: %+v, %v", name, results, err)
		}
	}
}

package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		tassert.NoError(t, err, "%s: %v, %v; want %v", mode, names(steps), err, want)
		tassert.Equal(t, want, names(steps), "%s: %v, %v; want %v", mode, names(steps), err, want)
	}
	_, err := TableSteps("everything")
	tassert.Error(t, err, "an unknown mode was planned")
}

func TestStrictModeExpectsTheFindingsToPassAndTheOthersExpectThemToFail(t *testing.T) {
	t.Parallel()
	strict, _ := TableSteps("strict")
	for _, s := range strict {
		tassert.Equal(t, "pass", s.Kind, "strict %s = %+v", s.Name, s)
		tassert.Equal(t, VerdictPass, s.Met, "strict %s = %+v", s.Name, s)
		tassert.Equal(t, 1, s.Workers, "strict %s = %+v", s.Name, s)
	}
	witnesses, _ := TableSteps("witnesses")
	want := map[string]string{
		"MCTableOnePlace": "OnePlacePerTable", "MCTableBind": "BindPreservesOwned", "MCTableBindRetained": "BindPreservesOwned",
		"MCTableBoundAlias": "CellWritesPreserveBoundSets", "MCTableDropAlias": "DropPreservesBound",
	}
	for _, s := range witnesses {
		tassert.Equal(t, "invariant", s.Kind, "witness %s = %+v", s.Name, s)
		tassert.Equal(t, want[s.Name], s.Property, "witness %s = %+v", s.Name, s)
		tassert.Equal(t, VerdictExpectedCounterexample, s.Met, "witness %s = %+v", s.Name, s)
	}
	all, _ := TableSteps("all")
	first, last := all[0], all[len(all)-1]
	tassert.Equal(t, 2, first.Workers, "first = %+v, last = %+v", first, last)
	tassert.Equal(t, "pass", first.Kind, "first = %+v, last = %+v", first, last)
	tassert.Equal(t, VerdictExpectedScopeControl, last.Met, "first = %+v, last = %+v", first, last)
	tassert.Equal(t, "OneTablePerMember", last.Property, "first = %+v, last = %+v", first, last)
	for _, s := range all {
		tassert.Equal(t, "MCTableMachine.tla", s.Module, "%s runs in %s", s.Name, s.Module)
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
		tassert.NoError(t, err, "%s: %v, %v; want %v", suite, names(steps), err, want)
		tassert.Equal(t, want, names(steps), "%s: %v, %v; want %v", suite, names(steps), err, want)
	}
	steps, _ := MemberSteps("all", 3)
	for _, s := range steps {
		if s.Kind == "pass" {
			tassert.Equal(t, 3, s.Workers, "%s = %+v", s.Name, s)
			tassert.Equal(t, VerdictPass, s.Met, "%s = %+v", s.Name, s)
		} else {
			tassert.Equal(t, 1, s.Workers, "%s = %+v", s.Name, s)
			tassert.Equal(t, VerdictExpectedMutationFailure, s.Met, "%s = %+v", s.Name, s)
		}
	}
	last := steps[len(steps)-1]
	tassert.Equal(t, "action", last.Kind, "last = %+v", last)
	tassert.Equal(t, "StaleWritesRefuse", last.Property, "last = %+v", last)
	tassert.Equal(t, "MCEpochMemberTable.tla", last.Module, "last = %+v", last)
	_, err := MemberSteps("nothing", 4)
	tassert.Error(t, err, "an unknown suite was planned")
	_, err = MemberSteps("all", 0)
	tassert.Error(t, err, "zero workers were planned")
}

func tlcLog(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "tlc", name))
	require.NoError(t, err)
	return string(raw)
}

// models is a directory of one module and one configuration.
func models(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"MCTableMachine.tla": "model\n", "MCTableOnePlace.cfg": "cfg\n", "notes.md": "not a model\n"} {
		err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644)
		require.NoError(t, err)
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
		err := os.WriteFile(log, []byte(a.log), 0o644)
		tassert.NoError(t, err)
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
	require.NoError(t, err, "results = %v, %v", results, err)
	require.Len(t, results, 3, "results = %v, %v", results, err)
	require.Len(t, reported, 3, "results = %v, %v", results, err)
	for i, want := range []string{VerdictPass, VerdictExpectedCounterexample, VerdictExpectedMutationFailure} {
		tassert.Equal(t, want, results[i].Verdict, "step %d verdict = %s ok=%v, want %s", i, results[i].Verdict, results[i].OK, want)
		tassert.True(t, results[i].OK, "step %d verdict = %s ok=%v, want %s", i, results[i].Verdict, results[i].OK, want)
	}
	tassert.Equal(t, "15518", results[0].Outcome.Generated, "outcomes = %+v, %+v", results[0].Outcome, results[1].Outcome)
	tassert.Equal(t, "3", results[1].Outcome.Distinct, "outcomes = %+v, %+v", results[0].Outcome, results[1].Outcome)
	// Two workers for the positive case, one for a control; the private copy of
	// the models holds the modules and configurations and nothing else.
	tassert.Equal(t, 2, seen[0].Workers, "runs = %+v", seen)
	tassert.Equal(t, 1, seen[1].Workers, "runs = %+v", seen)
	tassert.False(t, seen[0].LnCheckFinal, "runs = %+v", seen)
	tassert.False(t, seen[0].NoDeadlock, "runs = %+v", seen)
	tassert.Equal(t, []string{"-XX:+UseParallelGC", "-Xmx2g"}, seen[0].JVM, "run = %+v", seen[0])
	tassert.Equal(t, "MCTableFixedPoint.cfg", seen[0].Config, "run = %+v", seen[0])
	tassert.Equal(t, filepath.Join(dir, "work"), seen[0].Dir, "TLC ran in %s", seen[0].Dir)
	_, err = os.Stat(filepath.Join(dir, "work", "MCTableMachine.tla"))
	tassert.NoError(t, err, "the model was not copied")
	_, err = os.Stat(filepath.Join(dir, "work", "notes.md"))
	tassert.Error(t, err, "a file that is not a model was copied")
	_, err = os.Stat(seen[0].TmpDir)
	tassert.Error(t, err, "a temporary directory was left behind")
}

func TestRunStepsStopsAtTheFirstStepThatIsNotAsExpected(t *testing.T) {
	t.Parallel()
	steps, _ := TableSteps("contracts")
	var seen []tlc.Run
	// The model is expected to pass; TLC found a violation.
	exec := scripted(t, map[string]answer{"MCTableMachine": {12, tlcLog(t, "invariant.log")}}, &seen)
	results, err := RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: time.Minute, Exec: exec})
	require.NoError(t, err, "results = %d, ran = %d, %v", len(results), len(seen), err)
	require.Len(t, results, 1, "results = %d, ran = %d, %v", len(results), len(seen), err)
	require.Len(t, seen, 1, "results = %d, ran = %d, %v", len(results), len(seen), err)
	r := results[0]
	require.False(t, r.OK, "result = %+v", r)
	require.Equal(t, VerdictFail, r.Verdict, "result = %+v", r)
	require.Equal(t, 12, r.Code, "result = %+v", r)
}

func TestRunStepsCallsATimeoutATimeoutAndNeverAResult(t *testing.T) {
	t.Parallel()
	steps, _ := TableSteps("contracts")
	var seen []tlc.Run
	exec := scripted(t, map[string]answer{"MCTableMachine": {tlc.ExitTimeout, "TLC suite budget exhausted during this case\n"}}, &seen)
	// A budget already over: the context is done when the step ends.
	results, err := RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: -time.Second, Exec: exec})
	require.NoError(t, err, "results = %v, %v", results, err)
	require.Len(t, results, 1, "results = %v, %v", results, err)
	r := results[0]
	require.False(t, r.OK, "result = %+v", r)
	require.Equal(t, VerdictTimeout, r.Verdict, "result = %+v", r)
	// The same exit inside the budget is a failure of the case, not a timeout.
	results, _ = RunSteps(steps, StepOptions{Jar: "j", Java: "java", Models: models(t), Dir: t.TempDir(), Budget: time.Hour, Exec: exec})
	r = results[0]
	require.False(t, r.OK, "result = %+v", r)
	require.Equal(t, VerdictFail, r.Verdict, "result = %+v", r)
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
		tassert.NoError(t, err, "%s: %+v, %v", name, results, err)
		if tassert.Len(t, results, 1, "%s: %+v, %v", name, results, err) {
			tassert.False(t, results[0].OK, "%s: %+v, %v", name, results, err)
			tassert.Contains(t, results[0].Verdict, "fail", "%s: %+v, %v", name, results, err)
		}
	}
}

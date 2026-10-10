package decide

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go test's output, plain and -v, read into its failures: a top-level test and its first
// lines (a subtest's lines are its test's), its package from the FAIL line after it; a
// package that did not build, and a timeout naming its test under "running tests:"; an ok
// package between failures carries none of them; output that is no go test's has none.
func TestParseGateOutputReadsEachFailingTest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, out string
		want      []Failure
	}{
		{"plain, two packages", "--- FAIL: TestA (0.01s)\n    a_test.go:9: want 1, got 2\nFAIL\nFAIL\tm/p\t0.1s\nok  \tm/ok\t0.2s\n--- FAIL: TestB (0.00s)\n    --- FAIL: TestB/sub (0.00s)\n        b_test.go:3: boom\nFAIL\nFAIL\tm/q\t0.3s\nFAIL\n",
			[]Failure{{Pkg: "m/p", Test: "TestA", Lines: []string{"a_test.go:9: want 1, got 2"}}, {Pkg: "m/q", Test: "TestB", Lines: []string{"--- FAIL: TestB/sub (0.00s)", "b_test.go:3: boom"}}}},
		{"-v puts a test's lines after its RUN", "=== RUN   TestC\n    c_test.go:4: listen: address in use\n--- FAIL: TestC (0.02s)\n=== RUN   TestD\n--- PASS: TestD (0.00s)\nFAIL\nFAIL\tm/r\t0.1s\n",
			[]Failure{{Pkg: "m/r", Test: "TestC", Lines: []string{"c_test.go:4: listen: address in use"}}}},
		{"a build failure has no test", "# m/s\ns.go:3:2: undefined: x\nFAIL\tm/s [build failed]\n",
			[]Failure{{Pkg: "m/s", Lines: []string{"# m/s", "s.go:3:2: undefined: x"}}}},
		{"a timeout names its test", "panic: test timed out after 10m0s\nrunning tests:\n\tTestE (10m0s)\n\ngoroutine 1 [running]:\nFAIL\tm/t\t600.1s\n",
			[]Failure{{Pkg: "m/t", Test: "TestE", Lines: []string{"panic: test timed out after 10m0s"}}}},
		{"no go test output", "make: *** [lint] Error 1\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ParseGateOutput(tc.out))
		})
	}
	long := "--- FAIL: TestL (0s)\n" + strings.Repeat("    "+strings.Repeat("x", 400)+"\n", 20) + "FAIL\tm/l\t0.1s\n"
	f := ParseGateOutput(long)
	require.Len(t, f, 1)
	assert.Len(t, f[0].Lines, maxFailureLines, "a failure carries its first lines only")
	assert.Len(t, f[0].Lines[0], maxLineBytes+len("..."), "each cut")
}

// The gate schema is one choice, class, over the three classes, its criteria as
// SPEC-NOVA-DECIDE section 12 states them word for word, so a reworded criterion turns this
// red; its hash is the record's identity for the gate's calibration.
func TestGateSchemaIsValid(t *testing.T) {
	t.Parallel()
	s := GateSchema()
	assert.Empty(t, s.Problems())
	class := s.Questions["class"]
	require.Equal(t, []string{Caused, Flaky, PreExisting}, slices.Sorted(maps.Keys(class.Criteria)))
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for option, rule := range class.Criteria {
		assert.Contains(t, string(spec), "| `"+option+"` | "+rule+" |", "the spec's gate table states %s's criterion word for word", option)
	}
	assert.NotEqual(t, ReadSchema().Hash(), s.Hash())
}

// The gate bars are each a probability or empty, unset (the sprint row's default): an unset
// bar is reached by no probability, so its route is never taken and the decision is only
// recorded; two set bars sum above 1, so no failure meets both; every problem is named in
// one error.
func TestParseGateBarsNamesEveryProblem(t *testing.T) {
	t.Parallel()
	b, err := ParseGateBars("0.8", " 0.75 ")
	require.NoError(t, err)
	assert.Equal(t, GateBars{Flaky: 0.8, PreExisting: 0.75}, b)
	b, err = ParseGateBars("", "")
	require.NoError(t, err)
	assert.Equal(t, GateBars{Flaky: Unset, PreExisting: Unset}, b)
	assert.False(t, Set(b.Flaky))
	certain := Decision{Answers: map[string]Answer{"class": {Type: Choice, Value: Flaky, P: map[string]float64{Flaky: 1, PreExisting: 1}}}}
	assert.Equal(t, Caused, b.Route(certain), "an unset bar routes no failure, at p 1")
	b, err = ParseGateBars("0.6", "")
	require.NoError(t, err)
	assert.Equal(t, GateBars{Flaky: 0.6, PreExisting: Unset}, b, "one bar set alone")
	assert.Equal(t, Flaky, b.Route(certain))
	_, err = ParseGateBars("x", "1.2")
	assert.ErrorContains(t, err, `decide_gate_flaky "x" is not a decimal; decide_gate_preexisting 1.2 is not a probability in [0, 1]`)
	_, err = ParseGateBars("0.5", "0.5")
	assert.ErrorContains(t, err, "sum to at most 1, so one failure could meet both")
}

// classed answers the gate decision by the failure its state names: test -> p per class.
type classed struct {
	p    map[string]map[string]float64
	asks int
}

func (c *classed) Name() string { return "fake" }

func (c *classed) Ask(ctx context.Context, s Schema, state string) (map[string]Answer, Usage, error) {
	c.asks++
	first, _, _ := strings.Cut(strings.SplitN(state, "\n", 3)[1], " ")
	p := c.p[first]
	best := Caused
	for k, v := range p {
		if v > p[best] {
			best = k
		}
	}
	return map[string]Answer{"class": {Type: Choice, Value: best, P: p}}, Usage{InputTokens: len(state)}, nil
}

// Each failure is asked once, recorded under <op>/<pkg>.<Test>, and routed at the bars:
// flaky at or above the flaky bar, pre-existing at or above the pre-existing bar, else
// caused; a build failure is caused, unasked. The gate is caused when one failure is, else
// flaky when one is (those are rerun), else pre-existing. Asked again under the same op it
// is answered from the record. A gate with no failure decides nothing and has no route.
func TestGateRoutesEachFailureAtTheBars(t *testing.T) {
	t.Parallel()
	bars := GateBars{Flaky: 0.8, PreExisting: 0.8}
	fake := &classed{p: map[string]map[string]float64{
		"TestFlaky":   {Flaky: 0.85, Caused: 0.1, PreExisting: 0.05},
		"TestOld":     {Flaky: 0.05, Caused: 0.1, PreExisting: 0.85},
		"TestBorder":  {Flaky: 0.8, Caused: 0.1, PreExisting: 0.1},
		"TestUnsure":  {Flaky: 0.6, Caused: 0.3, PreExisting: 0.1},
		"TestWrecked": {Flaky: 0.05, Caused: 0.9, PreExisting: 0.05},
	}}
	f := func(test string) Failure {
		return Failure{Pkg: "m/p", Test: test, Lines: []string{"x_test.go:1: boom"}}
	}
	for _, tc := range []struct {
		name     string
		failures []Failure
		routes   []string
		gate     string
	}{
		{"all flaky", []Failure{f("TestFlaky"), f("TestBorder")}, []string{Flaky, Flaky}, Flaky},
		{"flaky and pre-existing", []Failure{f("TestFlaky"), f("TestOld")}, []string{Flaky, PreExisting}, Flaky},
		{"all pre-existing", []Failure{f("TestOld")}, []string{PreExisting}, PreExisting},
		{"under both bars is caused", []Failure{f("TestFlaky"), f("TestUnsure")}, []string{Flaky, Caused}, Caused},
		{"a build failure is caused, unasked", []Failure{f("TestOld"), {Pkg: "m/b"}}, []string{PreExisting, Caused}, Caused},
		{"caused", []Failure{f("TestWrecked")}, []string{Caused}, Caused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			record := filepath.Join(t.TempDir(), "gate.jsonl")
			fake := &classed{p: fake.p}
			in := GateInput{Failures: tc.failures, Paths: []string{"p/x.go"}}
			r, err := Gate(context.Background(), fake, bars, in, record, "c1@2@gate", at)
			require.NoError(t, err)
			var routes []string
			for _, c := range r.Calls {
				routes = append(routes, c.Route)
			}
			assert.Equal(t, tc.routes, routes)
			assert.Equal(t, tc.gate, r.Route)
			asked := fake.asks
			again, err := Gate(context.Background(), fake, bars, in, record, "c1@2@gate", at)
			require.NoError(t, err)
			assert.Equal(t, asked, fake.asks, "a recorded op asks nothing")
			assert.Equal(t, r.Route, again.Route)
			assert.True(t, again.Calls[0].Existing)
		})
	}
	record := filepath.Join(t.TempDir(), "gate.jsonl")
	none, err := Gate(context.Background(), fake, bars, GateInput{}, record, "c8@1@gate", at)
	require.NoError(t, err)
	assert.Equal(t, GateResult{}, none, "no failure: no decision and no route, never pre-existing")
	_, err = Gate(context.Background(), fake, bars, GateInput{Failures: []Failure{f("TestFlaky")}}, record, "c9@1@gate", at)
	require.NoError(t, err)
	ds, err := Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "c9@1@gate/m/p.TestFlaky", ds[0].ID)
	assert.Equal(t, GateName, ds[0].Decision)
	assert.Equal(t, GateSchema().Hash(), ds[0].Schema)
}

// The rerun's result is each flaky decision's outcome: green is flaky; red again is
// pre-existing when the test is red at the base, caused when it is green there, red-again
// when the base was not run. The gate after the rerun is caused when a rerun failure is red
// again, else pre-existing when one failure was routed so, else green; calibrate reads it.
func TestSettleGateAttachesTheRerunsResult(t *testing.T) {
	t.Parallel()
	bars := GateBars{Flaky: 0.8, PreExisting: 0.8}
	fake := &classed{p: map[string]map[string]float64{
		"TestA": {Flaky: 0.9, Caused: 0.05, PreExisting: 0.05},
		"TestB": {Flaky: 0.85, Caused: 0.1, PreExisting: 0.05},
		"TestC": {Flaky: 0.1, Caused: 0.05, PreExisting: 0.85},
	}}
	f := func(test string) Failure { return Failure{Pkg: "m/p", Test: test} }
	for _, tc := range []struct {
		name     string
		failures []Failure
		red      map[string]bool
		base     map[string]bool
		gate     string
		labels   []string
	}{
		{"the rerun passes", []Failure{f("TestA"), f("TestB")}, nil, map[string]bool{}, Green, []string{Flaky, Flaky}},
		{"passes, a pre-existing one stays", []Failure{f("TestA"), f("TestC")}, nil, map[string]bool{"m/p.TestC": true}, PreExisting, []string{Flaky, ""}},
		{"red again, green at the base", []Failure{f("TestA"), f("TestB")}, map[string]bool{"m/p.TestB": true}, map[string]bool{}, Caused, []string{Flaky, Caused}},
		{"red again, red at the base", []Failure{f("TestA")}, map[string]bool{"m/p.TestA": true}, map[string]bool{"m/p.TestA": true}, Caused, []string{PreExisting}},
		{"red again, the base not run", []Failure{f("TestA")}, map[string]bool{"m/p.TestA": true}, nil, Caused, []string{RedAgain}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &classed{p: fake.p}
			record := filepath.Join(t.TempDir(), "gate.jsonl")
			r, err := Gate(context.Background(), fake, bars, GateInput{Failures: tc.failures, BaseRed: tc.base}, record, "op", at)
			require.NoError(t, err)
			route, err := SettleGate(record, r, tc.red, tc.base, at)
			require.NoError(t, err)
			assert.Equal(t, tc.gate, route)
			ds, err := Load(record)
			require.NoError(t, err)
			var labels []string
			for _, d := range ds {
				label := ""
				if d.Outcome != nil {
					label = d.Outcome.Label
				}
				labels = append(labels, label)
			}
			assert.Equal(t, tc.labels, labels)
		})
	}
	record := filepath.Join(t.TempDir(), "gate.jsonl")
	for i, test := range []string{"TestA", "TestB"} {
		r, err := Gate(context.Background(), fake, bars, GateInput{Failures: []Failure{f(test)}}, record, "op"+string(rune('0'+i)), at)
		require.NoError(t, err)
		_, err = SettleGate(record, r, map[string]bool{"m/p.TestB": true}, map[string]bool{}, at)
		require.NoError(t, err)
	}
	ds, err := Load(record)
	require.NoError(t, err)
	cal, err := Calibrate(ds, GateName, "class="+Flaky, []string{Flaky}, []string{Caused, PreExisting, RedAgain})
	require.NoError(t, err)
	assert.Equal(t, 1.0, cal.AUC(), "TestA (0.9) passed its rerun, TestB (0.85) did not")
}

// A failure's state is the failure and its lines, the base, the gate's other failures, the
// card's PATHS and the diff's summary, each under its own heading; the card's PATHS line is
// read by the header's rule, and a diff is summarised per file with its lines added and
// removed, a rename as old -> new.
func TestGateStateHoldsTheFailureTheBaseAndTheCard(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/p/x.go b/p/x.go\n--- a/p/x.go\n+++ b/p/x.go\n@@ -1,2 +1,2 @@\n-a\n+b\n+c\n diff --git\ndiff --git a/p/old.go b/p/new.go\nsimilarity index 90%\nrename from p/old.go\nrename to p/new.go\n"
	in := GateInput{
		Failures: []Failure{{Pkg: "m/p", Test: "TestA", Lines: []string{"a_test.go:3: boom"}}, {Pkg: "m/q"}},
		BaseRed:  map[string]bool{"m/p.TestA": true},
		Paths:    CardPaths("REPO: x/y\nPATHS: p/x.go, p/old.go\nTEST: none\n\nThe task."),
		Diff:     DiffSummary(diff),
	}
	assert.Equal(t, "FAILURE (the test that failed at the card's head, and the first lines it printed):\nTestA in m/p\na_test.go:3: boom\n\n"+
		"AT THE BASE (the same test on the commit the card started from): red\n\n"+
		"OTHER FAILURES (the gate's other failing tests): m/q\n\n"+
		"CARD PATHS (the files the card may change): p/x.go, p/old.go\n\n"+
		"DIFF SUMMARY (the files the card changed, lines added and removed):\np/x.go +2 -1\np/old.go -> p/new.go +0 -0\n", GateState(in, 0))
	s := GateState(GateInput{Failures: in.Failures}, 1)
	assert.Contains(t, s, "the package m/q did not build or run\n")
	assert.Contains(t, s, "AT THE BASE (the same test on the commit the card started from): not run\n")
	assert.Contains(t, s, "CARD PATHS (the files the card may change): none named\n")
	assert.Contains(t, s, "DIFF SUMMARY (the files the card changed, lines added and removed):\nno change\n")
	assert.Nil(t, CardPaths("PATHS: none\n"))
	assert.Equal(t, "TestA, m/q", Names(in.Failures))
}

// A gate's classes are shown one per failure, <Test>:<class>:<p>, an unasked one so named.
func TestGateClassesShowEachDecision(t *testing.T) {
	t.Parallel()
	fake := &classed{p: map[string]map[string]float64{"TestA": {Flaky: 0.86, Caused: 0.1, PreExisting: 0.04}}}
	r, err := Gate(context.Background(), fake, GateBars{Flaky: Unset, PreExisting: Unset},
		GateInput{Failures: []Failure{{Pkg: "m/p", Test: "TestA"}, {Pkg: "m/b"}}}, filepath.Join(t.TempDir(), "g.jsonl"), "op", at)
	require.NoError(t, err)
	assert.Equal(t, "TestA:flaky:0.86,m/b:unasked", r.Classes())
	assert.Equal(t, Caused, r.Route, "unset bars route every failure caused: the take as reported")
}

// The calibration of 2026-10-03 (SPEC-NOVA-DECIDE section 12): 60 failing tests of the
// coordinator bench's CI runs of this repository (account and machine names replaced by
// generic ones before they were asked), each through Jev, labelled by git and the other runs
// (flaky: the same code passed in another run; pre-existing: red at the nearest ancestor
// run; caused: green there, and the change touches the package). Asked with the base run
// (the ancestor run's result, as the member runs the base), the class separates the
// outcomes, and at the default bars (0.8 and 0.8) no caused failure is routed flaky or
// pre-existing; asked with the base not run (the lander's case), pre-existing does not
// separate. The records are pinned to the schema's hash: a reworded criterion names the
// stale fixture, which is asked again in the same change.
func TestTheGateCalibrationRecordsSupportTheBars(t *testing.T) {
	t.Parallel()
	bars := GateBars{Flaky: 0.8, PreExisting: 0.8}
	auc := func(ds []Decision, class string) float64 {
		var neg []string
		for _, c := range []string{Flaky, Caused, PreExisting} {
			if c != class {
				neg = append(neg, c)
			}
		}
		cal, err := Calibrate(ds, GateName, "class="+class, []string{class}, neg)
		require.NoError(t, err)
		assert.Equal(t, 60, len(cal.Positives)+len(cal.Negatives))
		return float64(int(cal.AUC()*1000+0.5)) / 1000
	}
	for _, tc := range []struct {
		file                  string
		flaky, caused, preExi float64
	}{
		{"gate-calibration-base-run.jsonl", 0.716, 0.914, 0.907},
		{"gate-calibration-base-not-run.jsonl", 0.72, 0.8, 0.522},
	} {
		ds, err := Load(filepath.Join("testdata", tc.file))
		require.NoError(t, err)
		for _, d := range ds {
			require.Equal(t, GateSchema().Hash(), d.Schema, "%s holds %s under another schema: the gate schema changed, so ask the calibration again (cmd/nova-decide gate --backend jev) and replace the fixture", tc.file, d.ID)
		}
		assert.Equal(t, []float64{tc.flaky, tc.caused, tc.preExi}, []float64{auc(ds, Flaky), auc(ds, Caused), auc(ds, PreExisting)}, tc.file)
		if tc.file != "gate-calibration-base-run.jsonl" {
			continue
		}
		routed := map[string]int{}
		for _, d := range ds {
			if d.Outcome != nil {
				routed[d.Outcome.Label+"->"+bars.Route(d)]++
			}
		}
		assert.Equal(t, map[string]int{"caused->caused": 13, "pre-existing->pre-existing": 8, "flaky->flaky": 4, "flaky->pre-existing": 24, "flaky->caused": 11}, routed)
	}
}

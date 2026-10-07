package tablemodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/replay holds one real capture: trace.json as a bench wrote it, and
// the harness (and the configuration) that the original Python runner
// generated from that trace. The Go harness must be the same text, byte for
// byte, or TLC would be given something other than what was checked before.
func replayFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "replay", name))
	require.NoError(t, err)
	return raw
}

func loadTrace(t *testing.T) Trace {
	t.Helper()
	var tr Trace
	err := json.Unmarshal(replayFile(t, "trace.json"), &tr)
	require.NoError(t, err, "trace.json: %v", err)
	return tr
}

func harnessOf(tr Trace, mutate bool) string {
	observed := []State{tr.Initial.Clone()}
	steps := make([]string, len(tr.Steps))
	for i, s := range tr.Steps {
		observed = append(observed, s.State.Clone())
		steps[i] = s.Model
	}
	if mutate {
		observed[1].Place[0].Locations[0].Cell = NoPlace
	}
	return HarnessModule(observed, steps)
}

func TestHarnessIsTheModuleTheOriginalRunnerGenerated(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	require.Len(t, tr.Steps, 32, "the recorded trace has %d steps", len(tr.Steps))
	got, want := harnessOf(tr, false), string(replayFile(t, "MemberReceiptReplay.tla"))
	require.Equal(t, want, got, "the harness differs from the recorded one at byte %d", firstDifference(got, want))
	got, want = harnessOf(tr, true), string(replayFile(t, "MemberReceiptReplay.mutated.tla"))
	require.Equal(t, want, got, "the mutated harness differs from the recorded one at byte %d", firstDifference(got, want))
	gotCfg, wantCfg := HarnessConfig(string(replayFile(t, "MCEpochMemberTable.cfg")), len(tr.Steps)), string(replayFile(t, "MemberReceiptReplay.cfg"))
	require.Equal(t, wantCfg, gotCfg, "the harness configuration is\n%s\nwant\n%s", gotCfg, wantCfg)
}

func firstDifference(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func TestTheMutatedHarnessDiffersInExactlyOneObservedLink(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	a, b := strings.Split(harnessOf(tr, false), "\n"), strings.Split(harnessOf(tr, true), "\n")
	require.Equal(t, len(b), len(a), "line counts %d and %d", len(a), len(b))
	var differing []int
	for i := range a {
		if a[i] != b[i] {
			differing = append(differing, i)
		}
	}
	// Only the state after the first step is corrupted.
	require.Len(t, differing, 1, "lines differing: %v", differing)
	require.True(t, strings.HasPrefix(a[differing[0]], "<<{"), "lines differing: %v", differing)
}

func TestTheTraceRoundTripsThroughItsJSON(t *testing.T) {
	t.Parallel()
	raw, err := json.MarshalIndent(loadTrace(t), "", "  ")
	require.NoError(t, err)
	var before, after map[string]any
	err = json.Unmarshal(replayFile(t, "trace.json"), &before)
	require.NoError(t, err)
	err = json.Unmarshal(raw, &after)
	require.NoError(t, err)
	for _, key := range []string{"initial", "source_sha256", "model_sha256"} {
		tassert.Equal(t, before[key], after[key], "%s changed in the round trip", key)
	}
	steps, again := before["steps"].([]any), after["steps"].([]any)
	require.Equal(t, len(again), len(steps), "%d steps became %d", len(steps), len(again))
	// The arguments of a step are compared in the test of the actions: the
	// runner that wrote the fixture spaced the JSON inside them differently.
	for i := range steps {
		x, y := steps[i].(map[string]any), again[i].(map[string]any)
		for _, key := range []string{"verb", "actor", "refused", "receipt", "model", "state"} {
			tassert.Equal(t, x[key], y[key], "step %d: %s changed in the round trip", i, key)
		}
	}
	tassert.Contains(t, string(raw), `"refused": null`, "a step with no table call does not carry refused: null")
}

func TestActionsAreTheThirtyTwoOfTheTrace(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	actions := Actions()
	require.Equal(t, len(tr.Steps), len(actions), "%d actions, %d recorded steps", len(actions), len(tr.Steps))
	for i, a := range actions {
		s := tr.Steps[i]
		tassert.Equal(t, s.Verb, a.Verb, "action %d = %+v, recorded %s %v %s", i, a, s.Verb, s.Args, s.Actor)
		tassert.Equal(t, s.Actor, a.Actor, "action %d = %+v, recorded %s %v %s", i, a, s.Verb, s.Args, s.Actor)
		tassert.Equal(t, len(s.Args), len(a.Args), "action %d = %+v, recorded %s %v %s", i, a, s.Verb, s.Args, s.Actor)
	}
}

func TestActionTLARendersEachVerb(t *testing.T) {
	t.Parallel()
	cell := `<<<<"t1",1>>,"r1","c1">>`
	tests := []struct {
		a    Action
		want string
	}{
		{Action{"advance", nil, "w1"}, `Advance("w1")`},
		{Action{"read_epoch", nil, "w2"}, `ReadEpoch("w2")`},
		{Action{"clear", []string{"t1"}, "w2"}, `EpochClear("w2",<<"t1",1>>)`},
		{Action{"drop", []string{"t1"}, "w1"}, `EpochDrop("w1",<<"t1",1>>)`},
		{Action{"create", []string{"t1", "{}"}, "w1"}, `EpochCreate("w1",<<"t1",1>>)`},
		{Action{"row_del", []string{"t1", "r1"}, "w1"}, `EpochRowDelete("w1",<<"t1",1>>,"r1")`},
		{Action{"row_add", []string{"t1", "r1", "{}"}, "w1"}, `EpochRowAdd("w1",<<"t1",1>>,"r1",{})`},
		{Action{"row_add", []string{"t1", "r1", `{"binds":{"c2":"external"}}`}, "w1"},
			`EpochRowAdd("w1",<<"t1",1>>,"r1",{<<<<<<"t1",1>>,"r1","c2">>,<<<<"external",0>>,"external","external">>>>})`},
		{Action{"row_add", []string{"t1", "r2", `{"binds":{"c3":"table:t2:1:cell:r1:c1"}}`}, "w2"},
			`EpochRowAdd("w2",<<"t1",1>>,"r2",{<<<<<<"t1",1>>,"r2","c3">>,<<<<"t2",1>>,"r1","c1">>>>})`},
		{Action{"bind", []string{"t1", `{"rows":[{"key":"r2"}]}`}, "w1"}, `EpochBind("w1",<<"t1",1>>,{"r2"},{})`},
		{Action{"bind", []string{"t1", `{"rows":[]}`}, "w1"}, `EpochBind("w1",<<"t1",1>>,{},{})`},
		{Action{"cell_add", []string{"t1", "r1", "c1", "2", "m2"}, "w1"}, `EpochAdd("w1",` + cell + `,"m2",2)`},
		{Action{"cell_remove", []string{"t1", "r1", "c1", "m1"}, "w1"}, `EpochRemove("w1",` + cell + `,"m1")`},
		{Action{"cell_move", []string{"t1", "r1", "c1", "c2", "m1"}, "w1"}, `EpochMove("w1",` + cell + `,<<<<"t1",1>>,"r1","c2">>,"m1")`},
	}
	for _, tc := range tests {
		got, err := ActionTLA(tc.a, 1)
		tassert.NoError(t, err, "%s: %q, %v\nwant %q", tc.a.Verb, got, err, tc.want)
		tassert.Equal(t, tc.want, got, "%s: %q, %v\nwant %q", tc.a.Verb, got, err, tc.want)
	}
	got, _ := ActionTLA(Action{"clear", []string{"t2"}, "w1"}, 2)
	tassert.Equal(t, `EpochClear("w1",<<"t2",2>>)`, got, "the epoch is not the step's: %q", got)
}

func TestActionTLARefusesWhatItCannotMap(t *testing.T) {
	t.Parallel()
	for name, a := range map[string]Action{
		"an unmapped verb":          {"row_swap", []string{"t1", "r1", "r2", "c1"}, "w1"},
		"an unmapped binding":       {"row_add", []string{"t1", "r1", `{"binds":{"c1":"table:t9:1:cell:r1:c1"}}`}, "w1"},
		"an unmapped bind":          {"bind", []string{"t1", `{"rows":[{"key":"r1","binds":{"c1":"elsewhere"}}]}`}, "w1"},
		"options that are not JSON": {"row_add", []string{"t1", "r1", "{"}, "w1"},
		"rows that are not JSON":    {"bind", []string{"t1", "["}, "w1"},
		"a verb with no table":      {"clear", nil, "w1"},
		"a cell verb with too few":  {"cell_add", []string{"t1", "r1", "c1"}, "w1"},
	} {
		got, err := ActionTLA(a, 1)
		tassert.Error(t, err, "%s was rendered as %q", name, got)
	}
}

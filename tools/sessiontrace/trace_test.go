package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeStep is one committed write with its printed and durable receipt.
func writeStep(id, before, after string) step {
	a := action{Kind: "ok", Command: "put", Write: true}
	return step{Action: a,
		Stdout:   "TABLE RECEIPT event=" + id + " epoch=0 before=" + before + " after=" + after + " outcome=changed\n",
		Receipts: []receipt{{ID: id, Values: map[string]any{"rev_before": before, "rev_after": after, "actor": "trace"}}}}
}

// sixteen is a valid capture: one session per seed and keep, each one write.
func sixteen() []trace {
	var ts []trace
	for seed := int64(1); seed <= 8; seed++ {
		for _, keep := range []bool{false, true} {
			s := writeStep("1-0", "0", "1")
			ts = append(ts, trace{Seed: seed, Keep: keep, Actions: []action{s.Action}, Steps: []step{s}, EOF: true})
		}
	}
	return ts
}

// encode renders traces as the test2json stream readTraces reads, with an
// unrelated output line between records and a finish event.
func encode(t *testing.T, traces []trace, finish string, extra ...string) string {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	for _, tr := range traces {
		raw, err := json.Marshal(tr)
		require.NoError(t, err)
		require.NoError(t, enc.Encode(testEvent{Action: "output", Test: "T", Output: "    x_test.go:1: SESSION_TRACE " + string(raw) + "\n"}))
		require.NoError(t, enc.Encode(testEvent{Action: "output", Test: "T", Output: "=== RUN   T\n"}))
	}
	for _, line := range extra {
		require.NoError(t, enc.Encode(testEvent{Action: "output", Test: "T", Output: line + "\n"}))
	}
	if finish != "" {
		require.NoError(t, enc.Encode(testEvent{Action: finish}))
	}
	return out.String()
}

// TestReadTracesRefusesEachBadShape: every check of a captured session is a
// refusal naming its shape: a record that is not JSON, fewer than sixteen
// sessions, a seed outside 1..8, a session repeated, more steps than
// actions, a step whose action or exit code is not the plan's, and a first
// step that is not a write with one receipt. The valid capture reads back
// all sixteen with the raw rows.
func TestReadTracesRefusesEachBadShape(t *testing.T) {
	t.Parallel()

	mutate := func(f func(ts []trace)) []trace {
		ts := sixteen()
		f(ts)
		return ts
	}
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"valid", encode(t, sixteen(), "pass"), ""},
		{"bad json record", encode(t, sixteen(), "pass", "    x_test.go:2: SESSION_TRACE {not json"), "invalid SESSION_TRACE record"},
		{"fifteen sessions", encode(t, sixteen()[:15], "pass"), "wanted all 16 bounded sessions, got 15"},
		{"seed out of range", encode(t, mutate(func(ts []trace) { ts[0].Seed = 9 }), "pass"), "duplicate or unexpected session 9/false"},
		{"seed zero", encode(t, mutate(func(ts []trace) { ts[0].Seed = 0 }), "pass"), "duplicate or unexpected session 0/false"},
		{"repeated session", encode(t, mutate(func(ts []trace) { ts[1].Keep = false }), "pass"), "duplicate or unexpected session 1/false"},
		{"more steps than actions", encode(t, mutate(func(ts []trace) { ts[0].Steps = append(ts[0].Steps, ts[0].Steps[0]) }), "pass"), "incomplete session 1/false"},
		{"no steps", encode(t, mutate(func(ts []trace) { ts[0].Steps = nil }), "pass"), "incomplete session 1/false"},
		{"step off the plan", encode(t, mutate(func(ts []trace) { ts[0].Steps[0].Action.Command = "other" }), "pass"), "invalid observation in session 1/false step 0"},
		{"exit code 3", encode(t, mutate(func(ts []trace) { ts[0].Steps[0].Code = 3 }), "pass"), "invalid observation in session 1/false step 0"},
		{"first step not a write", encode(t, mutate(func(ts []trace) { ts[0].Actions[0].Write = false; ts[0].Steps[0].Action.Write = false }), "pass"), "session 1/false lacks its initial write witness"},
		{"first write with two receipts", encode(t, mutate(func(ts []trace) {
			ts[0].Steps[0].Receipts = append(ts[0].Steps[0].Receipts, ts[0].Steps[0].Receipts[0])
		}), "pass"), "lacks its initial write witness"},
		{"a failed test event", encode(t, sixteen(), "fail"), "execution test failed or skipped"},
		{"no pass event", encode(t, sixteen(), ""), "execution test did not pass"},
		{"not a test event stream", "{\"Action\": 5}", "invalid test event"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			traces, raw, err := readTraces(strings.NewReader(tc.input))
			if tc.want == "" {
				require.NoError(t, err)
				assert.Len(t, traces, 16)
				assert.Len(t, raw, 16)
				assert.Equal(t, int64(1), traces[0].Seed)
				return
			}
			assert.ErrorContains(t, err, tc.want)
			assert.Nil(t, traces)
			assert.Nil(t, raw)
		})
	}
}

// TestCheckReceiptsEveryMismatch: a write has one durable receipt and one
// printed line whose event is the durable id; a lost write prints nothing
// but is still durable; a non-write has neither; the receipt's revisions run
// from 0 and its actor is trace; an id seen twice is the duplicate error.
func TestCheckReceiptsEveryMismatch(t *testing.T) {
	t.Parallel()

	lost := writeStep("2-0", "1", "2")
	lost.Action.Lost = true
	lost.Stdout = ""
	read := step{Action: action{Kind: "ok", Command: "get"}, Stdout: "value\n"}
	cases := []struct {
		name  string
		steps []step
		want  string
	}{
		{"write, read, lost write", []step{writeStep("1-0", "0", "1"), read, lost}, ""},
		{"lost write that printed", func() []step {
			s := lost
			s.Stdout = "TABLE RECEIPT event=2-0 \n"
			return []step{writeStep("1-0", "0", "1"), s}
		}(), "printed receipt count differs"},
		{"write without a durable receipt", func() []step { s := writeStep("1-0", "0", "1"); s.Receipts = nil; return []step{s} }(), "receipt count does not match committed effects"},
		{"read with a durable receipt", func() []step {
			s := read
			s.Receipts = writeStep("1-0", "0", "1").Receipts
			return []step{writeStep("1-0", "0", "1"), s}
		}(), "receipt count does not match committed effects"},
		{"write that printed twice", func() []step { s := writeStep("1-0", "0", "1"); s.Stdout += s.Stdout; return []step{s} }(), "printed receipt count differs"},
		{"wrong revision", []step{writeStep("1-0", "0", "1"), writeStep("2-0", "5", "6")}, "receipt revision or actor differs"},
		{"wrong actor", func() []step { s := writeStep("1-0", "0", "1"); s.Receipts[0].Values["actor"] = "me"; return []step{s} }(), "receipt revision or actor differs"},
		{"printed id differs", func() []step {
			s := writeStep("1-0", "0", "1")
			s.Stdout = "TABLE RECEIPT event=9-9 x\n"
			return []step{s}
		}(), "printed receipt ID differs from durable event"},
		{"duplicate id across steps", []step{writeStep("1-0", "0", "1"), writeStep("1-0", "1", "2")}, errDuplicateReceipt.Error()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var acts []action
			for _, s := range tc.steps {
				acts = append(acts, s.Action)
			}
			err := checkReceipts([]trace{{Seed: 1, Actions: acts, Steps: tc.steps}})
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// TestModuleAndConfigurationFromTraces: the generated module carries one
// case per trace with its inputs, keep, exit, remaining actions, end (eof,
// quit or fail) and tick length; a usage or long line reads as code 2, a
// verb with exit 2 is a dead connection, and the observation pins the high
// exit so far; the configuration bounds MaxLines by the longest plan and
// names every invariant.
func TestModuleAndConfigurationFromTraces(t *testing.T) {
	t.Parallel()

	ok := action{Kind: "ok", Command: "put", Write: true}
	usage := action{Kind: "usage", Command: "?"}
	quit := action{Kind: "quit", Command: "quit"}
	no := action{Kind: "no", Command: "bad"}
	traces := []trace{
		{Seed: 1, Keep: true, Exit: 0, EOF: true, Actions: []action{ok, usage}, Steps: []step{{Action: ok}, {Action: usage, Code: 2}}},
		{Seed: 2, Keep: false, Exit: 2, Actions: []action{no, quit, ok}, Steps: []step{{Action: no, Code: 2}, {Action: quit}}},
		{Seed: 3, Keep: false, Exit: 1, Actions: []action{no}, Steps: []step{{Action: no, Code: 1}}},
	}
	m := module(traces)
	for _, want := range []string{
		"MODULE SessionTrace",
		`ObservedInputs == <<<<"ok", "usage">>, <<"no", "quit", "ok">>, <<"no">>>>`,
		"ObservedKeep == <<TRUE, FALSE, FALSE>>",
		"ObservedExit == <<0, 2, 1>>",
		"ObservedRemaining == <<0, 1, 0>>",
		`ObservedEnd == <<"eof", "quit", "fail">>`,
		// case 1: ReadLine, RunVerb, ReadLine (usage: no verb), EndOfInput = 4 ticks;
		// case 2: ReadLine, RunVerb (dead), ReadLine (quit: no verb) = 3; case 3: 2.
		"TraceLength == <<4, 3, 2>>",
		"traceCase \\in 1..3",
		"traceCase = 1 /\\ tick = 0\n     /\\ ReadLine /\\ lineCode' = 0",
		"traceCase = 1 /\\ tick = 1\n     /\\ RunVerb /\\ conn' = \"live\"",
		"traceCase = 1 /\\ tick = 2\n     /\\ ReadLine /\\ lineCode' = 2",
		"traceCase = 1 /\\ tick = 3\n     /\\ EndOfInput /\\ UNCHANGED lineCode",
		"traceCase = 2 /\\ tick = 1\n     /\\ RunVerb /\\ conn' = \"dead\"",
		"((traceCase = 1 /\\ tick = 2) => (exit = 0 /\\ lineCode = 0))",
		"((traceCase = 1 /\\ tick = 3) => (exit = 2 /\\ lineCode = 2))",
		"((traceCase = 2 /\\ tick = 2) => (exit = 2 /\\ lineCode = 2))",
		"((traceCase = 2 /\\ tick = 3) => (exit = 2 /\\ lineCode = 0))",
		"((traceCase = 3 /\\ tick = 2) => (exit = 1 /\\ lineCode = 1))",
	} {
		assert.Contains(t, m, want)
	}
	cfg := configuration(traces)
	assert.True(t, strings.HasPrefix(cfg, "SPECIFICATION TraceSpec\nCONSTANTS\n Inputs <- TraceInputs\n MaxLines = 3\n"))
	for _, inv := range []string{"TypeOK", "AtMostOnce", "ExitIsHighest", "TraceObserved", "TraceEnd"} {
		assert.Contains(t, cfg, " "+inv)
	}
	assert.Equal(t, "<<a, b>>", seq([]string{"a", "b"}))
	assert.Equal(t, "<<>>", seq(nil))
}

// TestCloneIsDeep: a clone shares no actions, steps, receipts or receipt
// values with the original, so a negative control cannot bend it.
func TestCloneIsDeep(t *testing.T) {
	t.Parallel()

	orig := sixteen()
	c := clone(orig)
	c[0].Actions[0].Kind = "x"
	c[0].Steps[0].Receipts[0].ID = "9-9"
	c[0].Steps[0].Receipts[0].Values["actor"] = "nobody"
	c[0].Steps = append(c[0].Steps, c[0].Steps[0])
	assert.Equal(t, "ok", orig[0].Actions[0].Kind)
	assert.Equal(t, "1-0", orig[0].Steps[0].Receipts[0].ID)
	assert.Equal(t, "trace", orig[0].Steps[0].Receipts[0].Values["actor"])
	assert.Len(t, orig[0].Steps, 1)
	assert.Empty(t, clone(nil))
}

// TestWriteJSONIsIndentedWithANewline: the file holds the indented value and
// ends in one newline; an unwritable path is the error.
func TestWriteJSONIsIndentedWithANewline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	require.NoError(t, writeJSON(path, map[string]any{"b": 1, "a": "x"}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"a\": \"x\",\n  \"b\": 1\n}\n", string(data))
	assert.Error(t, writeJSON(filepath.Join(dir, "missing", "result.json"), 1))
	assert.Error(t, writeJSON(path, func() {}), "an unmarshalable value is refused before any write")
}

// TestRepoRootFindsTheCheckout: run from the package directory, the root is
// the ancestor holding the TableSession model.
func TestRepoRootFindsTheCheckout(t *testing.T) {
	t.Parallel()

	root, err := repoRoot()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(root, "tla", "TableSession.tla"))
	assert.FileExists(t, filepath.Join(root, "tools", "sessiontrace", "main.go"))
}

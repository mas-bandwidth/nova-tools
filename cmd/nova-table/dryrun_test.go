package main

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// badKey is a row key with an ASCII control character, which internal/ntable
// refuses before it sends anything.
const badKey = "bad\x01key"

// writeRefusals is, for every verb that writes, one input it refuses: some
// are refused by the verb's own parsing, the rest only by the internal/ntable
// call's checks before its first command (marked store-free below), which a
// dry run must make as the real run does.
var writeRefusals = map[string][]string{
	"create":        {"create", "demo", "--columns", "ok,okpct:pct(ok/ok+failed)"},
	"set":           {"set", "demo", "--rename", "bad name"}, // store-free: ntable.Set
	"drop":          {"drop"},
	"clear":         {"clear", "a", "b"},
	"row add":       {"row", "add", "demo", badKey}, // store-free: ntable.RowAdd
	"row set":       {"row", "set", "demo", "build", "not-an-assignment"},
	"row hide":      {"row", "hide", "demo", badKey}, // store-free: ntable.RowsHide
	"row show":      {"row", "show", "demo", badKey}, // store-free: ntable.RowsHide
	"row del":       {"row", "del", "demo"},
	"row move":      {"row", "move", "demo", badKey, "--first"}, // store-free: ntable.Set
	"row order":     {"row", "order", "demo", badKey, "build"},  // store-free: ntable.Set
	"row sort":      {"row", "sort", "demo", "--manual", "--keep"},
	"col add":       {"col", "add", "demo", "a:rows"},
	"col del":       {"col", "del", "demo"},
	"col move":      {"col", "move", "demo", "done", "--first", "--last"},
	"cell add":      {"cell", "add", "demo", "build", "ready", "m1", "--score", "x"},
	"cell remove":   {"cell", "remove", "demo", "build", "ready"},
	"cell move":     {"cell", "move", "demo", "build", "ready"},
	"member create": {"member", "create", "demo"},
	"view set":      {"view", "set", "work"},
	"view state":    {"view", "state", "work"},
	"view del":      {"view", "del"},
	"batch":         {"batch", "--actor", strings.Repeat("a", 1<<20), batchManifest}, // store-free: ntable.ApplyBatch's outgoing bytes
}

// batchManifest is the manifest batch's help shows.
const batchManifest = `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}`

// tableResult runs nova-table in process as a testkit.Result.
func tableResult(args ...string) testkit.Result {
	code, out, errout := runTable(args...)
	return testkit.Result{Code: code, Stdout: out, Stderr: errout}
}

// dryAgainstReal holds each case's --dry-run to its real run on testkit.DryRunAgrees (the
// same exit and status words, nothing written under the case's root) and then, finer than
// the status words, the same refusal line for line: the same stderr, no stdout, and no
// word of an unreachable store, so the refusal came before any dial. Each case's
// arguments end with --redis at an address under its root that holds no store; --dry-run
// goes last, as a person types it.
func dryAgainstReal(t *testing.T, cases map[string][]string) {
	t.Helper()
	nowhere := func(root string) string { return filepath.Join(root, "no-store.sock") }
	var dry []testkit.DryCase
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		dry = append(dry, testkit.DryCase{Name: name, Setup: func(_ *testing.T, root string) ([]string, []string) {
			return append(slices.Clone(cases[name]), "--redis", nowhere(root)), nil
		}})
	}
	testkit.DryRunAgrees(t, tableResult, dry)
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		t.Run(name+"/the same refusal", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			args := append(slices.Clone(cases[name]), "--redis", nowhere(root))
			real, dry := tableResult(args...), tableResult(append(args, "--dry-run")...)
			assert.NotEqualValues(t, 0, real.Code, "%q is refused", cases[name])
			assert.Equal(t, real, dry, "the refusal with and without --dry-run")
			if !slices.Contains(args, "--json") {
				assert.Empty(t, real.Stdout+dry.Stdout, "a refusal in lines is on stderr alone")
			}
			assert.NotContains(t, real.Stderr+real.Stdout, "unreachable", "the refusal comes before any dial")
		})
	}
}

// TestADryRunIsTheRealRunsOwnPlan: for every verb that writes, a bad input is refused in
// the same words and with the same exit with --dry-run and without it (dryAgainstReal),
// and the help's example under --dry-run prints its plan, naming the command it would
// send; the address given holds no store, so a run that dialled would be answered with
// the unreachable store instead (and a dry run that dialled could not say dialled=0).
func TestADryRunIsTheRealRunsOwnPlan(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	var writes []string
	for _, c := range commands {
		if strings.HasPrefix(effectOf(c.name), "store write") && c.name != "shell" {
			writes = append(writes, c.name)
		}
	}
	require.ElementsMatch(t, writes, slices.Collect(maps.Keys(writeRefusals)), "every verb that writes has a refusal row, and no row names another verb")
	dryAgainstReal(t, writeRefusals)
	for _, c := range commands {
		if !slices.Contains(writes, c.name) {
			continue
		}
		t.Run(c.name+"/the example's plan", func(t *testing.T) {
			t.Parallel()
			lines := strings.Split(c.example, "\n")
			words, err := shellWords(lines[len(lines)-1])
			require.NoError(t, err)
			args := append(words[1:], "--redis", nowhere)
			if !slices.Contains(args, "--dry-run") {
				args = append(args, "--dry-run")
			}
			code, out, errout := runTable(args...)
			require.EqualValues(t, 0, code, "%v: %q", args, errout)
			assert.Empty(t, errout)
			assert.True(t, strings.HasPrefix(out, "TABLE DRY-RUN verb="+strings.Join(strings.Fields(c.name), "-")+" "), "%q", out)
			assert.Contains(t, out, ` sends="FCALL ns_`, "the plan names the command the real run sends first")
			assert.Contains(t, out, " redis="+nowhere+" dialled=0 written=0\n")
			assert.Equal(t, 1, strings.Count(out, "\n"), "%q", out)
		})
	}
	t.Run("no address", func(t *testing.T) {
		t.Parallel()
		app := &application{getenv: func(string) string { return "" }}
		var out, errout strings.Builder
		code := app.dispatch([]string{"cell", "move", "demo", "build", "ready", "done", "b2", "--dry-run", "--redis", ""}, &out, &errout)
		require.EqualValues(t, 0, code, "%q", errout.String())
		assert.Equal(t, "TABLE DRY-RUN verb=cell-move arg1=demo arg2=build arg3=ready arg4=done arg5=b2 sends=\"FCALL ns_table_cell_move\" redis=- dialled=0 written=0\n", out.String())
	})
}

// TestABatchDryRunMakesTheEncodedRequestsChecks: a manifest that passes the raw input's
// checks and grows past the manifest bound only when it is encoded for sending (an actor
// of 200000 '<', escaped six bytes each; an actor added by --actor to a manifest naming
// none) is refused LIMIT by the dry run as by the real run (dryAgainstReal), in the lines
// and in JSON, before any dial.
func TestABatchDryRunMakesTheEncodedRequestsChecks(t *testing.T) {
	t.Parallel()
	escaped := strings.Replace(batchManifest, `"members"`, `"actor":"`+strings.Repeat("<", 200000)+`","members"`, 1)
	cases := map[string][]string{
		"an actor that grows when encoded": {"batch", escaped},
		"an actor added by --actor":        {"batch", "--actor", strings.Repeat("a", 1<<20), batchManifest},
	}
	for name, args := range maps.Clone(cases) {
		cases[name+", --json"] = append(slices.Clone(args), "--json")
	}
	dryAgainstReal(t, cases)
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for name, args := range cases {
		t.Run(name+"/LIMIT", func(t *testing.T) {
			t.Parallel()
			dry := tableResult(append(slices.Clone(args), "--redis", nowhere, "--dry-run")...)
			assert.EqualValues(t, 1, dry.Code, "%+v", dry)
			if slices.Contains(args, "--json") {
				assert.Contains(t, dry.Stdout, `"status":"refused"`)
				assert.Contains(t, dry.Stdout, "manifest bytes")
			} else {
				assert.Contains(t, dry.Stderr, "BATCH REFUSED: ")
				assert.Contains(t, dry.Stderr, "manifest bytes")
			}
		})
	}
}

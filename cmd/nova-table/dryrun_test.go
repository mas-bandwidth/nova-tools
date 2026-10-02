package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
}

// batchManifest is the manifest batch's help shows.
const batchManifest = `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}`

// TestADryRunIsTheRealRunsOwnPlan: for every verb that writes, a bad input is
// refused in the same words and with the same exit with --dry-run and without
// it, and the help's example under --dry-run prints its plan, naming the
// command it would send; the address given holds no store, so a run that
// dialled would be answered with the unreachable store instead (and a dry run
// that dialled could not say dialled=0).
func TestADryRunIsTheRealRunsOwnPlan(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	var writes []string
	for _, c := range commands {
		if strings.HasPrefix(effectOf(c.name), "store write") && c.name != "shell" && c.name != "batch" {
			writes = append(writes, c.name)
		}
	}
	require.Len(t, writes, len(writeRefusals), "every verb that writes has a refusal row, and no row names another verb")
	for _, c := range commands {
		if !slices.Contains(writes, c.name) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bad, ok := writeRefusals[c.name]
			require.True(t, ok, "no refusal row for %s", c.name)
			real, realOut, realErr := runTable(append(slices.Clone(bad), "--redis", nowhere)...)
			dry, dryOut, dryErr := runTable(append(slices.Clone(bad), "--redis", nowhere, "--dry-run")...)
			assert.Empty(t, realOut)
			assert.Empty(t, dryOut)
			assert.NotEqualValues(t, 0, real, "%q is refused", bad)
			assert.Equal(t, real, dry, "the exit with and without --dry-run")
			assert.Equal(t, realErr, dryErr, "the refusal with and without --dry-run")
			assert.NotContains(t, realErr, "unreachable", "the refusal comes before any dial")

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
			if c.name != "batch" {
				assert.Contains(t, out, ` sends="FCALL ns_`, "the plan names the command the real run sends first")
			}
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

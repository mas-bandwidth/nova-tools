package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrongFlagNamesTheVerbsActualFlags(t *testing.T) {
	t.Parallel()
	code, out, errout := runTable("view", "set", "work", "--summry", "done")
	for _, want := range []string{"unknown flag --summry", "view set flags:", "--summary", "--tables", "run: nova-table help view set"} {
		assert.Contains(t, errout, want, "missing %q in %q", want, errout)
	}
	require.EqualValues(t, 2, code, "%d %q %q", code, out, errout)
	require.Empty(t, out, "%d %q %q", code, out, errout)
	require.NotContains(t, errout, "flag provided but not defined", "%d %q %q", code, out, errout)
	require.EqualValues(t, 1, strings.Count(errout, "\n"), "%d %q %q", code, out, errout)
}

func TestHelpExplainsColumnDefaultsAndDeletion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "create"}, "pct defaults to the pooled fold, sum to the sum fold"},
		{[]string{"help"}, "pct(<count-column>/<a>+<b>) (the share of the named count columns a, b of the\nrow), or sum(<a>+<b>)"},
		{[]string{"help", "drop"}, "the identity hash and the rows of every epoch; keep the definition snapshots of earlier epochs"},
		{[]string{"help", "row", "del"}, "missing row succeeds with existed=0"},
		{[]string{"help", "render"}, "--view"},
	} {
		code, out, errout := runTable(tc.args...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", tc.args, code, out, errout)
		require.Empty(t, errout, "%v: %d %q %q", tc.args, code, out, errout)
		require.Contains(t, out, tc.want, "%v: %d %q %q", tc.args, code, out, errout)
	}
}

func TestEndFlagsKeepsAllRemainingPositionals(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"t", "--redis", "socket", "r", "c", "--", "--first", "--second"},
		{"--redis", "socket", "--", "t", "r", "c", "--first", "--second"},
		{"--label", "--", "--receipt", "--redis=socket", "--", "t", "r", "c", "--first", "--second"},
	} {
		fs := verbflag.New("cell remove")
		addr := fs.String("redis", "", "address")
		label := fs.String("label", "--", "label")
		fs.Bool("receipt", false, "receipt")
		pos, err := parseInterleaved(fs, args)
		require.NoError(t, err, "%v: %q %s %s %v", args, pos, *addr, *label, err)
		require.Equal(t, "socket", *addr, "%v: %q %s %s %v", args, pos, *addr, *label, err)
		require.Equal(t, "--", *label, "%v: %q %s %s %v", args, pos, *addr, *label, err)
		require.Equal(t, "t,r,c,--first,--second", strings.Join(pos, ","), "%v: %q %s %s %v", args, pos, *addr, *label, err)
	}
}

// TestCreateRefusesAFormulaOfNoCountColumn: a named share or a sum whose
// columns are not count columns of the table is refused before any store is
// dialled, naming the column and the remedy.
func TestCreateRefusesAFormulaOfNoCountColumn(t *testing.T) {
	t.Parallel()
	for spec, want := range map[string]string{
		"ok,okpct:pct(ok/ok+failed)":           "wants a count column named failed in the same table; declare failed as a count column",
		"ok,note:text,done:sum(ok+note)":       "reads note, a text column; a formula reads count columns only",
		"ok,failed,done:sum(ok+failed):pooled": "folds pooled, which wants a pct column",
	} {
		code, out, errout := runTable("create", "t", "--columns", spec)
		require.EqualValues(t, 2, code, "%s: %d %q %q", spec, code, out, errout)
		require.Empty(t, out, "%s: %d %q %q", spec, code, out, errout)
		require.Contains(t, errout, want, "%s: %d %q %q", spec, code, out, errout)
	}
}

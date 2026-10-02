package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryCommandHasEquivalentDiscoverableHelp(t *testing.T) {
	t.Parallel()
	code, banner, errout := runTable("help")
	require.EqualValues(t, 0, code, "top help: %d %q", code, errout)
	require.Empty(t, errout, "top help: %d %q", code, errout)
	for _, c := range commands {
		assert.Contains(t, banner, "nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n", "not discoverable: %s", c.name)
		words := strings.Fields(c.name)
		code, want, errout := runTable(append([]string{"help"}, words...)...)
		require.EqualValues(t, 0, code, "help %s: %d %q %q", c.name, code, want, errout)
		require.Empty(t, errout, "help %s: %d %q %q", c.name, code, want, errout)
		require.Contains(t, want, "usage: nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n", "help %s: %d %q %q", c.name, code, want, errout)
		require.Contains(t, want, "example:\n  "+strings.ReplaceAll(c.example, "\n", "\n  ")+"\n", "help %s: %d %q %q", c.name, code, want, errout)
		lines := strings.Split(c.example, "\n")
		assert.Contains(t, lines[len(lines)-1], "nova-table "+c.name, "help %s: the verb's own example line is last: %q", c.name, c.example)
		for _, flag := range []string{"--help", "-h"} {
			code, got, errout := runTable(append(append([]string{}, words...), flag)...)
			assert.EqualValues(t, 0, code, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
			assert.Equal(t, want, got, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
			assert.Empty(t, errout, "%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
		}
	}
	for _, group := range []string{"row", "col", "cell", "member", "view"} {
		_, want, _ := runTable("help", group)
		for _, alias := range []string{"--help", "-h", "help"} {
			code, got, errout := runTable(group, alias)
			assert.EqualValues(t, 0, code, "%s %s: %d %q %q", group, alias, code, got, errout)
			assert.Equal(t, want, got, "%s %s: %d %q %q", group, alias, code, got, errout)
			assert.Empty(t, errout, "%s %s: %d %q %q", group, alias, code, got, errout)
		}
	}
	_, create, _ := runTable("help", "create")
	require.LessOrEqual(t, strings.Index(create, "--columns <string>"), strings.Index(create, "--actor <string>"), "%v", "receipt metadata precedes product flags")
	_, show, _ := runTable("help", "view", "show")
	require.NotContains(t, show, "--summary", "%v", "view show advertises view set flags")
	require.NotContains(t, show, "--title", "%v", "view show advertises view set flags")
}

// TestEveryVerbStatesItsEffectAndAWriteTakesADryRun: every verb's help ends
// with its effect (docs/STANDARD.md section 2), and a verb that is not an
// inspection lists --dry-run; the tool-answers walk reads the same two lines.
func TestEveryVerbStatesItsEffectAndAWriteTakesADryRun(t *testing.T) {
	t.Parallel()
	for _, c := range commands {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, out, errout := runTable(append(strings.Fields(c.name), "-h")...)
			require.EqualValues(t, 0, code, "%q", errout)
			effect := out[strings.LastIndex(out, "\neffect: ")+1:]
			require.True(t, strings.HasPrefix(effect, "effect: "), "help %s ends with no effect line:\n%s", c.name, out)
			if strings.HasPrefix(effect, "effect: inspection") {
				assert.NotContains(t, out, "  --dry-run", "an inspection takes no --dry-run")
				return
			}
			assert.True(t, strings.HasPrefix(effect, "effect: store write: "), "%s: %q", c.name, effect)
			assert.Contains(t, out, "\n  --dry-run  ", "%s writes and lists no --dry-run:\n%s", c.name, out)
		})
	}
}

// TestADryRunOfEveryWriteExampleDialsNothing: the example of every verb that
// writes, run with --dry-run against an address where no store is, exits 0
// with the one DRY-RUN line naming dialled=0 written=0; the address is never
// dialled, because a dial of it would refuse at exit 2. With no address at
// all the plan still runs, and says redis=-.
func TestADryRunOfEveryWriteExampleDialsNothing(t *testing.T) {
	t.Parallel()
	nowhere := t.TempDir() + "/no-store.sock"
	for _, c := range commands {
		if !strings.HasPrefix(effectOf(c.name), "store write") || c.name == "shell" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			lines := strings.Split(c.example, "\n")
			words, err := shellWords(lines[len(lines)-1])
			require.NoError(t, err)
			require.Equal(t, "nova-table", words[0])
			args := append(words[1:], "--redis", nowhere)
			if !slices.Contains(args, "--dry-run") {
				args = append(args, "--dry-run")
			}
			code, out, errout := runTable(args...)
			require.EqualValues(t, 0, code, "%v: %q", args, errout)
			assert.Empty(t, errout)
			assert.True(t, strings.HasPrefix(out, "TABLE DRY-RUN verb="+strings.Join(strings.Fields(c.name), "-")+" "), "%q", out)
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
		assert.Equal(t, "TABLE DRY-RUN verb=cell-move arg1=demo arg2=build arg3=ready arg4=done arg5=b2 redis=- dialled=0 written=0\n", out.String())
	})
	t.Run("a refusal still refuses", func(t *testing.T) {
		t.Parallel()
		code, out, errout := runTable("create", "t", "--columns", "ok,okpct:pct(ok/ok+failed)", "--dry-run", "--redis", nowhere)
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errout, "CREATE REFUSED: --columns: ")
	})
}

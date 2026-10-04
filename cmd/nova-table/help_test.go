package main

import (
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

	// The banner's three new lines are held to the behaviour they describe.
	// The dry-run create under example: runs as printed (it dials nothing, so
	// it runs in this tier; the --redis argument changes what it dials and
	// nothing it prints). The refusal shape pasted under usage is the line the
	// stale-epoch refusal prints, byte for byte, held against the store by the
	// functional tier; show and the receipt are the epoch's two sources.
	require.Contains(t, banner, "a throwaway store, by hand:", "the throwaway-store commands stand under no heading of their own:\n%s", banner)
	require.Contains(t, banner,
		"example: (the lines need the store the first run describes; this one runs with none)\n"+
			"  nova-table create demo --columns ready,working,done --dry-run\n",
		"the --dry-run create is not the line under example::\n%s", banner)
	code, out, errout := runTable("create", "demo", "--columns", "ready,working,done", "--dry-run", "--redis", t.TempDir()+"/no-store.sock")
	require.EqualValues(t, 0, code, "the example: line does not run as printed: %d %q %q", code, out, errout)
	require.Empty(t, errout, "the example: line does not run as printed: %d %q %q", code, out, errout)
	require.Contains(t, out, `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=`,
		"the example: line does not run as printed: %q", out)
	require.Contains(t, out, "dialled=0 written=0\n", "the example: line does not run as printed: %q", out)
	require.Contains(t, banner,
		"CELL-ADD REFUSED: requested epoch is stale, not the active epoch: requested 0, active 1; run: nova-table help cell add\n",
		"the stale refusal's shape is not pasted:\n%s", banner)
	require.Contains(t, banner,
		"read the epoch off show <table> (it prints epoch=<n>) or off the receipt of every write",
		"the epoch's two sources are not stated:\n%s", banner)
	require.Contains(t, banner, "a second cell add of the same member\n", "the retried write is not described:\n%s", banner)
	require.Contains(t, banner, "and a second row add rewrites the row and keeps its\n", "the retried write is not described:\n%s", banner)
	// The four usage lines that ran past 100 columns are wrapped.
	for _, want := range []string{
		"  nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>]\n      [--width <col=n,...>]\n",
		"  nova-table set <table> [--footer <label>] [--rename <name>] [--columns <spec>]\n      [--hide <cols>] [--show <cols>] [--hidden | --visible]\n",
		"  nova-table render <table> | --view <name> [--at-epoch <n>]\n      [--width <col=n,...>] [--label-width <n>]\n",
		"  nova-table watch <table>[,<table>...] | --view <name> [--every <duration>] [--out <file>]\n      [--title <text>] [--width <col=n,...>] [--label-width <n>] [--check] [--once]\n",
	} {
		require.Contains(t, banner, want, "the usage line is not wrapped at 100 columns:\n%s", banner)
	}
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

// TestTheBannerSaysWhatAFirstRunNeeds: the banner's first-run lines name the
// store a first run needs, the commands that start a throwaway one (and stop
// it), the environment that points every verb at it, and a line that runs
// with no store at all, which does: it exits 0 and dials nothing.
func TestTheBannerSaysWhatAFirstRunNeeds(t *testing.T) {
	t.Parallel()
	code, banner, _ := runTable("help")
	require.EqualValues(t, 0, code)
	head, _, ok := strings.Cut(banner, "\nusage:\n")
	require.True(t, ok)
	for _, want := range []string{
		"first run: needs a Redis 7 or later",
		"every verb that writes\nruns under --dry-run",
		`redis-server --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes`,
		`redis-cli -s "$d/redis.sock" shutdown nosave`,
		`unset NOVA_SEAT NOVA_SPRINT_SEAT NOVA_SPRINT_REDIS_USER; export NOVA_SPRINT_REDIS="$d/redis.sock"`,
		"refuses at exit 2 naming the address it tried",
	} {
		assert.Contains(t, head, want)
	}
	var dry string
	for _, l := range strings.Split(head, "\n") {
		if strings.HasPrefix(l, "  nova-table ") && strings.HasSuffix(l, "--dry-run") {
			dry = strings.TrimSpace(l)
		}
	}
	require.NotEmpty(t, dry, "the first run names no line that runs with no store")
	words, err := shellWords(dry)
	require.NoError(t, err)
	code, out, errout := runTable(append(words[1:], "--redis", t.TempDir()+"/no-store.sock")...)
	assert.EqualValues(t, 0, code, "%q", errout)
	assert.Contains(t, out, "TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done ")
}

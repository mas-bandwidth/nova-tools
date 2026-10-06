package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens the store it was pointed at (the CLI style's rule (b), #4505).
//
// The banner also states what --publish does, what a duplicate and a conflict
// print and exit, and the exit codes by verb. Every sentence here is RUN against
// the behaviour it describes, so the help and the tool cannot drift: a recorded
// policy word, an append's own word, the duplicate and conflict exits.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	store := []string{"--store", "{dir}/cairns", "--session", "s1"}
	testverbhelp.Check(t, cli.NoStdin(), []testverbhelp.Case{
		{Verb: "open", Flags: store},
		{Verb: "append", Flags: store},
		{Verb: "index", Flags: store},
		{Verb: "receipt", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli.NoStdin(), "nova-cairn", "open", "append", "version")

	help := cli.OK(t, "help").Stdout
	for _, want := range []string{
		"--publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append with no --publish carries the session's, and one that differs is a conflict naming both (exit 1).",
		"Same id, same words: duplicate (duplicate=true, exit 0); same id, other words: conflict (exit 1).",
		"exit codes: 0 done, 2 usage or could not run, for every verb; by verb:",
		"  open: 0 ",
		"  append: 0 ",
		"  index: 0 ",
		"  receipt: 0 ",
	} {
		require.Contains(t, help, want, "`nova-cairn help` no longer says %q", want)
	}

	dir := t.TempDir()
	// open records the policy; append with no --publish carries it, and one that
	// differs is a conflict naming both.
	require.Equal(t, 0, cli.Run("open", "--store", dir, "--session", "s1", "--publish", "manual").Code)
	r := cli.Run("append", "--store", dir, "--session", "s1", "--entry", "e1", "--text", "w")
	require.Equal(t, 0, r.Code)
	require.Contains(t, r.Stdout, "publish=manual", "an append with no --publish printed %q", r.Stdout)
	r = cli.Run("append", "--store", dir, "--session", "s1", "--entry", "e2", "--text", "w", "--publish", "immediate")
	require.Equal(t, 1, r.Code)
	require.Contains(t, r.Stderr, "holds publish=manual", "a differing append --publish did not name the session's: %q", r.Stderr)
	require.Contains(t, r.Stderr, "--publish immediate", "a differing append --publish did not name its own: %q", r.Stderr)
	// The same id and words is a duplicate at exit 0; other words a conflict at exit 1.
	r = cli.Run("append", "--store", dir, "--session", "s1", "--entry", "e1", "--text", "w", "--publish", "manual")
	require.Equal(t, 0, r.Code)
	require.Contains(t, r.Stdout, "duplicate=true", "the duplicate the help names prints duplicate=true")
	require.Equal(t, 1, cli.Run("append", "--store", dir, "--session", "s1", "--entry", "e1", "--text", "other").Code)
}

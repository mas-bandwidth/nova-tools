package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/require"
)

// Every named verb answers -h and --help with its own help on stdout at exit 0,
// and none of them probes, wraps, writes a worktree or a ruleset
// (the CLI style's rule (b), #4505). Only -h is in this rule: the bare wrap and its
// 125 are not verbs and are not touched.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, novaSandbox.NoStdin(), []testverbhelp.Case{
		{Verb: "check"},
		{Verb: "run", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "reap"},
		{Verb: "worktree", Flags: []string{"--repo", "{dir}/repo"}},
		{Verb: "egress"},
		{Verb: "egress plan", Flags: []string{"--out", "{dir}/egress.nft"}},
		{Verb: "egress apply"},
		{Verb: "egress check"},
		{Verb: "egress drop"},
		{Verb: "policy", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "probe", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, novaSandbox.NoStdin(), "nova-sandbox", "worktree", "egress plan", "probe", "version")
}

// help names verbs only: `help <anything else>` is the banner, never the bare
// wrap of a command called <anything else>.
func TestHelpNeverReachesTheBareWrap(t *testing.T) {
	t.Parallel()
	r := novaSandbox.Do(t, "help", "sh", "-c", "exit 7").Exit(0)
	require.True(t, strings.HasPrefix(r.Stdout, "nova-sandbox:"), r)
	require.Empty(t, r.Stderr, r)
}

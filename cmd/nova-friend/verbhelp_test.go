package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them dials the store it names (the CLI style's rule (b)).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	store := []string{"--redis", "{addr}"}
	cli := newRig(t).cli().NoStdin()
	testverbhelp.Check(t, cli, []testverbhelp.Case{
		{Verb: "run", Flags: store},
		{Verb: "install", Flags: store},
		{Verb: "uninstall"},
		{Verb: "check", Flags: store},
		{Verb: "ping", Flags: store},
		{Verb: "ping install", Flags: store},
		{Verb: "ping uninstall"},
		{Verb: "pong", Flags: store},
		{Verb: "wait-pong", Flags: store},
		{Verb: "status"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli, "nova-friend", "run", "install", "uninstall", "check", "ping", "ping install", "ping uninstall", "pong", "wait-pong", "status", "version")
}

func TestCommandReferenceNamesEveryKnownHarness(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	_, section, ok := strings.Cut(string(raw), "What a first run gets wrong: a `--harness`")
	require.True(t, ok)
	list, _, ok := strings.Cut(section, ";\na `pong --as`")
	require.True(t, ok)
	list = strings.Join(strings.Fields(list), " ")
	require.Contains(t, list, strings.Join(friend.Harnesses, ", "))
	require.Contains(t, list, "known but passive")
}

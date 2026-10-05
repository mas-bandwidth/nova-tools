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
		{Verb: "pong", Flags: store},
		{Verb: "wait-pong", Flags: store},
		{Verb: "status"},
		{Verb: "renew"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli, "nova-friend", "run", "install", "uninstall", "check", "ping", "pong", "wait-pong", "status", "renew", "version")
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

// renew's help is enough to use it cold, and docs/CLI.md says the same: every
// flag, output line, JSON field and exit code, and an example that runs as
// written against a friend's daemon state.
func TestRenewHelpNamesEverythingAndItsExampleRuns(t *testing.T) {
	t.Parallel()
	r := newRenewRig(t, "opencode", true, true)
	help := r.cli().Do(t, "help", "renew").Exit(0).Stdout
	raw, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	for _, want := range []string{"--as", "--friend", "--reason", "--state-dir", "--redis", "--dry-run", "--json",
		"RENEW OK friend= old= new=", "RENEW NEW friend= harness= old= new=", "RENEW SEEDED friend= bytes=",
		"RENEW PINNED friend= session=", "RENEW RAN command=", "RENEW PLAN command=", "dry_run=true",
		"0 renewed (or", "1 refused and nothing changed", "2 could not run",
		`nova-friend renew --as ada bob --reason "the provider refuses every turn" --dry-run`} {
		require.Contains(t, help, want, "the help")
		require.Contains(t, strings.Join(strings.Fields(string(raw)), " "), want, "docs/CLI.md")
	}
	for _, field := range []string{"friend", "old", "new", "seeded", "pinned", "ran"} {
		require.Contains(t, help, field)
	}
	r.cli().Do(t, "renew", "--as", "ada", "bob", "--reason", "the provider refuses every turn", "--dry-run").Exit(0).Out("dry_run=true")
}

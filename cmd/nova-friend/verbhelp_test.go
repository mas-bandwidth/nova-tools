package main

import (
	"os"
	"slices"
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
		{Verb: "watch", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli, "nova-friend", "run", "install", "uninstall", "check", "ping", "pong", "wait-pong", "status", "watch", "version")
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

// The check verb's help names every output line, every JSON field, the verdict
// rules and the exit codes, and docs/CLI.md carries the same lines.
func TestCheckHelpAndCommandReferenceNameEveryLineFieldAndExit(t *testing.T) {
	t.Parallel()
	help := newRig(t).cli().Do(t, "check", "-h").Exit(0).Stdout
	raw, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	doc := string(raw)

	var lines []string
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "CHECK ") || strings.HasPrefix(line, "Exit 0 when every verdict is ok") {
			lines = append(lines, line)
		}
	}
	require.Len(t, lines, 6, "five CHECK lines and the exit-code line")
	for _, prefix := range []string{"CHECK DAEMON", "CHECK HARNESS", "CHECK BUS", "CHECK WORK", "CHECK VERDICT", "Exit 0"} {
		require.True(t, slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) }), prefix)
	}
	for _, line := range lines {
		require.Contains(t, doc, line, "docs/CLI.md carries the help's line")
	}
	for _, text := range []string{
		"CHECK OK friends=<n> ok=<n> broken=<n> deaf=<n> silent=<n> down=<n> untrue=<n>",
		"broken when the session is marked", "deaf when a delivery in", "silent when", "down by presence", "untrue: shown",
		"friends[] each with", "daemon{friend, agent, pid, status, connection, challenge, pong_age, presence, seen_age}",
		"failed_of_last20, deferred, delivered, failed, broken,", "bus{friend, real_since, last_real}",
		"work{friend, inbox, outbox, newest_outbox, newest_at}", "verdict{friend, verdict, shown, why}",
		"summary{friends, ok, broken, deaf, silent, down, untrue}",
		"Exit 0 when every verdict is ok, 1 when any is not", "2 when it could not run",
		"--since", "--shown", "--json", "example: nova-friend check --as ada bob",
	} {
		require.Contains(t, help, text)
		require.Contains(t, doc, strings.TrimPrefix(text, "example: nova-friend "))
	}
}

// The watch verb's help names every output line, every JSON field, the wake
// conditions and the exit codes, and docs/CLI.md carries the same lines.
func TestWatchHelpAndCommandReferenceNameEveryLineFieldAndExit(t *testing.T) {
	t.Parallel()
	help := newRig(t).cli().Do(t, "watch", "-h").Exit(0).Stdout
	raw, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	doc := string(raw)

	for _, text := range []string{
		"WATCH MESSAGE",
		"WATCH EVENT",
		"WATCH WAKE",
		"WATCH OK after=",
		"WATCH NONE waited=",
		"--timeout",
		"--state-dir",
		"--json",
		"messages",
		"events",
		"wake",
		"after",
		"waited",
		"Exit 0 wake occurred, 1 timeout expired, 2 could not run",
		"example: nova-friend watch --as ada --timeout 1s",
	} {
		require.Contains(t, help, text)
		require.Contains(t, doc, strings.TrimPrefix(text, "example: nova-friend "))
	}
}

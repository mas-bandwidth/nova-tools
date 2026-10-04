//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// --to all resolved from participants; to wakes, cc does not; send prints wakes=<n>
func TestSendToAllExpandsToExactlyTheParticipants(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "From: Ada\nTo: all\nSubject: s\n\nbody\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "wakes=3")
	path := field(t, r.stdout, "path=")
	stored, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
	require.NoError(t, err)
	text := string(stored)
	require.Containsf(t, text, "To: Ada; Bo; Dana\n", "the stored note did not expand --to all to the participants:\n%s", text)
}

func TestSendCcLandsWithoutAWakeMark(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "From: Ada\nTo: Bo\nCc: Dana\nSubject: s\n\nbody\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "wakes=1")
}

func TestSendPrintsWakesCountingToNamesOnlyForTable(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "From: Ada\nTo: table\nSubject: s\n\nbody\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "wakes=2")
	path := field(t, r.stdout, "path=")
	stored, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
	require.NoError(t, err)
	require.NotContainsf(t, string(stored), "To: Ada", "--to table must not name the sender:\n%s", string(stored))
	require.Containsf(t, string(stored), "To: Bo; Dana\n", "--to table did not resolve to everyone but the sender:\n%s", string(stored))
}

// The draft verb vouches for the alias unexpanded, and send resolves it -- the round trip
// a friend runs through the changed verb.
func TestDraftToAllRoundTripResolvesAtSend(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "all", "--subject", "the gate").
		mustCode(t, 0)
	require.Containsf(t, r.stdout, "To: all\n", "draft did not carry the alias verbatim:\n%s", r.stdout)
	sent := strings.Replace(r.stdout, "<the note goes here>\n", "the gate opens.\n", 1)
	invoke(t, sent, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "wakes=3")
}

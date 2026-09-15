package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err != nil {
		t.Fatal(err)
	}
	text := string(stored)
	if !strings.Contains(text, "To: Ada; Bo; Dana\n") {
		t.Fatalf("the stored note did not expand --to all to the participants:\n%s", text)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "To: Ada") {
		t.Fatalf("--to table must not name the sender:\n%s", string(stored))
	}
	if !strings.Contains(string(stored), "To: Bo; Dana\n") {
		t.Fatalf("--to table did not resolve to everyone but the sender:\n%s", string(stored))
	}
}

// The draft verb vouches for the alias unexpanded, and send resolves it -- the round trip
// a friend runs through the changed verb.
func TestDraftToAllRoundTripResolvesAtSend(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "all", "--subject", "the gate").
		mustCode(t, 0)
	if !strings.Contains(r.stdout, "To: all\n") {
		t.Fatalf("draft did not carry the alias verbatim:\n%s", r.stdout)
	}
	sent := strings.Replace(r.stdout, "<the note goes here>\n", "the gate opens.\n", 1)
	invoke(t, sent, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "wakes=3")
}

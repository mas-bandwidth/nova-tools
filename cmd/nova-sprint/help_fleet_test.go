package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The top-level help says how a real fleet connects (serverWords): the server run
// --listen starts, the coordinator's NOVA_SPRINT_SERVER, and each machine's member and
// reader loops; and run -h quotes the server's line from it.
func TestTheHelpSaysHowAFleetConnectsToTheServer(t *testing.T) {
	t.Parallel()
	help := banner()
	for _, want := range []string{
		"nova-sprint run --listen <address>:<port> --land",
		ServerEnv + "=127.0.0.1:<port>",
		"nova-swarm member --as <name> --server <address>:<port>",
		"nova-swarm member --as <reader> --server <address>:<port> --reader --width <n>",
		"init --members",
		"reader add",
	} {
		assert.Contains(t, help, want, "nova-sprint help names %q", want)
	}
	var out, errb bytes.Buffer
	code := newApp(func(string) string { return "" }).run([]string{"run", "-h"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "nova-sprint run --listen <address>:<port> --land", "run -h quotes the server's line")
	assert.Contains(t, out.String(), ServerEnv+"=127.0.0.1:<port>", "run -h's --listen names where the coordinator's verbs go")
}

// The words section defines, one line each, every word the help and the verbs' output
// use without defining it where it stands.
func TestTheWordsSectionDefinesEachWordOnce(t *testing.T) {
	t.Parallel()
	words := wordsSection()
	require.Contains(t, banner(), words, "nova-sprint help carries the words section")
	for _, w := range []string{
		"primary", "work card", "read card", "kind", "sentinel", "judgment", "HAPPENED", "DECIDED",
		"group", "cursor", "deal", "level", "drain", "tier", "route", "provider", "head",
		"the stream's base", "held", "stuck", "staging", "reaped", "resolve", "quack", "play", "wall",
	} {
		assert.Equal(t, 1, strings.Count(words, "\n  "+w+" "), "the words section defines %q on one line of its own", w)
	}
}

// An empty brief is said once, as empty, never as every rule it does not quote.
func TestAnEmptyBriefIsSaidOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c1.md"), []byte("\n  \n"), 0o644))
	ta := newTestApp(t)
	ta.ok("init --members m1")
	code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, 1, strings.Count(errs, "LINT DRIFT"), errs)
	assert.Contains(t, errs, "the card is empty")
	assert.NotContains(t, errs, "rule-worktree")
}

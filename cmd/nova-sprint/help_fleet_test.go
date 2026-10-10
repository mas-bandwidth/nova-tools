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
		"nova-worker member --as <name> --server <address>:<port>",
		"nova-worker member --as <reader> --server <address>:<port> --reader --width <n>",
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

// Every verb's -h names that verb's own usage. The placeholder [flags] is a
// regression: a synopsis comes from the verb table, and a verb with an empty
// synopsis names the flags it registers. inbox -h keeps the judgment
// walkthrough, release points at the three holds, and friend beat, friend up,
// and friend down say what they do.
func TestVerbHelpNamesItsUsageAndTheColdWords(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	for _, v := range verbs {
		var out, errb bytes.Buffer
		args := append(strings.Fields(v.name), "-h")
		code := a.run(args, &out, &errb)
		require.Equal(t, 0, code, "%s -h: %s", v.name, errb.String())
		help := out.String()
		first, _, _ := strings.Cut(help, "\n")
		assert.NotContains(t, first, "[flags]", "%s -h usage line is the placeholder: %s", v.name, first)
		if syn := strings.TrimSpace(v.syntax); syn != "" {
			assert.Equal(t, "usage: nova-sprint "+strings.TrimSpace(v.name+" "+syn), first, v.name)
		} else {
			assert.Contains(t, first, "[--", "%s -h names no flag: %s", v.name, first)
		}
	}
	helpOf := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		require.Equal(t, 0, code, "%v: %s", args, errb.String())
		return out.String()
	}
	run := helpOf("run", "-h")
	runFirst, _, _ := strings.Cut(run, "\n")
	assert.Contains(t, runFirst, "[--listen <address:port>]")
	assert.Contains(t, runFirst, "[--land]")
	assert.NotContains(t, runFirst, "[flags]")

	inbox := helpOf("inbox", "-h")
	assert.Equal(t, 1, strings.Count(inbox, "reading the inbox and answering a judgment:"))
	assert.Contains(t, inbox, "one answer to each judgment")

	release := helpOf("release", "-h")
	for _, hold := range []string{"fleet up <member>", "reader up <reader>", "friend up <friend>"} {
		assert.Contains(t, release, hold, "release -h")
	}

	beat := helpOf("friend", "beat", "-h")
	assert.Contains(t, beat, "it never makes her up, whoever sends it")
	assert.Contains(t, beat, "friend sync exits 3")
	down := helpOf("friend", "down", "-h")
	assert.Contains(t, down, "held is the coordinator's decision alone, whatever she beats or the coordinator's daemon observes")
	assert.Contains(t, down, "--reason <text> and --until <RFC3339>")
	assert.Contains(t, down, "friend sync exits 3")
	up := helpOf("friend", "up", "-h")
	assert.Contains(t, up, "It is no evidence:")
	assert.Contains(t, up, "friend sync exits 3")
	for _, help := range []string{beat, down, up} {
		first, _, _ := strings.Cut(help, "\n")
		assert.NotContains(t, first, "[flags]", first)
	}
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

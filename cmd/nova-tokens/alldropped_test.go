package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A fold that drops EVERY message folded nothing, and exits 1 with the counts: a gate that
// reads the exit code must not call it green (the third cold rating of the tools: a fold
// of two messages with no id wrote nothing and exited 0 under a TOKENS NOTE).
func TestFoldThatDroppedEveryMessageFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 9000}, "/x/schema/a.go"),
		msg("", "2026-09-11T10:00:01Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
	}, "\n")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 1)
	fail := lineWith(r.stderr, "FOLD FAIL dropped=")
	wantContains(t, fail, "FOLD FAIL dropped=2 of 2: no message had an id")
	wantContains(t, fail, "run: nova-tokens sources")
	wantContains(t, r.stderr, "TOKENS FAIL days=0 rows=0")
	wantNotContains(t, r.stdout, "TOKENS OK")
	// the note still names the source
	wantContains(t, lineWith(r.stdout, "TOKENS NOTE"), "claude:g")
}

// Some messages dropped is the NOTE alone: the day is short and says so, and the fold did
// its job for the rest, exit 0 (TestMessagesWithNoIDReachTheRemedyLine holds the same
// fold for its note).
func TestFoldThatDroppedSomeMessagesStaysAnote(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 9000}, "/x/schema/a.go"),
		msg("m1", "2026-09-11T10:00:01Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"),
	}, "\n")+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	wantExit(t, r, 0)
	wantNotContains(t, r.stderr, "dropped=")
	wantContains(t, lineWith(r.stdout, "TOKENS NOTE"), "no id")
}

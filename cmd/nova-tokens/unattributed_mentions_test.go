package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheUnattributedFieldCountsMentions pins the field's meaning and its name: the tally
// counts the mentions a path got -- one per message that touched it, whatever the message's
// tokens -- so `SOURCES UNATTRIBUTED` prints mentions= in text and in --json, the old
// tokens= is gone, and the help says what the count is. Two 1,500-token messages on one path
// print mentions=2, never tokens=2.
func TestTheUnattributedFieldCountsMentions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 1500}, "/home/nova/tree/a.go"),
		msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 1500}, "/home/nova/tree/b.go"),
	}, "\n")+"\n")

	r := invoke(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "SOURCES UNATTRIBUTED stem=/home/nova/tree mentions=2")
	wantNotContains(t, r.stdout, "tokens=2")

	_, got := asJSON(t, "sources", "--repos", repos, "--all", "--claude", "g="+tr, "--unattributed", "--json")
	f := itemFields(t, got, "unattributed")
	assert.Equal(t, 2.0, f["mentions"], "the item carries the mention count under its new name")
	_, keptOld := f["tokens"]
	assert.False(t, keptOld, "tokens is renamed to mentions, not kept beside it")

	// The help says what the count is: one mention per message that touched the path.
	h := invoke(t, "sources", "-h")
	wantExit(t, h, 0)
	wantContains(t, h.stdout, "mentions")
}

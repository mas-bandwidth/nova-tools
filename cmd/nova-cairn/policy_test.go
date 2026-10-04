package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The policy is named once, at open: an append with no --publish carries the
// session's, an append naming one keeps its own, and a flat record, which
// records no policy, says publish=unknown rather than inventing one.
func TestAnAppendCarriesTheSessionsPolicy(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "deferred")
	printed(t, c.ok("append", "--session", "s", "--entry", "inherits", "--text", "w"), " publish=deferred ")
	printed(t, c.ok("receipt", "--session", "s", "--entry", "inherits"), " publish=deferred")
	printed(t, c.ok("append", "--session", "s", "--entry", "own", "--text", "w", "--publish", "never"), " publish=never ")

	testkit.WriteFile(t, c.path("flat.md"), "# by hand\n")
	printed(t, c.ok("append", "--session", "flat", "--entry", "e", "--text", "w"), " publish=unknown ")
}

// A nested record whose log holds no open record has no policy to carry: the
// append without --publish is refused, naming the flag, and writes nothing.
func TestAnAppendWithNoPolicyToCarryIsRefused(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	require.NoError(t, os.Remove(c.path("log.jsonl")))
	r := c.run("append", "--session", "s", "--entry", "e", "--text", "w")
	refused(t, r, "--publish is required")
	c.wroteNothing("s", "e")
	printed(t, c.ok("append", "--session", "s", "--entry", "e", "--text", "w", "--publish", "manual"), " publish=manual ")
}

// A re-open naming the recorded policy and source changes nothing; one naming
// another policy or another source is a conflict at exit 1 whose remedy is the
// open that matches, and nothing is written.
func TestAReOpenNamingAnotherPolicyOrSourceIsAConflict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"same policy", []string{"--publish", "manual"}, 0},
		{"same policy and source", []string{"--publish", "manual", "--source", "src"}, 0},
		{"another policy", []string{"--publish", "never"}, 1},
		{"another source", []string{"--publish", "manual", "--source", "elsewhere"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newRig(t)
			c.ok("open", "--session", "s", "--source", "src", "--publish", "manual")
			log := testkit.ReadFile(t, c.path("log.jsonl"))
			r := c.run("open", append([]string{"--session", "s"}, tc.args...)...)
			require.Equal(t, tc.code, r.Code, "%+v", r)
			assert.Equal(t, log, testkit.ReadFile(t, c.path("log.jsonl")), "a re-open wrote to the log")
			if tc.code == 0 {
				printed(t, r.Stdout, "OPEN OK session=s ", " source=src publish=manual ")
				return
			}
			assert.Empty(t, r.Stdout)
			printed(t, r.Stderr, "OPEN FAIL: session \"s\" is already open with publish=manual source=src",
				"; run: nova-cairn open --store "+c.store+" --session s --source src --publish manual\n")
		})
	}
}

// Every refusal that knows the next step names it: a conflicting entry names
// the receipt that reads what it holds, a missing entry the index of its
// session, a missing session the index of the store.
func TestAConflictOrAMissingEntryNamesTheCommandToRunNext(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	c.ok("append", "--session", "s", "--entry", "e", "--text", "first words")
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"conflict", []string{"append", "--session", "s", "--entry", "e", "--text", "other words"}, 1,
			"APPEND FAIL session=s entry=e: entry \"e\" already holds different prose; append these words under a new --entry id, or read what it holds; run: nova-cairn receipt --store " + c.store + " --session s --entry e --text\n"},
		{"missing entry", []string{"receipt", "--session", "s", "--entry", "absent"}, 2,
			"RECEIPT REFUSED: no such entry \"absent\" in session \"s\"; run: nova-cairn index --store " + c.store + " --session s\n"},
		{"missing session", []string{"index", "--session", "nosuch"}, 2,
			"INDEX REFUSED: no such session \"nosuch\" under store \"" + c.store + "\"; run: nova-cairn index --store " + c.store + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.run(tc.args[0], tc.args[1:]...)
			assert.Equal(t, tc.code, r.Code, "%+v", r)
			assert.Equal(t, tc.want, r.Stderr)
		})
	}
}

// The remedy is a shell line that runs as printed, whatever the store path
// holds: a space or a quote is quoted, a control byte is decoded from octal.
func TestARemedyQuotesTheStorePath(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "my store's")
	require.NoError(t, os.Mkdir(store, 0o755))
	r := cli.Run("index", "--store", store, "--session", "nosuch")
	require.Equal(t, 2, r.Code, "%+v", r)
	printed(t, r.Stderr, "; run: nova-cairn index --store '"+strings.ReplaceAll(store, "'", `'\''`)+"'\n")
}

// --dry-run on open and append runs every check the write would, reports what
// the write would, and writes nothing: no record, no entry, no log line.
func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	printed(t, c.ok("open", "--session", "s", "--source", "src", "--publish", "manual", "--dry-run"),
		"OPEN OK session=s ", " source=src publish=manual ", " dry_run=true")
	entries, err := os.ReadDir(c.store)
	require.NoError(t, err)
	require.Empty(t, entries, "open --dry-run wrote")

	c.ok("open", "--session", "s", "--source", "src", "--publish", "manual")
	log := testkit.ReadFile(t, c.path("log.jsonl"))
	printed(t, c.ok("append", "--session", "s", "--entry", "e", "--text", "w", "--dry-run"),
		" source=src persisted=false published=false publish=manual duplicate=false ", " dry_run=true")
	c.wroteNothing("s", "e")
	assert.Equal(t, log, testkit.ReadFile(t, c.path("log.jsonl")), "append --dry-run wrote to the log")

	// The plan of a retry is a duplicate already persisted; of other words, the conflict.
	c.ok("append", "--session", "s", "--entry", "e", "--text", "w")
	printed(t, c.ok("append", "--session", "s", "--entry", "e", "--text", "w", "--dry-run"), " persisted=true ", " duplicate=true ")
	assert.Equal(t, 1, c.run("append", "--session", "s", "--entry", "e", "--text", "other", "--dry-run").Code)
	assert.Equal(t, 1, c.run("open", "--session", "s", "--publish", "never", "--dry-run").Code)

	testkit.WriteFile(t, c.path("flat.md"), "# by hand\n")
	printed(t, c.ok("append", "--session", "flat", "--entry", "f", "--text", "w", "--dry-run"), " persisted=false ", " dry_run=true")
	assert.Equal(t, "# by hand\n", testkit.ReadFile(t, c.path("flat.md")), "a flat append --dry-run wrote")
}

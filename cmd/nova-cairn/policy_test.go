package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppendPublishThatDisagreesIsRefused pins the session's policy as a fact,
// not a guess: an append with no --publish, or with the same one, prints it;
// --publish that names another is refused naming both; no open session stays
// a refusal and writes no record.
func TestAppendPublishThatDisagreesIsRefused(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual", "--now", "2026-01-02T03:04:05Z")
	printed(t, c.ok("append", "--session", "s", "--entry", "same", "--text", "w", "--publish", "manual"), " publish=manual ")
	printed(t, c.ok("append", "--session", "s", "--entry", "carried", "--text", "w"), " publish=manual ")

	r := c.run("append", "--session", "s", "--entry", "other", "--text", "w", "--publish", "never")
	require.Equal(t, 1, r.Code, "%+v", r)
	require.Empty(t, r.Stdout)
	printed(t, r.Stderr, "holds publish=manual", "--publish never",
		"; run: nova-cairn append --store "+c.store+" --session s --entry other --publish manual")
	c.wroteNothing("s", "other")

	r = c.run("append", "--session", "missing", "--entry", "e", "--text", "w", "--publish", "never")
	require.Equal(t, 2, r.Code, "%+v", r)
	require.Contains(t, r.Stderr, `no such session "missing"`)
	_, err := os.Lstat(c.path("sessions", "missing.md"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// The policy is named once, at open: an append with no --publish carries the
// session's, an append naming another is a conflict, and a flat record, which
// records no policy, says publish=unknown rather than inventing one.
func TestAnAppendCarriesTheSessionsPolicy(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "deferred")
	printed(t, c.ok("append", "--session", "s", "--entry", "inherits", "--text", "w"), " publish=deferred ")
	printed(t, c.ok("receipt", "--session", "s", "--entry", "inherits"), " publish=deferred")
	r := c.run("append", "--session", "s", "--entry", "own", "--text", "w", "--publish", "never")
	require.Equal(t, 1, r.Code, "%+v", r)
	printed(t, r.Stderr, "holds publish=deferred", "--publish never")

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

// TestReopenPrintsStoredOpenedTime pins a re-open: the same policy prints
// reopened=true and the opened time the session file stored, not the clock of
// this call, and a different --publish is refused naming both policies.
func TestReopenPrintsStoredOpenedTime(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	stored := "2026-01-02T14:59:58Z"
	later := "2026-01-02T15:00:06Z"
	c.ok("open", "--session", "s", "--publish", "manual", "--now", stored)

	out := c.ok("open", "--session", "s", "--publish", "manual", "--now", later)
	printed(t, out, "OPEN OK session=s ", " reopened=true ", " stamp="+stored)
	require.NotContains(t, out, later)

	r := c.run("open", "--session", "s", "--publish", "manual", "--now", later, "--json")
	require.Equal(t, 0, r.Code, "%+v", r)
	require.Contains(t, r.Stdout, `"reopened":true`)
	require.Contains(t, r.Stdout, stored)
	require.NotContains(t, r.Stdout, later)

	r = c.run("open", "--session", "s", "--publish", "never", "--now", later)
	require.Equal(t, 1, r.Code, "%+v", r)
	require.Empty(t, r.Stdout)
	printed(t, r.Stderr, "publish=manual", "--publish never",
		"; run: nova-cairn open --store "+c.store+" --session s --publish manual")
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
			printed(t, r.Stderr, "OPEN FAILED: session \"s\" is already open with publish=manual source=src",
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
			"APPEND FAILED session=s entry=e: entry \"e\" already holds different prose; append these words under a new --entry id, or read what it holds; run: nova-cairn receipt --store " + c.store + " --session s --entry e --text\n"},
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

// TestEveryRefusalEndsAtTheCommandThatMovesTheUserOn pins the door of each
// refusal kind a cold user meets (use-cairn-t, remedies): a missing flag at
// the tool's door (docs/ONBOARDING.md point 1, #1451), an unknown flag at the
// verb's -h (docs/CLI-STYLE.md, refusals), a conflict at the receipt --text
// that reads what the id holds, and a missing entry at the index that lists
// what is there. A refusal that ends anywhere else leaves the reader guessing
// the next command.
func TestEveryRefusalEndsAtTheCommandThatMovesTheUserOn(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	c.ok("append", "--session", "s", "--entry", "e", "--text", "first words")
	for _, tc := range []struct {
		name string
		args []string
		code int
		door string
	}{
		{"missing flag", []string{"open", "--session", "s2"}, 2, "; run: nova-cairn help"},
		{"unknown flag", []string{"append", "--session", "s", "--entry", "e9", "--text", "w", "--nope"}, 2, "; run: nova-cairn append -h"},
		{"conflict", []string{"append", "--session", "s", "--entry", "e", "--text", "other words"}, 1, "; run: nova-cairn receipt --store " + c.store + " --session s --entry e --text"},
		{"missing entry", []string{"receipt", "--session", "s", "--entry", "absent"}, 2, "; run: nova-cairn index --store " + c.store + " --session s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.run(tc.args[0], tc.args[1:]...)
			assert.Equal(t, tc.code, r.Code, "%+v", r)
			last := strings.TrimSuffix(r.Stderr, "\n")
			assert.True(t, strings.HasSuffix(last, tc.door), "the refusal does not end at the command that moves the user on: %q", r.Stderr)
		})
	}
}

// The remedy is a shell line that runs as printed, whatever the store path
// holds: a space or a quote is quoted; a path the one-line rendering cannot
// carry is refused whole, and the refusal names the rename.
func TestARemedyQuotesTheStorePath(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "my store's")
	require.NoError(t, os.Mkdir(store, 0o755))
	r := cli.Run("index", "--store", store, "--session", "nosuch")
	require.Equal(t, 2, r.Code, "%+v", r)
	printed(t, r.Stderr, "; run: nova-cairn index --store '"+strings.ReplaceAll(store, "'", `'"'"'`)+"'\n")
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

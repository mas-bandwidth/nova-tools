// CLI tests for nova-tools #248: the four verbs work end to end, and the
// lifecycle verbs the issue rules out stay refused.
package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cli is the tool's entry point in process.
var cli = testkit.Main(runCairn)

// rig is one store under test: a fresh directory and the tool pointed at it.
type rig struct {
	t     *testing.T
	store string
}

// entry is the part of a stored entry file the tests read.
type entry struct {
	Text string `json:"text"`
}

func newRig(t *testing.T) *rig { return &rig{t: t, store: t.TempDir()} }

// ok runs `nova-cairn <verb> --store <store> args...`, requires exit 0, and
// returns stdout.
func (c *rig) ok(verb string, args ...string) string { return c.okIn("", verb, args...) }

// okIn is ok with stdin holding the given text.
func (c *rig) okIn(stdin, verb string, args ...string) string {
	c.t.Helper()
	return cli.OKIn(c.t, stdin, append([]string{verb, "--store", c.store}, args...)...).Stdout
}

// run is ok without the exit check.
func (c *rig) run(verb string, args ...string) testkit.Result {
	return cli.Run(append([]string{verb, "--store", c.store}, args...)...)
}

// path is a path inside the store.
func (c *rig) path(elem ...string) string {
	return filepath.Join(append([]string{c.store}, elem...)...)
}

// printed requires out to hold every one of wants. A failure names the
// missing text and the whole output, and t.Helper puts the caller's line on it.
func printed(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		require.Contains(t, out, w)
	}
}

// wroteNothing is the "nothing written" half of the two provenance refusals:
// no entry file for the id and no pointer line in the session file.
func (c *rig) wroteNothing(session, entry string) {
	c.t.Helper()
	_, err := os.Lstat(c.path("entries", session, entry+".json"))
	require.ErrorIs(c.t, err, fs.ErrNotExist, "a refused append left an entry file")
	require.NotContains(c.t, testkit.ReadFile(c.t, c.path("sessions", session+".md")), "ENTRY "+entry+" ", "a refused append left a pointer line")
}

func TestOpenAppendIndexReceiptRoundTrip(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	out := c.ok("open", "--session", "s1", "--source", "bench/session-3", "--publish", "manual")
	printed(t, out, "OPEN OK", "session=s1")
	prose := "the friend's chosen words — café, \"as above\" nowhere, byte-exact"
	out = c.ok("append", "--session", "s1", "--entry", "e1", "--text", prose, "--source", "bench/session-3#L9", "--publish", "manual")
	printed(t, out, "APPEND OK", "persisted=true published=false")
	// The duplicate request succeeds without a duplicate entry.
	out = c.ok("append", "--session", "s1", "--entry", "e1", "--text", prose, "--publish", "manual")
	require.Contains(t, out, "duplicate=true", "retry printed %q, want duplicate=true", out)
	// Same id, different prose: exit 1, never an overwrite.
	code := c.run("append", "--session", "s1", "--entry", "e1", "--text", "other words", "--publish", "manual").Code
	require.Equal(t, 1, code, "conflicting append exited %d, want 1", code)
	out = c.ok("receipt", "--session", "s1", "--entry", "e1")
	printed(t, out, "RECEIPT OK", "persisted=true published=false")
	require.NotContains(t, out, " text=", "the default receipt remains unchanged")
	printed(t, c.ok("index"), "INDEX ENTRY session=s1 entry=e1", "INDEX OK sessions=1 entries=1")
	require.Equal(t, prose, testkit.ReadJSON[entry](t, c.path("entries", "s1", "e1.json")).Text, "the stored entry is not the exact prose")
}

func TestReceiptTextReturnsStoredWordsInBothRenderings(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	prose := "line one\nline \"two\""
	c.ok("append", "--session", "s", "--entry", "e", "--text", prose, "--publish", "manual")

	plain := c.ok("receipt", "--session", "s", "--entry", "e", "--text")
	// Quoted, with its spaces kept: the words read as words, never as \x20 escapes.
	require.True(t, strings.HasSuffix(plain, ` text="line one\nline \"two\""`+"\n"), "text is one quoted fact, last on the line: %s", plain)

	result := c.run("receipt", "--session", "s", "--entry", "e", "--text", "--json")
	require.Zero(t, result.Code, result.Stderr)
	var out struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out), result.Stdout)
	require.Equal(t, prose, out.Facts["text"], "JSON carries the exact stored words, not a second quoted spelling")
}

func TestOfflineAppendSucceedsWithPublicationPending(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "deferred")
	out := c.ok("append", "--session", "s", "--entry", "e", "--text", "offline note", "--publish", "deferred")
	require.Contains(t, out, "persisted=true published=false", "offline append printed %q", out)
}

func TestAppendViaFileAndStdinKeepsExactBytes(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "never")
	prose := "line one\nline two  with trailing spaces   \n\ttabbed\n"
	f := filepath.Join(t.TempDir(), "note.txt")
	testkit.WriteFile(t, f, prose)
	c.ok("append", "--session", "s", "--entry", "from-file", "--file", f, "--publish", "never")
	c.okIn(prose, "append", "--session", "s", "--entry", "from-stdin", "--file", "-", "--publish", "never")
	for _, id := range []string{"from-file", "from-stdin"} {
		out := c.ok("receipt", "--session", "s", "--entry", id)
		require.Contains(t, out, "RECEIPT OK", "receipt %s printed %q", id, out)
	}
	require.Equal(t, prose, testkit.ReadJSON[entry](t, c.path("entries", "s", "from-stdin.json")).Text, "stdin bytes not preserved")
}

func TestInterruptedAppendRecoversAtCLI(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	c.ok("append", "--session", "s", "--entry", "other", "--text", "other writer's note", "--publish", "manual")
	testkit.WriteFile(t, c.path("entries", "s", "mine.json.tmp"), "{partial")
	out := c.ok("append", "--session", "s", "--entry", "mine", "--text", "my note after the crash", "--publish", "manual")
	require.Contains(t, out, "APPEND OK", "recovery printed %q", out)
	printed(t, c.ok("index"), "entries=2", "INDEX ENTRY session=s entry=other", "INDEX ENTRY session=s entry=mine")
}

func TestExistingDirtyWorkIsUntouched(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	dirty := c.path("my-unfinished-work.md")
	before := "half-written thought, do not touch\n"
	testkit.WriteFile(t, dirty, before)
	c.ok("open", "--session", "s", "--publish", "never")
	c.ok("append", "--session", "s", "--entry", "e", "--text", "a checkpoint alongside dirty work", "--publish", "never")
	c.ok("index")
	require.Equal(t, before, testkit.ReadFile(t, dirty), "dirty work changed")
	_, err := os.Lstat(c.path(".git"))
	require.ErrorIs(t, err, fs.ErrNotExist, "tool must not init or touch version control in the store")
}

func TestConcurrentRecordsAndAlternateHeaders(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	for _, s := range []string{"alpha", "beta"} {
		c.ok("open", "--session", s, "--publish", "never")
		c.ok("append", "--session", s, "--entry", "e", "--text", "note in "+s, "--publish", "never")
	}
	sessFile := c.path("sessions", "alpha.md")
	lines := strings.Split(testkit.ReadFile(t, sessFile), "\n")
	lines[0] = "# Our team files records under its own headings"
	testkit.WriteFile(t, sessFile, strings.Join(lines, "\n"))
	out := c.ok("index")
	require.Contains(t, out, "INDEX OK sessions=2 entries=2", "index printed %q", out)
	out = c.ok("index", "--session", "alpha")
	require.Contains(t, out, "INDEX ENTRY session=alpha entry=e", "per-session index printed %q", out)
}

// TestIndexSessionCountsOnlyTheSelection pins the --session coverage fix:
// index --session reports the count the selection covers, not the whole store,
// and prints an INDEX SESSION line for every session in the selection, entries
// or none, so an empty session is found.
func TestIndexSessionCountsOnlyTheSelection(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s1", "--publish", "manual")
	c.ok("append", "--session", "s1", "--entry", "e1", "--text", "words of s1", "--publish", "manual")
	c.ok("open", "--session", "s2", "--publish", "manual")

	// An empty session is counted and named: sessions= is the selection's count,
	// not the store's, and INDEX SESSION lists it with entries=0.
	out := c.ok("index", "--session", "s2")
	printed(t, out, "INDEX SESSION session=s2 publish=manual ", " entries=0", "INDEX OK sessions=1 entries=0")
	require.Regexp(t, `INDEX SESSION session=s2 publish=manual opened=\S+ entries=0`, out)
	require.NotContains(t, out, "sessions=2", "sessions= counted the whole store, not the selection: %s", out)

	// The full index names every session, empty or not.
	out = c.ok("index")
	printed(t, out, "INDEX SESSION session=s1 publish=manual ", "INDEX SESSION session=s2 publish=manual ",
		"INDEX OK sessions=2 entries=1")
	require.Regexp(t, `INDEX SESSION session=s1 publish=manual opened=\S+ entries=1`, out)
	require.Regexp(t, `INDEX SESSION session=s2 publish=manual opened=\S+ entries=0`, out)
}

// TestIndexNamesEachSessionsPolicyAndOpened pins the session item index was
// missing: one INDEX SESSION line per session, before the entries, carrying
// the id, the publish policy and the stored opened time, and --max bounds
// those lines with MORE the way it bounds entries.
func TestIndexNamesEachSessionsPolicyAndOpened(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	c.ok("open", "--session", "s1", "--publish", "manual", "--source", "bench/s1", "--now", "2026-01-02T03:04:05Z")
	c.ok("append", "--session", "s1", "--entry", "e1", "--text", "words of s1", "--now", "2026-01-02T04:00:00Z")
	c.ok("open", "--session", "empty1", "--publish", "never", "--source", "bench/empty", "--now", "2026-01-03T00:00:00Z")
	testkit.WriteFile(t, c.path("flat.md"), "# by hand\n")

	out := c.ok("index")
	require.Less(t, strings.Index(out, "INDEX SESSION"), strings.Index(out, "INDEX ENTRY"),
		"session lines stand before entries:\n%s", out)
	printed(t, out,
		"INDEX SESSION session=empty1 publish=never opened=2026-01-03T00:00:00Z entries=0",
		"INDEX SESSION session=flat publish=unknown opened=- entries=0",
		"INDEX SESSION session=s1 publish=manual opened=2026-01-02T03:04:05Z entries=1",
		"INDEX OK sessions=3 entries=1",
		"INDEX ENTRY session=s1 entry=e1")

	capped := c.ok("index", "--max", "1")
	printed(t, capped, "INDEX MORE kind=session shown=1 total=3", "INDEX OK sessions=3 entries=1")
	require.Equal(t, 1, strings.Count(capped, "INDEX SESSION"), "capped index:\n%s", capped)
}

// TestHelpWithMoreThanOneWordRefusesAsHelp pins the second half of the finding:
// `help` with more than one word is one HELP REFUSED, naming the single verb
// name it wants, before the named verb runs its flag checks. `nova-cairn help
// open append` once dispatched as `open` with a stray positional and printed
// three missing-flag OPEN REFUSED lines instead.
func TestHelpWithMoreThanOneWordRefusesAsHelp(t *testing.T) {
	t.Parallel()

	r := cli.Run("help", "open", "append")
	require.Equal(t, 2, r.Code, "want exit 2: %+v", r)
	require.Empty(t, r.Stdout, "a refusal prints no banner: %q", r.Stdout)
	require.Contains(t, r.Stderr, "HELP REFUSED: help takes one verb name", "stderr: %q", r.Stderr)
	require.NotContains(t, r.Stderr, "OPEN REFUSED", "help ran open's flag checks: %q", r.Stderr)
	require.NotContains(t, r.Stderr, "positional", "help carried the stray word into a verb: %q", r.Stderr)

	// A flag between help, the verb and the stray word is no verb dispatch
	// either: `help open --json append` and `help open -- append` refuse as
	// help before open runs its flag checks.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag before the stray word", []string{"help", "open", "--json", "append"}},
		{"-- before the stray word", []string{"help", "open", "--", "append"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := cli.Run(tc.args...)
			require.Equal(t, 2, r.Code, "want exit 2: %+v", r)
			got := r.Stdout + r.Stderr
			require.Contains(t, got, "help takes one verb name", "help refused as the wrong verb: %q", got)
			require.NotContains(t, got, "OPEN REFUSED", "help ran open's flag checks: %q", got)
			require.NotContains(t, got, "positional", "help carried the stray word into a verb: %q", got)
			require.NotContains(t, got, "store is required", "help ran open's flag checks: %q", got)
		})
	}
}

func TestLifecycleVerbsStayRefused(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	for _, verb := range []string{"seal", "consume", "delete", "grade", "consolidate", "wake", "rollup", "retention"} {
		assert.Equal(t, 2, cli.Run(verb, "--store", store).Code, "%s is not a subcommand", verb)
	}
}

func TestMissingFlagsAreRefusedNeverGuessed(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"open without --store":             {"open", "--session", "s"},
		"append without words":             {"append", "--store", "x", "--session", "s", "--entry", "e", "--publish", "never"},
		"append without --entry/--publish": {"append", "--store", "x", "--session", "s"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, 2, cli.Run(args...).Code)
		})
	}
}

func TestBadClockIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t).run("open", "--session", "s", "--publish", "never", "--now", "tomorrow-ish")
	require.Equal(t, 2, r.Code, "open with a bad --now")
}

// TestSourcePointerIsRecordedNeverOpened is SPEC-CAIRN line 18, and the
// dogfood finding of 2026-09-18 behind it: open carried --source and every
// entry line then printed source= empty. The pointer names a path that does
// not exist, so a verb that opened it would fail; an append with no --source
// carries the session's pointer; an append with its own keeps its own; every
// verb prints source=, one field even when the pointer holds a space; and an
// entry with no pointer anywhere prints source=-.
func TestSourcePointerIsRecordedNeverOpened(t *testing.T) {
	t.Parallel()

	c := newRig(t)
	ptr := c.path("no such transcript", "session.jsonl")
	field := strings.ReplaceAll(ptr, " ", `\x20`)
	out := c.ok("open", "--session", "s1", "--source", ptr, "--publish", "manual")
	require.Contains(t, out, " source="+field+" ", "open printed %q, want source=%s", out, field)
	out = c.ok("append", "--session", "s1", "--entry", "inherits", "--text", "words with no pointer of their own", "--publish", "manual")
	require.Contains(t, out, " source="+field+" ", "append with no --source printed %q, want the session's source=%s", out, field)
	out = c.ok("append", "--session", "s1", "--entry", "own", "--text", "words with a pointer", "--source", "bench-a/session-7#L3", "--publish", "manual")
	require.Contains(t, out, " source=bench-a/session-7#L3 ", "append --source printed %q, want its own source", out)
	out = c.ok("index")
	printed(t, out, "entry=inherits stamp=", "entry=own stamp=")
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "entry=inherits "):
			require.True(t, strings.HasSuffix(line, " source="+field), "index row %q, want the session's source", line)
		case strings.Contains(line, "entry=own "):
			require.True(t, strings.HasSuffix(line, " source=bench-a/session-7#L3"), "index row %q, want the entry's own source", line)
		}
	}
	out = c.ok("receipt", "--session", "s1", "--entry", "inherits")
	require.Contains(t, out, " source="+field+" ", "receipt printed %q, want source=%s", out, field)
	_, err := os.Lstat(ptr)
	require.ErrorIs(t, err, fs.ErrNotExist, "the source pointer was created or opened")

	// A session opened with no pointer: the entry has none, and says so.
	c.ok("open", "--session", "s2", "--publish", "manual")
	out = c.ok("append", "--session", "s2", "--entry", "bare", "--text", "words from nowhere named", "--publish", "manual")
	require.Contains(t, out, " source=- ", "append with no pointer anywhere printed %q, want source=-", out)
	out = c.ok("receipt", "--session", "s2", "--entry", "bare")
	require.Contains(t, out, " source=- ", "receipt with no pointer printed %q, want source=-", out)
}

// TestAnUnreadableLogRefusesTheAppendAndWritesNothing is SPEC-CAIRN line 29:
// a log.jsonl that exists and cannot be read (mode 0200, so still appendable)
// is not a store with no source. The append that would have inherited the
// session's pointer refuses at exit 2, names the log, and writes nothing,
// rather than filing APPEND OK source=- over the pointer open recorded.
func TestAnUnreadableLogRefusesTheAppendAndWritesNothing(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("windows: os.Chmod(0200) leaves the file readable, so an unreadable log cannot be made")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0200 file; the permission case cannot be made")
	}

	c := newRig(t)
	c.ok("open", "--session", "s1", "--source", "session:x", "--publish", "manual")
	log := c.path("log.jsonl")
	require.NoError(t, os.Chmod(log, 0o200), "chmod")
	t.Cleanup(func() { _ = os.Chmod(log, 0o644) })
	// The fixture is only a fixture if the read really fails: some
	// filesystems and privileged runs read a 0200 file anyway.
	if _, err := os.ReadFile(log); err == nil {
		t.Skip("the 0200 log is still readable here (filesystem or privilege); the permission case cannot be made")
	}

	r := c.run("append", "--session", "s1", "--entry", "e1", "--text", "words that would inherit the pointer", "--publish", "manual")
	require.Equal(t, 2, r.Code, "append over an unreadable log: %+v", r)
	require.NotContains(t, r.Stdout, "APPEND OK", "want a refusal and no APPEND OK")
	require.Contains(t, r.Stderr, log, "want a refusal naming the log")
	c.wroteNothing("s1", "e1")
}

// TestAMalformedOpenRecordRefusesTheAppendAndWritesNothing is SPEC-CAIRN line
// 30: an open record for the session that does not decode (a torn line, or a
// source that is not a string) is corrupt provenance, and corrupt provenance
// never reads as none. The append refuses at exit 2 naming the log and
// writes nothing; another session's malformed line is not this one's.
func TestAMalformedOpenRecordRefusesTheAppendAndWritesNothing(t *testing.T) {
	t.Parallel()

	for name, bad := range map[string]string{
		"torn":       `{"event":"open","session":"s1","source":"sess`,
		"not-string": `{"event":"open","session":"s1","source":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newRig(t)
			c.ok("open", "--session", "s1", "--source", "session:x", "--publish", "manual")
			log := c.path("log.jsonl")
			testkit.WriteFile(t, log, bad+"\n")
			r := c.run("append", "--session", "s1", "--entry", "e1", "--text", "words that would inherit the pointer", "--publish", "manual")
			require.Equal(t, 2, r.Code, "want exit 2: %+v", r)
			require.NotContains(t, r.Stdout, "APPEND OK")
			require.Contains(t, r.Stderr, log, "want a refusal naming the log")
			c.wroteNothing("s1", "e1")
			require.Equal(t, bad+"\n", testkit.ReadFile(t, log), "a refused append changed the log")

			// A malformed line that names another session is not this one's.
			c.ok("open", "--session", "s2", "--source", "session:y", "--publish", "manual")
			out := c.ok("append", "--session", "s2", "--entry", "e2", "--text", "words of another session", "--publish", "manual")
			require.Contains(t, out, " source=session:y ", "s2 append printed %q, want its own session's source", out)
		})
	}
}

// Every problem of one invocation is named in one run, one line each: the
// missing flags, a --now that is not a clock, and a stray argument together.
func TestEveryProblemIsNamedAtOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"open", []string{"open", "--store", "./c", "--now", "yesterday", "stray"},
			[]string{"--session is required", "--publish is required", "--now must parse", `takes no positional arguments, got "stray"`}},
		{"append", []string{"append", "--text", "a", "--file", "b"},
			[]string{"--store is required", "--session is required", "--entry is required", "--text and --file both"}},
		// Bad values are named with each other, not one per run: an id that is
		// not an id and a policy that is not a policy beside a --now that is not a clock.
		{"bad values", []string{"append", "--store", "./c", "--session", "a b", "--entry", "x/y", "--text", "w", "--publish", "sometimes", "--now", "tomorrow"},
			[]string{`--session "a b" is not an id`, "--now must parse", "--publish \"sometimes\" is not a policy", `--entry "x/y" is not an id`}},
		{"open bad values", []string{"open", "--store", "./c", "--session", "..", "--publish", "always", "--now", "tomorrow"},
			[]string{`--session ".." is not an id`, "--now must parse", "--publish \"always\" is not a policy"}},
		{"two bad things", []string{"receipt", "--store", "s", "--session", "x", "stray"},
			[]string{"--entry is required", `takes no positional arguments, got "stray"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			args := append([]string(nil), tc.args...)
			for i, a := range args {
				if a == "./c" {
					args[i] = dir + "/c"
				}
			}
			r := cli.Run(args...)
			lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
			require.Equal(t, 2, r.Code, "%+v", r)
			require.Empty(t, r.Stdout)
			require.Len(t, lines, len(tc.want), "stderr:\n%s", r.Stderr)
			for i, w := range tc.want {
				assert.Contains(t, lines[i], w, "line %d", i)
				assert.True(t, strings.HasSuffix(lines[i], "; run: nova-cairn help"), "line %d does not end at the door: %q", i, lines[i])
			}
			entries, _ := os.ReadDir(dir)
			assert.Empty(t, entries, "a refused invocation wrote files")
		})
	}
}

// nova-cairn's definition meets the standard its banner and help cannot hold
// by construction: every verb's effect, and a how text of five short lines.
func TestCairnToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, cairnTool().Problems())
}

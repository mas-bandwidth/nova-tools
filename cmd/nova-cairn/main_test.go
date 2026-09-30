// CLI tests for nova-tools #248: the four verbs work end to end, and the
// lifecycle verbs the issue rules out stay refused.
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cli is the tool's entry point in process.
var cli = testkit.Main(run)

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
	require.Contains(t, out, "OPEN OK", "open printed %q", out)
	require.Contains(t, out, "session=s1", "open printed %q", out)
	prose := "the friend's chosen words — café, \"as above\" nowhere, byte-exact"
	out = c.ok("append", "--session", "s1", "--entry", "e1", "--text", prose, "--source", "bench/session-3#L9", "--publish", "manual")
	require.Contains(t, out, "APPEND OK", "append printed %q", out)
	require.Contains(t, out, "persisted=true published=false", "append printed %q", out)
	// The duplicate request succeeds without a duplicate entry.
	out = c.ok("append", "--session", "s1", "--entry", "e1", "--text", prose, "--publish", "manual")
	require.Contains(t, out, "duplicate=true", "retry printed %q, want duplicate=true", out)
	// Same id, different prose: exit 1, never an overwrite.
	code := c.run("append", "--session", "s1", "--entry", "e1", "--text", "other words", "--publish", "manual").Code
	require.Equal(t, 1, code, "conflicting append exited %d, want 1", code)
	out = c.ok("receipt", "--session", "s1", "--entry", "e1")
	require.Contains(t, out, "RECEIPT OK", "receipt printed %q", out)
	require.Contains(t, out, "persisted=true published=false", "receipt printed %q", out)
	out = c.ok("index")
	require.Contains(t, out, "INDEX ENTRY session=s1 entry=e1", "index printed %q", out)
	require.Contains(t, out, "INDEX COVERAGE sessions=1 entries=1", "coverage printed %q", out)
	require.Equal(t, prose, testkit.ReadJSON[entry](t, c.path("entries", "s1", "e1.json")).Text, "the stored entry is not the exact prose")
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
	out = c.ok("index")
	require.Contains(t, out, "entries=2", "index after recovery printed %q", out)
	require.Contains(t, out, "INDEX ENTRY session=s entry=other", "other writer lost or partial indexed: %q", out)
	require.Contains(t, out, "INDEX ENTRY session=s entry=mine", "other writer lost or partial indexed: %q", out)
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
	require.Contains(t, out, "INDEX COVERAGE sessions=2 entries=2", "index printed %q", out)
	out = c.ok("index", "--session", "alpha")
	require.Contains(t, out, "INDEX ENTRY session=alpha entry=e", "per-session index printed %q", out)
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
	for _, want := range []string{"entry=inherits stamp=", "entry=own stamp="} {
		require.Contains(t, out, want, "index printed %q, missing %q", out, want)
	}
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

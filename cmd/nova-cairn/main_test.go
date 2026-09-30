// CLI tests for nova-tools #248: the four verbs work end to end, and the
// lifecycle verbs the issue rules out stay refused.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runOK(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var in *strings.Reader
	if stdin == "" {
		in = strings.NewReader("")
	} else {
		in = strings.NewReader(stdin)
	}
	if code := run(args, in, &stdout, &stderr); code != 0 {
		t.Fatalf("run %v exited %d: stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func runCode(stdin string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestOpenAppendIndexReceiptRoundTrip(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	out, _ := runOK(t, "", "open", "--store", store, "--session", "s1", "--source", "bench/session-3", "--publish", "manual")
	require.Contains(t, out, "OPEN OK", "open printed %q", out)
	require.Contains(t, out, "session=s1", "open printed %q", out)
	prose := "the friend's chosen words — café, \"as above\" nowhere, byte-exact"
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", prose, "--source", "bench/session-3#L9", "--publish", "manual")
	require.Contains(t, out, "APPEND OK", "append printed %q", out)
	require.Contains(t, out, "persisted=true published=false", "append printed %q", out)
	// The duplicate request succeeds without a duplicate entry.
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", prose, "--publish", "manual")
	require.Contains(t, out, "duplicate=true", "retry printed %q, want duplicate=true", out)
	// Same id, different prose: exit 1, never an overwrite.
	code, _, _ := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", "other words", "--publish", "manual")
	require.Equal(t, 1, code, "conflicting append exited %d, want 1", code)
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "e1")
	require.Contains(t, out, "RECEIPT OK", "receipt printed %q", out)
	require.Contains(t, out, "persisted=true published=false", "receipt printed %q", out)
	out, _ = runOK(t, "", "index", "--store", store)
	require.Contains(t, out, "INDEX ENTRY session=s1 entry=e1", "index printed %q", out)
	require.Contains(t, out, "INDEX COVERAGE sessions=1 entries=1", "coverage printed %q", out)
	raw, err := os.ReadFile(filepath.Join(store, "entries", "s1", "e1.json"))
	require.NoError(t, err)
	var stored struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, prose, stored.Text, "stored entry holds %q, want the exact prose %q", stored.Text, prose)
}

func TestOfflineAppendSucceedsWithPublicationPending(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "deferred")
	out, _ := runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "e",
		"--text", "offline note", "--publish", "deferred")
	require.Contains(t, out, "persisted=true published=false", "offline append printed %q", out)
}

func TestAppendViaFileAndStdinKeepsExactBytes(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "never")
	prose := "line one\nline two  with trailing spaces   \n\ttabbed\n"
	f := filepath.Join(t.TempDir(), "note.txt")
	require.NoError(t, os.WriteFile(f, []byte(prose), 0o644))
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "from-file", "--file", f, "--publish", "never")
	runOK(t, prose, "append", "--store", store, "--session", "s", "--entry", "from-stdin", "--file", "-", "--publish", "never")
	for _, id := range []string{"from-file", "from-stdin"} {
		out, _ := runOK(t, "", "receipt", "--store", store, "--session", "s", "--entry", id)
		require.Contains(t, out, "RECEIPT OK", "receipt %s printed %q", id, out)
	}
	raw, err := os.ReadFile(filepath.Join(store, "entries", "s", "from-stdin.json"))
	require.NoError(t, err)
	var stored struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, prose, stored.Text, "stdin bytes not preserved: got %q want %q", stored.Text, prose)
}

func TestInterruptedAppendRecoversAtCLI(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "manual")
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "other",
		"--text", "other writer's note", "--publish", "manual")
	edir := filepath.Join(store, "entries", "s")
	require.NoError(t, os.MkdirAll(edir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(edir, "mine.json.tmp"), []byte("{partial"), 0o644))
	out, _ := runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "mine",
		"--text", "my note after the crash", "--publish", "manual")
	require.Contains(t, out, "APPEND OK", "recovery printed %q", out)
	out, _ = runOK(t, "", "index", "--store", store)
	require.Contains(t, out, "entries=2", "index after recovery printed %q", out)
	require.Contains(t, out, "INDEX ENTRY session=s entry=other", "other writer lost or partial indexed: %q", out)
	require.Contains(t, out, "INDEX ENTRY session=s entry=mine", "other writer lost or partial indexed: %q", out)
}

func TestExistingDirtyWorkIsUntouched(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	dirty := filepath.Join(store, "my-unfinished-work.md")
	before := []byte("half-written thought, do not touch\n")
	require.NoError(t, os.WriteFile(dirty, before, 0o644))
	runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "never")
	runOK(t, "", "append", "--store", store, "--session", "s", "--entry", "e",
		"--text", "a checkpoint alongside dirty work", "--publish", "never")
	runOK(t, "", "index", "--store", store)
	after, err := os.ReadFile(dirty)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "dirty work changed to %q", after)
	_, err = os.Stat(filepath.Join(store, ".git"))
	require.True(t, os.IsNotExist(err), "tool must not init or touch version control in the store")
}

func TestConcurrentRecordsAndAlternateHeaders(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	for _, s := range []string{"alpha", "beta"} {
		runOK(t, "", "open", "--store", store, "--session", s, "--publish", "never")
		runOK(t, "", "append", "--store", store, "--session", s, "--entry", "e",
			"--text", "note in "+s, "--publish", "never")
	}
	sessFile := filepath.Join(store, "sessions", "alpha.md")
	raw, err := os.ReadFile(sessFile)
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	lines[0] = "# Our team files records under its own headings"
	require.NoError(t, os.WriteFile(sessFile, []byte(strings.Join(lines, "\n")), 0o644))
	out, _ := runOK(t, "", "index", "--store", store)
	require.Contains(t, out, "INDEX COVERAGE sessions=2 entries=2", "index printed %q", out)
	out, _ = runOK(t, "", "index", "--store", store, "--session", "alpha")
	require.Contains(t, out, "INDEX ENTRY session=alpha entry=e", "per-session index printed %q", out)
}

func TestLifecycleVerbsStayRefused(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	for _, verb := range []string{"seal", "consume", "delete", "grade", "consolidate", "wake", "rollup", "retention"} {
		code, _, _ := runCode("", verb, "--store", store)
		require.Equal(t, 2, code, "%s exited %d, want 2 (unknown subcommand)", verb, code)
	}
}

func TestMissingFlagsAreRefusedNeverGuessed(t *testing.T) {
	t.Parallel()

	code, _, _ := runCode("", "open", "--session", "s")
	require.Equal(t, 2, code, "open without --store exited %d, want 2", code)
	code, _, _ = runCode("", "append", "--store", "x", "--session", "s", "--entry", "e",
		"--publish", "never")
	require.Equal(t, 2, code, "append without words exited %d, want 2", code)
	code, _, _ = runCode("", "append", "--store", "x", "--session", "s")
	require.Equal(t, 2, code, "append without --entry/--publish exited %d, want 2", code)
}

func TestBadClockIsRefused(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	code, _, _ := runCode("", "open", "--store", store, "--session", "s",
		"--publish", "never", "--now", "tomorrow-ish")
	require.Equal(t, 2, code, "open with bad --now exited %d, want 2", code)
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

	store := t.TempDir()
	ptr := filepath.Join(store, "no such transcript", "session.jsonl")
	field := strings.ReplaceAll(ptr, " ", `\x20`)
	out, _ := runOK(t, "", "open", "--store", store, "--session", "s1", "--source", ptr, "--publish", "manual")
	require.Contains(t, out, " source="+field+" ", "open printed %q, want source=%s", out, field)
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "inherits",
		"--text", "words with no pointer of their own", "--publish", "manual")
	require.Contains(t, out, " source="+field+" ", "append with no --source printed %q, want the session's source=%s", out, field)
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s1", "--entry", "own",
		"--text", "words with a pointer", "--source", "bench-a/session-7#L3", "--publish", "manual")
	require.Contains(t, out, " source=bench-a/session-7#L3 ", "append --source printed %q, want its own source", out)
	out, _ = runOK(t, "", "index", "--store", store)
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
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "s1", "--entry", "inherits")
	require.Contains(t, out, " source="+field+" ", "receipt printed %q, want source=%s", out, field)
	_, err := os.Stat(ptr)
	require.True(t, os.IsNotExist(err), "the source pointer was created or opened: %v", err)

	// A session opened with no pointer: the entry has none, and says so.
	runOK(t, "", "open", "--store", store, "--session", "s2", "--publish", "manual")
	out, _ = runOK(t, "", "append", "--store", store, "--session", "s2", "--entry", "bare",
		"--text", "words from nowhere named", "--publish", "manual")
	require.Contains(t, out, " source=- ", "append with no pointer anywhere printed %q, want source=-", out)
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "s2", "--entry", "bare")
	require.Contains(t, out, " source=- ", "receipt with no pointer printed %q, want source=-", out)
}

// appendWroteNothing is the "nothing written" half of the two provenance
// refusals: no entry file for the id and no pointer line in the session file.
func appendWroteNothing(t *testing.T, store, session, entry string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(store, "entries", session, entry+".json")); !os.IsNotExist(err) {
		t.Fatalf("a refused append left an entry file: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(store, "sessions", session+".md"))
	require.NoError(t, err, "read the session file")
	require.NotContains(t, string(raw), "ENTRY "+entry+" ", "a refused append left a pointer line:\n%s", raw)
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

	store := t.TempDir()
	runOK(t, "", "open", "--store", store, "--session", "s1", "--source", "session:x", "--publish", "manual")
	log := filepath.Join(store, "log.jsonl")
	require.NoError(t, os.Chmod(log, 0o200), "chmod")
	t.Cleanup(func() { _ = os.Chmod(log, 0o644) })
	// The fixture is only a fixture if the read really fails: some
	// filesystems and privileged runs read a 0200 file anyway.
	if _, err := os.ReadFile(log); err == nil {
		t.Skip("the 0200 log is still readable here (filesystem or privilege); the permission case cannot be made")
	}

	code, out, errOut := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e1",
		"--text", "words that would inherit the pointer", "--publish", "manual")
	require.Equal(t, 2, code, "append over an unreadable log exited %d, want 2: stdout=%q stderr=%q", code, out, errOut)
	require.NotContains(t, out, "APPEND OK", "want a refusal naming %s and no APPEND OK: stdout=%q stderr=%q", log, out, errOut)
	require.Contains(t, errOut, log, "want a refusal naming %s and no APPEND OK: stdout=%q stderr=%q", log, out, errOut)
	appendWroteNothing(t, store, "s1", "e1")
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
			store := t.TempDir()
			runOK(t, "", "open", "--store", store, "--session", "s1", "--source", "session:x", "--publish", "manual")
			log := filepath.Join(store, "log.jsonl")
			require.NoError(t, os.WriteFile(log, []byte(bad+"\n"), 0o644), "write log")
			code, out, errOut := runCode("", "append", "--store", store, "--session", "s1", "--entry", "e1",
				"--text", "words that would inherit the pointer", "--publish", "manual")
			if code != 2 || strings.Contains(out, "APPEND OK") || !strings.Contains(errOut, log) {
				t.Fatalf("want exit 2 naming %s: code=%d stdout=%q stderr=%q", log, code, out, errOut)
			}
			appendWroteNothing(t, store, "s1", "e1")
			raw, _ := os.ReadFile(log)
			require.Equal(t, bad+"\n", string(raw), "a refused append changed the log:\n%s", raw)

			// A malformed line that names another session is not this one's.
			runOK(t, "", "open", "--store", store, "--session", "s2", "--source", "session:y", "--publish", "manual")
			out, _ = runOK(t, "", "append", "--store", store, "--session", "s2", "--entry", "e2",
				"--text", "words of another session", "--publish", "manual")
			require.Contains(t, out, " source=session:y ", "s2 append printed %q, want its own session's source", out)
		})
	}
}

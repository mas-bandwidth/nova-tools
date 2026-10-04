// Red test for nova-tools #248: nova-cairn optional checkpoint mechanics
// without imposing a memory lifecycle.
//
// The friend's exact words must survive byte-for-byte with a real clock stamp
// and stable identifiers; a retried append must not duplicate; local
// persistence and remote publication are reported separately; an interrupted
// append must be recoverable without touching other writers' work.
package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/require"
)

// persistedNotPublished requires the split every append and receipt reports:
// durable here (persisted=true) and not published there (published=false).
func persistedNotPublished(t *testing.T, persisted, published bool, msgAndArgs ...any) {
	t.Helper()
	require.True(t, persisted, msgAndArgs...)
	require.False(t, published, msgAndArgs...)
}

func TestAppendKeepsExactProseAndReportsPersistenceSeparately(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // nanoseconds: the stamp round-trips at full precision
	require.NoError(t, Open(store, "sess-1", "bench-a/session-7", now, "manual"), "Open")
	prose := "the friend's chosen words — \"as above\" is banned, \"café — 日本語\" stays byte-exact\nsecond line"
	res, err := Append(store, "sess-1", "e-1", prose, "bench-a/session-7#L3", now, "manual")
	require.NoError(t, err, "Append")
	require.True(t, res.Persisted, "Append must report persisted=true once the note is fsync-durable, got %+v", res)
	require.False(t, res.Published, "local persistence must not imply remote publication, got %+v", res)
	got, err := EntryText(store, "sess-1", "e-1")
	require.NoError(t, err, "EntryText")
	require.Equal(t, prose, got, "prose mangled")
	rc, err := Receipt(store, "sess-1", "e-1")
	require.NoError(t, err, "Receipt")
	persistedNotPublished(t, rc.Persisted, rc.Published, "receipt must repeat persisted=true published=false, got %+v", rc)
	require.True(t, rc.Stamp.Equal(now), "receipt stamp = %v, want the real clock stamp %v", rc.Stamp, now)
}

func TestDuplicateAppendIsIdempotentAndConflictingEntryRefused(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	require.NoError(t, Open(store, "s", "src", now, "never"), "Open")
	_, err := Append(store, "s", "e", "same words", "src", now, "never")
	require.NoError(t, err, "Append")
	dup, err := Append(store, "s", "e", "same words", "src", now, "never")
	require.NoError(t, err, "retry of the same request must succeed")
	require.True(t, dup.Duplicate, "retry must report duplicate=true, got %+v", dup)
	_, err = Append(store, "s", "e", "DIFFERENT words", "src", now, "never")
	require.Error(t, err, "same entry id with different prose must be refused, not duplicated")
	rows, _, err := Index(store, "", 0)
	require.NoError(t, err, "Index")
	require.Len(t, rows, 1, "duplicate retry left more than one index row")
}

func TestInterruptedAppendRecoversAndPreservesOtherWriters(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	require.NoError(t, Open(store, "s", "src", now, "never"), "Open")
	_, err := Append(store, "s", "other", "other writer's note", "src", now, "never")
	require.NoError(t, err, "Append other")
	// Simulate a crash between temp write and rename: a stale partial file.
	testkit.WriteFile(t, filepath.Join(store, "entries", "s", ".mine.json.tmp-deadbeef"), "{partial")
	res, err := Append(store, "s", "mine", "my note after the crash", "src", now, "never")
	require.NoError(t, err, "retry after interruption must succeed")
	persistedNotPublished(t, res.Persisted, res.Published, "recovered append must report persisted=true published=false, got %+v", res)
	got, _ := EntryText(store, "s", "other")
	require.Equal(t, "other writer's note", got, "other writer's entry mangled")
	rows, _, err := Index(store, "", 0)
	require.NoError(t, err, "Index")
	require.Len(t, rows, 2, "partial tmp must never index")
}

func TestOfflineAppendSucceedsWithPublicationPending(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	require.NoError(t, Open(store, "s", "src", now, "deferred"), "Open")
	res, err := Append(store, "s", "e", "offline note", "", now, "deferred")
	require.NoError(t, err, "offline append must succeed locally")
	persistedNotPublished(t, res.Persisted, res.Published, "offline append is durable-but-unpublished: persisted=true published=false, got %+v", res)
}

func TestOpenConcurrentRecordsAndAlternateHeaders(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	for _, s := range []string{"alpha", "beta"} {
		require.NoError(t, Open(store, s, "src", now, "never"), "Open %s", s)
		_, err := Append(store, s, "e", "note in "+s, "src", now, "never")
		require.NoError(t, err, "Append %s", s)
	}
	// A friend with different header conventions rewrites the session file's
	// header by hand; the entries and the index must survive it.
	sessFile := filepath.Join(store, "sessions", "alpha.md")
	lines := strings.Split(testkit.ReadFile(t, sessFile), "\n")
	lines[0] = "# My own heading convention — tool must not care"
	testkit.WriteFile(t, sessFile, strings.Join(lines, "\n"))
	got, err := EntryText(store, "alpha", "e")
	require.NoError(t, err, "EntryText after header rewrite")
	require.Equal(t, "note in alpha", got, "entry mangled")
	rows, total, err := Index(store, "", 0)
	require.NoError(t, err, "Index")
	require.Equal(t, 2, total, "want 2 across both concurrent records")
	require.Len(t, rows, 2, "want 2 across both concurrent records")
	require.Equal(t, Ledger{Sessions: 2, Entries: 2}, Coverage(store), "coverage ledger")
}

func TestUnreadableExistingEntryRefusedOnAppendAndRead(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	require.NoError(t, Open(store, "s", "src", now, "manual"), "Open")
	prose := "initial durable words"
	res, err := Append(store, "s", "e1", prose, "src", now, "manual")
	require.NoError(t, err, "initial Append")
	require.True(t, res.Persisted)

	entryFile := filepath.Join(store, "entries", "s", "e1.json")
	require.NoError(t, os.Chmod(entryFile, 0o000), "chmod unreadable")
	t.Cleanup(func() { require.NoError(t, os.Chmod(entryFile, 0o644), "restore mode on cleanup") })

	// If running on a filesystem/platform where chmod 0000 remains readable.
	if _, err := os.ReadFile(entryFile); err == nil {
		t.Skip("skipping unreadable permission test: file remains readable despite mode 0000 on this filesystem/platform")
	}

	// C1: Appending with different prose or same id must refuse due to unreadable existing state
	// and MUST NOT overwrite or replace the file.
	_, err = Append(store, "s", "e1", "conflicting prose", "src", now, "manual")
	require.Error(t, err, "append against unreadable existing entry must fail")
	require.True(t, os.IsPermission(err) || strings.Contains(err.Error(), "permission"), "must surface read permission error, got %v", err)

	// Verify reading unreadable entry fails and surfaces read error, not NotFoundError.
	_, err = EntryText(store, "s", "e1")
	require.Error(t, err, "EntryText against unreadable entry must fail")
	var notFound *NotFoundError
	require.False(t, errors.As(err, &notFound), "unreadable entry must not report NotFoundError, got %v", err)

	_, err = Receipt(store, "s", "e1")
	require.Error(t, err, "Receipt against unreadable entry must fail")
	require.False(t, errors.As(err, &notFound), "unreadable entry must not report NotFoundError, got %v", err)

	// Restore permission and verify original entry was completely untouched.
	require.NoError(t, os.Chmod(entryFile, 0o644))
	got, err := EntryText(store, "s", "e1")
	require.NoError(t, err)
	require.Equal(t, prose, got, "original entry words must be preserved")
}

func TestCorruptStampRejectedByReaders(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 123456789, time.UTC) // a clock reading with nanoseconds, passed in
	require.NoError(t, Open(store, "s", "src", now, "manual"), "Open")

	entriesDir := filepath.Join(store, "entries", "s")
	require.NoError(t, os.MkdirAll(entriesDir, 0o755))

	// Store an entry with valid JSON but an invalid RFC 3339 timestamp.
	entryFile := filepath.Join(entriesDir, "e-bad.json")
	corruptContent := `{"session":"s","id":"e-bad","stamp":"2026-99-28T01:02:03Z","source":"src","publish":"manual","text":"some words"}`
	require.NoError(t, os.WriteFile(entryFile, []byte(corruptContent), 0o644))

	// Receipt must refuse with corruption error naming invalid stamp, never substitute zero time.
	_, err := Receipt(store, "s", "e-bad")
	require.Error(t, err, "Receipt must reject invalid stamp")
	require.Contains(t, err.Error(), "is corrupt", "Receipt error must report corruption, got %v", err)
	require.Contains(t, err.Error(), "invalid stamp", "Receipt error must mention invalid stamp, got %v", err)

	// Index scoped to session must refuse with corruption error.
	_, _, err = Index(store, "s", 0)
	require.Error(t, err, "Index scoped to session must reject invalid stamp")
	require.Contains(t, err.Error(), "is corrupt", "Index error must report corruption, got %v", err)
	require.Contains(t, err.Error(), "invalid stamp", "Index error must mention invalid stamp, got %v", err)

	// Index over all sessions must also refuse.
	_, _, err = Index(store, "", 0)
	require.Error(t, err, "Index over all sessions must reject invalid stamp")
	require.Contains(t, err.Error(), "is corrupt", "Index error must report corruption, got %v", err)
	require.Contains(t, err.Error(), "invalid stamp", "Index error must mention invalid stamp, got %v", err)

	// EntryText must also reject corrupt entry stamp.
	_, err = EntryText(store, "s", "e-bad")
	require.Error(t, err, "EntryText must reject invalid stamp")
	require.Contains(t, err.Error(), "is corrupt", "EntryText error must report corruption, got %v", err)
	require.Contains(t, err.Error(), "invalid stamp", "EntryText error must mention invalid stamp, got %v", err)

	// Malformed JSON must also be rejected by Receipt and Index.
	entryBroken := filepath.Join(entriesDir, "e-broken.json")
	require.NoError(t, os.WriteFile(entryBroken, []byte(`{"session":"s",`), 0o644))
	_, err = Receipt(store, "s", "e-broken")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is corrupt")
}

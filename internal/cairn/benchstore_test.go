// Red test for the bench store: a directory of markdown files, one per
// session, kept by hand.
//
// The hurt (2026-09-18, the Studio): the documented sequence
//
//	nova-cairn append --store cairns --session b9395d11 --entry <id> \
//	  --publish manual --file -
//
// refused with `no such session "b9395d11"; open first` although
// `cairns/b9395d11.md` was right there, 370 lines of it, appended by hand all
// day. The tool looks only under `sessions/<id>.md`; the bench keeps the file
// directly under the store. Two costs, both real: the refusal was FALSE (the
// record exists), and it named no verb a friend could run — `open first` on a
// store that already holds the session would have written a SECOND record
// under sessions/ and split the session in two.
//
// So: `append` and `open` read the store's own shape. A file per session under
// the store is a record like any other; the append lands as a dated section in
// the file's own shape, and nothing else in the directory is touched. The
// fixture below is the first 60 lines of the real cairn, copied on the day.
package cairn

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// benchFixture is today's real cairn, copied into testdata. A test that proves
// an append lands "in the file's own shape" has to read the real shape.
const benchFixture = "testdata/bench-b9395d11.md"

// benchStore lays the fixture down as the bench keeps it: <store>/<session>.md,
// no sessions/ directory, no entries/, no log.
func benchStore(t *testing.T, session string) (store, file string, before []byte) {
	t.Helper()
	raw, err := os.ReadFile(benchFixture)
	require.NoError(t, err, "read fixture")
	store = t.TempDir()
	file = filepath.Join(store, session+".md")
	require.NoError(t, os.WriteFile(file, raw, 0o644), "write bench file")
	return store, file, raw
}

func TestAppendLandsInTheBenchFileStore(t *testing.T) {
	t.Parallel()

	const session = "b9395d11"
	store, file, before := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	prose := "## 14:05Z beat: the lane class landed\n- two PRs, red first; the tails on the card."

	res, err := Append(store, session, "beat-1405", prose, "transcript#L1", now, "manual")
	require.NoError(t, err, "Append into the bench store")
	persistedNotPublished(t, res.Persisted, res.Published, "append must report persisted=true published=false, got %+v", res)

	got := testkit.ReadFile(t, file)
	require.True(t, strings.HasPrefix(got, string(before)), "the hand-kept record was rewritten; an append only adds to the end")
	// strings.Contains under True, not require.Contains: a failure names the
	// record's tail, never the whole hand-kept record.
	head := "## " + now.Format(time.RFC3339) + " — beat-1405"
	require.True(t, strings.Contains(got, head), "no dated section %q in the file; the append must land in the file's own shape:\n%s", head, tail(got, 400))
	require.True(t, strings.Contains(got, prose), "the friend's words are not in the record byte-for-byte:\n%s", tail(got, 400))
	require.NotContains(t, got[strings.Index(got, head):], "\n\n\n", "the appended section is not in the file's shape (one blank line between sections)")
	// Nothing else appears beside a hand-kept store: no sessions/, no entries/.
	for _, unwanted := range []string{"sessions", "entries", "log.jsonl"} {
		_, err := os.Stat(filepath.Join(store, unwanted))
		assert.ErrorIs(t, err, fs.ErrNotExist, "append created %s/ beside a hand-kept store; the file IS the record", unwanted)
	}
}

func TestAppendToTheBenchFileRetriesAsADuplicate(t *testing.T) {
	t.Parallel()

	const session = "b9395d11"
	store, file, _ := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	prose := "the same words, twice"

	_, err := Append(store, session, "e-1", prose, "src", now, "manual")
	require.NoError(t, err, "first Append")
	once := testkit.ReadFile(t, file)
	res, err := Append(store, session, "e-1", prose, "src", now.Add(time.Minute), "manual")
	require.NoError(t, err, "retry Append")
	assert.True(t, res.Duplicate, "a retry of the same id and the same words is duplicate=true, got %+v", res)
	assert.Equal(t, once, testkit.ReadFile(t, file), "the retry wrote a second section; a retry adds nothing")
}

func TestAppendToTheBenchFileRefusesDifferentProseUnderTheSameID(t *testing.T) {
	t.Parallel()

	const session = "b9395d11"
	store, _, _ := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	_, err := Append(store, session, "e-1", "the first words", "src", now, "manual")
	require.NoError(t, err, "first Append")
	_, err = Append(store, session, "e-1", "different words", "src", now, "manual")
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict, "different prose under a used id must be a conflict")
}

func TestOpenOnABenchFileIsANoOpAndNeverSplitsTheRecord(t *testing.T) {
	t.Parallel()

	const session = "b9395d11"
	store, file, before := benchStore(t, session)
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)

	require.NoError(t, Open(store, session, "src", now, "manual"), "Open on an existing bench record")
	_, err := os.Stat(filepath.Join(store, "sessions", session+".md"))
	require.ErrorIs(t, err, fs.ErrNotExist, "open wrote a second record under sessions/; the session would be split in two")
	require.Equal(t, string(before), testkit.ReadFile(t, file), "open rewrote the hand-kept record; re-opening an open session is a no-op")
}

func TestAppendWithNoRecordAnywhereNamesTheOpenVerb(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	now := time.Date(2026, 9, 18, 14, 5, 0, 0, time.UTC)
	_, err := Append(store, "nosuch", "e-1", "words", "src", now, "manual")
	require.Error(t, err, "append into a store with no record must refuse")
	for _, want := range []string{"nova-cairn open", "--store", "--session 'nosuch'", "--publish"} {
		assert.ErrorContains(t, err, want, "the refusal must name the remedy verb whole")
	}
}

func TestCoverageCountsTheBenchSessionFiles(t *testing.T) {
	t.Parallel()

	store, _, _ := benchStore(t, "b9395d11")
	require.Equal(t, 1, Coverage(store).Sessions, "a bench store holding one record; the ledger must not read zero over a store it can append to")
}

// tail is the last n bytes of s, for a failure that names what it saw without
// printing a 370-line record.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

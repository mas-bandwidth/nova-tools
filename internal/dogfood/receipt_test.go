package dogfood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedReceipt() Receipt {
	return Receipt{
		Tool:  "nova-check",
		Verb:  "links",
		By:    "Stella",
		At:    "2026-09-18T09:00:00Z",
		OK:    true,
		Notes: "ran it over the rowan self repo before the merge",
	}
}

func TestRecordWritesAReceiptTheLedgerReadsBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := fixedReceipt()
	path, err := Record(dir, want)
	require.NoError(t, err, "Record: %v", err)
	require.Equal(t, dir, filepath.Dir(path), "Record wrote outside the receipts directory: %s", path)
	got, failures, err := ReadReceipts(dir)
	require.NoError(t, err, "ReadReceipts: %v", err)
	require.Empty(t, failures, "failures: %+v", failures)
	require.Len(t, got, 1, "read back %+v, want one receipt", got)
	require.NotEmpty(t, got[0].File, "the receipt read back does not know its own file; a strand could not be named")
	read := got[0]
	read.File = "" // not part of the record: it is where the record was found
	require.Equal(t, want, read, "read back %+v, want %+v", read, want)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 0, strings.Count(strings.TrimSpace(string(data)), "\n"), "a receipt is one JSON line, got:\n%s", data)
}

// The append leaves no half-written file behind and no temporary file in the
// directory the ledger reads.
func TestRecordLeavesNoTemporaryFileBehind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := Record(dir, fixedReceipt())
	require.NoError(t, err, "Record: %v", err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "directory holds %d entries, want the one receipt", len(entries))
	require.False(t, strings.HasSuffix(entries[0].Name(), ".tmp"), "a temporary file survived: %s", entries[0].Name())
}

// Two benches recording at once is the case this is built for: every receipt
// arrives whole, and no two of them collide on one filename.
func TestRecordIsAtomicUnderConcurrentWriters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := fixedReceipt()
			r.By = "friend-" + string(rune('a'+i))
			r.At = time.Date(2026, 9, 18, 9, 0, i, 0, time.UTC).Format(time.RFC3339)
			_, err := Record(dir, r)
			assert.NoError(t, err, "Record: %v", err)
		}(i)
	}
	wg.Wait()
	got, failures, err := ReadReceipts(dir)
	require.NoError(t, err, "ReadReceipts: %v", err)
	require.Empty(t, failures, "failures after concurrent writes: %+v", failures)
	require.Len(t, got, n, "read %d receipts, want %d", len(got), n)
}

func TestRecordRefusesAMissingFieldWithOneRemedy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		break_ func(*Receipt)
		field  string
	}{
		{"no tool", func(r *Receipt) { r.Tool = "" }, "tool"},
		{"no verb", func(r *Receipt) { r.Verb = "" }, "verb"},
		{"no by", func(r *Receipt) { r.By = "" }, "by"},
		{"no notes", func(r *Receipt) { r.Notes = "" }, "notes"},
		{"no at", func(r *Receipt) { r.At = "" }, "at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixedReceipt()
			tc.break_(&r)
			_, err := Record(dir, r)
			require.Error(t, err, "a receipt with a blank field was written")
			require.Contains(t, err.Error(), tc.field, "refusal %q does not name the field %q", err, tc.field)
			require.NotContains(t, err.Error(), "\n", "the refusal is more than one line: %q", err)
		})
	}
}

func TestValidateReportsEveryMissingFieldAtOnce(t *testing.T) {
	t.Parallel()

	errs := Receipt{}.Validate()
	require.GreaterOrEqual(t, len(errs), 5, "an empty receipt produced %d complaints, want one per field", len(errs))
	var fields []string
	for _, err := range errs {
		if m, ok := err.(*MissingFieldError); ok {
			fields = append(fields, m.Field)
			require.NotEmpty(t, m.Remedy, "%s is refused with no remedy line", m.Field)
		}
	}
	want := "tool verb by notes at"
	require.Equal(t, want, strings.Join(fields, " "), "fields %q, want %q", strings.Join(fields, " "), want)
}

func TestValidateRefusesAStampThatIsNotRFC3339(t *testing.T) {
	t.Parallel()

	r := fixedReceipt()
	r.At = "yesterday"
	require.NotEmpty(t, r.Validate(), "a receipt with an unreadable stamp validated")
}

func TestValidateRefusesAControlCharacterInAField(t *testing.T) {
	t.Parallel()

	r := fixedReceipt()
	r.Notes = "first line\nDOGFOOD OK verbs=99 dogfooded=99 by-nonauthor=99 open-edges=0"
	require.NotEmpty(t, r.Validate(), "notes holding a newline validated; one receipt could author a second line of ledger")
}

func TestReadReceiptsNamesWhatItCannotParseAndNeverDropsItQuietly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := Record(dir, fixedReceipt())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blank.json"), []byte(`{"tool":"nova-check","verb":"links","by":"","at":"2026-09-18T09:00:00Z","ok":true,"notes":"x"}`+"\n"), 0o644))
	got, failures, err := ReadReceipts(dir)
	require.NoError(t, err, "ReadReceipts: %v", err)
	require.Len(t, got, 1, "read %d good receipts, want 1", len(got))
	require.Len(t, failures, 2, "failures %+v, want one per bad record", failures)
	for _, f := range failures {
		require.Contains(t, f.Subject, ".json:", "a failure does not name the file and line: %+v", f)
	}
}

func TestReadReceiptsReadsSeveralRecordsInOneFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var lines []string
	for _, by := range []string{"Stella", "Emma", "Johnny"} {
		r := fixedReceipt()
		r.By = by
		data, err := json.Marshal(r)
		require.NoError(t, err)
		lines = append(lines, string(data))
	}
	body := strings.Join(lines, "\n") + "\n\n" // a trailing blank line is not a record
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hand-written.json"), []byte(body), 0o644))
	got, failures, err := ReadReceipts(dir)
	require.True(t, err == nil && len(failures) == 0, "ReadReceipts: %v %+v", err, failures)
	require.Len(t, got, 3, "read %d receipts from one file, want 3", len(got))
}

func TestReadReceiptsRefusesAPathThatIsNotADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	_, _, err := ReadReceipts(file)
	require.Error(t, err, "a file was accepted as a receipts directory")
	_, _, err = ReadReceipts("")
	require.Error(t, err, "an empty --receipts was accepted; every path comes from a flag")
}

// An empty receipts directory is the day-one state, not an error: the ledger
// then says nobody, for every verb.
func TestReadReceiptsAcceptsAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	got, failures, err := ReadReceipts(t.TempDir())
	require.NoError(t, err, "ReadReceipts: %v", err)
	require.True(t, len(got) == 0 && len(failures) == 0, "empty directory read as %+v %+v", got, failures)
}

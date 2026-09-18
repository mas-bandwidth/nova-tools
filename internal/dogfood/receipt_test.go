package dogfood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	dir := t.TempDir()
	want := fixedReceipt()
	path, err := Record(dir, want)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("Record wrote outside the receipts directory: %s", path)
	}
	got, failures, err := ReadReceipts(dir)
	if err != nil {
		t.Fatalf("ReadReceipts: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures: %+v", failures)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(data)), "\n") != 0 {
		t.Fatalf("a receipt is one JSON line, got:\n%s", data)
	}
}

// The append leaves no half-written file behind and no temporary file in the
// directory the ledger reads.
func TestRecordLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	if _, err := Record(dir, fixedReceipt()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want the one receipt", len(entries))
	}
	if strings.HasSuffix(entries[0].Name(), ".tmp") {
		t.Fatalf("a temporary file survived: %s", entries[0].Name())
	}
}

// Two benches recording at once is the case this is built for: every receipt
// arrives whole, and no two of them collide on one filename.
func TestRecordIsAtomicUnderConcurrentWriters(t *testing.T) {
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
			if _, err := Record(dir, r); err != nil {
				t.Errorf("Record: %v", err)
			}
		}(i)
	}
	wg.Wait()
	got, failures, err := ReadReceipts(dir)
	if err != nil {
		t.Fatalf("ReadReceipts: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures after concurrent writes: %+v", failures)
	}
	if len(got) != n {
		t.Fatalf("read %d receipts, want %d", len(got), n)
	}
}

func TestRecordRefusesAMissingFieldWithOneRemedy(t *testing.T) {
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
			if err == nil {
				t.Fatal("a receipt with a blank field was written")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("refusal %q does not name the field %q", err, tc.field)
			}
			if strings.Contains(err.Error(), "\n") {
				t.Fatalf("the refusal is more than one line: %q", err)
			}
		})
	}
}

func TestValidateReportsEveryMissingFieldAtOnce(t *testing.T) {
	errs := Receipt{}.Validate()
	if len(errs) < 5 {
		t.Fatalf("an empty receipt produced %d complaints, want one per field", len(errs))
	}
	var fields []string
	for _, err := range errs {
		if m, ok := err.(*MissingFieldError); ok {
			fields = append(fields, m.Field)
			if m.Remedy == "" {
				t.Fatalf("%s is refused with no remedy line", m.Field)
			}
		}
	}
	want := "tool verb by notes at"
	if strings.Join(fields, " ") != want {
		t.Fatalf("fields %q, want %q", strings.Join(fields, " "), want)
	}
}

func TestValidateRefusesAStampThatIsNotRFC3339(t *testing.T) {
	r := fixedReceipt()
	r.At = "yesterday"
	if errs := r.Validate(); len(errs) == 0 {
		t.Fatal("a receipt with an unreadable stamp validated")
	}
}

func TestValidateRefusesAControlCharacterInAField(t *testing.T) {
	r := fixedReceipt()
	r.Notes = "first line\nDOGFOOD OK verbs=99 dogfooded=99 by-nonauthor=99 open-edges=0"
	errs := r.Validate()
	if len(errs) == 0 {
		t.Fatal("notes holding a newline validated; one receipt could author a second line of ledger")
	}
}

func TestReadReceiptsNamesWhatItCannotParseAndNeverDropsItQuietly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Record(dir, fixedReceipt()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blank.json"), []byte(`{"tool":"nova-check","verb":"links","by":"","at":"2026-09-18T09:00:00Z","ok":true,"notes":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, failures, err := ReadReceipts(dir)
	if err != nil {
		t.Fatalf("ReadReceipts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d good receipts, want 1", len(got))
	}
	if len(failures) != 2 {
		t.Fatalf("failures %+v, want one per bad record", failures)
	}
	for _, f := range failures {
		if !strings.Contains(f.Subject, ".json:") {
			t.Fatalf("a failure does not name the file and line: %+v", f)
		}
	}
}

func TestReadReceiptsReadsSeveralRecordsInOneFile(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for _, by := range []string{"Stella", "Emma", "Johnny"} {
		r := fixedReceipt()
		r.By = by
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	body := strings.Join(lines, "\n") + "\n\n" // a trailing blank line is not a record
	if err := os.WriteFile(filepath.Join(dir, "hand-written.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, failures, err := ReadReceipts(dir)
	if err != nil || len(failures) != 0 {
		t.Fatalf("ReadReceipts: %v %+v", err, failures)
	}
	if len(got) != 3 {
		t.Fatalf("read %d receipts from one file, want 3", len(got))
	}
}

func TestReadReceiptsRefusesAPathThatIsNotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadReceipts(file); err == nil {
		t.Fatal("a file was accepted as a receipts directory")
	}
	if _, _, err := ReadReceipts(""); err == nil {
		t.Fatal("an empty --receipts was accepted; every path comes from a flag")
	}
}

// An empty receipts directory is the day-one state, not an error: the ledger
// then says nobody, for every verb.
func TestReadReceiptsAcceptsAnEmptyDirectory(t *testing.T) {
	got, failures, err := ReadReceipts(t.TempDir())
	if err != nil {
		t.Fatalf("ReadReceipts: %v", err)
	}
	if len(got) != 0 || len(failures) != 0 {
		t.Fatalf("empty directory read as %+v %+v", got, failures)
	}
}

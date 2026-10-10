package dogfood

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReceiptCoverIsReceiptID pins the one shape both the verb that takes a
// receipt id and the record that stores one ask about: eight lowercase hex
// characters, surrounding space forgiven, everything else refused.
func TestReceiptCoverIsReceiptID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"eight lowercase hex", "deadbeef", true},
		{"leading and trailing space is trimmed", "  0a1b2c3d ", true},
		{"all digits", "01234567", true},
		{"empty is not an id", "", false},
		{"seven characters is not an id", "deadbee", false},
		{"nine characters is not an id", "deadbeef0", false},
		{"uppercase hex is not an id", "DEADBEEF", false},
		{"non-hex letters are not an id", "ghijklmn", false},
		{"an embedded space is not an id", "dead beef", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsReceiptID(tc.in), "IsReceiptID(%q)", tc.in)
		})
	}
}

// TestReceiptCoverPlanRecord pins that a plan takes the same path a write would
// and touches nothing, and that it refuses exactly where Record refuses.
func TestReceiptCoverPlanRecord(t *testing.T) {
	t.Parallel()

	t.Run("main path names the path Record writes and writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		want := fixedReceipt()

		planned, err := PlanRecord(dir, want)
		require.NoError(t, err, "PlanRecord: %v", err)
		require.Equal(t, dir, filepath.Dir(planned), "the plan put the receipt outside the receipts directory: %s", planned)
		require.True(t, strings.HasSuffix(planned, want.ID()+".json"), "the plan path %q does not end in the receipt id %s", planned, want.ID())

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries, "PlanRecord wrote %d entries; a plan writes nothing", len(entries))

		wrote, err := Record(dir, want)
		require.NoError(t, err, "Record: %v", err)
		require.Equal(t, wrote, planned, "the plan path and the written path differ")
	})

	for _, tc := range []struct {
		name string
		dir  string
		brk  func(*Receipt)
		want string
	}{
		{"a blank directory is refused", "   ", nil, "no directory given"},
		{"an invalid receipt is refused", "", func(r *Receipt) { r.Tool = "" }, "tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.dir
			if dir == "" {
				dir = t.TempDir()
			}
			r := fixedReceipt()
			if tc.brk != nil {
				tc.brk(&r)
			}
			_, err := PlanRecord(dir, r)
			require.Error(t, err, "PlanRecord accepted %s", tc.name)
			assert.Contains(t, err.Error(), tc.want, "the refusal %q does not name %q", err, tc.want)
		})
	}
}

// TestReceiptCoverRecordLine pins the one line record prints once the receipt
// is on disk, both renderings of ok and issue.
func TestReceiptCoverRecordLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join("receipts", "x.json")
	for _, tc := range []struct {
		name  string
		ok    bool
		issue int
		want  string
	}{
		{
			name:  "an ok receipt with no issue renders a dash",
			ok:    true,
			issue: 0,
			want:  "DOGFOOD RECORD OK tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=yes issue=- file=receipts/x.json",
		},
		{
			name:  "a failed receipt with a filed issue renders the number",
			ok:    false,
			issue: 42,
			want:  "DOGFOOD RECORD OK tool=nova-check verb=links by=Stella at=2026-09-18T09:00:00Z ok=no issue=42 file=receipts/x.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixedReceipt()
			r.OK = tc.ok
			r.Issue = tc.issue
			assert.Equal(t, tc.want, r.RecordLine(path))
		})
	}
}

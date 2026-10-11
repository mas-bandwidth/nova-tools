package dogfood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A receipt file holds exactly one JSON object per nonblank line, and the
// reader refuses anything after the object: a second object or garbage on the
// same line is evidence the parser has not read, and accepting it would let
// that evidence vanish from the gate instead of failing by name
// (ReadReceipts's contract; docs/STANDARD.md sections 2 and 3, no silent
// failure). Trailing whitespace stays accepted, and one rejected line never
// discards the evidence of the sibling lines around it.
func TestReadReceiptsRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	valid, err := json.Marshal(fixedReceipt())
	require.NoError(t, err, "json.Marshal: %v", err)

	for _, tc := range []struct {
		name      string
		line      string
		wantReads int
		wantFail  bool
	}{
		{"trailing whitespace is not content", string(valid) + "  \t", 2, false},
		{"a second JSON object is refused", string(valid) + " " + string(valid), 1, true},
		{"arbitrary garbage after the object is refused", string(valid) + " not json", 1, true},
		{"a malformed second object is refused", string(valid) + ` {"tool":`, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Line 1 is a valid sibling receipt: a rejected line 2 must not
			// discard it.
			body := string(valid) + "\n" + tc.line + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "one.json"), []byte(body), 0o644))
			got, failures, err := ReadReceipts(dir)
			require.NoError(t, err, "ReadReceipts: %v", err)
			assert.Len(t, got, tc.wantReads, "read %+v, want %d receipts", got, tc.wantReads)
			if !tc.wantFail {
				assert.Empty(t, failures, "failures: %+v", failures)
				return
			}
			// Only the sibling line survives; the rejected line 2 reads as no
			// receipt at all.
			for _, r := range got {
				assert.True(t, strings.HasSuffix(r.File, ":1"), "receipts come from the sibling line, got %q", r.File)
			}
			require.Len(t, failures, 1, "failures: %+v, want one named Failure", failures)
			assert.Equal(t, filepath.Join(dir, "one.json:2"), failures[0].Subject, "the failure does not name the line holding the trailing content: %+v", failures[0])
		})
	}
}

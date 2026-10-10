//go:build unix

package dogfood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadReceiptsNamesASymlinkedAndAFifoJSONInsteadOfReadingThem pins that ReadReceipts
// names a symlinked and a FIFO JSON file as failures instead of reading them.
func TestReadReceiptsNamesASymlinkedAndAFifoJSONInsteadOfReadingThem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	good := fixedReceipt()
	goodBytes, err := json.Marshal(good)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.json"), append(goodBytes, '\n'), 0o644))

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.json")
	outsideReceipt := fixedReceipt()
	outsideReceipt.By = "Johnny"
	outsideBytes, err := json.Marshal(outsideReceipt)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outsideFile, append(outsideBytes, '\n'), 0o644))

	symlinkPath := filepath.Join(dir, "s.json")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	fifoPath := filepath.Join(dir, "f.json")
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}

	got, failures, err := ReadReceipts(dir)
	require.NoError(t, err, "ReadReceipts: %v", err)
	require.Len(t, got, 1, "got %d receipts, want 1 good receipt", len(got))
	assert.Equal(t, "Stella", got[0].By)
	require.Len(t, failures, 2, "failures %+v, want 2 failures for non-regular files", failures)
	for _, f := range failures {
		assert.Contains(t, f.Reason, "not a regular file", "failure reason %q does not contain 'not a regular file'", f.Reason)
	}
	subjects := []string{failures[0].Subject, failures[1].Subject}
	assert.Contains(t, subjects, symlinkPath)
	assert.Contains(t, subjects, fifoPath)
}

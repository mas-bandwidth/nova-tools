package tty

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file that is not a terminal is not one and has no size: a regular file,
// the null device (a character device that is not a terminal) and a file
// already closed. No test opens a terminal.
func TestNothingButATerminalIsOneOrHasASize(t *testing.T) {
	t.Parallel()
	regular, err := os.Create(filepath.Join(t.TempDir(), "plain"))
	require.NoError(t, err)
	defer func() {
		_ = regular.Close() // ignored: test cleanup of temporary file
	}()
	null, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer func() {
		_ = null.Close() // ignored: test cleanup of file handle
	}()
	closed, err := os.Create(filepath.Join(t.TempDir(), "closed"))
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	for name, f := range map[string]*os.File{"a regular file": regular, "the null device": null, "a closed file": closed} {
		assert.False(t, IsTerminal(f), "%s is a terminal", name)
		rows, cols := Size(f)
		assert.Equal(t, [2]int{0, 0}, [2]int{rows, cols}, "%s has a size, %d rows by %d columns", name, rows, cols)
	}
}

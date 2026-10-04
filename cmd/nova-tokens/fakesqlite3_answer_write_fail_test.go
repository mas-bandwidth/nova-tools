package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// failWriter is an answer pipe closed on the reader's side: every write fails.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("planted: the answer pipe is closed")
}

// The fake sqlite3 must refuse like the real tool it stands in for: an answer it cannot
// write is a failure, not a silent exit 0 that leaves the caller reading a truncated row
// (docs/STANDARD.md section 8, "a fake is strict like the real tool").
func TestFakeSqlite3ExitsOneWhenItsAnswerCannotBeWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions"), ocSession("s1", "", "/work/repo"))
	code := fakeSqlite3Main(dir, []string{"-json", "SELECT id FROM session"}, failWriter{})
	assert.Equal(t, 1, code, "exit code on an unwritable answer")
}

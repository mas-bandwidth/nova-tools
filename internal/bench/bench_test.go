package bench

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingCopy answers every shell line and fails the copy.
type failingCopy struct{ lines []string }

func (f *failingCopy) Shell(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
	f.lines = append(f.lines, line)
	if line == MakeLine(DefaultRoot) {
		_, _ = io.WriteString(stdout, DefaultRoot+"/run.AbCd1234\n")
	}
	return 0, nil
}

func (f *failingCopy) Copy(context.Context, string, string, string, bool, io.Writer) error {
	return errors.New("rsync exit 23")
}

// A copy that fails is an error, never a command status, and the directory the
// run made is still removed.
func TestRunRemovesTheRunDirectoryWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	f := &failingCopy{}
	res, err := Run(context.Background(), f, Options{Hosts: []string{"vision"}, Dir: t.TempDir(), Argv: []string{"go", "version"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rsync exit 23")
	assert.True(t, res.Removed)
	assert.Equal(t, []string{MakeLine(DefaultRoot), RemoveLine(DefaultRoot + "/run.AbCd1234")}, f.lines)
}

func TestLinesQuoteEveryWord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `cd '/r/run.x/repo' && GOCACHE='/c' GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 nice -n 19 'go' 'test' '-run' 'A B'\''C'`,
		ExecLine("/r/run.x", "/c", []string{"go", "test", "-run", "A B'C"}))
	assert.Equal(t, "rm -rf -- 'r/run.x'", RemoveLine("r/run.x"))
}

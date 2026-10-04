package update

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failWriter is a writer whose every write fails, so a test pins where a
// write error folds into the verb's exit code (docs/STANDARD.md §2: nothing
// fails silently).
type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) { return 0, errors.New("the write failed") }

// A write error while printing help folds into the exit code: `help` returns 1
// on a failing writer, 0 on a good one, and Run hands the verb that code, so a
// closed pipe cannot leave a reader with no banner and exit 0.
func TestHelpFoldsAWriteErrorIntoTheExitCode(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1, help("nova-update", failWriter{}))
	var good bytes.Buffer
	assert.Equal(t, 0, help("nova-update", &good))
	require.Contains(t, good.String(), updateVerbs)
	assert.Equal(t, 1, Run("nova-update", []string{"help"}, "", failWriter{}, &bytes.Buffer{}, Environment{}))
}

// A write error while printing a refusal folds into the code the caller
// returns: without an error of its own to report, the refusal returns 1; with
// one, the usage code 2 stands, as it does when the line was printed whole.
func TestRefusalFoldsAWriteErrorIntoTheExitCode(t *testing.T) {
	t.Parallel()

	some := errors.New("the input was wrong")
	assert.Equal(t, 1, refusal(failWriter{}, "ADOPT", "nova-update watch -h", nil))
	assert.Equal(t, 2, refusal(failWriter{}, "ADOPT", "nova-update watch -h", some))
	var good bytes.Buffer
	assert.Equal(t, 2, refusal(&good, "ADOPT", "nova-update watch -h", some))
	require.Contains(t, good.String(), "ADOPT REFUSED")
}

// A write error while printing the adoption pass's done line folds into the
// pass's exit code, so a pass whose receipt never reached the reader does not
// report itself done.
func TestWatchAdoptFailsWhenItsDoneLineCannotBePrinted(t *testing.T) {
	t.Parallel()

	code := watchAdopt(context.Background(), nil, options{timeout: time.Second}, time.Now(), failWriter{}, &bytes.Buffer{}, Environment{})
	assert.Equal(t, 1, code)
}

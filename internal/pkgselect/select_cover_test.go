// Unit coverage for the one function the unit tier's per-function table showed
// at zero: (*ListError).Error (select.go:44). The rows run in process over the
// package's own type and its own Runner seam: no sleeps, no real time, no
// network, no subprocess, no Redis or Postgres.
package pkgselect

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelectCoverListErrorTrimsTrailingNewline pins Error (select.go:44): the
// message a caller prints is Text with its trailing newlines trimmed, so the
// failure line the job prints ends in the reason and never in a blank line.
func TestSelectCoverListErrorTrimsTrailingNewline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		// main path: the shape failed builds, one trailing newline trimmed.
		{
			name: "the failed go list shape loses its trailing newline",
			text: "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\nopen /home/u/.cache/go-build/5e/5e1f-d: no such file or directory\n",
			want: "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\nopen /home/u/.cache/go-build/5e/5e1f-d: no such file or directory",
		},
		// spare blank lines at the end lose them all.
		{
			name: "every trailing newline is trimmed",
			text: "go list exited 0 and listed no packages\n\n\n",
			want: "go list exited 0 and listed no packages",
		},
		// refusal: a text with no trailing newline is returned unchanged.
		{
			name: "a text with no trailing newline is returned unchanged",
			text: "select-packages: git diff base HEAD exited 128: fatal: bad object base",
			want: "select-packages: git diff base HEAD exited 128: fatal: bad object base",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &ListError{Text: tc.text}
			assert.Equal(t, tc.want, e.Error(), "Error() of Text %q", tc.text)
		})
	}
}

// TestSelectCoverFailedGoListCarriesItsTextThroughError reaches Error the way
// a caller does: Select's go list fails, the selection is a *ListError, and
// err.Error() is the failure the job prints, ending in the reason with no
// trailing newline (NEVER SILENTLY NOTHING, select.go).
func TestSelectCoverFailedGoListCarriesItsTextThroughError(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{
		diffCmd:  {Stdout: "cmd/foo/foo.go\n"},
		listTree: {Stderr: cacheErr, Code: 1},
	})
	_, err := Select(f.run, Options{Root: tree(t), Base: "base"})
	require.Error(t, err, "a failed go list under the default options fails the job")
	var le *ListError
	require.ErrorAs(t, err, &le, "a failed go list is a ListError")
	assert.Equal(t, "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\nopen /home/u/.cache/go-build/5e/5e1f-d: no such file or directory", err.Error(),
		"the message the caller prints is the text, trimmed")
	assert.False(t, strings.HasSuffix(err.Error(), "\n"), "the printed failure must not end in a blank line")
}

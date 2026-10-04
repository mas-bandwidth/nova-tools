//go:build !windows

package bus

// The per-function coverage table had one function no unit test reached:
// noReplaceRename (internal/bus/noreplace_other.go:28) sat at 0.0%, because
// this build always refuses and every publish path that would call it is
// answered by the hard link first on the filesystems the tests run on. The
// tests here close that line by calling the function directly: the refusal is
// the function's whole behaviour, and it is pinned in its own words.

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoreplaceOtherCoverRefusalNamesThisBuild pins the one path this build's
// noReplaceRename has: whatever paths it is given, it answers the build's own
// sentence about the missing OS no-replace rename, and never a borrowed errno
// that would claim a call was made and answered.
func TestNoreplaceOtherCoverRefusalNamesThisBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "plain paths", from: "draft.json", to: "note.json"},
		{name: "empty paths", from: "", to: ""},
		{name: "missing source", from: "no-such-draft.json", to: "note.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := noReplaceRename(tt.from, tt.to)
			require.Error(t, err, "a build with no no-replace rename must refuse, never succeed")

			assert.ErrorIs(t, err, errNoNoReplaceRename, "the refusal is the build's own error, not a wrapped one")
			assert.ErrorContains(t, err, "no-replace rename", "the refusal names the call that is missing")
			assert.ErrorContains(t, err, "this build", "the refusal is a sentence about this build")

			assert.NotErrorIs(t, err, os.ErrInvalid, "no call was made, so the refusal never borrows an errno")
			assert.NotErrorIs(t, err, os.ErrNotExist, "the refusal never claims the source was read")
		})
	}
}

// TestNoreplaceOtherCoverRefusalIsStablePins that the refusal is the same
// error on every call, so a caller comparing identifications across a publish
// sees one refusal, not fresh copies.
func TestNoreplaceOtherCoverRefusalIsStable(t *testing.T) {
	t.Parallel()

	first := noReplaceRename("draft.json", "note.json")
	require.Error(t, first)
	second := noReplaceRename("draft.json", "note.json")
	require.Error(t, second)

	assert.ErrorIs(t, second, first, "every call answers the same build refusal")
}

// TestNoreplaceOtherCoverRefusalNeverSucceedsPins the create-exclusive
// contract: the function is a publish that must never report success, so a
// nil answer would silently replace a published note.
func TestNoreplaceOtherCoverRefusalNeverSucceeds(t *testing.T) {
	t.Parallel()

	err := noReplaceRename("from", "to")
	require.Error(t, err)

	var sentinel error = errNoNoReplaceRename
	assert.True(t, errors.Is(err, sentinel), "the refusal identifies as the build's own sentinel")
}

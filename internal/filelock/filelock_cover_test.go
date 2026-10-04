//go:build unix || windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilelockCoverSafePathErrorUnwrapReachesThePathError pins the lock-file error
// wrapper: a refusal from a missing or unreadable lockfile unwraps to the path error
// beneath it, so callers match os.ErrNotExist with errors.Is and errors.As, and the
// wrapper's own line adds escaping, not prose.
func TestFilelockCoverSafePathErrorUnwrapReachesThePathError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := ReadStamp(filepath.Join(dir, "absent.lock"))
	require.Error(t, err, "the missing lockfile was read without a refusal: %v", err)
	assert.ErrorIs(t, err, os.ErrNotExist, "errors.Is does not reach the missing-file cause through the wrapper")
	var pe *os.PathError
	require.ErrorAs(t, err, &pe, "errors.As does not cross the wrapper: %v", err)

	cases := []struct {
		name  string
		inner *os.PathError
		want  string
	}{
		{name: "a missing file is named", inner: &os.PathError{Op: "open", Path: "held.lock", Err: os.ErrNotExist}, want: "open held.lock: file does not exist"},
		{name: "a permission refusal is named", inner: &os.PathError{Op: "open", Path: "held.lock", Err: os.ErrPermission}, want: "open held.lock: permission denied"},
		{name: "a path that breaks a line is escaped, and still named", inner: &os.PathError{Op: "open", Path: "bad\nname.lock", Err: os.ErrNotExist}, want: "open bad\\x0aname.lock: file does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := wrapPathError(tc.inner)
			require.NotNil(t, wrapped, "a path error was left unwrapped")
			var safe *safePathError
			require.ErrorAs(t, wrapped, &safe, "wrapPathError did not wrap the path error: %v", wrapped)
			assert.Equal(t, tc.inner, safe.Unwrap(), "Unwrap lost the inner path error")
			assert.Equal(t, tc.inner, errors.Unwrap(wrapped), "errors.Unwrap does not reach the inner path error")
			assert.ErrorIs(t, wrapped, tc.inner.Err, "errors.Is does not reach the inner cause")
			assert.Equal(t, tc.want, wrapped.Error(), "the wrapper's line is not the escaped path error")
			assert.NotContains(t, wrapped.Error(), "\n", "the wrapper's line is not a single line")
		})
	}
}

// TestFilelockCoverSafePathErrorUnwrapRefusesAnEmptyWrapper pins the refusal: a
// wrapper with no inner error unwraps to nil and prints no line, so an empty
// wrapper is never mistaken for a path error.
func TestFilelockCoverSafePathErrorUnwrapRefusesAnEmptyWrapper(t *testing.T) {
	t.Parallel()

	empty := &safePathError{}
	assert.Nil(t, empty.Unwrap(), "an empty wrapper unwrapped to something")
	assert.Empty(t, empty.Error(), "an empty wrapper printed a line")
	assert.Nil(t, errors.Unwrap(error(empty)), "errors.Unwrap on an empty wrapper returned something")
	assert.NoError(t, wrapPathError(nil), "a nil error was wrapped into one")
}

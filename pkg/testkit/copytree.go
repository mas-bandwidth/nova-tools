package testkit

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// CopyTree copies the directory tree at src into dst, creating dst, and fails
// the test if any file cannot be copied. It is os.CopyFS over src as a file
// system, so the recursive copies the command tests each hand-rolled become
// one call; execute bits survive, other permission bits do not (os.CopyFS).
// dst must not already hold a name the tree carries: os.CopyFS refuses to
// overwrite, which is what a test wants a copy to say.
func CopyTree(t testing.TB, src, dst string) {
	t.Helper()
	require.NoError(t, os.CopyFS(dst, os.DirFS(src)), "copy %s to %s", src, dst)
}

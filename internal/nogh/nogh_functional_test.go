//go:build functional

package nogh

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWrittenGhRefuses: the written gh exits 2 with Refusal and is found
// first through PathFirst, ahead of a gh later on PATH.
func TestWrittenGhRefuses(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the refusing gh is a /bin/sh script")
	}
	require.NotContains(t, Refusal, "'", "Refusal holds a single quote, which Script cannot quote")
	dir := filepath.Join(t.TempDir(), "shim")
	path, err := Install(dir)
	require.NoError(t, err, "Install = %q, %v", path, err)
	require.Equal(t, filepath.Join(dir, Name), path, "Install = %q, %v", path, err)
	_, err = Install(dir)
	require.NoError(t, err, "a second Install over the first: %v", err)
	entries, _ := os.ReadDir(dir)
	require.Len(t, entries, 1, "shim dir holds %d entries, want only gh (no temp left behind)", len(entries))
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	cmd := exec.Command(sh, "-c", "gh api user")
	cmd.Env = PathFirst([]string{"PATH=/usr/bin:/bin"}, dir)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "gh: err=%v out=%q, want exit 2 and the refusal", err, out)
	require.Equal(t, 2, ee.ExitCode(), "gh: err=%v out=%q, want exit 2 and the refusal", err, out)
	require.Equal(t, Refusal, strings.TrimSpace(string(out)), "gh: err=%v out=%q, want exit 2 and the refusal", err, out)
}

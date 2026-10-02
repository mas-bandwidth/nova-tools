package ci

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ChangeBase defaults to "dev" when GITHUB_BASE_REF is unset or empty.
func TestChangeBaseDefaultsToDev(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{
			"-C", dir,
			"-c", "user.name=Emma",
			"-c", "user.email=emma@example.com",
			"-c", "init.defaultBranch=dev",
		}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
		return strings.TrimSpace(string(out))
	}

	git("init")
	git("commit", "--allow-empty", "-m", "dev commit")
	git("branch", "-M", "dev")
	devSHA := git("rev-parse", "HEAD")

	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "feature commit")

	gotSHA, err := ChangeBase(dir, func(k string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, devSHA, gotSHA, "ChangeBase should resolve against default 'dev' base")
}

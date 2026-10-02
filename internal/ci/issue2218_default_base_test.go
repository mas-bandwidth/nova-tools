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

	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "merge-base", "HEAD", "origin/dev").Output()
	if err != nil {
		out, err = exec.Command("git", "-C", root, "merge-base", "HEAD", "dev").Output()
	}
	if err != nil {
		t.Skip("neither origin/dev nor dev branch exists in repo clone")
	}
	wantSHA := strings.TrimSpace(string(out))

	gotSHA, err := ChangeBase(root, func(k string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, wantSHA, gotSHA, "ChangeBase should resolve against default 'dev' base")
}

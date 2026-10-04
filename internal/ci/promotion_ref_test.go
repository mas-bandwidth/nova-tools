package ci

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	ciOnce    sync.Once
	ciBinPath string
	ciBinErr  error
)

func buildCi(t *testing.T) string {
	t.Helper()
	ciOnce.Do(func() {
		root := repoRoot(t)
		dir, err := os.MkdirTemp("", "ci-bin-*")
		if err != nil {
			ciBinErr = err
			return
		}
		ciBinPath = filepath.Join(dir, "ci")
		buildCmd := exec.Command("go", "build", "-o", ciBinPath, filepath.Join(root, "tools", "ci"))
		buildCmd.Env = goenv.Clean(os.Environ())
		buildOut, err := buildCmd.CombinedOutput()
		if err != nil {
			ciBinErr = fmt.Errorf("building tools/ci: %w: %s", err, string(buildOut))
			return
		}
	})
	require.NoError(t, ciBinErr)
	return ciBinPath
}

// TestFetchAncestryPromotionWhenBranchPresentOnOrigin verifies that fetch-ancestry
// with --promotion succeeds when the promotion branch exists on origin and fetches
// it into refs/remotes/origin/<branch>.
func TestFetchAncestryPromotionWhenBranchPresentOnOrigin(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	originDir := filepath.Join(tmp, "origin.git")
	localDir := filepath.Join(tmp, "local")

	_, err := gitOut(tmp, "init", "--bare", "-q", originDir)
	require.NoError(t, err)

	_, err = gitOut(tmp, "init", "-q", "-b", "main", localDir)
	require.NoError(t, err)

	local := &scratchRepo{t: t, root: localDir}
	local.git("config", "user.name", "ci")
	local.git("config", "user.email", "ci@example.invalid")
	local.git("config", "commit.gpgsign", "false")
	local.git("config", "core.hooksPath", "/dev/null")
	local.git("config", "protocol.file.allow", "always")
	local.git("remote", "add", "origin", originDir)

	local.write("README.md", "init\n")
	base := local.commit("initial commit")
	local.git("push", "-q", "origin", "main")

	// Create and push sprint/foundation branch to origin
	local.git("checkout", "-q", "-b", "sprint/foundation", base)
	local.write("foundation.txt", "foundation\n")
	foundTip := local.commit("foundation commit")
	local.git("push", "-q", "origin", "sprint/foundation")

	// Assert origin holds it
	lsOut := local.git("ls-remote", "origin", "refs/heads/sprint/foundation")
	require.Contains(t, lsOut, "refs/heads/sprint/foundation")
	require.Contains(t, lsOut, foundTip)

	// Switch back to main and create a 2-parent merge so HEAD has promotion ancestry to read
	local.git("checkout", "-q", "main")
	local.git("merge", "--no-ff", "-q", "-m", "merge foundation", "sprint/foundation")

	// Run the shipped fetch-ancestry command
	ciBin := buildCi(t)
	cmd := exec.Command(ciBin, "fetch-ancestry", "--promotion", "sprint/foundation")
	cmd.Dir = localDir
	cmd.Env = goenv.Clean(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "fetch-ancestry failed: stdout: %s, stderr: %s", stdout.String(), stderr.String())

	// Verify remote ref was established and matches foundTip
	tip, err := gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	require.NoError(t, err)
	assert.Equal(t, foundTip, strings.TrimSpace(tip))
}

// TestFetchAncestryPromotionWhenBranchAbsentOnOrigin verifies that fetch-ancestry
// with --promotion succeeds when the promotion branch is absent on origin (the normal
// state when no promotion is in flight) and reports that there is no promotion to read.
func TestFetchAncestryPromotionWhenBranchAbsentOnOrigin(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	originDir := filepath.Join(tmp, "origin.git")
	localDir := filepath.Join(tmp, "local")

	_, err := gitOut(tmp, "init", "--bare", "-q", originDir)
	require.NoError(t, err)

	_, err = gitOut(tmp, "init", "-q", "-b", "main", localDir)
	require.NoError(t, err)

	local := &scratchRepo{t: t, root: localDir}
	local.git("config", "user.name", "ci")
	local.git("config", "user.email", "ci@example.invalid")
	local.git("config", "commit.gpgsign", "false")
	local.git("config", "core.hooksPath", "/dev/null")
	local.git("config", "protocol.file.allow", "always")
	local.git("remote", "add", "origin", originDir)

	local.write("README.md", "init\n")
	base := local.commit("initial commit")
	local.git("push", "-q", "origin", "main")

	// Create a merge commit so HEAD has promotion ancestry to read
	local.git("checkout", "-q", "-b", "feature", base)
	local.write("feat.txt", "feat\n")
	local.commit("feature commit")
	local.git("checkout", "-q", "main")
	local.git("merge", "--no-ff", "-q", "-m", "merge feature", "feature")

	ciBin := buildCi(t)
	cmd := exec.Command(ciBin, "fetch-ancestry", "--promotion", "sprint/foundation")
	cmd.Dir = localDir
	cmd.Env = goenv.Clean(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "fetch-ancestry should succeed when branch absent: stderr: %s", stderr.String())
	assert.Contains(t, stdout.String(), "origin has no branch sprint/foundation: no promotion to read")

	// Verify no remote tracking ref was created
	_, err = gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	assert.Error(t, err)
}

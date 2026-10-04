package ci

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildCi(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	ciBin := filepath.Join(t.TempDir(), "ci")
	buildCmd := exec.Command("go", "build", "-o", ciBin, filepath.Join(root, "tools", "ci"))
	buildCmd.Env = goenv.Clean(os.Environ())
	buildOut, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "building tools/ci: %s", string(buildOut))
	return ciBin
}

// TestAbsentPromotionBranchWithStaleLocalRefRefused verifies that when a promotion
// branch is absent on origin, an existing stale local ref is refused rather than
// excused (issue 5161).
func TestAbsentPromotionBranchWithStaleLocalRefRefused(t *testing.T) {
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

	// Create a stale local ref pointing to base commit while absent on origin
	local.git("update-ref", "refs/remotes/origin/sprint/foundation", base)

	// Verify the local ref exists locally
	tip, err := gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	require.NoError(t, err)
	require.Equal(t, base, strings.TrimSpace(tip))

	// Calling the shipped fetch-ancestry command must refuse because sprint/foundation is not on origin
	ciBin := buildCi(t)
	cmd := exec.Command(ciBin, "fetch-ancestry", "--promotion", "sprint/foundation")
	cmd.Dir = localDir
	cmd.Env = goenv.Clean(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.Error(t, err)
	wantErr := "promotion branch sprint/foundation not found on origin; run: git fetch origin sprint/foundation"
	assert.Contains(t, stderr.String(), wantErr)

	// Stale local ref must be deleted by the refusal
	_, err = gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	assert.Error(t, err)

	// Full ref name format must also be refused with the same message
	cmd2 := exec.Command(ciBin, "fetch-ancestry", "--promotion", "refs/remotes/origin/sprint/foundation")
	cmd2.Dir = localDir
	cmd2.Env = goenv.Clean(os.Environ())
	var stderr2 bytes.Buffer
	cmd2.Stderr = &stderr2
	err = cmd2.Run()
	require.Error(t, err)
	assert.Contains(t, stderr2.String(), wantErr)
}

// TestPromotionRefSuccessWhenPresentOnOrigin verifies that the shipped resolver succeeds
// when the promotion branch exists on origin and has been fetched into the checkout.
func TestPromotionRefSuccessWhenPresentOnOrigin(t *testing.T) {
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "fetch-ancestry failed: %s", stderr.String())

	// Verify remote ref was established and matches foundTip
	tip, err := gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	require.NoError(t, err)
	assert.Equal(t, foundTip, strings.TrimSpace(tip))
}

// TestPromotionRefAbsentBranchNoLocalRefRefused verifies that an absent promotion
// branch with no local ref at all is also refused.
func TestPromotionRefAbsentBranchNoLocalRefRefused(t *testing.T) {
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
	cmd := exec.Command(ciBin, "fetch-ancestry", "--promotion", "dev")
	cmd.Dir = localDir
	cmd.Env = goenv.Clean(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.Error(t, err)
	assert.Contains(t, stderr.String(), "promotion branch dev not found on origin; run: git fetch origin dev")
}

// TestFetchAncestryCommandRefusesAbsentPromotionBranch verifies that the built
// tools/ci fetch-ancestry command itself refuses an absent promotion branch
// and cleans up any retained local tracking ref (issue 5161).
func TestFetchAncestryCommandRefusesAbsentPromotionBranch(t *testing.T) {
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

	// Create a stale local ref pointing to base commit while absent on origin
	local.git("update-ref", "refs/remotes/origin/sprint/foundation", base)

	ciBin := buildCi(t)
	cmd := exec.Command(ciBin, "fetch-ancestry", "--promotion", "sprint/foundation")
	cmd.Dir = localDir
	cmd.Env = goenv.Clean(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.Error(t, err)
	assert.Contains(t, stderr.String(), "promotion branch sprint/foundation not found on origin; run: git fetch origin sprint/foundation")

	// The stale local ref must be deleted
	_, err = gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	assert.Error(t, err)
}

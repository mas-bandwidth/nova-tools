package ci

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promotionRef resolves the promotion branch's ref in root, ensuring that
// the remote branch exists on origin rather than falling back to a stale local ref.
// When the remote branch is absent on origin, it refuses:
// "promotion branch <name> not found on origin; run: git fetch origin <name>".
// Issue 5161 (control 4): an absent promotion branch must be refused, not excused.
func promotionRef(root, branch string) (string, error) {
	name := strings.TrimPrefix(branch, "refs/remotes/origin/")
	name = strings.TrimPrefix(name, "refs/heads/")
	if name == "" {
		return "", fmt.Errorf("empty promotion branch name")
	}

	out, err := gitOut(root, "ls-remote", "--heads", "origin", "refs/heads/"+name)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("promotion branch %s not found on origin; run: git fetch origin %s", name, name)
	}

	wantRef := "refs/heads/" + name
	found := false
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == wantRef {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("promotion branch %s not found on origin; run: git fetch origin %s", name, name)
	}

	localRef := "refs/remotes/origin/" + name
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", localRef+"^{commit}"); err != nil {
		return "", fmt.Errorf("promotion branch %s not found in local checkout; run: git fetch origin %s", name, name)
	}
	return localRef, nil
}

// promotionTip returns the tip commit of the promotion branch in root.
func promotionTip(root, branch string) (string, error) {
	ref, err := promotionRef(root, branch)
	if err != nil {
		return "", err
	}
	tip, err := gitOut(root, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(tip), nil
}

// resolvePromotionBranch is an alias for promotionRef.
func resolvePromotionBranch(root, branch string) (string, error) {
	return promotionRef(root, branch)
}

func (r *scratchRepo) promotionRef(branch string) (string, error) {
	return promotionRef(r.root, branch)
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

	// Create a stale local ref pointing to base commit while absent on origin
	local.git("update-ref", "refs/remotes/origin/sprint/foundation", base)

	// Verify the local ref exists locally
	tip, err := gitOut(localDir, "rev-parse", "--verify", "-q", "refs/remotes/origin/sprint/foundation^{commit}")
	require.NoError(t, err)
	require.Equal(t, base, strings.TrimSpace(tip))

	// Calling promotionRef must refuse because sprint/foundation is not on origin
	_, err = promotionRef(localDir, "sprint/foundation")
	require.Error(t, err)
	wantErr := "promotion branch sprint/foundation not found on origin; run: git fetch origin sprint/foundation"
	assert.Equal(t, wantErr, err.Error())

	// Full ref name format must also be refused with the same message
	_, err = promotionRef(localDir, "refs/remotes/origin/sprint/foundation")
	require.Error(t, err)
	assert.Equal(t, wantErr, err.Error())

	// Scratch repo method and resolvePromotionBranch alias must also refuse
	_, err = local.promotionRef("sprint/foundation")
	require.Error(t, err)
	assert.Equal(t, wantErr, err.Error())

	_, err = resolvePromotionBranch(localDir, "sprint/foundation")
	require.Error(t, err)
	assert.Equal(t, wantErr, err.Error())
}

// TestPromotionRefSuccessWhenPresentOnOrigin verifies that promotionRef succeeds
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

	local.git("checkout", "-q", "main")
	local.git("fetch", "-q", "origin", "sprint/foundation")

	ref, err := promotionRef(localDir, "sprint/foundation")
	require.NoError(t, err)
	assert.Equal(t, "refs/remotes/origin/sprint/foundation", ref)

	tip, err := promotionTip(localDir, "sprint/foundation")
	require.NoError(t, err)
	assert.Equal(t, foundTip, tip)
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
	local.commit("initial commit")
	local.git("push", "-q", "origin", "main")

	_, err = promotionRef(localDir, "dev")
	require.Error(t, err)
	assert.Equal(t, "promotion branch dev not found on origin; run: git fetch origin dev", err.Error())
}

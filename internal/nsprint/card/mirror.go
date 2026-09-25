package card

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mirrorRoot is where this host keeps its bare repository mirrors:
// $NOVA_MIRROR_ROOT when set, else ~/nova-bench/mirror (the harness default).
func mirrorRoot() string {
	if root := strings.TrimSpace(os.Getenv("NOVA_MIRROR_ROOT")); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "nova-bench", "mirror")
}

// mirrorPath is the local mirror for owner/name: <root>/<name>.git.
func mirrorPath(fullName string) string {
	root := mirrorRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, filepath.Base(fullName)+".git")
}

// MirrorPath is this host's bare mirror of owner/name (or name):
// <root>/<name>.git under $NOVA_MIRROR_ROOT or ~/nova-bench/mirror; "" when
// there is no root. The reconciler's done-already leg (#3919) reads it.
func MirrorPath(fullName string) string { return mirrorPath(fullName) }

// hasMirror reports whether this host holds a bare mirror of owner/name. A
// directory with objects/ counts: the repository exists even when an
// anonymous request cannot see it (a private repo answers 404).
func hasMirror(fullName string) bool {
	dir := mirrorPath(fullName)
	if dir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "objects"))
	return err == nil && fi.IsDir()
}

// MirrorBranchSHA is the tip of branch in this host's mirror of owner/name,
// read with git (no forge call): the base-sha a card cut on this host names.
func MirrorBranchSHA(fullName, branch string) (string, error) {
	dir := mirrorPath(fullName)
	if dir == "" || !hasMirror(fullName) {
		return "", &privateRepoError{Name: fullName}
	}
	out, err := exec.Command("git", "--git-dir", dir, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("mirror %s has no branch %s: %v", dir, branch, err)
	}
	sha := strings.TrimSpace(string(out))
	if !shaRE.MatchString(sha) {
		return "", fmt.Errorf("mirror %s branch %s is %q, not a sha", dir, branch, sha)
	}
	return sha, nil
}

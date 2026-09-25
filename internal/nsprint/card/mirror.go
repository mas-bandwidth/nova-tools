package card

import (
	"os"
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

// MirrorDir is this host's bare mirror of owner/name (or name):
// <root>/<name>.git, "" when there is no root. The card path reads the
// mirror, never the forge (nova-tools#3967).
func MirrorDir(fullName string) string { return mirrorPath(fullName) }

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

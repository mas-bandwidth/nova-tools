// Package layout derives all path defaults from machine and friend rows.
// Every path the tools use that derives from a root comes from here.
package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Defaults for path roots.
const (
	// DefaultNovaRoot is the default nova_root setting when unset.
	DefaultNovaRoot = "~/nova"
)

// Root holds the machine's path roots.
type Root struct {
	// AI is the AI root under the nova root. Default is AI/<name>/working for friends.
	AI string
	// Bench is the bench root under the nova root. Default is Bench/secrets, Bench/loops, Bench/mirror.
	Bench string
	// Secrets is the secrets store. Default is Bench/secrets.
	Secrets string
	// LoopLogs is the loop logs directory. Default is Bench/loops.
	LoopLogs string
	// Mirrors is the mirrors directory. Default is Bench/mirror.
	Mirrors string
}

// Bud returns the bud's working path.
// name is the bud's name.
func (r Root) Bud(name string) string {
	return filepath.Join(r.AI, "buds", name, "working")
}

// BudRead returns the bench scratch directory of a bud's read of one card:
// <ai>/buds/<name>/reads/<card>. A read runs its go commands on a Linux bench
// there, so the reader's own machine runs none.
func (r Root) BudRead(name, card string) string {
	return filepath.Join(r.AI, "buds", name, "reads", card)
}

// Shared returns the shared directory.
func (r Root) Shared() string {
	return filepath.Join(r.AI, "shared")
}

// Friend holds a friend's working path.
type Friend struct {
	// Working is the friend's working directory. Default is AI/<name>/working.
	Working string
}

// ExpandHome expands ~ to the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// ValidNovaRoot checks that the nova root is an existing directory and not a symlink.
func ValidNovaRoot(root string) error {
	if root == "" {
		return nil
	}
	root = ExpandHome(root)
	fi, err := os.Lstat(root)
	switch {
	case err != nil:
		return fmt.Errorf("nova_root %q is not an existing directory", root)
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("nova_root %q is a symlink; it must be a real directory", root)
	case !fi.IsDir():
		return fmt.Errorf("nova_root %q is not a directory", root)
	}
	return nil
}

// ResolveRoot returns the root paths for a machine.
// novaRoot is the machine's nova_root setting (empty uses default).
func ResolveRoot(novaRoot string) Root {
	if novaRoot == "" {
		novaRoot = DefaultNovaRoot
	}
	novaRoot = ExpandHome(novaRoot)

	aiRoot := filepath.Join(novaRoot, "ai")
	benchRoot := filepath.Join(novaRoot, "bench")
	secrets := filepath.Join(benchRoot, "secrets")
	loopLogs := filepath.Join(benchRoot, "loops")
	mirrors := filepath.Join(benchRoot, "mirror")

	return Root{
		AI:      aiRoot,
		Bench:   benchRoot,
		Secrets: secrets,
		LoopLogs: loopLogs,
		Mirrors: mirrors,
	}
}

// ResolveFriend returns the friend's working path.
// novaRoot is the machine's nova_root setting (empty uses default).
// name is the friend's name.
// dir is the friend's dir setting (empty uses default).
func ResolveFriend(novaRoot string, name, dir string) Friend {
	if dir == "" {
		root := ResolveRoot(novaRoot)
		dir = filepath.Join(root.AI, name, "working")
	}
	return Friend{Working: dir}
}

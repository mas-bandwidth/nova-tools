// Package layout derives all path defaults from machine and friend rows.
// Every path the tools use that derives from a root comes from here.
package layout

import (
	"os"
	"path/filepath"
	"strings"
)

// Defaults for path roots.
const (
	// DefaultAIRoot is the default ai_root setting when unset.
	DefaultAIRoot = "~/ai"
)

// Root holds the machine's path roots.
type Root struct {
	// AI is the AI root (ai_root). Default is DefaultAIRoot (~/ai).
	AI string
	// Bench is the bench root. Default is AI/bench.
	Bench string
	// Secrets is the secrets store. Default is Bench/secrets.
	Secrets string
	// LoopLogs is the loop logs directory. Default is Bench/loops.
	LoopLogs string
	// Mirrors is the mirrors directory. Default is Bench/mirror.
	Mirrors string
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

// ResolveRoot returns the root paths for a machine.
// aiRoot is the machine's ai_root setting (empty uses default).
// Validates that aiRoot, if set, is an absolute path.
func ResolveRoot(aiRoot string) Root {
	if aiRoot == "" {
		aiRoot = DefaultAIRoot
	}
	aiRoot = ExpandHome(aiRoot)

	benchRoot := filepath.Join(aiRoot, "bench")
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
// aiRoot is the machine's ai_root setting.
// name is the friend's name.
// dir is the friend's dir setting (empty uses default).
func ResolveFriend(aiRoot string, name, dir string) Friend {
	if dir == "" {
		aiRoot = ExpandHome(aiRoot)
		dir = filepath.Join(aiRoot, name, "working")
	}
	return Friend{Working: dir}
}

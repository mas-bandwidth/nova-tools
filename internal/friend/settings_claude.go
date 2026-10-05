package friend

import (
	"errors"
	"path/filepath"
)

// ErrNoConfigDir is the refusal of claude settings with no config directory:
// a friend on Claude Code has her own CLAUDE_CONFIG_DIR, and install writes
// it into the agent (run --config-dir) rather than a unit carrying it by hand.
var ErrNoConfigDir = errors.New("harness claude wants --config-dir (or CLAUDE_CONFIG_DIR): the friend's own Claude config directory, written into the agent")

// claudeSettings: the config directory is absolute and a real directory,
// made private when missing.
func claudeSettings(h HarnessSettings) ([]setting, error) {
	if h.ConfigDir == "" {
		return nil, ErrNoConfigDir
	}
	if !filepath.IsAbs(h.ConfigDir) {
		return nil, errors.New("--config-dir " + h.ConfigDir + " is not absolute")
	}
	return []setting{h.realDir("CLAUDE_CONFIG_DIR", h.ConfigDir, true)}, nil
}

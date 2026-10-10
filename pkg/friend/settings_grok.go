package friend

import (
	"fmt"
	"path/filepath"
)

// GrokWakePath is the wake file install names for a grok friend when
// --session names none: <state>/<friend>.wake, under the daemon's state
// directory, so no unit carries a path typed by hand.
func GrokWakePath(home, stateDir, friend string) string {
	if stateDir == "" {
		stateDir = DefaultStateDir(home, friend)
	}
	return filepath.Join(stateDir, friend+".wake")
}

// WakePath is the wake file these settings name for grok.
func (h HarnessSettings) WakePath() string {
	if h.Wake != "" {
		return h.Wake
	}
	return GrokWakePath(h.Home, h.StateDir, h.Friend)
}

// grokSettings: the wake file is absolute with no whitespace (the monitor's
// tail must name it whole), its directory a real directory (made when
// missing), and the file itself a file (made empty when missing).
func grokSettings(h HarnessSettings) ([]setting, error) {
	wake := h.WakePath()
	if !filepath.IsAbs(wake) {
		return nil, fmt.Errorf("the wake file %q is not absolute: the monitor's tail must name it whole", wake)
	}
	if err := noWhitespace("the wake file", wake); err != nil {
		return nil, err
	}
	return []setting{h.realDir("wake-directory", filepath.Dir(wake), true), {
		Setting: Setting{Harness: h.Harness, File: wake, Name: "wake-file", Want: KindFile}, guard: true,
		read:  func() (string, error) { return kindOf(h.fs(), wake) },
		write: func() error { return h.fs().WriteFile(wake, nil, 0o600) },
	}}, nil
}

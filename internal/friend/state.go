package friend

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultStateDir returns the default directory for daemon state.
func DefaultStateDir(home, friend string) (string, string) {
	if home == "" {
		return "", "no home"
	}
	return filepath.Join(home, ".nova-"+friend), ""
}

// StateDirIn returns the state directory inside dir.
func StateDirIn(dir string) string {
	return filepath.Join(dir, ".nova-friend")
}

// DaemonStateDir returns where a daemon with no --state-dir keeps its files.
func DaemonStateDir(dir string, mkdir func(string) error) (string, string) {
	if dir != "" {
		if mkdir != nil {
			if err := mkdir(StateDirIn(dir)); err != nil {
				return DefaultStateDir("", "friend")
			}
		}
		return StateDirIn(dir), ""
	}
	return DefaultStateDir(os.Getenv("HOME"), "friend")
}

// FindStateDir returns where a reader looks for state.
func FindStateDir(dir string) (string, string) {
	if dir != "" {
		sd := StateDirIn(dir)
		if _, err := os.Stat(filepath.Join(sd, "status.json")); err == nil {
			return sd, ""
		}
	}
	return DefaultStateDir(os.Getenv("HOME"), "friend")
}

// WatchPath is the watch cursor file path.
func WatchPath(stateDir string) string {
	return filepath.Join(stateDir, "watch.json")
}

// Watch is the coordinator watch's resume point.
type Watch struct {
	After      string `json:"after"`
	WakeOffset int64  `json:"wake_offset"`
}

// WriteWatch writes the watch atomically.
func WriteWatch(path string, w Watch) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

// ReadWatch reads the watch.
func ReadWatch(path string) (Watch, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Watch{}, false, nil
	}
	var w Watch
	if err := json.Unmarshal(data, &w); err != nil {
		return Watch{}, true, err
	}
	return w, true, nil
}

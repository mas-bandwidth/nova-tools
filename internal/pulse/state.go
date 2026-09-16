package pulse

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// #828, rule A: the state file replaces the shell counters the bash loop kept in memory.
// A restart picks up next_card and tick from disk, so neither the next candidate nor the
// cycle count is lost when the coordinator dies.

// State is the coordinator's durable position: which candidate is next and which tick it
// is on. It round-trips through the state file so a restart resumes in place.
type State struct {
	NextCard int
	Tick     int
}

// LoadState reads the state file at path. A missing file is the zero state (first run).
func LoadState(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	s := &State{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value, got %q", path, i+1, line)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: key %q: %q is not an integer", path, i+1, key, val)
		}
		switch key {
		case "next_card":
			s.NextCard = n
		case "tick":
			s.Tick = n
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %q", path, i+1, key)
		}
	}
	return s, nil
}

// Save writes the state to path atomically enough to survive a restart: it is a full file
// replace, so a killed writer leaves the previous state readable.
func (s *State) Save(path string) error {
	body := fmt.Sprintf("next_card = %d\ntick = %d\n", s.NextCard, s.Tick)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	return nil
}

package swarm

import (
	"os"
	"path/filepath"
	"sort"
)

// PullQueue takes up to admit cards from a bench's queue/ by renaming each into taken/ as
// <worker>-<name>.card, the ownership record (docs/SPEC-JOBS.md sections 2 and 7). The
// rename is atomic within the directory, so two workers cannot take one card. A full bench
// (admit 0) reads nothing and leaves every card in queue/.
//
// The names are sorted before the take, so a test and a reader see the same order; the take
// stops at admit, so the bench never crosses its capacity line.
func PullQueue(queueDir, takenDir, worker string, admit int) ([]string, error) {
	if admit <= 0 {
		return nil, nil
	}
	_, _ = ReconcileQueueLimbo(queueDir, MaxProviderAttempts)
	matches, err := filepath.Glob(filepath.Join(queueDir, "*.card"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if err := os.MkdirAll(takenDir, 0o755); err != nil {
		return nil, err
	}
	var taken []string
	for _, path := range matches {
		if len(taken) >= admit {
			break
		}
		name := filepath.Base(path)
		dest := filepath.Join(takenDir, worker+"-"+name)
		if err := os.Rename(path, dest); err != nil {
			continue
		}
		taken = append(taken, name)
	}
	return taken, nil
}

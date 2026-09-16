package pulse

// Card numbers, and the one lock the queue directory has.
//
// Pit stop 3, bug 1 (issue #828): five replay cards numbered by hand collided with five
// launched cards of the same numbers, their texts in queue/launched/ were overwritten and
// two job directories per number confused the harvest. The rule that retires the class:
// a card number comes only from the state file's next_card, taken under a lock, and
// `nova-pulse cut` is the only cutter.
//
// The state file is <queue>/state.tsv, one `key<TAB>value` line per fact, read and written
// whole under the lock: a key this verb does not own is carried through untouched, so the
// loop's own counters and the config's own keys can live in the same file.
//
// The lock is a directory, because mkdir is the one create-or-fail every filesystem this
// estate runs on agrees about: <queue>/.card.lock is made to take the lock and removed to
// release it. A lock older than staleLock is broken with one line in the state file's own
// directory, so a killed cutter never stops the queue for good.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	stateFileName  = "state.tsv"
	nextCardKey    = "next_card"
	legacyNextFile = "NEXT" // the manager tier's own counter, mirrored until that tier retires
	lockDirName    = ".card.lock"
	lockWait       = 10 * time.Second
	staleLock      = 2 * time.Minute
)

// NextCardNumber takes the next card number under the queue's lock and writes the state
// file back one higher. Two cutters never see the same number, whatever else is running.
func NextCardNumber(queue string) (int, error) {
	if strings.TrimSpace(queue) == "" {
		return 0, fmt.Errorf("the queue directory is required; refusing to guess (pass --queue <dir>)")
	}
	unlock, err := lockQueue(queue)
	if err != nil {
		return 0, err
	}
	defer unlock()

	state, err := readState(queue)
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(state[nextCardKey]))
	// The manager tier still reads <queue>/NEXT; until that card lands, the higher of the
	// two is the truth, so neither reader ever hands out a number the other has used.
	if legacy := readLegacyNext(queue); legacy > n {
		n = legacy
	}
	if n < 1 {
		n = 1
	}
	state[nextCardKey] = strconv.Itoa(n + 1)
	if err := writeState(queue, state); err != nil {
		return 0, err
	}
	writeLegacyNext(queue, n+1)
	return n, nil
}

// lockQueue takes the queue's mkdir lock and returns the release.
func lockQueue(queue string) (func(), error) {
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return nil, fmt.Errorf("the queue directory %s cannot be made: %w", queue, err)
	}
	path := filepath.Join(queue, lockDirName)
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("the card lock %s cannot be taken: %w", path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(path) // the holder is gone; a dead lock never stops the queue
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the card lock %s was held for %s (remove it if no cutter is running)", path, lockWait)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// readState reads <queue>/state.tsv into its key/value pairs; a missing file is empty state.
func readState(queue string) (map[string]string, error) {
	state := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(queue, stateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("the state file cannot be read: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		state[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return state, nil
}

// writeState writes the state file whole, in key order, by rename: a reader sees one state
// or the other and never half a file.
func writeState(queue string, state map[string]string) error {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\n", k, state[k])
	}
	tmp := filepath.Join(queue, stateFileName+".tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("the state file cannot be written: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(queue, stateFileName)); err != nil {
		return fmt.Errorf("the state file cannot be replaced: %w", err)
	}
	return nil
}

func readLegacyNext(queue string) int {
	raw, err := os.ReadFile(filepath.Join(queue, legacyNextFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return n
}

func writeLegacyNext(queue string, n int) {
	_ = os.WriteFile(filepath.Join(queue, legacyNextFile), []byte(strconv.Itoa(n)+"\n"), 0o644)
}

package friend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// NewestCodexSession resolves the saved thread before probing its writer lock
// (SPEC-FRIEND.md, Codex). Only the index and first session_meta line are read.
func NewestCodexSession(home, dir string) (string, error) {
	wanted, err := filepath.Abs(dir)
	if err == nil {
		wanted, err = filepath.EvalSymlinks(wanted)
	}
	if err != nil {
		return "", fmt.Errorf("codex working directory: %w", err)
	}
	updated := map[string]time.Time{}
	index, err := os.Open(filepath.Join(home, "session_index.jsonl"))
	if err == nil {
		scan := bufio.NewScanner(index)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for scan.Scan() {
			var row struct {
				ID      string    `json:"id"`
				Updated time.Time `json:"updated_at"`
			}
			if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
				index.Close() // ignored: a read-only index, nothing was written through it
				return "", fmt.Errorf("codex session index: %w", err)
			}
			if row.Updated.After(updated[row.ID]) {
				updated[row.ID] = row.Updated
			}
		}
		err = scan.Err()
		closeErr := index.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	var best string
	var stamp time.Time
	err = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1<<20)
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				ID        string    `json:"id"`
				Cwd       string    `json:"cwd"`
				Timestamp time.Time `json:"timestamp"`
			} `json:"payload"`
		}
		if scan.Scan() {
			err = json.Unmarshal(scan.Bytes(), &row)
		} else {
			err = scan.Err()
		}
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("codex session header %s: %w", path, err)
		}
		if closeErr != nil {
			return closeErr
		}
		if row.Type != "session_meta" || row.Payload.ID == "" {
			return nil
		}
		cwd, err := filepath.Abs(row.Payload.Cwd)
		if err == nil {
			cwd, err = filepath.EvalSymlinks(cwd)
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("codex saved working directory: %w", err)
		}
		if cwd != wanted {
			return nil
		}
		at := row.Payload.Timestamp
		if u := updated[row.Payload.ID]; !u.IsZero() {
			at = u
		}
		if at.After(stamp) || (at.Equal(stamp) && row.Payload.ID > best) {
			best, stamp = row.Payload.ID, at
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("codex saved sessions: %w", err)
	}
	if best == "" {
		return "", fmt.Errorf("no saved codex session for %s; start a thread there or name one with --session", dir)
	}
	return best, nil
}

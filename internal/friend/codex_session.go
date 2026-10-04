package friend

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CodexReceiptLineLimit bounds one rollout record while allowing the largest
// delivery text after JSON escaping. Oversized tool records are discarded
// through their newline and cannot wedge receipt polling.
const CodexReceiptLineLimit = 2 << 20

// CodexReceipt searches complete rollout records at or after from for the
// exact single-part user input. It validates the rollout's session_meta on
// every read and returns the next complete-line boundary; a partial final line
// remains before that boundary for the next poll.
func CodexReceipt(path, session, text string, from int64) (bool, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, from, err
	}
	defer f.Close()
	if err := codexSessionMeta(f, session); err != nil {
		return false, from, err
	}
	st, err := f.Stat()
	if err != nil {
		return false, from, err
	}
	if from < 0 || from > st.Size() {
		return false, from, fmt.Errorf("codex receipt boundary %d is outside rollout size %d", from, st.Size())
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return false, from, err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, st.Size()-from), 64<<10)
	next := from
	found := false
	lineLimit := max(CodexReceiptLineLimit, 6*len(text)+(64<<10))
	for {
		lineStart := next
		line, consumed, complete, readErr := boundedCodexLine(r, lineLimit)
		if complete {
			next = lineStart + consumed
			if codexUserReceipt(line, text) {
				found = true
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return found, next, nil
			}
			return false, next, readErr
		}
	}
}

func codexSessionMeta(f *os.File, session string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	line, _, complete, err := boundedCodexLine(bufio.NewReaderSize(f, 64<<10), CodexReceiptLineLimit)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if !complete || json.Unmarshal(line, &row) != nil || row.Type != "session_meta" || row.Payload.ID != session {
		return fmt.Errorf("codex rollout session_meta does not name thread %s", session)
	}
	return nil
}

func boundedCodexLine(r *bufio.Reader, limit int) ([]byte, int64, bool, error) {
	var line []byte
	var consumed int64
	over := false
	for {
		part, err := r.ReadSlice('\n')
		consumed += int64(len(part))
		if !over && len(line)+len(part) <= limit {
			line = append(line, part...)
		} else {
			over = true
		}
		if err == nil {
			if over {
				return nil, consumed, true, nil
			}
			return line, consumed, true, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, consumed, false, err
	}
}

func codexUserReceipt(line []byte, text string) bool {
	var row struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	return json.Unmarshal(line, &row) == nil && row.Type == "response_item" && row.Payload.Type == "message" &&
		row.Payload.Role == "user" && len(row.Payload.Content) == 1 && row.Payload.Content[0].Type == "input_text" && row.Payload.Content[0].Text == text
}

// NewestCodexSession resolves the saved thread before probing its writer lock
// (SPEC-FRIEND.md, Codex). Only the index and first session_meta line are read.
func NewestCodexSession(home, dir string) (string, error) {
	id, _, err := ResolveCodexSession(home, dir, "")
	return id, err
}

// ResolveCodexSession answers the exact saved thread and its rollout file.
// With id empty it selects the newest thread for dir; with id set it verifies
// that session_meta names that id. Receipt confirmation reads only this path.
func ResolveCodexSession(home, dir, id string) (string, string, error) {
	var wanted string
	var err error
	if id == "" {
		wanted, err = filepath.Abs(dir)
		if err == nil {
			wanted, err = filepath.EvalSymlinks(wanted)
		}
		if err != nil {
			return "", "", fmt.Errorf("codex working directory: %w", err)
		}
	}
	updated := map[string]time.Time{}
	index, err := os.Open(filepath.Join(home, "session_index.jsonl"))
	if id != "" {
		if err == nil {
			_ = index.Close() // ignored: explicit session lookup does not read the recency index
		}
	} else if err == nil {
		scan := bufio.NewScanner(index)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for scan.Scan() {
			var row struct {
				ID      string    `json:"id"`
				Updated time.Time `json:"updated_at"`
			}
			if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
				index.Close()
				return "", "", fmt.Errorf("codex session index: %w", err)
			}
			if row.Updated.After(updated[row.ID]) {
				updated[row.ID] = row.Updated
			}
		}
		err = scan.Err()
		closeErr := index.Close()
		if err != nil {
			return "", "", err
		}
		if closeErr != nil {
			return "", "", closeErr
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	var best, bestPath string
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
		if row.Type != "session_meta" || row.Payload.ID == "" || (id != "" && row.Payload.ID != id) {
			return nil
		}
		if id != "" {
			best, bestPath = row.Payload.ID, path
			return fs.SkipAll
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
			best, bestPath, stamp = row.Payload.ID, path, at
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) && !errors.Is(err, fs.SkipAll) {
		return "", "", fmt.Errorf("codex saved sessions: %w", err)
	}
	if best == "" {
		if id != "" {
			return "", "", fmt.Errorf("no saved codex session %s; start or reopen that thread", id)
		}
		return "", "", fmt.Errorf("no saved codex session for %s; start a thread there or name one with --session", dir)
	}
	return best, bestPath, nil
}

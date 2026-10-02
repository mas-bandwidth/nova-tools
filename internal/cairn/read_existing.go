package cairn

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Read verbs require an existing directory. An empty directory is a valid
// empty store; a missing directory is a wrong input, not an empty answer.
func existingStore(store string) error {
	if store == "" {
		return fmt.Errorf("no store given; refusing to guess")
	}
	info, err := os.Stat(store)
	if err != nil {
		return fmt.Errorf("cannot read store %q: %w", store, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("store %q is not a directory", store)
	}
	return nil
}

// Preserve the nested-record precedence without treating permission errors or
// a directory at the record path as evidence that the session is absent.
func recordForRead(store, session string) (string, bool, error) {
	if !validID(session) {
		return "", false, fmt.Errorf("bad session id %q: nonempty, no slashes, no whitespace", session)
	}
	for i, path := range []string{sessionFile(store, session), benchFile(store, session)} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("cannot read session %q: %w", session, err)
		}
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("session %q is not a regular file", session)
		}
		return path, i == 1, nil
	}
	return "", false, &NotFoundError{Msg: fmt.Sprintf("no such session %q under store %q", session, store)}
}

// Flat records store only a dated heading and prose. Source and publication
// policy are not recoverable from this format; never infer them from a later
// caller, unrelated log or prose. Body sizing matches benchSection's trimming.
func benchReceipts(path, session string) ([]ReceiptInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return benchReceiptsFrom(raw, session)
}

func benchReceiptsFrom(raw []byte, session string) ([]ReceiptInfo, error) {
	lines := strings.Split(string(raw), "\n")
	var rows []ReceiptInfo
	seen := map[string]bool{}
	for i := 0; i < len(lines); {
		m := benchHeadingRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, m[1])
		if err != nil || !validID(m[2]) {
			return nil, fmt.Errorf("invalid entry heading in session %q at line %d", session, i+1)
		}
		if seen[m[2]] {
			return nil, fmt.Errorf("duplicate entry %q in session %q", m[2], session)
		}
		seen[m[2]] = true
		end := i + 1
		for end < len(lines) && !benchHeadingRe.MatchString(lines[end]) {
			end++
		}
		body := strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
		rows = append(rows, ReceiptInfo{Session: session, ID: m[2], Stamp: stamp, Bytes: len(body), Policy: "unknown", Persisted: true})
		i = end
	}
	return rows, nil
}

func flatIndexRows(store, session string) ([]IndexRow, map[string]bool, error) {
	files, err := os.ReadDir(store)
	if err != nil {
		return nil, nil, err
	}
	var rows []IndexRow
	flat := map[string]bool{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".md")
		if !validID(id) || (session != "" && id != session) {
			continue
		}
		path, isFlat, err := recordForRead(store, id)
		if err != nil {
			return nil, nil, err
		}
		if !isFlat {
			continue
		}
		receipts, err := benchReceipts(path, id)
		if err != nil {
			return nil, nil, err
		}
		flat[id] = true
		for _, rc := range receipts {
			rows = append(rows, IndexRow{Session: rc.Session, ID: rc.ID, Stamp: rc.Stamp, Bytes: rc.Bytes})
		}
	}
	return rows, flat, nil
}

package cairn

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// existingStore holds the read verbs to a directory that is there. An empty
// directory is a valid empty store; a missing one is a wrong input, not an
// empty answer.
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

// recordForRead is locateRecord for the read verbs: the nested record still
// wins, and a permission error or a directory at the record path is an error,
// never evidence that the session is absent.
func recordForRead(store, session string) (string, bool, error) {
	if !ValidID(session) {
		return "", false, fmt.Errorf("bad session id %q: %s", session, IDRule)
	}
	for i, path := range []string{sessionFile(store, session), flatFile(store, session)} {
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
	return "", false, &NotFoundError{
		Msg:    fmt.Sprintf("no such session %q under store %q", session, store),
		Remedy: command("index", "--store", store),
	}
}

// flatReceipts holds a flat record's sections to the format and returns one
// receipt each. A flat record stores only a dated heading and the words, so
// its source and policy are not recoverable and are never inferred from a
// later caller, an unrelated log or the prose: Source is empty and Policy is
// PublishUnknown. An invalid stamp, an invalid id or one id filed twice is an
// error rather than an ambiguous receipt.
func flatReceipts(raw []byte, session string) ([]ReceiptInfo, error) {
	var rows []ReceiptInfo
	seen := map[string]bool{}
	for _, s := range flatSections(raw) {
		stamp, err := time.Parse(time.RFC3339Nano, s.stamp)
		if err != nil || !ValidID(s.id) {
			return nil, fmt.Errorf("invalid entry heading in session %q at line %d", session, s.line)
		}
		if seen[s.id] {
			return nil, fmt.Errorf("duplicate entry %q in session %q", s.id, session)
		}
		seen[s.id] = true
		rows = append(rows, ReceiptInfo{Session: session, ID: s.id, Stamp: stamp, Bytes: len(s.body), Policy: PublishUnknown, Persisted: true})
	}
	return rows, nil
}

// flatIndexRows is the index rows of every flat record under the store (or of
// session alone), and the set of sessions that are flat.
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
		if !ValidID(id) || (session != "" && id != session) {
			continue
		}
		path, isFlat, err := recordForRead(store, id)
		if err != nil {
			return nil, nil, err
		}
		if !isFlat {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		receipts, err := flatReceipts(raw, id)
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

package bus

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io/fs"
	"testing/fstest"
	"time"
)

// memStore is a strict in-memory Store for unit tests. It holds published changes at
// positions m1..mn as a map from position to filesystem, per-lane state (INDEX/RECEIPTS,
// CURSOR/OPEN), and rejects publishes that would create a conflict or change another lane.
// The design is LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a.
type memStore struct {
	// config is the bus roster, required for Publish validation.
	config *Config

	// positions holds the filesystem at each position, ordered by creation. The empty
	// Position is the beginning of history.
	positions []Position

	// fs maps each position to its complete filesystem.
	fs map[Position]fstest.MapFS

	// state holds per-lane state: INDEX, RECEIPTS, CURSOR, OPEN.
	state map[string]*laneState

	// head is the current position.
	head Position

	// Fail is a hook that injects a failure between Refresh and Publish, returning an
	// error for a named step. If set, it is called by Publish.
	Fail func(step string) error

	// now is the time function, defaulting to time.Now.
	now func() time.Time
}

type laneState struct {
	index    []IndexEntry
	receipts []string
	cursor   Cursor
	open     []OpenEntry
}

// NewMemStore creates a Store over an in-memory bus with the given roster. Every position
// has the empty Position as the beginning of history.
func NewMemStore(config *Config) Store {
	return &memStore{
		config:    config,
		positions: []Position{""}, // empty position is the start
		fs:        map[Position]fstest.MapFS{"": {}},
		state:     make(map[string]*laneState),
		head:      "",
		now:       time.Now,
	}
}

func (m *memStore) Roster(ctx context.Context) (*Config, error) {
	return m.config, nil
}

func (m *memStore) Head(ctx context.Context) (Position, error) {
	return m.head, nil
}

func (m *memStore) Refresh(ctx context.Context) (Position, bool, error) {
	// Refresh has no effect in the in-memory store; the head is always current.
	// Return the current head and moved=false.
	return m.head, false, nil
}

func (m *memStore) Since(ctx context.Context, from, to Position, limit int) (paths []string, capped bool, err error) {
	// Find positions.
	fromIdx := m.positionIndex(from)
	toIdx := m.positionIndex(to)

	if fromIdx == -1 {
		return nil, false, ErrUnknownPosition
	}
	if to == "" {
		toIdx = len(m.positions) - 1
	} else if toIdx == -1 {
		return nil, false, ErrUnknownPosition
	}

	if fromIdx >= toIdx {
		return nil, false, nil
	}

	// Collect unique paths changed from fromIdx+1 to toIdx (inclusive).
	pathSet := make(map[string]bool)
	for i := fromIdx + 1; i <= toIdx; i++ {
		pos := m.positions[i]
		for name := range m.fs[pos] {
			pathSet[name] = true
		}
	}

	for p := range pathSet {
		paths = append(paths, p)
		if limit > 0 && len(paths) >= limit {
			return paths, true, nil
		}
	}
	return paths, false, nil
}

func (m *memStore) Read(ctx context.Context, at Position) (fs.FS, error) {
	if idx := m.positionIndex(at); idx == -1 {
		return nil, ErrUnknownPosition
	}
	mfs, ok := m.fs[at]
	if !ok {
		return nil, fmt.Errorf("no filesystem at position %s", at)
	}
	// Return a copy to avoid external mutations.
	copy := make(fstest.MapFS)
	for k, v := range mfs {
		copy[k] = v
	}
	return copy, nil
}

func (m *memStore) Publish(ctx context.Context, change Change) (Published, error) {
	// Check for failure injection.
	if m.Fail != nil {
		if err := m.Fail("publish-start"); err != nil {
			return Published{}, err
		}
	}

	// Validate the author has a lane.
	author, ok := m.config.Lookup(change.Author.Name)
	if !ok {
		return Published{}, fmt.Errorf("author %q is not on the roster", change.Author.Name)
	}
	if author.Lane == "" {
		return Published{}, fmt.Errorf("author %q has no lane", change.Author.Name)
	}

	// Check that all note paths are in the author's lane.
	for path := range change.Notes {
		if !isInLane(path, author.Lane) {
			return Published{}, fmt.Errorf("note path %q is not in lane %q", path, author.Lane)
		}
	}

	// Check for conflicts: any existing note with different bytes.
	for path, bytes := range change.Notes {
		existing, ok := m.fs[m.head][path]
		if !ok {
			continue
		}
		if !bytesEqual(existing.Data, bytes) {
			return Published{}, ErrConflict
		}
	}

	// Check if already published: all notes with same bytes.
	allExist := true
	for path := range change.Notes {
		if _, ok := m.fs[m.head][path]; !ok {
			allExist = false
			break
		}
	}
	if allExist {
		return Published{At: m.head, Already: true}, nil
	}

	// Create a new position (commit sha).
	newPos := m.nextPosition()

	// Copy the previous filesystem and add new notes.
	newFS := make(fstest.MapFS)
	for k, v := range m.fs[m.head] {
		newFS[k] = v
	}
	for path, bytes := range change.Notes {
		newFS[path] = &fstest.MapFile{Data: bytes}
	}

	// Add INDEX entry if provided.
	laneState := m.ensureLaneState(author.Lane)
	if len(change.Index) > 0 {
		laneState.index = append(laneState.index, change.Index...)
		// Write INDEX file.
		var indexLines []byte
		for _, e := range laneState.index {
			indexLines = append(indexLines, []byte(IndexLine(e)+"\n")...)
		}
		newFS[IndexPath(author.Lane)] = &fstest.MapFile{Data: indexLines}
	}

	// Add RECEIPTS if provided.
	if len(change.Receipts) > 0 {
		laneState.receipts = append(laneState.receipts, change.Receipts...)
		var receiptsLines []byte
		for _, line := range laneState.receipts {
			receiptsLines = append(receiptsLines, []byte(line+"\n")...)
		}
		newFS[author.Lane+"/"+ReceiptsName] = &fstest.MapFile{Data: receiptsLines}
	}

	// Update CURSOR if provided.
	if change.Cursor != nil {
		laneState.cursor = *change.Cursor
		cursorLine := change.Cursor.Commit + " " + m.now().UTC().Format(ReceiptStampLayout)
		newFS[CursorPath(author.Lane)] = &fstest.MapFile{Data: []byte(cursorLine + "\n")}
	}

	// Update OPEN if provided.
	if change.Open != nil {
		laneState.open = *change.Open
		if len(laneState.open) == 0 {
			// Remove OPEN file if empty.
			delete(newFS, OpenPath(author.Lane))
		} else {
			var openLines []byte
			openLines = append(openLines, []byte(OpenHeader+"\n")...)
			for _, e := range laneState.open {
				openLines = append(openLines, []byte(OpenLine(e)+"\n")...)
			}
			newFS[OpenPath(author.Lane)] = &fstest.MapFile{Data: openLines}
		}
	}

	// Check for failure injection after state computation.
	if m.Fail != nil {
		if err := m.Fail("after-refresh"); err != nil {
			return Published{}, err
		}
	}

	// Store the new filesystem.
	m.fs[newPos] = newFS
	m.positions = append(m.positions, newPos)
	m.head = newPos

	return Published{At: newPos, Already: false, Attempts: 1}, nil
}

func (m *memStore) Find(ctx context.Context, id string) (note []byte, index IndexEntry, found bool, err error) {
	// Search the INDEX entries of all lanes for the id.
	for _, laneState := range m.state {
		for _, entry := range laneState.index {
			if entry.ID == id {
				// Read the note from the current filesystem.
				data, ok := m.fs[m.head][entry.Path]
				if !ok {
					return nil, entry, false, nil
				}
				return data.Data, entry, true, nil
			}
		}
	}
	return nil, IndexEntry{}, false, nil
}

// Helper methods.

func (m *memStore) nextPosition() Position {
	// Generate a synthetic commit hash based on the current head and state.
	h := sha1.New()
	h.Write([]byte(m.head))
	h.Write([]byte(fmt.Sprintf("%d", len(m.positions))))
	return Position(fmt.Sprintf("%040x", h.Sum(nil)))
}

func (m *memStore) positionIndex(pos Position) int {
	if pos == "" {
		return 0
	}
	for i, p := range m.positions {
		if p == pos {
			return i
		}
	}
	return -1
}

func (m *memStore) ensureLaneState(lane string) *laneState {
	if _, ok := m.state[lane]; !ok {
		m.state[lane] = &laneState{}
	}
	return m.state[lane]
}

func isInLane(path, lane string) bool {
	return path == lane || (len(path) > len(lane)+1 && path[:len(lane)+1] == lane+"/")
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

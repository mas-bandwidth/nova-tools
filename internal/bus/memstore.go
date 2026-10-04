package bus

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing/fstest"
)

// memCursorStamp is the annotation a cursor file carries beside its commit. Nothing in the
// unit tier reads it; the commit token is the claim the settlement compares.
const memCursorStamp = "2026-01-01T00:00:00Z"

// memSnap is the tree at one published position. files is what Read returns; byID is the
// catalogue Find answers from; changed is the lane paths this position added or replaced.
type memSnap struct {
	files   map[string][]byte
	byID    map[string]IndexEntry
	changed []string
}

// memBus is the shared history two benches of one in-memory bus publish onto. snaps[i] is
// the tree at position m{i+1}. Position m0 is the empty start, before any note, the same
// role as the bare repository's first commit: a second bench has a head to stand on.
// The design is LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a.
type memBus struct {
	mu     sync.Mutex
	snaps  []*memSnap
	roster []Participant
}

// memStore is a strict in-memory Store for the unit tier. Published changes stand at
// m1..mn. Read returns an fstest.MapFS as of the position asked. The design is
// LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a; the state machine is tla/BusCursor.tla.
type memStore struct {
	bus  *memBus
	seen int
	// Fail, when set, is called with step "publish" after Refresh and before the change
	// is recorded. A non-nil error is a lost reply: the change is recorded and the error
	// is returned, so a retry is Already. A nil error is the window in which another
	// bench publishes; this publish then settles against that.
	Fail func(step string) error
}

// NewMemStore is an empty bus at m0, shared by no other bench until Peer.
func NewMemStore() *memStore {
	return &memStore{bus: &memBus{}}
}

// Peer is a second bench over the same bus, standing at the remote head as it is now.
// It does not copy Fail: the injection belongs to the bench that publishes.
func (m *memStore) Peer() *memStore {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	return &memStore{bus: m.bus, seen: len(m.bus.snaps)}
}

// Roster is the participants that have published, validated the same way a roster file is.
func (m *memStore) Roster(ctx context.Context) (*Config, error) {
	m.bus.mu.Lock()
	ps := append([]Participant(nil), m.bus.roster...)
	m.bus.mu.Unlock()
	if len(ps) == 0 {
		return nil, fmt.Errorf("no participants%s", RosterShape)
	}
	c := &Config{Participants: ps}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Refresh brings this bench level with the shared history. moved is false when it was
// already there. See tla/BusCursor.tla (the advance action reads after it moves).
func (m *memStore) Refresh(ctx context.Context) (Position, bool, error) {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	moved := m.catchUp()
	return m.headPos(), moved, nil
}

// Head is the position this bench has refreshed to, m0 before the first note.
func (m *memStore) Head(ctx context.Context) (Position, error) {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	return m.headPos(), nil
}

// Since lists lane paths added or modified from one position to another. from empty is the
// beginning of history. A position that is not an ancestor of this bench's head is
// ErrUnknownPosition. A positive limit below the commit count sets capped. See
// tla/BusCursor.tla (BrokenNearerCursorWins) and LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a.
func (m *memStore) Since(ctx context.Context, from, to Position, limit int) ([]string, bool, error) {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	head := m.headPos()
	if to == "" {
		to = head
	}
	if to != head {
		return nil, false, ErrUnknownPosition
	}
	if from == "" {
		return lanePaths(m.tree()), false, nil
	}
	if from != to {
		if _, ok := posIndex(string(from), m.seen); !ok {
			return nil, false, ErrUnknownPosition
		}
	} else if _, ok := posIndex(string(from), m.seen); !ok {
		return nil, false, ErrUnknownPosition
	}
	fromIdx, _ := posIndex(string(from), m.seen)
	n := m.seen - fromIdx
	var paths []string
	seen := map[string]bool{}
	for i := fromIdx; i < m.seen; i++ {
		for _, p := range m.bus.snaps[i].changed {
			if seen[p] || !lanePath(p) {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, limit > 0 && n > limit, nil
}

// Read is the tree at one position, an fstest.MapFS. The empty position and the head are
// this bench's tree; an earlier ancestor is that snap; anything else is ErrUnknownPosition.
// See tla/BusCursor.tla: a reader reads the bus as it was at its cursor, not as it is now.
func (m *memStore) Read(ctx context.Context, at Position) (fs.FS, error) {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	head := m.headPos()
	var files map[string][]byte
	switch {
	case at == "" || at == head:
		files = m.tree().files
	default:
		idx, ok := posIndex(string(at), m.seen)
		if !ok {
			return nil, ErrUnknownPosition
		}
		if idx == 0 {
			files = nil
		} else {
			files = m.bus.snaps[idx-1].files
		}
	}
	return toMapFS(files), nil
}

// Publish lands one Change as one position, or reports it Already there. It refuses a
// lane-less author, a path outside the author's lane, an existing note with other bytes,
// and an INDEX line with no note, and it changes nothing when it refuses. Fail runs after
// Refresh and before the record. A cursor yields to the further read already on the bus.
// See LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a and tla/BusCursor.tla.
func (m *memStore) Publish(ctx context.Context, change Change) (Published, error) {
	if err := refuseShape(change); err != nil {
		return Published{}, err
	}
	m.bus.mu.Lock()
	if err := m.preflight(change); err != nil {
		m.bus.mu.Unlock()
		return Published{}, err
	}
	m.catchUp()
	m.bus.mu.Unlock()

	var failErr error
	if m.Fail != nil {
		failErr = m.Fail("publish")
	}

	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	m.catchUp()
	if err := m.preflight(change); err != nil {
		return Published{}, err
	}
	pub, err := m.apply(change)
	if err != nil {
		return Published{}, err
	}
	if failErr != nil {
		return pub, failErr
	}
	return pub, nil
}

// Find answers whether an id is on this bench's head, with the note's bytes and its INDEX
// entry. An id the bus does not hold is found=false and no error.
func (m *memStore) Find(ctx context.Context, id string) ([]byte, IndexEntry, bool, error) {
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	e, ok := m.tree().byID[id]
	if !ok {
		return nil, IndexEntry{}, false, nil
	}
	raw := m.tree().files[e.Path]
	if raw == nil {
		return nil, IndexEntry{}, false, fmt.Errorf("the index line for %s names %s, which is not a note", id, e.Path)
	}
	return append([]byte(nil), raw...), e, true, nil
}

func (m *memStore) headPos() Position {
	return Position("m" + strconv.Itoa(m.seen))
}

func (m *memStore) catchUp() bool {
	if m.seen == len(m.bus.snaps) {
		return false
	}
	m.seen = len(m.bus.snaps)
	return true
}

func (m *memStore) tree() *memSnap {
	if m.seen == 0 {
		return &memSnap{}
	}
	return m.bus.snaps[m.seen-1]
}

// refuseShape is the refusals that do not depend on the tree: a lane-less author, a note
// outside the lane, an INDEX line for another lane. See section 4a.
func refuseShape(change Change) error {
	if change.Author.Lane == "" {
		return fmt.Errorf("%q has no lane on this bus, so has nowhere to publish", change.Author.Name)
	}
	for rel := range change.Notes {
		if !strings.HasPrefix(rel, change.Author.Lane+"/") {
			return fmt.Errorf("the note %s is outside lane %s", rel, change.Author.Lane)
		}
	}
	for _, e := range change.Index {
		lane := e.Lane
		if lane == "" {
			lane = change.Author.Lane
		}
		if lane != change.Author.Lane {
			return fmt.Errorf("the index line for %s belongs to lane %s", e.ID, lane)
		}
	}
	return nil
}

// preflight is the refusals that read the tree and must change nothing: other bytes at a
// note path, and an INDEX line whose path is not a note. See section 4a.
func (m *memStore) preflight(change Change) error {
	files := m.tree().files
	for rel, want := range change.Notes {
		have, ok := files[rel]
		if !ok {
			continue
		}
		if !bytes.Equal(have, want) {
			return ErrConflict
		}
	}
	for _, e := range change.Index {
		if e.Path == "" {
			return fmt.Errorf("the index line for %s names no note", e.ID)
		}
		if _, ok := change.Notes[e.Path]; ok {
			continue
		}
		if _, ok := files[e.Path]; ok {
			continue
		}
		return fmt.Errorf("the index line for %s names %s, which is not a note", e.ID, e.Path)
	}
	return nil
}

// apply records the change on the shared head, or reports Already when the bytes are
// already there. INDEX and RECEIPTS keep both sides' lines. The further cursor wins, and
// the OPEN list is taken from that side. See section 4a and conflict.go.
func (m *memStore) apply(change Change) (Published, error) {
	base := m.tree()
	next := base.clone()
	lane := change.Author.Lane
	for rel, data := range change.Notes {
		if have, ok := next.files[rel]; ok {
			if !bytes.Equal(have, data) {
				return Published{}, ErrConflict
			}
			continue
		}
		next.files[rel] = append([]byte(nil), data...)
	}
	if len(change.Index) > 0 {
		var add strings.Builder
		for _, e := range change.Index {
			if e.Lane == "" {
				e.Lane = lane
			}
			add.WriteString(IndexLine(e) + "\n")
			if _, ok := next.byID[e.ID]; !ok {
				next.byID[e.ID] = e
			}
		}
		merged := UnionLines(string(next.files[IndexPath(lane)]), add.String())
		if merged != "" {
			next.files[IndexPath(lane)] = []byte(merged)
		}
	}
	if len(change.Receipts) > 0 {
		var add strings.Builder
		for _, line := range change.Receipts {
			add.WriteString(line + "\n")
		}
		merged := UnionLines(string(next.files[lane+"/"+ReceiptsName]), add.String())
		if merged != "" {
			next.files[lane+"/"+ReceiptsName] = []byte(merged)
		}
	}
	writeCursor := change.Cursor != nil
	if change.Cursor != nil {
		old := cursorCommit(next.files[CursorPath(lane)])
		if old != "" && furtherPos(old, change.Cursor.Commit, len(m.bus.snaps)) == old && old != change.Cursor.Commit {
			writeCursor = false
		}
	}
	if writeCursor {
		next.files[CursorPath(lane)] = []byte(change.Cursor.Commit + " " + memCursorStamp + "\n")
	}
	if change.Open != nil && (change.Cursor == nil || writeCursor) {
		writeOpen(next.files, lane, *change.Open)
	}
	if sameFiles(base.files, next.files) {
		return Published{At: m.headPos(), Already: true}, nil
	}
	next.changed = changedPaths(base.files, next.files)
	m.bus.snaps = append(m.bus.snaps, next)
	m.seen = len(m.bus.snaps)
	m.bus.remember(change.Author)
	return Published{At: m.headPos(), Attempts: 1}, nil
}

func (b *memBus) remember(p Participant) {
	if p.Name == "" {
		return
	}
	for _, have := range b.roster {
		if have.Name == p.Name {
			return
		}
	}
	b.roster = append(b.roster, p)
}

func (s *memSnap) clone() *memSnap {
	out := &memSnap{
		files: make(map[string][]byte, len(s.files)),
		byID:  make(map[string]IndexEntry, len(s.byID)),
	}
	for k, v := range s.files {
		out.files[k] = append([]byte(nil), v...)
	}
	for k, v := range s.byID {
		out.byID[k] = v
	}
	return out
}

func writeOpen(files map[string][]byte, lane string, entries []OpenEntry) {
	if len(entries) == 0 {
		delete(files, OpenPath(lane))
		return
	}
	var b strings.Builder
	b.WriteString(OpenHeader + "\n")
	for _, e := range entries {
		b.WriteString(OpenLine(e) + "\n")
	}
	files[OpenPath(lane)] = []byte(b.String())
}

func cursorCommit(raw []byte) string {
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// furtherPos is the cursor whose commit is a descendant of the other. A known position
// beats an unknown one. See conflict.go (the further read wins).
func furtherPos(a, b string, nSnaps int) string {
	ia, oka := posIndex(a, nSnaps)
	ib, okb := posIndex(b, nSnaps)
	switch {
	case oka && okb && ia >= ib:
		return a
	case oka && okb:
		return b
	case oka:
		return a
	default:
		return b
	}
}

// posIndex parses mN. N is known when it is m0 or a published position m1..m{nSnaps}.
func posIndex(s string, nSnaps int) (int, bool) {
	rest, ok := strings.CutPrefix(s, "m")
	if !ok || rest == "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || n > nSnaps {
		return 0, false
	}
	return n, true
}

func lanePath(p string) bool {
	return strings.HasPrefix(p, "from-") && strings.Contains(p, "/")
}

func lanePaths(s *memSnap) []string {
	var paths []string
	for p := range s.files {
		if lanePath(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

func changedPaths(before, after map[string][]byte) []string {
	var paths []string
	for k, v := range after {
		if !bytes.Equal(v, before[k]) {
			paths = append(paths, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			paths = append(paths, k)
		}
	}
	sort.Strings(paths)
	return paths
}

func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !bytes.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

func toMapFS(files map[string][]byte) fstest.MapFS {
	out := fstest.MapFS{}
	for p, b := range files {
		out[p] = &fstest.MapFile{Data: append([]byte(nil), b...)}
	}
	return out
}

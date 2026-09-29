// Package store binds the sprint core (internal/sprint) to the table layer
// (internal/ntable): one set read of the tables a step needs, then batch
// applies, chunked under the table layer's bounds, with an operation record
// for every step that touches more than one table or writes notifications,
// and the notifications written by the same step as the move that causes
// them. Backend is the small interface it needs of a store: Redis (the table
// layer's functions, in process) and Mem (in memory, for tests) implement it
// with the same refusal semantics.
package store

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Backend is what the sprint needs of a store.
type Backend interface {
	// Shapes reads each table's definition, rows, count cells, text cells and
	// revision, in one exchange.
	Shapes(ctx context.Context, tables []string) ([]ntable.Table, error)
	// CellIDs reads the member ids of every owned set cell of the tables, in
	// one exchange.
	CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error)
	// ReadSet reads members with their places, scores, revisions and fields
	// from one consistent snapshot of one table (at most
	// ntable.LimitReadSetMembers ids).
	ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error)
	// Apply applies one batch manifest atomically, or refuses it whole.
	Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error)

	Create(ctx context.Context, t ntable.Table) error
	RowsAdd(ctx context.Context, table string, rows []string) error
	RowSet(ctx context.Context, table, row string, texts map[string]string) error
	ViewSet(ctx context.Context, v ntable.View) error
	ViewDelete(ctx context.Context, name string) error
	DropTable(ctx context.Context, table string) error
	CheckTable(ctx context.Context, table string) error

	// ReadFence reads the sprint-wide fence: its generation and the pending
	// operation, if any, in one exchange.
	ReadFence(ctx context.Context) (Fence, error)
	// Acquire puts the operation in the fence and advances the generation,
	// only when the fence is empty at the generation the caller read; false
	// when it was not.
	Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error)
	// Release empties the fence of the operation, atomically with, when commit
	// is true, its notifications, the judgments it opens and closes, the
	// streams' progress and the result of a caller's operation id. It does
	// nothing when the fence no longer holds the operation (another writer
	// finished it).
	Release(ctx context.Context, op OpRecord, commit bool) error
	// Done is the recorded result of a caller's operation id.
	Done(ctx context.Context, callerOp string) (string, bool, error)
	// SetReview sets an open judgment's next review time (wait).
	SetReview(ctx context.Context, noteID string, at time.Time) error
	// Progress is each stream's last progress: the time of the last operation
	// that changed its state or any of its counts.
	Progress(ctx context.Context) (map[string]time.Time, error)
	// OpenNotes is every open judgment, one per subject.
	OpenNotes(ctx context.Context) ([]sprint.Open, error)
	// NotesSince is the notifications after the stream id (all when empty),
	// at most max, oldest first, with each one's stream id.
	NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error)
	Cursor(ctx context.Context) (string, error)
	SetCursor(ctx context.Context, id string) error
	// Coordinator is the sprint's coordinator, set by init ("" when none
	// is); release is refused for any other actor. It is kept in the notes
	// hash (under a name no note id has), so teardown removes it with it.
	Coordinator(ctx context.Context) (string, error)
	SetCoordinator(ctx context.Context, name string) error
	// RecordIDs is every member id the table has held a record of, placed
	// or not, as its change log names them: the records its drop keeps.
	RecordIDs(ctx context.Context, table string) ([]string, error)
	// DeleteKeys deletes exactly the keys named, and counts those it deleted.
	DeleteKeys(ctx context.Context, keys []string) (int, error)
}

// Fence is the sprint-wide fence as read: its generation, advanced by every
// acquisition, and the pending operation.
type Fence struct {
	Gen     uint64
	Pending *OpRecord
}

// OpRecord is a step's operation, held in the fence while it applies: its
// manifests in apply order (the work table last), and what it writes at its
// logical commit, the release: the notifications, the answers, the streams it
// moved, and its result under the caller's operation id.
type OpRecord struct {
	ID        string                 `json:"id"`
	Verb      string                 `json:"verb"`
	At        time.Time              `json:"at"`
	Manifests []ntable.BatchManifest `json:"manifests"`
	Notes     []sprint.Note          `json:"notes,omitempty"`   // happened and judgment, ids assigned
	Decided   []sprint.Note          `json:"decided,omitempty"` // answers to open judgments
	Closes    []string               `json:"closes,omitempty"`  // open keys the step closes
	Streams   []string               `json:"streams,omitempty"` // streams whose progress it is
	CallerOp  string                 `json:"caller_op,omitempty"`
	Result    string                 `json:"result,omitempty"` // the result, recorded under CallerOp
}

// Tables is the stored table names of the record's manifests, in order.
func (o OpRecord) Tables() []string {
	out := make([]string, len(o.Manifests))
	for i, m := range o.Manifests {
		out[i] = m.Table
	}
	return out
}

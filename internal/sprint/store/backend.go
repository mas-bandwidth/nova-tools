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
	// RowsHide hides rows from the drawn table, as the table layer's row hide does: each
	// stays in the table, its cells and its folds, and is not drawn (a friend's fleet row,
	// sprint.FriendRow, in the stored view).
	RowsHide(ctx context.Context, table string, rows []string) error
	// RowsDel removes rows, with the cards placed in them (a row absent is
	// skipped): the readers table's reader remove, which first refuses a row
	// that holds a card (docs/SPEC-SPRINT.md section 6), and stream remove,
	// which first refuses a stream that holds one, its control card going with
	// its merge row (sprint.StreamRemove).
	RowsDel(ctx context.Context, table string, rows []string) error
	// RowsDelIf removes each guard's row, with the cards placed in it, only
	// while the guard's record (the row's control card) is still on no cell
	// at the revision the guard names, every guard checked and every passing
	// row deleted as one atomic change: a record placed again or changed
	// since the caller read it keeps its row. It is the rows it removed
	// (sprint.FleetDrift, the removal).
	RowsDelIf(ctx context.Context, table string, guards []RowGuard) ([]string, error)
	// KeysDelIf deletes each guard's Keys only while its record is still on
	// no cell at the guard's revision and its row is not in the table, every
	// guard checked and every passing guard's keys deleted as one atomic
	// change: a member whose row came back or whose card was placed again
	// keeps them. It is the rows of the guards whose keys it deleted.
	KeysDelIf(ctx context.Context, table string, guards []RowGuard) ([]string, error)
	// Place puts a record that is on no cell back into a cell at the score, as
	// the table layer's cell add does (a batch never places a removed member):
	// a fleet member whose control card a sync took off rejoins (RejoinMembers).
	Place(ctx context.Context, table, row, col, id string, score float64) error
	RowSet(ctx context.Context, table, row string, texts map[string]string) error
	ViewSet(ctx context.Context, v ntable.View) error
	ViewDelete(ctx context.Context, name string) error
	DropTable(ctx context.Context, table string) error
	CheckTable(ctx context.Context, table string) error

	// Epoch reads the sprint's epoch: its number, when it was last cleared,
	// and whether the restore of the shape of the epoch before it is owed.
	Epoch(ctx context.Context) (EpochState, error)
	// AdvanceEpoch moves the sprint from epoch from to from+1 atomically,
	// recording the time and that the restore of epoch from's shape is owed;
	// false when the sprint is no longer at from.
	AdvanceEpoch(ctx context.Context, from uint64, at time.Time) (bool, error)
	// SettleEpoch records that the restore owed at epoch n is done; it does
	// nothing when the sprint is no longer at n.
	SettleEpoch(ctx context.Context, n uint64) error
	// AtEpoch is the backend pinned to an epoch: the sprint's keys of that
	// epoch, and its writes at that epoch. With old, reads of the tables read
	// that epoch as it was, not the active one.
	AtEpoch(epoch uint64, old bool) Backend

	// ReadFence reads the sprint-wide fence: its generation and the pending
	// operation, if any, with whether the machine is RUNNING and the length of
	// the work table's queue, in one exchange.
	ReadFence(ctx context.Context) (Fence, error)
	// QueueRead is the work table's queue, oldest first (sprint.QueuedChange): the
	// changes steps queued while the machine ran, which the pump drains.
	QueueRead(ctx context.Context) ([]sprint.QueuedChange, error)
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
	// DoneBefore is the latest epoch before the given one that recorded a
	// result of the caller's operation id, in one exchange.
	DoneBefore(ctx context.Context, callerOp string, before uint64) (uint64, bool, error)
	// SetReview sets an open judgment's next review time (wait).
	SetReview(ctx context.Context, noteID string, at, set time.Time) error
	// Progress is each stream's last progress: the time of the last operation
	// that changed its state or any of its counts.
	Progress(ctx context.Context) (map[string]time.Time, error)
	// OpenNotes is every open judgment, one per subject.
	OpenNotes(ctx context.Context) ([]sprint.Open, error)
	// NotesSince is the notifications after the stream id (all when empty),
	// at most max, oldest first, with each one's stream id.
	NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error)
	// LogSince is the log's lines after the stream id (all when empty), at
	// most max, with their stream ids: the epoch's append-only record.
	LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error)
	// Tails is the last stream id of the log and of the inbox's stream ("" when
	// empty), read in one round trip: what a read up to now covers.
	Tails(ctx context.Context) (log, inbox string, err error)
	Cursor(ctx context.Context) (string, error)
	SetCursor(ctx context.Context, id string) error
	// Coordinator is the sprint's coordinator, set by init ("" when none
	// is); release is refused for any other actor. It is one for the sprint,
	// kept by a clear, and teardown removes it.
	Coordinator(ctx context.Context) (string, error)
	SetCoordinator(ctx context.Context, name string) error
	// RecordIDs is every member id the table has held a record of, placed
	// or not, as its change log names them: the records its drop keeps.
	RecordIDs(ctx context.Context, table string) ([]string, error)
	// DeleteKeys deletes exactly the keys named, and counts those it deleted.
	DeleteKeys(ctx context.Context, keys []string) (int, error)
}

// RowGuard is one row a conditional delete names (RowsDelIf, KeysDelIf) and the
// record it is conditional on: its stored id, its store key (Names.RecordKey,
// the key the table layer writes it under) and the revision it was read at, on
// no cell; Keys are the keys KeysDelIf deletes with it.
type RowGuard struct {
	Row, ID, Key string
	Rev          uint64
	Keys         []string
}

// EpochState is the sprint's epoch as read.
type EpochState struct {
	N       uint64
	Cleared time.Time
	// Owed says the clear into N has not restored the shape of epoch N-1 at
	// it yet: the next verb, tick or clear performs the restore first.
	Owed bool
}

// Fence is the sprint-wide fence as read: its generation, advanced by every
// acquisition, and the pending operation.
type Fence struct {
	Gen     uint64
	Pending *OpRecord
	// Running says the machine's record is RUNNING, and Queued is the length
	// of the work table's queue: while either holds, a step other than the
	// pump queues its work-table changes (queue.go).
	Running bool
	Queued  int
	// Stuck is the stuck record (stuck.go) as it was read with the fence, ""
	// for none: the step that writes next carries its judgment, and reads no
	// record of its own for it.
	Stuck string
}

// OpRecord is a step's operation, held in the fence while it applies: its
// manifests in apply order (the work table last), and what it writes at its
// logical commit, the release: the notifications, the answers, the streams it
// moved, and its result under the caller's operation id.
type OpRecord struct {
	ID string `json:"id"`
	// Lock says the operation is a part's lock (lock.go): no manifests, in
	// flight within its grace, released unwritten past it.
	Lock      bool                   `json:"lock,omitempty"`
	Verb      string                 `json:"verb"`
	At        time.Time              `json:"at"`
	Manifests []ntable.BatchManifest `json:"manifests"`
	Notes     []sprint.Note          `json:"notes,omitempty"`   // happened and judgment, ids assigned
	Decided   []sprint.Note          `json:"decided,omitempty"` // answers to open judgments
	Log       []sprint.Line          `json:"log,omitempty"`     // the log's move lines of the step's changes
	Updates   []sprint.Note          `json:"updates,omitempty"` // open judgments rewritten in place
	Closes    []string               `json:"closes,omitempty"`  // open keys the step closes
	Streams   []string               `json:"streams,omitempty"` // streams whose progress it is
	CallerOp  string                 `json:"caller_op,omitempty"`
	Result    string                 `json:"result,omitempty"` // the result, recorded under CallerOp
	// Stuck is the stuck operation this one reports (its judgment is among
	// Notes): its commit deletes the stuck record.
	Stuck string `json:"stuck,omitempty"`
	// Queue is the work-table changes the step queued for the pump, appended
	// to the queue by its commit; Drain is how many entries of the queue the
	// pump's drain consumed, taken off its head by its commit, so that each
	// entry is applied once (sprint.Drain).
	Queue []sprint.QueuedChange `json:"queue,omitempty"`
	Drain int                   `json:"drain,omitempty"`
	// Seat is the seat's change (store/seat.go): its commit sets the
	// coordinator to its holder and records it as the seat's last change.
	Seat *sprint.SeatChange `json:"seat,omitempty"`
	// Roster is the friends roster's change: applied in Release atomically.
	Roster *sprint.FriendRosterChange `json:"roster,omitempty"`
}

// Tables is the stored table names of the record's manifests, in order.
func (o OpRecord) Tables() []string {
	out := make([]string, len(o.Manifests))
	for i, m := range o.Manifests {
		out[i] = m.Table
	}
	return out
}

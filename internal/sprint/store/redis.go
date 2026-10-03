package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// Redis is the Backend of a Redis store holding the table layer's function
// library: the tables through internal/ntable, in process; the operation
// records and the notifications under the deployment's prefix.
type Redis struct {
	C     redis.UniversalClient
	Names sprint.Names
	Now   func() time.Time
	// Pinned is the sprint epoch the backend is pinned to: the sprint keys it
	// names and the epoch its writes carry. Old reads the tables at that epoch
	// as it was, not the active one.
	Pinned uint64
	Old    bool
	// trips counts the client's round trips, once CountTrips is called
	// (stats.go); the backend's pinned copies share it.
	trips *atomic.Int64
	// beforeExec, set by a functional test only, runs inside a conditional
	// delete's transaction after its read and before its EXEC: the seam a test
	// writes another writer's change through (RowsDelIf).
	beforeExec func()
}

func (r *Redis) key(name string) string { return r.Names.KeyAt(name, r.Pinned) }

// AtEpoch is the backend pinned to an epoch.
func (r *Redis) AtEpoch(epoch uint64, old bool) Backend {
	c := *r
	c.Pinned, c.Old = epoch, old
	return &c
}

func (r *Redis) writeOpts() ntable.WriteOptions { return ntable.WriteOptions{Epoch: r.Pinned} }

// Epoch reads the sprint's epoch hash in one exchange.
func (r *Redis) Epoch(ctx context.Context) (EpochState, error) {
	vals, err := r.C.HMGet(ctx, r.Names.EpochKey(), "n", "cleared", "restore").Result()
	if err != nil {
		return EpochState{}, err
	}
	var es EpochState
	if v, ok := vals[0].(string); ok {
		if es.N, err = strconv.ParseUint(v, 10, 64); err != nil {
			return es, fmt.Errorf("the sprint's epoch %q is not a number", v)
		}
	}
	if v, ok := vals[1].(string); ok {
		es.Cleared, _ = time.Parse(time.RFC3339Nano, v)
	}
	if v, ok := vals[2].(string); ok && v != "" {
		es.Owed = true
	}
	return es, nil
}

// AdvanceEpoch is WATCH on the epoch hash, then one MULTI/EXEC; the hash's
// restore field names the epoch whose shape the new one owes.
func (r *Redis) AdvanceEpoch(ctx context.Context, from uint64, at time.Time) (bool, error) {
	key := r.Names.EpochKey()
	moved := false
	err := r.C.Watch(ctx, func(tx *redis.Tx) error {
		v, err := tx.HGet(ctx, key, "n").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		n := uint64(0)
		if v != "" {
			n, _ = strconv.ParseUint(v, 10, 64)
		}
		if n != from {
			moved = true
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, key, "n", strconv.FormatUint(from+1, 10), "cleared", at.UTC().Format(time.RFC3339Nano), "restore", strconv.FormatUint(from, 10))
			return nil
		})
		return err
	}, key)
	if errors.Is(err, redis.TxFailedErr) || moved {
		return false, nil
	}
	return err == nil, err
}

// SettleEpoch removes the restore owed at epoch n: WATCH on the epoch hash,
// then one MULTI/EXEC, only while the sprint is at n.
func (r *Redis) SettleEpoch(ctx context.Context, n uint64) error {
	key := r.Names.EpochKey()
	err := r.C.Watch(ctx, func(tx *redis.Tx) error {
		v, err := tx.HGet(ctx, key, "n").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if v != strconv.FormatUint(n, 10) {
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HDel(ctx, key, "restore")
			return nil
		})
		return err
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		// the epoch hash moved: another clear advanced it, or settled it
		return nil
	}
	return err
}

// Shapes reads every table in one pipeline; pinned to an old epoch, each at
// that epoch.
func (r *Redis) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	if r.Old {
		out := make([]ntable.Table, len(tables))
		for i, t := range tables {
			s, err := ntable.ReadAt(ctx, r.C, t, r.Pinned)
			if err != nil {
				return nil, err
			}
			out[i] = s
		}
		return out, nil
	}
	pipe := r.C.Pipeline()
	cmds := make([]*ntable.ReadCmd, len(tables))
	for i, t := range tables {
		cmds[i] = ntable.NewReader(t).Queue(ctx, pipe)
	}
	if _, err := pipe.Exec(ctx); err != nil && !isReply(err) {
		return nil, err
	}
	out := make([]ntable.Table, len(tables))
	for i, cmd := range cmds {
		t, _, err := cmd.Result()
		if err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

func isReply(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// CellIDs reads every owned set cell's members in one pipeline, through the
// table layer's cell reader: each set column is read as a members column.
func (r *Redis) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	pipe := r.C.Pipeline()
	views := make([]ntable.Table, len(shapes))
	cmds := make([]*ntable.CellsCmd, len(shapes))
	for i, t := range shapes {
		v := t
		v.Columns = append([]ntable.Column(nil), t.Columns...)
		for j := range v.Columns {
			if v.Columns[j].HasSet() {
				v.Columns[j].Projection = ntable.Members
			}
		}
		v.Rows = make([]ntable.Row, len(t.Rows))
		for k, row := range t.Rows {
			row.Cells = append([]ntable.Cell(nil), row.Cells...)
			v.Rows[k] = row
		}
		views[i] = v
		cmds[i] = ntable.QueueCells(ctx, pipe, &views[i])
	}
	if _, err := pipe.Exec(ctx); err != nil && !isReply(err) {
		return nil, err
	}
	out := map[string][]string{}
	for i, cmd := range cmds {
		cmd.Result()
		t := views[i]
		for _, row := range t.Rows {
			for j, c := range t.Columns {
				if !c.HasSet() || j >= len(row.Cells) {
					continue
				}
				cell := row.Cells[j]
				if cell.Unread {
					return nil, fmt.Errorf("table %s row %s column %s did not come back: %s", t.Name, row.Key, c.Name, cell.UnreadWhy)
				}
				for _, m := range cell.Members {
					out[t.Name] = append(out[t.Name], m.Member)
				}
			}
		}
	}
	return out, nil
}

func (r *Redis) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	if r.Old {
		return ntable.ReadSetMembers(ctx, r.C, table, ids, r.Pinned)
	}
	return ntable.ReadSetMembers(ctx, r.C, table, ids)
}

func (r *Redis) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	return ntable.ApplyBatch(ctx, r.C, m)
}

// ApplyAll applies manifests of one table in one round trip (one
// MULTI/EXEC): each its own batch, as Apply applies it.
func (r *Redis) ApplyAll(ctx context.Context, ms []ntable.BatchManifest) ([]ntable.Receipt, []error) {
	return ntable.ApplyBatches(ctx, r.C, ms)
}

var _ BatchApplier = (*Redis)(nil)

func (r *Redis) Create(ctx context.Context, t ntable.Table) error {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	return ntable.Create(ctx, r.C, t, now())
}

func (r *Redis) RowsAdd(ctx context.Context, table string, rows []string) error {
	_, err := ntable.RowsAdd(ctx, r.C, table, rows, r.writeOpts())
	return err
}

func (r *Redis) RowsHide(ctx context.Context, table string, rows []string) error {
	_, err := ntable.RowsHide(ctx, r.C, table, true, rows, r.writeOpts())
	return err
}

// RowsDel removes each row through the table layer's row delete, one write
// each; the caller checked that none holds a card but a stream's control
// card.
func (r *Redis) RowsDel(ctx context.Context, table string, rows []string) error {
	for _, row := range rows {
		if _, err := ntable.RowDel(ctx, r.C, table, row, r.writeOpts()); err != nil {
			return err
		}
	}
	return nil
}

// RowsDelIf removes the rows of the guards that hold, in one optimistic
// transaction for every guard: WATCH on each guard's record (its Key, where the
// table layer writes it) and on the table's rows, the records read (on no cell,
// at the guard's revision), and every passing row's delete sent through the
// table layer's row delete in one MULTI/EXEC. Every write of a record (a batch,
// a cell add) and every row added changes a watched key, so a member placed
// again after the read aborts the EXEC, and the read is made again (up to
// condTries times; past that nothing is deleted and the caller's next run
// tries again).
func (r *Redis) RowsDelIf(ctx context.Context, table string, guards []RowGuard) ([]string, error) {
	return r.delIf(ctx, table, guards, true, func(p redis.Pipeliner, g RowGuard, body string) {
		p.FCall(ctx, ntable.FnRowDel, []string{ntable.DefKey(table)}, table, g.Row, body)
	})
}

// KeysDelIf deletes the keys of the guards that hold, whose rows are not in the
// table, in one optimistic transaction as RowsDelIf: a row added or a record
// placed again after the read aborts it.
func (r *Redis) KeysDelIf(ctx context.Context, table string, guards []RowGuard) ([]string, error) {
	return r.delIf(ctx, table, guards, false, func(p redis.Pipeliner, g RowGuard, _ string) {
		if len(g.Keys) > 0 {
			p.Del(ctx, g.Keys...)
		}
	})
}

// condTries is how many times a conditional delete reads again after another
// writer changed a watched key.
const condTries = 3

// delIf is the one transaction of RowsDelIf and KeysDelIf: rowPresent says the
// guard's row must be in the table (a row delete) or must not be (a key delete).
func (r *Redis) delIf(ctx context.Context, table string, guards []RowGuard, rowPresent bool, op func(redis.Pipeliner, RowGuard, string)) ([]string, error) {
	if len(guards) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{Epoch: strconv.FormatUint(r.Pinned, 10)})
	if err != nil {
		return nil, err
	}
	watch := []string{ntable.RowsKeyAt(table, r.Pinned)}
	ids := make([]string, len(guards))
	for i, g := range guards {
		watch = append(watch, g.Key)
		ids[i] = g.ID
	}
	for try := 0; try < condTries; try++ {
		var pass []RowGuard
		var cmds []redis.Cmder
		err := r.C.Watch(ctx, func(tx *redis.Tx) error {
			pass = nil
			shape, err := ntable.Shape(ctx, tx, table)
			if err != nil {
				return err
			}
			rows := map[string]bool{}
			for _, row := range shape.Rows {
				rows[row.Key] = true
			}
			recs := map[string]ntable.ReadSetMember{}
			for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
				rs, err := ntable.ReadSetMembers(ctx, tx, table, ids[start:min(start+ntable.LimitReadSetMembers, len(ids))])
				if err != nil {
					return err
				}
				for _, m := range rs.Members {
					recs[m.ID] = m
				}
			}
			for _, g := range guards {
				if m, ok := recs[g.ID]; ok && !m.Placed && m.Revision == g.Rev && rows[g.Row] == rowPresent {
					pass = append(pass, g)
				}
			}
			if len(pass) == 0 {
				return nil
			}
			if r.beforeExec != nil {
				r.beforeExec()
			}
			cmds, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				for _, g := range pass {
					op(p, g, string(body))
				}
				return nil
			})
			return err
		}, watch...)
		if errors.Is(err, redis.TxFailedErr) {
			continue // another writer changed a watched key: read again
		}
		if err != nil {
			return nil, err
		}
		for _, c := range cmds {
			if fc, ok := c.(*redis.Cmd); ok {
				if reply, err := fc.Slice(); err == nil && len(reply) > 0 && fmt.Sprint(reply[0]) == "REFUSED" {
					return nil, fmt.Errorf("the table layer refused a conditional delete on %s: %v", table, reply)
				}
			}
		}
		out := make([]string, len(pass))
		for i, g := range pass {
			out[i] = g.Row
		}
		return out, nil
	}
	return nil, nil
}

// Place puts a record that is on no cell back into the cell, through the table
// layer's cell add, one write.
func (r *Redis) Place(ctx context.Context, table, row, col, id string, score float64) error {
	_, err := ntable.CellAdd(ctx, r.C, table, row, col, id, score, r.writeOpts())
	return err
}

func (r *Redis) RowSet(ctx context.Context, table, row string, texts map[string]string) error {
	_, err := ntable.RowSet(ctx, r.C, table, row, texts, r.writeOpts())
	return err
}

func (r *Redis) ViewSet(ctx context.Context, v ntable.View) error { return ntable.ViewSet(ctx, r.C, v) }

func (r *Redis) ViewDelete(ctx context.Context, name string) error {
	_, err := ntable.ViewDelete(ctx, r.C, name)
	return err
}

func (r *Redis) DropTable(ctx context.Context, table string) error {
	_, err := ntable.DropDefinition(ctx, r.C, table, r.writeOpts())
	return err
}

func (r *Redis) CheckTable(ctx context.Context, table string) error {
	_, err := ntable.Check(ctx, r.C, table)
	return err
}

// The log's keys, under the deployment's prefix.
const (
	keyFence    = "fence"    // STRING, the pending operation record
	keyGen      = "fencegen" // STRING, the fence's generation
	keyInbox    = "inbox"    // STREAM of notifications, field "note"
	keyLog      = "log"      // STREAM of the log's lines, field "line"
	keyNotes    = "notes"    // HASH note id -> judgment note
	keyOpen     = "open"     // HASH <note id>|<subject> -> note id, one per open subject
	keyCursor   = "cursor"   // STRING, the coordinator's last read stream id
	keyProgress = "progress" // HASH stream -> RFC3339 time of its last progress
	keyDone     = "done"     // HASH caller operation id -> result
	keyQueue    = "queue"    // LIST of the work table's queued changes (sprint.QueuedChange, JSON), oldest first
)

func (r *Redis) ReadFence(ctx context.Context) (Fence, error) {
	p := r.C.Pipeline()
	mget, llen := r.queueFence(ctx, p)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Fence{}, err
	}
	return fenceOf(mget, llen)
}

// queueFence queues the fence's read on a pipeline: the fence, its
// generation and the machine's state, and the queue's length.
func (r *Redis) queueFence(ctx context.Context, p redis.Pipeliner) (*redis.SliceCmd, *redis.IntCmd) {
	return p.MGet(ctx, r.key(keyFence), r.key(keyGen), r.Names.Key(keyMachine), r.Names.Key(keyStuck)), p.LLen(ctx, r.key(keyQueue))
}

// fenceOf is the fence a pipeline read.
func fenceOf(mget *redis.SliceCmd, llen *redis.IntCmd) (Fence, error) {
	vals, err := mget.Result()
	if err != nil {
		return Fence{}, err
	}
	var f Fence
	f.Queued = int(llen.Val())
	if s, ok := vals[2].(string); ok {
		var m Machine
		f.Running = json.Unmarshal([]byte(s), &m) == nil && m.Running()
	}
	if s, ok := vals[1].(string); ok {
		f.Gen, _ = strconv.ParseUint(s, 10, 64)
	}
	if len(vals) > 3 {
		f.Stuck, _ = vals[3].(string)
	}
	if s, ok := vals[0].(string); ok {
		var op OpRecord
		if err := json.Unmarshal([]byte(s), &op); err != nil {
			return f, fmt.Errorf("the fence holds an unreadable operation record: %w", err)
		}
		f.Pending = &op
	}
	return f, nil
}

var errFenceMoved = errors.New("the fence moved")

// Acquire is WATCH on the fence and its generation, then MULTI/EXEC.
func (r *Redis) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	body, err := json.Marshal(op)
	if err != nil {
		return false, err
	}
	fence, genKey, epochKey := r.key(keyFence), r.key(keyGen), r.Names.EpochKey()
	err = r.C.Watch(ctx, func(tx *redis.Tx) error {
		vals, err := tx.MGet(ctx, fence, genKey).Result()
		if err != nil {
			return err
		}
		var g uint64
		if s, ok := vals[1].(string); ok {
			g, _ = strconv.ParseUint(s, 10, 64)
		}
		if vals[0] != nil || g != gen {
			return errFenceMoved
		}
		e, err := tx.HGet(ctx, epochKey, "n").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if n, _ := strconv.ParseUint(e, 10, 64); n != r.Pinned {
			return errFenceMoved // the sprint was cleared since the step read it
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, fence, body, 0)
			p.Set(ctx, genKey, gen+1, 0)
			return nil
		})
		return err
	}, fence, genKey, epochKey)
	if errors.Is(err, errFenceMoved) || errors.Is(err, redis.TxFailedErr) {
		return false, nil
	}
	return err == nil, err
}

// Release is WATCH on the fence, then one MULTI/EXEC: the notifications
// appended, the judgments opened on every subject, the answered ones closed,
// the streams' progress, the caller's result, and the fence emptied.
func (r *Redis) Release(ctx context.Context, op OpRecord, commit bool) error {
	fence := r.key(keyFence)
	// The fence holds this operation when its record begins with this
	// operation's id (json.Marshal writes OpRecord's id first): read that
	// much of it, not the whole record, which carries every manifest.
	id, err := json.Marshal(op.ID)
	if err != nil {
		return err
	}
	prefix := `{"id":` + string(id) + `,`
	for i := 0; i < 8; i++ {
		err = r.C.Watch(ctx, func(tx *redis.Tx) error {
			cur, err := tx.GetRange(ctx, fence, 0, int64(len(prefix)-1)).Result()
			if err != nil {
				return err
			}
			if cur != prefix {
				return nil // empty (released) or another operation's
			}
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				if commit {
					if err := r.commit(ctx, p, op); err != nil {
						return err
					}
				}
				p.Del(ctx, fence)
				return nil
			})
			return err
		}, fence)
		// The fence moved while this release was prepared: another writer
		// finishing the same operation released it, or took the fence after;
		// read it again, and release only if it still holds this operation.
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	// The fence kept moving through every attempt, as Acquire takes it: read
	// it once more; an operation it no longer holds was released by the
	// writer that finished it, which is success. Only a true store error, or
	// the fence still holding it, is unknown.
	held, herr := r.heldOp(ctx)
	if herr != nil {
		return herr
	}
	recorded := false
	if commit && op.CallerOp != "" {
		_, ok, derr := r.Done(ctx, op.CallerOp)
		if derr != nil {
			return derr
		}
		recorded = ok
	}
	if released(op, commit, held, recorded) {
		return nil
	}
	return err
}

// heldOp is the id of the operation the fence holds, "" when it is empty.
func (r *Redis) heldOp(ctx context.Context) (string, error) {
	cur, err := r.C.Get(ctx, r.key(keyFence)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var held OpRecord
	if json.Unmarshal([]byte(cur), &held) != nil {
		return "", fmt.Errorf("the fence holds an unreadable operation record")
	}
	return held.ID, nil
}

// released says a release whose transaction kept failing is done: the fence
// no longer holds the operation (held is the id it holds, "" when empty) and,
// for a commit with a caller's operation id, its result is recorded, so the
// writer that released it finished it.
func released(op OpRecord, commit bool, held string, recorded bool) bool {
	if held == op.ID {
		return false
	}
	return !commit || op.CallerOp == "" || recorded
}

func (r *Redis) commit(ctx context.Context, p redis.Pipeliner, op OpRecord) error {
	if op.Drain > 0 {
		p.LTrim(ctx, r.key(keyQueue), int64(op.Drain), -1)
	}
	for _, x := range op.Queue {
		body, err := json.Marshal(x)
		if err != nil {
			return err
		}
		p.RPush(ctx, r.key(keyQueue), string(body))
	}
	for _, line := range op.Log {
		body, err := json.Marshal(line)
		if err != nil {
			return err
		}
		p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", string(body)}})
	}
	for _, n := range append(append([]sprint.Note{}, op.Notes...), op.Decided...) {
		body, err := json.Marshal(n.Bound())
		if err != nil {
			return err
		}
		lb, err := json.Marshal(sprint.NoteLine(n.Bound(), op.ID))
		if err != nil {
			return err
		}
		p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", string(lb)}})
		p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyInbox), Values: []any{"note", string(body)}})
		if n.Kind == sprint.Judgment || n.Kind == sprint.Acknowledged {
			p.HSet(ctx, r.key(keyNotes), n.ID, string(body))
			for _, s := range n.Subjects() {
				p.HSet(ctx, r.key(keyOpen), sprint.OpenKey(n.ID, s), n.ID)
			}
		}
	}
	for _, n := range op.Updates {
		body, err := json.Marshal(n.Bound())
		if err != nil {
			return err
		}
		line := sprint.NoteLine(n.Bound(), op.ID)
		line.Verb = "updated"
		lb, err := json.Marshal(line)
		if err != nil {
			return err
		}
		p.HSet(ctx, r.key(keyNotes), n.ID, string(body))
		p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", string(lb)}})
	}
	if len(op.Closes) > 0 {
		p.HDel(ctx, r.key(keyOpen), op.Closes...)
	}
	if op.Stuck != "" {
		p.Del(ctx, r.Names.Key(keyStuck))
	}
	for _, s := range op.Streams {
		p.HSet(ctx, r.key(keyProgress), s, op.At.UTC().Format(time.RFC3339))
	}
	if op.CallerOp != "" {
		p.HSet(ctx, r.key(keyDone), op.CallerOp, op.Result)
	}
	if op.Seat != nil {
		rec, err := seatRecord(op.Seat)
		if err != nil {
			return err
		}
		p.Set(ctx, r.Names.Key(keyCoordinator), op.Seat.Holder, 0)
		p.Set(ctx, r.Names.Key(keySeat), rec, 0)
	}
	return nil
}

// QueueRead is LRANGE over the whole queue: one exchange.
func (r *Redis) QueueRead(ctx context.Context) ([]sprint.QueuedChange, error) {
	raw, err := r.C.LRange(ctx, r.key(keyQueue), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]sprint.QueuedChange, len(raw))
	for i, b := range raw {
		if err := json.Unmarshal([]byte(b), &out[i]); err != nil {
			return nil, fmt.Errorf("the work table's queue holds an unreadable entry: %w", err)
		}
	}
	return out, nil
}

func (r *Redis) Done(ctx context.Context, callerOp string) (string, bool, error) {
	v, err := r.C.HGet(ctx, r.key(keyDone), callerOp).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

// DoneBefore asks every earlier epoch's results for the caller's operation id,
// in one pipeline.
func (r *Redis) DoneBefore(ctx context.Context, callerOp string, before uint64) (uint64, bool, error) {
	if before == 0 {
		return 0, false, nil
	}
	pipe := r.C.Pipeline()
	cmds := make([]*redis.BoolCmd, before)
	for e := uint64(0); e < before; e++ {
		cmds[e] = pipe.HExists(ctx, r.Names.KeyAt(keyDone, e), callerOp)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, false, err
	}
	for e := before; e > 0; e-- {
		if cmds[e-1].Val() {
			return e - 1, true, nil
		}
	}
	return 0, false, nil
}

func (r *Redis) SetReview(ctx context.Context, noteID string, at, set time.Time) error {
	raw, err := r.C.HGet(ctx, r.key(keyNotes), noteID).Result()
	if errors.Is(err, redis.Nil) {
		return fmt.Errorf("no judgment %s; run: nova-sprint inbox", noteID)
	}
	if err != nil {
		return err
	}
	var n sprint.Note
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return err
	}
	n.Review, n.ReviewSet = at, set
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return r.C.HSet(ctx, r.key(keyNotes), noteID, string(body)).Err()
}

func (r *Redis) Progress(ctx context.Context) (map[string]time.Time, error) {
	h, err := r.C.HGetAll(ctx, r.key(keyProgress)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for k, v := range h {
		out[k], _ = time.Parse(time.RFC3339, v)
	}
	return out, nil
}

func (r *Redis) OpenNotes(ctx context.Context) ([]sprint.Open, error) {
	open, err := r.C.HGetAll(ctx, r.key(keyOpen)).Result()
	if err != nil {
		return nil, err
	}
	return r.openOf(ctx, open)
}

// openOf is the open judgments of the open index read: the notes it names,
// read in one exchange.
func (r *Redis) openOf(ctx context.Context, open map[string]string) ([]sprint.Open, error) {
	if len(open) == 0 {
		return nil, nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, nid := range open {
		if !seen[nid] {
			seen[nid] = true
			ids = append(ids, nid)
		}
	}
	vals, err := r.C.HMGet(ctx, r.key(keyNotes), ids...).Result()
	if err != nil {
		return nil, err
	}
	notes := map[string]sprint.Note{}
	for i, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}
		var n sprint.Note
		if err := json.Unmarshal([]byte(s), &n); err != nil {
			return nil, fmt.Errorf("notification %s: %w", ids[i], err)
		}
		notes[ids[i]] = n
	}
	var out []sprint.Open
	for k, nid := range open {
		out = append(out, sprint.Open{Key: k, Note: notes[nid]})
	}
	sortOpen(out)
	return out, nil
}

func (r *Redis) NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error) {
	start := "-"
	if after != "" {
		start = "(" + after
	}
	msgs, err := r.C.XRangeN(ctx, r.key(keyInbox), start, "+", int64(max)).Result()
	if err != nil {
		return nil, nil, err
	}
	var notes []sprint.Note
	var ids []string
	for _, m := range msgs {
		var n sprint.Note
		if s, ok := m.Values["note"].(string); ok && json.Unmarshal([]byte(s), &n) == nil {
			notes = append(notes, n)
			ids = append(ids, m.ID)
		}
	}
	return notes, ids, nil
}

func (r *Redis) Tails(ctx context.Context) (string, string, error) {
	p := r.C.Pipeline()
	lg := p.XRevRangeN(ctx, r.key(keyLog), "+", "-", 1)
	in := p.XRevRangeN(ctx, r.key(keyInbox), "+", "-", 1)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", "", err
	}
	last := func(c *redis.XMessageSliceCmd) string {
		if ms, err := c.Result(); err == nil && len(ms) > 0 {
			return ms[0].ID
		}
		return ""
	}
	return last(lg), last(in), nil
}

func (r *Redis) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	start := "-"
	if after != "" {
		start = "(" + after
	}
	msgs, err := r.C.XRangeN(ctx, r.key(keyLog), start, "+", int64(max)).Result()
	if err != nil {
		return nil, nil, err
	}
	var lines []sprint.Line
	var ids []string
	for _, m := range msgs {
		var l sprint.Line
		if s, ok := m.Values["line"].(string); ok && json.Unmarshal([]byte(s), &l) == nil {
			lines = append(lines, l)
			ids = append(ids, m.ID)
		}
	}
	return lines, ids, nil
}

func (r *Redis) Cursor(ctx context.Context) (string, error) {
	v, err := r.C.Get(ctx, r.key(keyCursor)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (r *Redis) SetCursor(ctx context.Context, id string) error {
	return r.C.Set(ctx, r.key(keyCursor), id, 0).Err()
}

// keyCoordinator holds the coordinator's name: one for the sprint, as the
// machine's records, so a clear keeps it.
const keyCoordinator = "coordinator"

func (r *Redis) Coordinator(ctx context.Context) (string, error) {
	v, err := r.C.Get(ctx, r.Names.Key(keyCoordinator)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (r *Redis) SetCoordinator(ctx context.Context, name string) error {
	return r.C.Set(ctx, r.Names.Key(keyCoordinator), name, 0).Err()
}

// changeEvent is the part of a table change stream's event the twin reads
// (twin.go): the revisions it moved the table between, the epoch, the verb,
// and the records it names.
type changeEvent struct {
	epoch, verb         string
	before, after       uint64
	members, batchDelta string
}

// changePage is how many events one read of a change stream takes.
const changePage = 64

// twinVerbs are the table writes whose events name every record they changed,
// or that name none (the twin then re-reads the table's rows, texts and
// properties from the shape, which every read brings up to date): a batch's
// account names each of its entries ("apply"); a row's texts ("row_set") and
// rows ("rows_add", "row_add") name none; a table's order, sort and columns
// ("set") name none, and the shape's rows carry the order; a row's delete
// ("row_del") names the cards it unplaced. Any other write in the span makes
// the twin read the table whole.
var twinVerbs = map[string]bool{"apply": true, "row_set": true, "rows_add": true, "row_add": true, "set": true, "row_del": true}

// TableChanges reads the table's change stream from its newest event back to
// the one that left revision from, and says the records the writes between
// from and to named (twin.go). ok is false when the events do not chain from
// from to to at the pinned epoch, or one is a write that does not name its
// records.
func (r *Redis) TableChanges(ctx context.Context, table string, from, to uint64) ([]string, bool, error) {
	gap := func(why string) ([]string, bool, error) {
		return nil, false, &GapError{Table: table, From: from, To: to, Why: why}
	}
	if to < from {
		return gap("the twin is ahead of the table")
	}
	if to == from {
		return nil, true, nil
	}
	key := ntable.DefKey(table) + ":changes"
	epoch := strconv.FormatUint(r.Pinned, 10)
	need := to
	var ids []string
	end := "+"
	for page := 0; page < 64; page++ {
		evs, err := r.C.XRevRangeN(ctx, key, end, "-", changePage).Result()
		if err != nil && strings.Contains(err.Error(), "NOPERM") {
			// a user not granted the stream's read: said, and the twin reads
			// the table whole
			return nil, false, &GrantError{Command: "XREVRANGE", Key: key, Cause: err}
		}
		if err != nil {
			return nil, false, err
		}
		if len(evs) == 0 {
			return gap(fmt.Sprintf("the stream ends before revision %d", need))
		}
		for _, x := range evs {
			ev := readChange(x.Values)
			if ev.epoch != epoch || ev.after > to {
				// another epoch's, or written after the shape was read (the
				// read of the records sees it, and refuses the revision)
				continue
			}
			if ev.after != need {
				return gap(fmt.Sprintf("the event before revision %d leaves revision %d", need, ev.after))
			}
			if !twinVerbs[ev.verb] {
				return gap(fmt.Sprintf("a write %q at revision %d names no records", ev.verb, ev.after))
			}
			named, err := changeIDs(ev)
			if err != nil {
				return gap(fmt.Sprintf("the event at revision %d is unreadable: %v", ev.after, err))
			}
			ids = append(ids, named...)
			need = ev.before
			if need == from {
				return ids, true, nil
			}
			if need < from {
				return gap(fmt.Sprintf("the events skip revision %d", from))
			}
		}
		end = "(" + evs[len(evs)-1].ID
	}
	return gap("more than 64 pages of events")
}

func readChange(v map[string]any) changeEvent {
	str := func(k string) string { s, _ := v[k].(string); return s }
	ev := changeEvent{epoch: str("epoch"), verb: str("verb"), members: str("members"), batchDelta: str("batch_delta")}
	ev.before, _ = strconv.ParseUint(str("rev_before"), 10, 64)
	ev.after, _ = strconv.ParseUint(str("rev_after"), 10, 64)
	return ev
}

// changeIDs is the records an event names: the members it moved and every
// entry of its batch's account.
func changeIDs(ev changeEvent) ([]string, error) {
	var out []string
	var moved []struct {
		ID string `json:"id"`
	}
	if ev.members != "" && ev.members != "[]" {
		if err := json.Unmarshal([]byte(ev.members), &moved); err != nil {
			return nil, err
		}
	}
	for _, m := range moved {
		out = append(out, m.ID)
	}
	if ev.batchDelta != "" {
		var d struct {
			Members []struct {
				ID string `json:"id"`
			} `json:"members"`
		}
		if err := json.Unmarshal([]byte(ev.batchDelta), &d); err != nil {
			return nil, err
		}
		for _, m := range d.Members {
			out = append(out, m.ID)
		}
	} else if ev.verb == "apply" {
		return nil, fmt.Errorf("a batch event without its account")
	}
	return out, nil
}

var _ TableChanger = (*Redis)(nil)

// RowsSet writes the display cells of many rows in one round trip.
func (r *Redis) RowsSet(ctx context.Context, table string, rows map[string]map[string]string) error {
	return ntable.RowSetMany(ctx, r.C, table, rows, r.writeOpts())
}

var _ RowsSetter = (*Redis)(nil)

// ReadView reads, in one exchange, what a read of the twin reads first (the
// fence, the tables' shapes, the open judgments' index and the coordinator),
// then the notes the index names in a second: two round trips where one each
// took five (twin.go).
func (r *Redis) ReadView(ctx context.Context, tables []string) (View, error) {
	if r.Old {
		return View{}, errors.New("a read of an earlier epoch reads no view")
	}
	p := r.C.Pipeline()
	mget, llen := r.queueFence(ctx, p)
	shapes := make([]*ntable.ReadCmd, len(tables))
	for i, t := range tables {
		shapes[i] = ntable.NewReader(t).Queue(ctx, p)
	}
	open := p.HGetAll(ctx, r.key(keyOpen))
	coord := p.Get(ctx, r.Names.Key(keyCoordinator))
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReply(err) {
		return View{}, err
	}
	var v View
	var err error
	if v.Fence, err = fenceOf(mget, llen); err != nil {
		return View{}, err
	}
	v.Shapes = make([]ntable.Table, len(tables))
	for i, cmd := range shapes {
		if v.Shapes[i], _, err = cmd.Result(); err != nil {
			return View{}, err
		}
	}
	idx, err := open.Result()
	if err != nil {
		return View{}, err
	}
	if v.Coordinator, err = coord.Result(); err != nil && !errors.Is(err, redis.Nil) {
		return View{}, err
	}
	if v.Open, err = r.openOf(ctx, idx); err != nil {
		return View{}, err
	}
	return v, nil
}

var _ ViewReader = (*Redis)(nil)

// RowsOrder puts the named rows first, in that order, through the table
// layer's row order, ending any standing sort in the same call (the table
// layer refuses a row order under one: SORTED).
func (r *Redis) RowsOrder(ctx context.Context, table string, rows []string) error {
	_, err := ntable.Set(ctx, r.C, table, ntable.SetOpts{RowSort: &ntable.Sort{Manual: true}, RowOrder: rows}, r.writeOpts())
	return err
}

var _ RowsOrderer = (*Redis)(nil)

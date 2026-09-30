package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// The tick-end note and the coordinator's one wake (errata 3 amendment 8, the
// owner's rule: "the coordinator is woken at the end of the tick, ONCE";
// "no wake, if nothing in the inbox"). A tick that finds notes for the
// coordinator (sprint.TickEndCounts) after the last note a tick-end covered
// writes one tick-end note, "judgments=N", in a notes-only step of its own;
// inbox --wait blocks until the next one.

// keyTickEnd holds "<epoch> <stream id>": the epoch the mark is of and the
// last note a tick-end scanned. A mark of another epoch (a clear since) is
// none: the new epoch's notes are scanned from their first.
const keyTickEnd = "tickend"

// tickEndScan is the most notes one tick-end reads.
const tickEndScan = 10000

// tickEnd writes the tick-end note when notes for the coordinator came after
// the mark, and moves the mark to the last note it scanned: a note that lands
// after the scan is after the mark, so the next tick counts it, once. The
// count it wrote, 0 for none. A backend with no keys (KV) writes none.
func (st *Store) tickEnd(ctx context.Context) (int, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return 0, nil
	}
	raw, _, err := kv.GetKey(ctx, keyTickEnd)
	if err != nil {
		return 0, err
	}
	epoch := strconv.FormatUint(st.epoch, 10)
	mark := ""
	if e, id, ok := strings.Cut(raw, " "); ok && e == epoch {
		mark = id
	}
	notes, ids, err := st.B.NotesSince(ctx, mark, tickEndScan)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	n := 0
	for _, x := range notes {
		if sprint.TickEndCounts(x) {
			n++
		}
	}
	if n > 0 {
		to, err := st.B.Coordinator(ctx)
		if err != nil {
			return 0, err
		}
		if _, err := st.Run(ctx, Step{Verb: "tick end", Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NTickEnd, Who: sprint.MachineActor,
				At: s.Now, To: to, What: fmt.Sprintf("judgments=%d", n)}}}
		}}); err != nil {
			return 0, err
		}
	}
	return n, kv.SetKey(ctx, keyTickEnd, epoch+" "+ids[len(ids)-1])
}

// NotesWaiter is a backend that can block on its notes.
type NotesWaiter interface {
	// WaitNotes blocks until a note after the stream id after is there, or
	// for at most d, and says whether one came. One exchange with the store.
	WaitNotes(ctx context.Context, after string, d time.Duration) (bool, error)
}

// WaitNotes is XREAD BLOCK on the notes from after, one note.
func (r *Redis) WaitNotes(ctx context.Context, after string, d time.Duration) (bool, error) {
	start := after
	if start == "" {
		start = "0-0"
	}
	res, err := r.C.XRead(ctx, &redis.XReadArgs{Streams: []string{r.key(keyInbox), start}, Count: 1, Block: max(d, time.Millisecond)}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return err == nil && len(res) > 0 && len(res[0].Messages) > 0, err
}

// WaitTickEnd blocks until a tick-end note comes after the stream id after,
// or for at most d; true when one came. A backend that cannot block is read
// every tenth of a second by the store's clock.
func (st *Store) WaitTickEnd(ctx context.Context, after string, d time.Duration) (bool, error) {
	end := st.now().Add(d)
	for {
		notes, ids, err := st.B.NotesSince(ctx, after, tickEndScan)
		if err != nil {
			return false, err
		}
		for i, n := range notes {
			if n.Type == sprint.NTickEnd {
				return true, nil
			}
			after = ids[i]
		}
		left := end.Sub(st.now())
		if left <= 0 || ctx.Err() != nil {
			return false, ctx.Err()
		}
		if w, ok := st.B.(NotesWaiter); ok {
			if _, err := w.WaitNotes(ctx, after, left); err != nil {
				return false, err
			}
		} else {
			st.sleep(min(left, 100*time.Millisecond))
		}
	}
}

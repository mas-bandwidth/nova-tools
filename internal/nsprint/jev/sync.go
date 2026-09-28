package jev

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// SyncResult is one sync batch.
type SyncResult struct {
	Events, Decisions, Outcomes int
	From, Cursor                string
	// Moved is the review moves whose record had moved on to a later review
	// before sync read it: counted, printed, no row.
	Moved []string
	// Gap is set when ws:log was trimmed past the cursor (its first entry
	// is after it) before sync read it: moves in between may be missing.
	Gap string
}

// Sync reads up to max moves of ws:log after jev:cursor and writes the
// decisions and outcomes they are, and the cursor, in one MULTI. Four round
// trips whatever the batch: the cursor and the log's first id; the moves;
// the records; the rows and the read waits.
func Sync(ctx context.Context, c redis.Cmdable, max int64) (SyncResult, error) {
	var res SyncResult
	var cur *redis.StringCmd
	var first *redis.XMessageSliceCmd
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		cur = p.Get(ctx, KeyCursor)
		first = p.XRangeN(ctx, KeyMoves, "-", "+", 1)
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("read %s: %w", KeyCursor, err)
	}
	res.From = cur.Val()
	start := "-"
	if res.From != "" {
		start = "(" + res.From
		if f := first.Val(); len(f) == 1 && streamIDLess(res.From, f[0].ID) {
			res.Gap = res.From + ".." + f[0].ID
		}
	}
	msgs, err := c.XRangeN(ctx, KeyMoves, start, "+", max).Result()
	if err != nil {
		return res, fmt.Errorf("read %s: %w", KeyMoves, err)
	}
	res.Cursor = res.From
	if len(msgs) == 0 {
		return res, nil
	}
	events := make([]Event, len(msgs))
	for i, m := range msgs {
		f := map[string]string{}
		for k, v := range m.Values {
			f[k] = fmt.Sprint(v)
		}
		events[i] = Event{ID: m.ID, Fields: f}
	}
	res.Events = len(events)

	snap := Snapshot{Records: map[string]map[string]string{}, Rows: map[string]bool{}, Reads: map[string]map[string]string{}}
	ids := Needs(events)
	if err := hgetAll(ctx, c, ids, func(id string) string { return "task:" + id }, snap.Records); err != nil {
		return res, err
	}
	rows, waits, opens, builts := Then(events)
	ex := make([]*redis.IntCmd, len(rows))
	var openV, builtV *redis.SliceCmd
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, k := range rows {
			ex[i] = p.Exists(ctx, k)
		}
		if len(opens) > 0 {
			openV = p.HMGet(ctx, KeyOpenReview, opens...)
		}
		if len(builts) > 0 {
			builtV = p.HMGet(ctx, KeyBuilt, builts...)
		}
		return nil
	}); err != nil {
		return res, fmt.Errorf("read rows: %w", err)
	}
	for i, k := range rows {
		snap.Rows[k] = ex[i].Val() == 1
	}
	snap.Open, snap.Built = fields(opens, openV), fields(builts, builtV)
	if err := hgetAll(ctx, c, waits, ReadsKey, snap.Reads); err != nil {
		return res, err
	}

	pl := MakePlan(events, snap)
	at := time.Now().UnixMilli()
	if _, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, d := range pl.Decisions {
			recordCmds(ctx, p, d, at)
		}
		for _, o := range pl.Outcomes {
			joinCmds(ctx, p, o, at)
		}
		for _, w := range pl.Reads {
			p.HSet(ctx, ReadsKey(w.Primary), w.Copy, w.Score+" "+w.Head)
		}
		for _, id := range pl.Settled {
			p.Del(ctx, ReadsKey(id))
		}
		for _, h := range []struct {
			key string
			m   map[string]string
		}{{KeyOpenReview, pl.Open}, {KeyBuilt, pl.Built}} {
			for _, id := range sortedKeys(h.m) {
				if v := h.m[id]; v == "" {
					p.HDel(ctx, h.key, id)
				} else {
					p.HSet(ctx, h.key, id, v)
				}
			}
		}
		p.Set(ctx, KeyCursor, pl.Cursor, 0)
		return nil
	}); err != nil {
		return res, fmt.Errorf("write the ledger: %w", err)
	}
	res.Decisions, res.Outcomes, res.Cursor, res.Moved = len(pl.Decisions), len(pl.Outcomes), pl.Cursor, pl.Moved
	return res, nil
}

// hgetAll reads HGETALL key(id) for every id in one pipeline into out (a
// missing key stays absent).
func hgetAll(ctx context.Context, c redis.Cmdable, ids []string, key func(string) string, out map[string]map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			cmds[i] = p.HGetAll(ctx, key(id))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("read %s...: %w", key(ids[0]), err)
	}
	for i, id := range ids {
		if m := cmds[i].Val(); len(m) > 0 {
			out[id] = m
		}
	}
	return nil
}

// fields reads an HMGET reply against its field names; nil is empty.
func fields(names []string, cmd *redis.SliceCmd) map[string]string {
	out := map[string]string{}
	if cmd == nil {
		return out
	}
	for i, v := range cmd.Val() {
		if sv, ok := v.(string); ok && i < len(names) {
			out[names[i]] = sv
		}
	}
	return out
}

// streamID is a stream entry id's two parts.
func streamID(s string) (uint64, uint64, bool) {
	a, b, _ := strings.Cut(s, "-")
	ms, err1 := strconv.ParseUint(a, 10, 64)
	seq, err2 := strconv.ParseUint(b, 10, 64)
	return ms, seq, err1 == nil && err2 == nil
}

// streamIDLess is a < b as stream ids.
func streamIDLess(a, b string) bool {
	am, as, ok1 := streamID(a)
	bm, bs, ok2 := streamID(b)
	if !ok1 || !ok2 {
		return false
	}
	return am < bm || (am == bm && as < bs)
}

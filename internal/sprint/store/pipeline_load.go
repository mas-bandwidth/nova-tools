package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

func redisBackend(b Backend) *Redis {
	switch v := b.(type) {
	case *Redis:
		return v
	case interface{ Unwrap() Backend }:
		return redisBackend(v.Unwrap())
	default:
		return nil
	}
}

// PipelinedLoadWithFence loads a snapshot across all requested tables and
// checks the trailing fence in a 2-stage pipeline:
// Stage 1: Member Discovery (Shapes + CellIDs).
// Stage 2: Single Pipeline [All ReadSet Chunks + OpenNotes + Coordinator + Trailing Fence].
// The trailing fence f2 is queued at the tail of the Stage 2 pipeline so Redis executes
// it contiguously immediately after the table reads, collapsing the OCC collision window.
func (st *Store) PipelinedLoadWithFence(ctx context.Context, tables []string, every []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, Fence, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, Fence{}, err
	}
	var last *movedError
	r := st.retry(ctx)
	for r.next(LoadTries) {
		s, f2, err := st.pipelinedLoadOnceWithFence(ctx, tables, every, extras)
		var moved *movedError
		if errors.As(err, &moved) {
			last = moved
			continue
		}
		return s, f2, err
	}
	return nil, Fence{}, fmt.Errorf("the tables are busy: table %s kept changing while it was read, %d reads in %s; nothing was changed; run the verb again",
		last.table, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) pipelinedLoadOnceWithFence(ctx context.Context, tables []string, every []string, extras func(*sprint.Snapshot) map[string][]string) (*sprint.Snapshot, Fence, error) {
	r := redisBackend(st.B)
	if r == nil {
		// Non-redis backend (e.g. Mem for in-memory tests): fall back to sequential Load + ReadFence.
		s, err := st.loadOnce(ctx, tables, every, extras)
		if err != nil {
			return nil, Fence{}, err
		}
		f2, err := st.B.ReadFence(ctx)
		return s, f2, err
	}

	// Stage 1: Member Discovery
	s := &sprint.Snapshot{Now: st.now(), Epoch: st.epoch, Cleared: st.cleared, Prefix: st.Names.Prefix}
	stored := make([]string, len(tables))
	for i, t := range tables {
		stored[i] = st.Names.Table(t)
	}
	shapes, err := st.shapes(ctx, stored)
	if err != nil {
		return nil, Fence{}, err
	}
	if len(tables) > 0 {
		st.stats().reads.Add(1)
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, Fence{}, err
	}
	for _, shape := range shapes {
		if st.pinned && shape.Epoch != st.epoch {
			return nil, Fence{}, errCleared
		}
	}

	tbls := make([]*sprint.Table, len(tables))
	for i, shape := range shapes {
		t := sprint.NewTable(tables[i])
		t.Epoch, t.Revision = shape.Epoch, shape.Revision
		t.SetProps(shape.Props)
		for _, row := range shape.Rows {
			t.SetRows(append(t.Rows(), row.Key))
			if row.Hidden {
				t.SetHidden(row.Key)
			}
			if len(row.Texts) > 0 {
				t.Texts[row.Key] = row.Texts
			}
		}
		tbls[i] = t
		switch tables[i] {
		case sprint.Work:
			s.Work = t
		case sprint.Readers:
			s.Readers = t
		case sprint.Merge:
			s.Merge = t
		case sprint.Fleet:
			s.Fleet = t
		}
	}

	// Stage 2: Bulk ReadSet Chunks + OpenNotes + Coordinator + Trailing Fence in ONE Redis Pipeline!
	pipe := r.C.Pipeline()

	type queuedChunk struct {
		table    *sprint.Table
		shape    ntable.Table
		chunkIDs []string
		cmd      *ntable.ReadSetCmd
		placed   bool
	}
	var chunks []queuedChunk

	for i, shape := range shapes {
		t := tbls[i]
		tableIDs := ids[shape.Name]
		for start := 0; start < len(tableIDs); start += ntable.LimitReadSetMembers {
			end := min(start+ntable.LimitReadSetMembers, len(tableIDs))
			chunkIDs := tableIDs[start:end]
			var cmd *ntable.ReadSetCmd
			if r.Old {
				cmd, err = ntable.QueueReadSetMembers(ctx, pipe, shape.Name, chunkIDs, r.Pinned)
			} else {
				cmd, err = ntable.QueueReadSetMembers(ctx, pipe, shape.Name, chunkIDs)
			}
			if err != nil {
				return nil, Fence{}, err
			}
			chunks = append(chunks, queuedChunk{
				table:    t,
				shape:    shape,
				chunkIDs: chunkIDs,
				cmd:      cmd,
				placed:   true,
			})
		}
	}

	// Queue OpenNotes
	openCmd := pipe.HGetAll(ctx, r.key(keyOpen))

	// Queue Coordinator and the seat record
	coordCmd := pipe.Get(ctx, r.Names.Key(keyCoordinator))
	seatCmd := pipe.Get(ctx, r.Names.Key(keySeat))

	// Queue Trailing Fence (f2) at the tail of Stage 2 pipeline
	fenceCmd, queueCmd := r.queueFence(ctx, pipe)

	// Execute Stage 2 in 1 single network round trip
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, Fence{}, err
	}

	// Decode Trailing Fence f2
	f2, err := fenceOf(fenceCmd, queueCmd)
	if err != nil {
		return nil, f2, err
	}

	// Decode Coordinator
	coordVal, err := coordCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, f2, err
	}
	s.Coordinator = coordVal
	seatVal, err := seatCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, f2, err
	}
	if s.SeatGeneration, err = seatGenerationOf(seatVal, err == nil); err != nil {
		return nil, f2, err
	}
	s.Actor = st.Actor

	// Decode OpenNotes
	openMap, err := openCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, f2, err
	}
	if len(openMap) > 0 {
		openNotes, err := r.openOf(ctx, openMap)
		if err != nil {
			return nil, f2, err
		}
		s.Open, s.Acked = sprint.SplitOpen(openNotes)
	}

	// Decode All Table Chunks
	for _, chunk := range chunks {
		res, err := chunk.cmd.Result()
		if err != nil {
			return nil, f2, err
		}
		st.stats().rows.Add(int64(len(res.Members)))
		if res.Revision != chunk.shape.Revision || (chunk.placed && len(res.Missing) > 0) {
			return nil, f2, &movedError{table: chunk.shape.Name}
		}
		for _, m := range res.Members {
			if chunk.placed && !m.Placed {
				return nil, f2, &movedError{table: chunk.shape.Name}
			}
			c := &sprint.Card{ID: sprint.CardID(m.ID), Score: m.Score, Rev: m.Revision, Fields: m.Fields}
			if m.Placed {
				c.Row, c.Col = m.Row, m.Col
			}
			chunk.table.Put(c)
		}
	}

	// Extras (if any)
	after := len(openMap) > 0 // the open notes were read after the trailing fence
	if extras != nil {
		for table, want := range extras(s) {
			t := s.T(table)
			var missing []string
			for _, id := range want {
				if t.Card(id) == nil {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				after = true
				if err := st.readInto(ctx, t, st.sids(missing), false); err != nil {
					return nil, f2, err
				}
			}
		}
	}
	// Every record of a table (Step.EveryRecord), read after the pipeline as
	// the extras are: the records kept off the table (a dropped primary), so
	// a step that selects dropped primaries by stream finds them.
	if len(every) > 0 {
		after = true
		if err := st.readKeptRecords(ctx, s, every); err != nil {
			return nil, f2, err
		}
	}
	if after {
		// What was read after the trailing fence (the open notes, the extras)
		// is of the same state only if the fence is still where it was: the
		// fence read last is the one the read answers with, so a generation
		// that moved makes the caller read again, as a fresh read does.
		f3, err := st.B.ReadFence(ctx)
		if err != nil {
			return nil, f2, err
		}
		if f3.Gen != f2.Gen || f3.Pending != nil {
			return s, f3, nil
		}
	}
	return s, f2, nil
}

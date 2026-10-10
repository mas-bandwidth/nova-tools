package store

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// The unit cover of the two Redis reads of teardown.go a reader found at 0.0%:
// RecordIDs (teardown.go:190), the table's change log read a page an exchange,
// and DeleteKeys (teardown.go:229), the named keys deleted in one pipelined
// exchange. Both are reached through the package's own seam, Redis.C, an
// interface the test fakes (as tick_cover_test.go does): every call the cover
// does not name panics on the nil embedded interface. No sleep, no real time,
// no network, no subprocess, no live store.

// teardownCoverClient is a redis.UniversalClient faking only the stream read
// and the pipeline opener the two functions make.
type teardownCoverClient struct {
	redis.UniversalClient // nil: a call the cover does not name panics
	xrange                func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	pipeline              func() redis.Pipeliner
}

func (f *teardownCoverClient) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	return f.xrange(ctx, stream, start, stop, count)
}

func (f *teardownCoverClient) Pipeline() redis.Pipeliner {
	return f.pipeline()
}

// teardownCoverPipe is the pipeliner DeleteKeys queues its DELs into: the
// chunk sizes and the EXEC's reply are what the rows name.
type teardownCoverPipe struct {
	redis.Pipeliner // nil: a call the cover does not name panics
	del             func(ctx context.Context, keys ...string) *redis.IntCmd
	exec            func(ctx context.Context) ([]redis.Cmder, error)
}

func (p *teardownCoverPipe) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	return p.del(ctx, keys...)
}

func (p *teardownCoverPipe) Exec(ctx context.Context) ([]redis.Cmder, error) {
	return p.exec(ctx)
}

// TestTeardownCoverRedisRecordIDsReadsTheChangeLog pins Redis.RecordIDs
// (teardown.go:190): every member id the change log names, deduped and sorted,
// read recordIDsPage events an exchange until a page comes short; an event
// naming no member is skipped; the refusal is the store not answering the read.
func TestTeardownCoverRedisRecordIDsReadsTheChangeLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// page is one full exchange of recordIDsPage events, member ids counting
	// down so the result's sort is pinned; want is their sorted order.
	page := make([]redis.XMessage, recordIDsPage)
	sorted := make([]string, recordIDsPage)
	for i := range page {
		page[i] = redis.XMessage{ID: "1-" + strconv.Itoa(i),
			Values: map[string]any{"members": fmt.Sprintf(`[{"id":"m%04d"}]`, recordIDsPage-1-i)}}
		sorted[i] = fmt.Sprintf("m%04d", i)
	}
	for _, tt := range []struct {
		name       string
		replies    []*redis.XMessageSliceCmd
		want       []string
		wantStarts []string
		wantErr    error
	}{
		{name: "one page, deduped, sorted, nameless events skipped", replies: []*redis.XMessageSliceCmd{redis.NewXMessageSliceCmdResult([]redis.XMessage{
			{ID: "1-0", Values: map[string]any{"members": `[{"id":"b"},{"id":"a"},{"id":"b"}]`}},
			{ID: "1-1", Values: map[string]any{"members": ""}},
			{ID: "1-2", Values: map[string]any{"members": "[]"}},
			{ID: "1-3", Values: map[string]any{"members": "{}"}},
			{ID: "1-4", Values: map[string]any{}},
		}, nil)}, want: []string{"a", "b"}, wantStarts: []string{"-"}},
		{name: "two pages, the next read opening after the last event", replies: []*redis.XMessageSliceCmd{
			redis.NewXMessageSliceCmdResult(page, nil),
			redis.NewXMessageSliceCmdResult([]redis.XMessage{
				{ID: "2-0", Values: map[string]any{"members": `[{"id":"m9999"},{"id":"z"}]`}}}, nil),
		}, want: append(append([]string{}, sorted...), "m9999", "z"), wantStarts: []string{"-", "(1-999"}},
		{name: "refused: the store does not answer the read",
			replies:    []*redis.XMessageSliceCmd{redis.NewXMessageSliceCmdResult(nil, errLost)},
			wantStarts: []string{"-"}, wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stream, stop string
			var starts []string
			var counts []int64
			call := 0
			r := &Redis{C: &teardownCoverClient{xrange: func(_ context.Context, s, start, st string, n int64) *redis.XMessageSliceCmd {
				stream, stop = s, st
				starts, counts = append(starts, start), append(counts, n)
				require.Less(t, call, len(tt.replies), "one exchange per named reply")
				reply := tt.replies[call]
				call++
				return reply
			}}}
			got, err := r.RecordIDs(ctx, "card")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr, tt.name)
				assert.Nil(t, got, "a refused read returns nothing")
			} else {
				assert.NoError(t, err, tt.name)
				assert.Equal(t, tt.want, got, "every id the log names, sorted")
			}
			assert.Equal(t, ntable.ChangesKey("card"), stream, "the table's change log, unread by prefix")
			assert.Equal(t, tt.wantStarts, starts, "the next read opens exclusive of the last event seen")
			assert.Equal(t, "+", stop, "the read stops at the log's end")
			for _, n := range counts {
				assert.Equal(t, int64(recordIDsPage), n, "a page an exchange")
			}
		})
	}
}

// TestTeardownCoverRedisDeleteKeysChunksOnePipeline pins Redis.DeleteKeys
// (teardown.go:229): nothing is exchanged for no keys, deleteChunk keys go in
// one DEL, a longer list spills into the next, and the count is the keys that
// were there; the refusal is the store not answering the EXEC.
func TestTeardownCoverRedisDeleteKeysChunksOnePipeline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "k" + strconv.Itoa(i)
		}
		return out
	}
	for _, tt := range []struct {
		name     string
		keys     []string
		vals     []int64 // one DEL's reply per queued chunk
		execErr  error
		want     int
		wantErr  error
		wantDels []int // chunk sizes the DELs must name
	}{
		{name: "no keys, no exchange", keys: nil, wantDels: []int{}},
		{name: "one chunk", keys: keys(3), vals: []int64{2}, want: 2, wantDels: []int{3}},
		{name: "a full chunk and a part one", keys: keys(deleteChunk + 10), vals: []int64{deleteChunk, 10},
			want: deleteChunk + 10, wantDels: []int{deleteChunk, 10}},
		{name: "refused: the store does not answer the EXEC", keys: keys(2), execErr: errLost,
			wantErr: errLost, wantDels: []int{2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var dels [][]string
			opened := 0
			pipe := &teardownCoverPipe{
				del: func(_ context.Context, ks ...string) *redis.IntCmd {
					dels = append(dels, ks)
					return redis.NewIntResult(0, nil)
				},
				exec: func(_ context.Context) ([]redis.Cmder, error) {
					if tt.execErr != nil {
						return nil, tt.execErr
					}
					require.Len(t, tt.vals, len(dels), "one reply per queued DEL")
					cmds := make([]redis.Cmder, len(dels))
					for i, v := range tt.vals {
						cmds[i] = redis.NewIntResult(v, nil)
					}
					return cmds, nil
				},
			}
			r := &Redis{C: &teardownCoverClient{pipeline: func() redis.Pipeliner {
				opened++
				return pipe
			}}}
			n, err := r.DeleteKeys(ctx, tt.keys)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr, tt.name)
			} else {
				assert.NoError(t, err, tt.name)
			}
			assert.Equal(t, tt.want, n, "the keys that were there")
			sizes := make([]int, len(dels))
			for i, d := range dels {
				sizes[i] = len(d)
			}
			assert.Equal(t, tt.wantDels, sizes, "the DELs' chunks")
			if len(tt.keys) == 0 {
				assert.Equal(t, 0, opened, "no keys opens no pipeline")
			} else {
				assert.Equal(t, 1, opened, "one pipelined exchange whatever the DELs")
			}
		})
	}
}

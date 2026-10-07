package store

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// The unit cover of the Redis WaitLog (waitlog.go, a reader's finding: the
// one function of the file the unit tier never reached). The client is the
// package's own seam, Redis.C, an interface the test fakes; the replies a row
// names come back from the pipeline, so no sleep, no real time, no network,
// no live store.

// waitlogCoverPipe is the pipeliner one WaitLog exchange holds: the XRead of
// one line and the read of the log's last id, and the replies a row names.
// Every other call panics on the nil embedded interface, as a fake is strict
// like the real pipeliner: it refuses what the real one refuses.
type waitlogCoverPipe struct {
	redis.Pipeliner // nil: a call the cover does not name panics
	xread           *redis.XStreamSliceCmd
	tail            *redis.XMessageSliceCmd
	execErr         error
	args            []*redis.XReadArgs // the XReads the exchange queues
}

func (p *waitlogCoverPipe) XRead(_ context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	p.args = append(p.args, a)
	return p.xread
}

func (p *waitlogCoverPipe) XRevRangeN(context.Context, string, string, string, int64) *redis.XMessageSliceCmd {
	return p.tail
}

func (p *waitlogCoverPipe) Exec(context.Context) ([]redis.Cmder, error) {
	if p.execErr != nil {
		return nil, p.execErr
	}
	return nil, nil
}

// waitlogCoverClient is the client one WaitLog exchange holds: its pipeline.
// coverClient (tick_cover_test.go) names no pipeline, and this cover may not
// touch the file it lives in.
type waitlogCoverClient struct {
	redis.UniversalClient // nil: a call the cover does not name panics
	pipe                  *waitlogCoverPipe
}

func (c *waitlogCoverClient) Pipeline() redis.Pipeliner { return c.pipe }

// TestWaitlogCoverRedisWaitLog pins the Redis WaitLog's one exchange: the
// XRead is queued at the log's key from the cursor ("0-0" when there is none)
// for at least a millisecond, a line after the cursor wakes it, the log's
// last id read after the XRead is the cursor for the next wait, and the
// store's refusal of the exchange is the error, with the caller's cursor.
func TestWaitlogCoverRedisWaitLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "w-"}
	logKey := names.KeyAt(keyLog, 0)
	line := func(id string) []redis.XStream {
		return []redis.XStream{{Stream: logKey, Messages: []redis.XMessage{{ID: id}}}}
	}
	tail := func(id string) *redis.XMessageSliceCmd {
		return redis.NewXMessageSliceCmdResult([]redis.XMessage{{ID: id}}, nil)
	}
	empty := redis.NewXMessageSliceCmdResult(nil, nil)
	for _, tt := range []struct {
		name      string
		after     string
		d         time.Duration
		xread     *redis.XStreamSliceCmd
		tail      *redis.XMessageSliceCmd
		execErr   error
		want      string
		wantWoke  bool
		wantErr   error
		wantStart string
		wantBlock time.Duration
	}{
		{
			name:      "a line after the cursor wakes it and the tail is the cursor",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(line("7-0"), nil),
			tail:      tail("7-0"),
			want:      "7-0",
			wantWoke:  true,
			wantStart: "0-0",
			wantBlock: time.Second,
		},
		{
			name:      "a quiet log keeps the cursor and says it was not woken",
			after:     "5-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(nil, redis.Nil),
			tail:      empty,
			want:      "5-0",
			wantStart: "5-0",
			wantBlock: time.Second,
		},
		{
			name:      "the tail read after the XRead is the cursor even when no line woke it",
			after:     "3-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(nil, redis.Nil),
			tail:      tail("9-0"),
			want:      "9-0",
			wantStart: "3-0",
			wantBlock: time.Second,
		},
		{
			name:      "the least wait is a millisecond",
			xread:     redis.NewXStreamSliceCmdResult(nil, redis.Nil),
			tail:      empty,
			want:      "",
			wantStart: "0-0",
			wantBlock: time.Millisecond,
		},
		{
			name:      "a Nil exchange is an empty log, not an error",
			after:     "2-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(nil, redis.Nil),
			tail:      empty,
			execErr:   redis.Nil,
			want:      "2-0",
			wantStart: "2-0",
			wantBlock: time.Second,
		},
		{
			name:      "an XRead that did not answer is not woken and the tail is still the cursor",
			after:     "6-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(nil, errLost),
			tail:      tail("9-0"),
			want:      "9-0",
			wantStart: "6-0",
			wantBlock: time.Second,
		},
		{
			name:      "a tail the store did not answer keeps the caller's cursor",
			after:     "6-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(nil, redis.Nil),
			tail:      redis.NewXMessageSliceCmdResult(nil, errLost),
			want:      "6-0",
			wantStart: "6-0",
			wantBlock: time.Second,
		},
		{
			name:      "the store refusing the exchange is the error, with the caller's cursor",
			after:     "4-0",
			d:         time.Second,
			xread:     redis.NewXStreamSliceCmdResult(line("7-0"), nil),
			tail:      tail("9-0"),
			execErr:   errLost,
			want:      "4-0",
			wantStart: "4-0",
			wantBlock: time.Second,
			wantErr:   errLost,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &waitlogCoverPipe{xread: tt.xread, tail: tt.tail, execErr: tt.execErr}
			r := &Redis{C: &waitlogCoverClient{pipe: p}, Names: names}
			got, woke, err := r.WaitLog(ctx, tt.after, tt.d)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr, tt.name)
			} else {
				assert.NoError(t, err, tt.name)
			}
			assert.Equal(t, tt.wantWoke, woke, tt.name)
			assert.Equal(t, tt.want, got, tt.name)
			require.Len(t, p.args, 1, tt.name)
			assert.Equal(t, []string{logKey, tt.wantStart}, p.args[0].Streams, "the log is read from the cursor", tt.name)
			assert.Equal(t, int64(1), p.args[0].Count, "one line a wait", tt.name)
			assert.Equal(t, tt.wantBlock, p.args[0].Block, "the wait a row asks for", tt.name)
		})
	}
}

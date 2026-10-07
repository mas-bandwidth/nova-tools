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

// The unit cover of the notes wait (a reader's finding: Redis.WaitNotes of
// tickend.go, which the unit tier never reached). The Redis side is reached
// through the package's own seam, Redis.C, an interface the test fakes: no
// sleep, no real time, no network, no live store.

// tickEndClient is a redis.UniversalClient whose XRead is the fake a row
// names; every other call panics on the nil embedded interface, as a fake is
// strict like the real client: it refuses what the real one refuses.
type tickEndClient struct {
	redis.UniversalClient // nil: a call the cover does not name panics
	xread                 func(context.Context, *redis.XReadArgs) *redis.XStreamSliceCmd
}

func (f *tickEndClient) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	return f.xread(ctx, a)
}

// TestTickendCoverWaitNotes covers Redis.WaitNotes's main path and its
// refusals: the notes stream read from the id after (the first note when
// there is none), one note asked for, the block never under a millisecond, a
// note that came, the store's timeout (redis.Nil), a refusal, and a reply
// that holds no note.
func TestTickendCoverWaitNotes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	note := redis.XStream{Stream: names.Key(keyInbox), Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"note": "x"}}}}
	for _, tt := range []struct {
		name    string
		after   string
		d       time.Duration
		reply   *redis.XStreamSliceCmd
		want    bool
		wantErr error
		wantID  string
		wantBlk time.Duration
	}{
		{name: "a note came", after: "1-0", d: time.Second, reply: redis.NewXStreamSliceCmdResult([]redis.XStream{note}, nil), want: true, wantID: "1-0", wantBlk: time.Second},
		{name: "from the first note", after: "", d: time.Second, reply: redis.NewXStreamSliceCmdResult([]redis.XStream{note}, nil), want: true, wantID: "0-0", wantBlk: time.Second},
		{name: "the store's timeout is no note", after: "1-0", d: time.Second, reply: redis.NewXStreamSliceCmdResult(nil, redis.Nil), wantID: "1-0", wantBlk: time.Second},
		{name: "the store refuses", after: "1-0", d: time.Second, reply: redis.NewXStreamSliceCmdResult(nil, errLost), wantErr: errLost, wantID: "1-0", wantBlk: time.Second},
		{name: "a reply with no stream", after: "1-0", d: time.Second, reply: redis.NewXStreamSliceCmdResult(nil, nil), wantID: "1-0", wantBlk: time.Second},
		{name: "a stream with no note", after: "1-0", d: time.Second, reply: redis.NewXStreamSliceCmdResult([]redis.XStream{{Stream: names.Key(keyInbox)}}, nil), wantID: "1-0", wantBlk: time.Second},
		{name: "a wait under the floor blocks a millisecond", after: "1-0", d: 0, reply: redis.NewXStreamSliceCmdResult(nil, redis.Nil), wantID: "1-0", wantBlk: time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got *redis.XReadArgs
			r := &Redis{C: &tickEndClient{xread: func(_ context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
				got = a
				return tt.reply
			}}, Names: names}
			ok, err := r.WaitNotes(ctx, tt.after, tt.d)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err, tt.name)
				assert.Equal(t, tt.want, ok, tt.name)
			}
			require.NotNil(t, got, "the store was read")
			assert.Equal(t, []string{names.Key(keyInbox), tt.wantID}, got.Streams, "the notes stream from the id after")
			assert.Equal(t, int64(1), got.Count, "one note")
			assert.Equal(t, tt.wantBlk, got.Block, "the wait's block")
		})
	}
}

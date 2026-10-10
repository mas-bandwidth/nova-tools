package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// The unit cover of the machine records and the machine clock (a reader's
// finding: eight functions of tick.go the unit tier never reached). The Redis
// side is reached through the package's own seam, Redis.C, an interface the
// test fakes; the store side through its Mem backend at one fixed clock. No
// sleep, no real time, no network, no live store.

// coverClient is a redis.UniversalClient whose calls a row names are fakes;
// every other call panics on the nil embedded interface, as a fake is strict
// like the real client: it refuses what the real one refuses.
type coverClient struct {
	redis.UniversalClient // nil: a call the cover does not name panics
	get                   func(context.Context, string) *redis.StringCmd
	set                   func(context.Context, string, any, time.Duration) *redis.StatusCmd
	mget                  func(context.Context, ...string) *redis.SliceCmd
	fcall                 func(context.Context, string, []string, ...any) *redis.Cmd
	tx                    func(context.Context, func(redis.Pipeliner) error) ([]redis.Cmder, error)
}

func (f *coverClient) Get(ctx context.Context, key string) *redis.StringCmd {
	return f.get(ctx, key)
}

func (f *coverClient) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	return f.set(ctx, key, value, expiration)
}

func (f *coverClient) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	return f.mget(ctx, keys...)
}

func (f *coverClient) FCall(ctx context.Context, function string, keys []string, args ...any) *redis.Cmd {
	return f.fcall(ctx, function, keys, args...)
}

func (f *coverClient) TxPipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return f.tx(ctx, fn)
}

// coverPipe is the pipeliner one MULTI/EXEC holds: the record's SET and the
// queued view state's FCall, never run.
type coverPipe struct {
	redis.Pipeliner // nil: a call the cover does not name panics
	set             func(context.Context, string, any, time.Duration) *redis.StatusCmd
	fcall           func(context.Context, string, []string, ...any) *redis.Cmd
}

func (p *coverPipe) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	return p.set(ctx, key, value, expiration)
}

func (p *coverPipe) FCall(ctx context.Context, function string, keys []string, args ...any) *redis.Cmd {
	return p.fcall(ctx, function, keys, args...)
}

// coverStore is a Mem-backed store at the clock the row holds.
func coverStore(now func() time.Time) *Store {
	return &Store{B: NewMem(), Names: sprint.Names{Prefix: "t-"}, Actor: "tester", Now: now}
}

// coverSetMachine writes the state record the rows read back.
func coverSetMachine(t *testing.T, st *Store, m Machine) {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, st.B.(*Mem).SetKey(context.Background(), keyMachine, string(b)))
}

// coverEpochLost fails only the epoch read; the machine's records still read
// (through st.B), so the rows reach the epoch-read error alone.
type coverEpochLost struct {
	Backend // nil: a call the cover does not name panics
}

func (coverEpochLost) Epoch(context.Context) (EpochState, error) { return EpochState{}, errLost }

func TestTickCoverRedisGetKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	for _, tt := range []struct {
		name    string
		reply   *redis.StringCmd
		want    string
		wantOK  bool
		wantErr error
	}{
		{name: "present", reply: redis.NewStringResult("v", nil), want: "v", wantOK: true},
		{name: "missing", reply: redis.NewStringResult("", redis.Nil)},
		{name: "refused", reply: redis.NewStringResult("", errLost), wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ""
			r := &Redis{C: &coverClient{get: func(_ context.Context, key string) *redis.StringCmd {
				got = key
				return tt.reply
			}}, Names: names}
			v, ok, err := r.GetKey(ctx, keyMachine)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err, tt.name)
				assert.Equal(t, tt.wantOK, ok, tt.name)
				assert.Equal(t, tt.want, v, tt.name)
			}
			assert.Equal(t, names.Key(keyMachine), got, "the key the record is read at")
		})
	}
}

func TestTickCoverRedisSetKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	for _, tt := range []struct {
		name    string
		reply   *redis.StatusCmd
		wantErr error
	}{
		{name: "written", reply: redis.NewStatusResult("OK", nil)},
		{name: "refused", reply: redis.NewStatusResult("", errLost), wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, value, expiry := "", "", time.Hour
			r := &Redis{C: &coverClient{set: func(_ context.Context, k string, v any, d time.Duration) *redis.StatusCmd {
				key, value, expiry = k, v.(string), d
				return tt.reply
			}}, Names: names}
			err := r.SetKey(ctx, keyMachine, "v")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err, tt.name)
			}
			assert.Equal(t, names.Key(keyMachine), key, "the key the record is written at")
			assert.Equal(t, "v", value)
			assert.Zero(t, expiry, "the machine record does not expire")
		})
	}
}

func TestTickCoverRedisGetKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	for _, tt := range []struct {
		name    string
		reply   *redis.SliceCmd
		want    []string
		wantOK  []bool
		wantErr error
	}{
		{name: "present", reply: redis.NewSliceResult([]any{"v1", "v2"}, nil), want: []string{"v1", "v2"}, wantOK: []bool{true, true}},
		{name: "one missing", reply: redis.NewSliceResult([]any{nil, "v2"}, nil), want: []string{"", "v2"}, wantOK: []bool{false, true}},
		{name: "refused", reply: redis.NewSliceResult(nil, errLost), wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var keys []string
			r := &Redis{C: &coverClient{mget: func(_ context.Context, ks ...string) *redis.SliceCmd {
				keys = ks
				return tt.reply
			}}, Names: names}
			vals, oks, err := r.GetKeys(ctx, []string{"a", "b"})
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err, tt.name)
				assert.Equal(t, tt.want, vals, tt.name)
				assert.Equal(t, tt.wantOK, oks, tt.name)
			}
			assert.Equal(t, []string{names.Key("a"), names.Key("b")}, keys, "the keys one round trip reads")
		})
	}
}

func TestTickCoverRedisSetKeyShowing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	view, state := "coord", "RUNNING"
	for _, tt := range []struct {
		name        string
		reply       *redis.Cmd
		txErr       error
		wantErr     error
		wantRefused bool
	}{
		{name: "written", reply: redis.NewCmdResult([]any{"OK"}, nil)},
		{name: "view gone is no error", reply: redis.NewCmdResult([]any{"REFUSED", "NOVIEW"}, nil)},
		{name: "store refuses the transaction", reply: redis.NewCmdResult([]any{"OK"}, nil), txErr: errLost, wantErr: errLost},
		{name: "state refused by the store", reply: redis.NewCmdResult([]any{"REFUSED", "ARGS", "the state"}, nil), wantRefused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, value := "", ""
			var fcall string
			var fkeys []string
			var fargs []any
			r := &Redis{C: &coverClient{tx: func(_ context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
				p := &coverPipe{
					set: func(_ context.Context, k string, v any, _ time.Duration) *redis.StatusCmd {
						key, value = k, v.(string)
						return redis.NewStatusResult("OK", nil)
					},
					fcall: func(_ context.Context, function string, keys []string, args ...any) *redis.Cmd {
						fcall, fkeys, fargs = function, keys, args
						return tt.reply
					},
				}
				if err := fn(p); err != nil {
					return nil, err
				}
				return nil, tt.txErr
			}}, Names: names}
			err := r.SetKeyShowing(ctx, keyMachine, "v", view, state)
			switch {
			case tt.wantErr != nil:
				assert.ErrorIs(t, err, tt.wantErr, tt.name)
				return
			case tt.wantRefused:
				assert.Error(t, err, tt.name)
				assert.NotErrorIs(t, err, ntable.ErrNoView, tt.name)
				assert.Equal(t, "ns_view_state", fcall, "the state was queued before the refusal")
				return
			}
			assert.NoError(t, err, tt.name)
			assert.Equal(t, names.Key(keyMachine), key, "the record and the state are one MULTI/EXEC")
			assert.Equal(t, "v", value)
			assert.Equal(t, "ns_view_state", fcall, "the view state is queued in the same MULTI/EXEC")
			assert.Equal(t, []string{"view:" + view}, fkeys)
			assert.Equal(t, []any{view, state}, fargs)
		})
	}
}

func TestTickCoverRedisShowState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	names := sprint.Names{Prefix: "r-"}
	for _, tt := range []struct {
		name     string
		state    string
		reply    *redis.Cmd
		noStore  bool
		wantText string
	}{
		{name: "written", state: "RUNNING", reply: redis.NewCmdResult([]any{"OK"}, nil)},
		{name: "view gone is no error", state: "RUNNING", reply: redis.NewCmdResult([]any{"REFUSED", "NOVIEW"}, nil)},
		{name: "state refused before the store", state: "RUNNING\nRUNNING", noStore: true, wantText: "one line"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			called := ""
			var fkeys []string
			var fargs []any
			r := &Redis{C: &coverClient{fcall: func(_ context.Context, function string, keys []string, args ...any) *redis.Cmd {
				called, fkeys, fargs = function, keys, args
				return tt.reply
			}}, Names: names}
			err := r.ShowState(ctx, "coord", tt.state)
			if tt.noStore {
				assert.ErrorContains(t, err, tt.wantText, "an unusable state is refused before the store is asked")
				assert.Empty(t, called, "the refused state never reaches the store")
				return
			}
			assert.NoError(t, err, tt.name)
			assert.Equal(t, "ns_view_state", called)
			assert.Equal(t, []string{"view:coord"}, fkeys)
			assert.Equal(t, []any{"coord", tt.state}, fargs)
		})
	}
}

func TestTickCoverViewShown(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		err     error
		wantErr error
	}{
		{name: "written"},
		{name: "a view that is not there is no error", err: fmt.Errorf("view coord: %w", ntable.ErrNoView)},
		{name: "the store's error passes", err: errLost, wantErr: errLost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := viewShown(tt.err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err, tt.name)
			}
		})
	}
}

func TestTickCoverStoreSinceFirstStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) // the epoch's span opened
	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) // the machine's first start
	running := Machine{State: Running, Since: t1, Who: sprint.MachineActor, Spans: []Span{{From: t0, To: t1}}}
	for _, tt := range []struct {
		name      string
		rec       Machine
		now       time.Time
		unread    bool
		epochLost bool
		want      time.Duration
		ok        bool
	}{
		{name: "running two hours", rec: running, now: t1.Add(2 * time.Hour), want: 2 * time.Hour, ok: true},
		{name: "never started this epoch", rec: Machine{State: Stopped, Since: t0, Spans: []Span{{From: t0}}}, now: t1.Add(2 * time.Hour)},
		{name: "clock before the first start", rec: running, now: t0},
		{name: "records unread", rec: running, now: t1.Add(2 * time.Hour), unread: true},
		{name: "epoch unread", rec: running, now: t1.Add(2 * time.Hour), epochLost: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := coverStore(func() time.Time { return tt.now })
			switch {
			case tt.unread:
				st.B.(*Mem).Fail = func(string) error { return errors.New("lost") }
			case tt.epochLost:
				st.root = coverEpochLost{}
			default:
				coverSetMachine(t, st, tt.rec)
			}
			d, ok := st.SinceFirstStart(ctx)
			assert.Equal(t, tt.ok, ok, tt.name)
			assert.Equal(t, tt.want, d, tt.name)
		})
	}
}

func TestTickCoverStoreLandingRate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) // the epoch's span opened
	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) // the machine's first start
	now := t1.Add(2 * time.Hour)
	running := Machine{State: Running, Since: t1, Who: sprint.MachineActor, Spans: []Span{{From: t0, To: t1}}}
	landed := make([]time.Time, 6)
	for i := range landed { // six landings in the window's last hour
		landed[i] = t1.Add(time.Hour + time.Duration(i+1)*time.Minute)
	}
	for _, tt := range []struct {
		name      string
		rec       Machine
		landed    []time.Time
		total     int64
		unread    bool
		epochLost bool
		want      float64
	}{
		{name: "six landings in the hour", rec: running, landed: landed, total: 6, want: 6},
		{name: "one landing, the whole sprint's average", rec: running, landed: landed[:1], total: 10, want: 5},
		{name: "not started", rec: Machine{State: Stopped, Since: t0}, landed: landed, total: 6},
		{name: "records unread", rec: running, landed: landed, total: 6, unread: true},
		{name: "epoch unread", rec: running, landed: landed, total: 6, epochLost: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := coverStore(func() time.Time { return now })
			switch {
			case tt.unread:
				st.B.(*Mem).Fail = func(string) error { return errors.New("lost") }
			case tt.epochLost:
				st.root = coverEpochLost{}
			default:
				coverSetMachine(t, st, tt.rec)
			}
			assert.Equal(t, tt.want, st.LandingRate(ctx, tt.landed, tt.total), tt.name)
		})
	}
}

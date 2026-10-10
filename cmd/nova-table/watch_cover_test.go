package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watch_fake is a redis.Cmdable and redis.Pipeliner pair that never opens a
// store: FCallRO on the client hands back a preset *redis.Cmd (the ns_view_get
// reply the watcher reads), and Pipeline() returns a fakePipeline whose FCallRO
// hands back a preset *redis.Cmd (the ns_table_read snapshot) and whose Exec
// reports success or a preset error. The commands of the embedded nil
// interfaces are never reached on this path, so the embedded nil is safe.

// replyError is a value implementing the redis.Error interface so isReplyError
// and the pipeline's isReplyError branch can be driven without a store.
type replyError struct{ msg string }

func (e replyError) Error() string { return e.msg }
func (replyError) RedisError()     {}

// demoSnapshot is the raw array ns_table_read (ntable.FnRead) returns for a
// one-row "demo" table with count columns ready,working and a "build" row with
// ready=3, working=0: the same shape the watcher decodes from a live store,
// handed back here by the fake pipeline.
func demoSnapshot() []any {
	return []any{
		"TABLE",
		[]any{"order", "ready,working", "col:ready", "count:sum:0:", "col:working", "count:sum:0:"},
		[]any{
			[]any{"build", []any{}, []any{
				[]any{"OK", int64(3), []any{}},
				[]any{"OK", int64(0), []any{}},
			}},
		},
	}
}

// demoViewReply is the raw ns_view_get reply for a view over the single table
// "demo", titled "Work View", with no summary state so the frame omits a
// summary line.
func demoViewReply() []any {
	return []any{"OK", []any{"title", "Work View", "tables", "demo"}}
}

type fakePipeline struct {
	redis.Pipeliner
	snapshot *redis.Cmd
	execErr  error
}

func (p *fakePipeline) Pipeline() redis.Pipeliner { return p }

func (p *fakePipeline) Exec(_ context.Context) ([]redis.Cmder, error) {
	if p.execErr != nil {
		return nil, p.execErr
	}
	return []redis.Cmder{p.snapshot}, nil
}

func (p *fakePipeline) FCallRO(_ context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return p.snapshot
}

type fakeCmdable struct {
	redis.Cmdable
	viewReply []any
	viewErr   error
	pipe      *fakePipeline
}

func (f *fakeCmdable) Pipeline() redis.Pipeliner { return f.pipe }

func (f *fakeCmdable) FCallRO(_ context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return redis.NewCmdResult(f.viewReply, f.viewErr)
}

// newWatchClient builds a fakeCmdable whose store answers the "myview" view
// (viewReader's seam, ntable.ViewGet) and the "demo" table snapshot
// (tableSnapshots' seam). execErr and viewErr force the refusal paths.
func newWatchClient(snapshot []any, execErr, viewErr error) *fakeCmdable {
	return &fakeCmdable{
		viewReply: demoViewReply(),
		viewErr:   viewErr,
		pipe: &fakePipeline{
			snapshot: redis.NewCmdResult(snapshot, nil),
			execErr:  execErr,
		},
	}
}

// TestWatchCoverIsReplyError pins isReplyError: a Redis error is a reply error,
// an ordinary error is not.
func TestWatchCoverIsReplyError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"main path: a Redis error is a reply error", replyError{"LOADING Redis is loading"}, true},
		{"refusal: an ordinary error is not a reply error", errors.New("connection refused"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isReplyError(tc.err))
		})
	}
}

// TestWatchCoverTableSnapshots pins tableSnapshots: a pipeline that answers a
// snapshot decodes it, and a plain pipeline error is returned (the Redis-Nil
// and reply-error branches are skipped).
func TestWatchCoverTableSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	want := demoTable(3)
	pipelineErr := errors.New("pipeline broken")
	for _, tc := range []struct {
		name    string
		client  *fakeCmdable
		want    ntable.Table
		wantErr error
	}{
		{
			name:   "main path: a snapshot decodes to the table",
			client: newWatchClient(demoSnapshot(), nil, nil),
			want:   want,
		},
		{
			name:    "refusal: a plain pipeline error is returned",
			client:  newWatchClient(nil, pipelineErr, nil),
			wantErr: pipelineErr,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tables, err := tableSnapshots(tc.client, []string{"demo"})(ctx)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, tables, 1)
			assert.Equal(t, tc.want.Name, tables[0].Name)
			wantRender := ntable.Render(tc.want, ntable.RenderOpts{Title: tc.want.Name})
			gotRender := ntable.Render(tables[0], ntable.RenderOpts{Title: tables[0].Name})
			assert.Equal(t, wantRender, gotRender)
		})
	}
}

// TestWatchCoverTablesReader pins tablesReader: a readable snapshot renders the
// table through renderAll, and a snapshot failure refuses the read.
func TestWatchCoverTablesReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	want := demoTable(3)
	wantRender := ntable.Render(want, ntable.RenderOpts{Title: want.Name})
	pipelineErr := errors.New("pipeline broken")
	for _, tc := range []struct {
		name    string
		client  *fakeCmdable
		want    string
		wantErr error
	}{
		{
			name:   "main path: the snapshot renders",
			client: newWatchClient(demoSnapshot(), nil, nil),
			want:   wantRender,
		},
		{
			name:    "refusal: a snapshot failure returns the error",
			client:  newWatchClient(nil, pipelineErr, nil),
			wantErr: pipelineErr,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			read := tablesReader(tc.client, []string{"demo"}, "", ntable.RenderOpts{}, false)
			got, err := read(ctx)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestWatchCoverViewReader pins viewReader: a stored view renders its tables
// through viewReaderWith, and a missing view refuses the read.
func TestWatchCoverViewReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	want := demoTable(3)
	wantRender := ntable.Render(want, ntable.RenderOpts{Title: want.Name})
	viewErr := errors.New("no such view")
	for _, tc := range []struct {
		name    string
		client  *fakeCmdable
		wantErr error
	}{
		{
			name:   "main path: the view renders its table",
			client: newWatchClient(demoSnapshot(), nil, nil),
		},
		{
			name:    "refusal: a missing view returns the error",
			client:  newWatchClient(nil, nil, viewErr),
			wantErr: viewErr,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			read := viewReader(tc.client, "myview", ntable.RenderOpts{}, false)
			got, err := read(ctx)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, got, "Work View")
			assert.Contains(t, got, wantRender)
		})
	}
}

// guard that the fake satisfies the interfaces the seams take, at compile time.
var (
	_ redis.Cmdable   = (*fakeCmdable)(nil)
	_ redis.Pipeliner = (*fakePipeline)(nil)
)

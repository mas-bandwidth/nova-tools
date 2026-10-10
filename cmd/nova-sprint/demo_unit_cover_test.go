package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// demoCoverTime is the fixed clock every cover app is given, so a state file's
// started stamp is the same on every run and no test reads the wall clock.
var demoCoverTime = time.Date(2026, 10, 4, 23, 36, 0, 0, time.UTC)

// coverApp is an app with the fixed clock every cover test uses.
func coverApp() *app {
	a := newApp(func(string) string { return "" })
	a.now = func() time.Time { return demoCoverTime }
	return a
}

// coverPart writes one part under a fresh test dir. With no sum file or
// SHA256SUMS beside it the load reads the checksum as unchecked, so the bytes
// are never read and only the file's existence matters.
func coverPart(t *testing.T, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sprint-2026-10-04.redis.txt.xz.part-aa")
	require.NoError(t, os.WriteFile(p, []byte(data), 0o600))
	return p
}

// coverXZ writes an executable file for --xz: exec.LookPath checks the
// executable bit only, so it is found without xz ever running.
func coverXZ(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xz")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700))
	return p
}

// coverClient is a redis client on an unused loopback address that is never
// dialled: every refusal that gets one returns before its pipeline runs.
func coverClient(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: time.Second, MaxRetries: -1})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// demoDirArg shows the --dir flag back only when it was given.
func TestSprintDemoCoverDirArg(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", demoDirArg(""))
	assert.Equal(t, " --dir /d", demoDirArg("/d"))
}

// demoUndo of a stubbed-dead demo removes the redis-* directory and the state
// file under the root a failed load made, and says so for the failure's line.
func TestSprintDemoCoverUndoRemovesTheDemo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "redis-cover")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:6379", PID: 4242, Dir: dir}))
	stubDemo(t, root, nil, func(int) bool { return false }, func(demoState) error {
		assert.Fail(t, "a dead demo's Redis was signalled")
		return nil
	})
	got := demoUndo(root, demoState{Addr: "127.0.0.1:6379", PID: 4242, Dir: dir})
	assert.Equal(t, "; the demo's Redis was stopped and its directory removed", got)
	assert.NoDirExists(t, dir)
	assert.NoFileExists(t, filepath.Join(root, demoStateFile))
}

// demoUndo of a directory outside the root stops and removes nothing: the
// directory is not one demo load makes, so the line says so and names the
// remedy, and the directory stays.
func TestSprintDemoCoverUndoKeepsAnOutsideDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "redis-outside")
	require.NoError(t, os.MkdirAll(outside, 0o700))
	stubDemo(t, root, nil, func(int) bool { return false }, nil)
	got := demoUndo(root, demoState{Addr: "127.0.0.1:6379", PID: 4242, Dir: outside})
	assert.Contains(t, got, "the demo was not cleaned up")
	assert.Contains(t, got, "run: nova-sprint demo stop")
	assert.DirExists(t, outside)
}

// demo load's refusals: one part file, a fake --xz and the root are given to
// every case, and each case is refused before its own step, so a start that
// follows the refusal is never reached. The two cases that reach the stub
// (a start that fails, an address that is not loopback) undo what they made.
func TestSprintDemoCoverLoadRefusals(t *testing.T) {
	t.Parallel()
	type loadCase struct {
		name  string
		setup func(t *testing.T, root string) []string
		start func(ctx context.Context) (demoServer, error)
		code  int
		want  []string
		undo  bool
	}
	for _, tc := range []loadCase{
		{
			name: "a start that fails is undone",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"demo", "load", "--dir", root, "--xz", coverXZ(t), coverPart(t, "data")}
			},
			start: func(context.Context) (demoServer, error) {
				return demoServer{}, errors.New("the demo's redis-server did not start")
			},
			code: 1,
			want: []string{"the demo's redis-server did not start", "; the demo's Redis was stopped and its directory removed"},
			undo: true,
		},
		{
			name: "an address that is not loopback is undone",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"demo", "load", "--dir", root, "--xz", coverXZ(t), coverPart(t, "data")}
			},
			start: func(context.Context) (demoServer, error) {
				return demoServer{Addr: "demo.invalid:6379", PID: 0}, nil
			},
			code: 1,
			want: []string{"not the IPv4 loopback", "; the demo's Redis was stopped and its directory removed"},
			undo: true,
		},
		{
			name: "a state file already there refuses",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				require.NoError(t, os.MkdirAll(root, 0o700))
				require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:6379", PID: 4242, Dir: filepath.Join(root, "redis-live")}))
				return []string{"demo", "load", "--dir", root, "--xz", coverXZ(t), coverPart(t, "data")}
			},
			code: 1,
			want: []string{"a demo is up at", "demo stop --dir {root}"},
		},
		{
			name: "a state file that is not JSON refuses",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				require.NoError(t, os.MkdirAll(root, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(root, demoStateFile), []byte("{not json"), 0o600))
				return []string{"demo", "load", "--dir", root, "--xz", coverXZ(t), coverPart(t, "data")}
			},
			code: 1,
			want: []string{"cannot be read", "demo stop --dir {root}"},
		},
		{
			name: "no part refuses",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"demo", "load", "--dir", root}
			},
			code: 2,
			want: []string{"wants the backup's parts"},
		},
		{
			name: "a part that is a directory refuses",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"demo", "load", "--dir", root, "--xz", coverXZ(t), t.TempDir()}
			},
			code: 1,
			want: []string{"is not a file"},
		},
		{
			name: "a missing xz refuses",
			setup: func(t *testing.T, root string) []string {
				t.Helper()
				return []string{"demo", "load", "--dir", root, "--xz", filepath.Join(t.TempDir(), "no-xz"), coverPart(t, "data")}
			},
			code: 1,
			want: []string{"to decompress the backup"},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "demo")
			args := tc.setup(t, root)
			calls := 0
			stubDemo(t, root, func(ctx context.Context, _, _ string) (demoServer, error) {
				calls++
				if tc.start == nil {
					return demoServer{}, errors.New("the start stub was reached before the refusal")
				}
				return tc.start(ctx)
			}, nil, nil)
			a := coverApp()
			var out, errb bytes.Buffer
			code := a.run(args, &out, &errb)
			assert.Equal(t, tc.code, code, "%s", errb.String())
			for _, w := range tc.want {
				assert.Contains(t, errb.String(), strings.ReplaceAll(w, "{root}", root))
			}
			if tc.start == nil {
				assert.Zero(t, calls, "the start stub ran for a refusal that comes before it")
			} else {
				assert.Equal(t, 1, calls, "the start stub did not run exactly once")
			}
			assert.NotContains(t, out.String(), "DEMO LOADED", "a refused load printed a result")
			if tc.undo {
				assert.True(t, strings.HasSuffix(strings.TrimRight(errb.String(), "\n"), "; the demo's Redis was stopped and its directory removed"), "%s", errb.String())
				ents, err := os.ReadDir(root)
				require.NoError(t, err)
				for _, e := range ents {
					assert.False(t, strings.HasPrefix(e.Name(), "redis-"), "the undo left %s under the root", e.Name())
					assert.NotEqual(t, demoStateFile, e.Name(), "the undo left the state file")
				}
			}
		})
	}
}

// demoReplay of a missing part returns its error from openParts, before the xz
// is ever started and while the client is never dialled.
func TestSprintDemoCoverReplayMissingPart(t *testing.T) {
	t.Parallel()
	c := coverClient(t)
	missing := filepath.Join(t.TempDir(), "sprint-2026-10-04.redis.txt.xz.part-aa")
	n, err := demoReplay(context.Background(), c, "xz", []string{missing})
	assert.Zero(t, n)
	assert.Error(t, err)
}

// demoReplayLines reads a dump a line at a time: comments and blanks count for
// nothing, and a line that is not a RESTORE, has too few words, or cannot be
// split is refused naming its line, before any pipeline runs.
func TestSprintDemoCoverReplayLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		n    int
		want string
	}{
		{
			name: "comments and blanks count for nothing",
			in:   "# a hand backup header\n\n   \n# another comment\n",
			n:    0,
		},
		{
			name: "a line that is not a RESTORE is refused naming its line",
			in:   "SET k v\n",
			n:    0,
			want: "line 1 of the dump is not a RESTORE",
		},
		{
			name: "a RESTORE with too few words is refused naming its line",
			in:   "RESTORE k 0\n",
			n:    0,
			want: "line 1 of the dump is not a RESTORE",
		},
		{
			name: "an unbalanced quote cannot be read",
			in:   `RESTORE k 0 "open` + "\n",
			n:    0,
			want: "cannot be read",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := coverClient(t)
			n, err := demoReplayLines(context.Background(), c, strings.NewReader(tc.in))
			assert.Equal(t, tc.n, n)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

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

// coverDemoNow is the one clock every app here is given: no test reads the
// machine's.
var coverDemoNow = time.Date(2026, 10, 4, 23, 36, 0, 0, time.UTC)

// coverDemoApp is an app whose clock is fixed: the demo's Started is stable
// and no verb reaches the wall.
func coverDemoApp(t *testing.T) *app {
	t.Helper()
	a := newApp(func(string) string { return "" })
	a.now = func() time.Time { return coverDemoNow }
	return a
}

// coverDemoXZ is an executable file named xz: exec.LookPath checks the
// executable bit alone, so a demo load finds it and never runs it.
func coverDemoXZ(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xz")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	return p
}

// coverDemoClean asserts a root holds no demo state: no redis-* directory the
// demo made and no demo.json; an absent root is clean.
func coverDemoClean(t *testing.T, root string) {
	t.Helper()
	ents, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)
	for _, e := range ents {
		assert.False(t, strings.HasPrefix(e.Name(), "redis-"), "the load left the directory %s", e.Name())
		assert.NotEqual(t, demoStateFile, e.Name(), "the load left its state file")
	}
}

// demoDirArg says nothing with no --dir and names it with one.
func TestSprintDemoCoverDirArg(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"none", "", ""},
		{"a directory", "/d", " --dir /d"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, demoDirArg(tc.in))
		})
	}
}

// demoUndo removes a directory demo load made directly under the root, with
// its state file; a directory that is not one it makes is kept and the undo
// says so.
func TestSprintDemoCoverUndo(t *testing.T) {
	t.Parallel()
	t.Run("under the root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "redis-abc")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, writeDemoState(root, demoState{Dir: dir}))
		stubDemo(t, root, nil, func(int) bool { return false }, func(demoState) error { return nil })

		got := demoUndo(root, demoState{Dir: dir})
		assert.Equal(t, "; the demo's Redis was stopped and its directory removed", got)
		assert.NoDirExists(t, dir)
		assert.NoFileExists(t, filepath.Join(root, demoStateFile))
	})
	t.Run("outside the root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(t.TempDir(), "redis-abc")
		require.NoError(t, os.MkdirAll(dir, 0o700))

		got := demoUndo(root, demoState{Dir: dir})
		assert.Contains(t, got, "the demo was not cleaned up")
		assert.Contains(t, got, "run: nova-sprint demo stop")
		assert.DirExists(t, dir)
	})
}

// demo load refuses before it starts anything when it cannot read its inputs,
// and cleans up what a start it did make left when the start fails or the
// server listens off the IPv4 loopback.
func TestSprintDemoCoverLoad(t *testing.T) {
	t.Parallel()
	errStart := errors.New("redis-server could not start here")
	type loadCase struct {
		name string
		// start is the stub a case that reaches the start gets; nil means the
		// start must never be called (the refusal comes first).
		start func(context.Context, string, string) (demoServer, error)
		// prep makes what the case needs beyond a part and a fake xz.
		prep func(t *testing.T, root, part, xz string)
		// args is the load's words after `demo load`.
		args func(root, part, xz string) []string
		want int
		// holds are the lines the refusal must carry; dirArg says it must name
		// the root in its remedy; ends is the undo text it must end with.
		holds  []string
		dirArg bool
		ends   string
		clean  bool
	}
	cases := []loadCase{
		{
			name:  "a start that fails",
			start: func(context.Context, string, string) (demoServer, error) { return demoServer{}, errStart },
			args:  func(_, part, xz string) []string { return []string{"--xz", xz, part} },
			want:  1,
			holds: []string{"demo load FAILED", "redis-server could not start here"},
			ends:  "the demo's Redis was stopped and its directory removed",
			clean: true,
		},
		{
			name: "a server off the IPv4 loopback",
			start: func(context.Context, string, string) (demoServer, error) {
				// a reserved name, not a real host: any address but 127.0.0.1
				// takes the refusal branch (docs/STANDARD.md, no real hosts)
				return demoServer{Addr: "demo.invalid:6379", PID: 0}, nil
			},
			args:  func(_, part, xz string) []string { return []string{"--xz", xz, part} },
			want:  1,
			holds: []string{"not the IPv4 loopback"},
			ends:  "the demo's Redis was stopped and its directory removed",
			clean: true,
		},
		{
			name: "a demo already up",
			prep: func(t *testing.T, root, _, _ string) {
				require.NoError(t, os.MkdirAll(root, 0o700))
				require.NoError(t, writeDemoState(root, demoState{Addr: "127.0.0.1:6379", PID: 42, Dir: filepath.Join(root, "redis-up")}))
			},
			args:   func(_, part, xz string) []string { return []string{"--xz", xz, part} },
			want:   1,
			holds:  []string{"a demo is up at"},
			dirArg: true,
		},
		{
			name: "a state file that is not JSON",
			prep: func(t *testing.T, root, _, _ string) {
				require.NoError(t, os.MkdirAll(root, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(root, demoStateFile), []byte("not json"), 0o600))
			},
			args:  func(_, part, xz string) []string { return []string{"--xz", xz, part} },
			want:  1,
			holds: []string{"cannot be read"},
		},
		{
			name:  "no part",
			args:  func(_, _, xz string) []string { return []string{"--xz", xz} },
			want:  2,
			holds: []string{"wants the backup's parts"},
		},
		{
			name: "a part that is a directory",
			prep: func(t *testing.T, _, part, _ string) {
				require.NoError(t, os.MkdirAll(part+".dir", 0o700))
			},
			args:  func(_, part, xz string) []string { return []string{"--xz", xz, part + ".dir"} },
			want:  1,
			holds: []string{"is not a file"},
		},
		{
			name: "a missing xz",
			args: func(_, part, xz string) []string {
				return []string{"--xz", filepath.Join(filepath.Dir(xz), "no-such-xz"), part}
			},
			want:  1,
			holds: []string{"to decompress the backup"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "demo")
			dir := t.TempDir()
			part := filepath.Join(dir, "s.redis.txt.xz")
			require.NoError(t, os.WriteFile(part, []byte("dump"), 0o600))
			xz := coverDemoXZ(t)
			if tc.prep != nil {
				tc.prep(t, root, part, xz)
			}
			calls := 0
			start := tc.start
			if start == nil {
				start = func(context.Context, string, string) (demoServer, error) {
					calls++
					return demoServer{}, errStart
				}
			} else {
				inner := start
				start = func(ctx context.Context, bin, d string) (demoServer, error) {
					calls++
					return inner(ctx, bin, d)
				}
			}
			stubDemo(t, root, start, func(int) bool { return false }, func(demoState) error { return nil })

			a := coverDemoApp(t)
			var out, errb bytes.Buffer
			code := a.run(append([]string{"demo", "load", "--dir", root}, tc.args(root, part, xz)...), &out, &errb)
			assert.Equal(t, tc.want, code, errb.String())
			for _, h := range tc.holds {
				assert.Contains(t, errb.String(), h, errb.String())
			}
			if tc.dirArg {
				assert.Contains(t, errb.String(), "demo stop --dir "+root, errb.String())
			}
			if tc.ends != "" {
				assert.True(t, strings.HasSuffix(strings.TrimSpace(errb.String()), tc.ends), errb.String())
			}
			if tc.start == nil {
				assert.Zero(t, calls, "the start stub ran before the refusal")
			}
			if tc.clean {
				coverDemoClean(t, root)
			}
		})
	}
}

// demoReplay refuses a missing part before it starts xz: the error is the
// open's, and a bogus xz path is never run.
func TestSprintDemoCoverReplay(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = c.Close() })
	missing := filepath.Join(t.TempDir(), "gone.redis.txt.xz.part-aa")
	_, err := demoReplay(context.Background(), c, filepath.Join(t.TempDir(), "no-such-xz"), []string{missing})
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist, "the missing part is not what was refused: %v", err)
}

// demoReplayLines sends only RESTORE lines: comments and blanks are skipped,
// any other command, too few words and an unreadable line are refused, and
// none of them reaches the pipeline.
func TestSprintDemoCoverReplayLines(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = c.Close() })
	for _, tc := range []struct {
		name  string
		body  string
		want  int
		holds string
	}{
		{"comments and blank lines", "# a comment\n\n   \n#another\n", 0, ""},
		{"a SET line", "SET k v\n", 0, "is not a RESTORE"},
		{"a RESTORE with too few words", "RESTORE k 0\n", 0, "is not a RESTORE"},
		{"an unbalanced quote", "RESTORE \"k 0 x\n", 0, "cannot be read"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, err := demoReplayLines(context.Background(), c, strings.NewReader(tc.body))
			assert.Equal(t, tc.want, n)
			if tc.holds == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.holds)
		})
	}
}

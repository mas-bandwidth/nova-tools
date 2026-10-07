package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// followWorld is the rig's world with the follow's edges faked: the fetch stages a path, the
// staged binary answers a version line, the swap and the restart are recorded.
type followWorld struct {
	mu       sync.Mutex
	fetches  []string
	fetchErr error
	version  string
	swaps    []string
	execs    []string
	beats    []string
}

func (f *followWorld) wire(w *world, build string) {
	w.build = build
	w.fetch = func(_ context.Context, repo, version, dir string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fetches = append(f.fetches, repo+" "+version+" "+dir)
		if f.fetchErr != nil {
			return "", f.fetchErr
		}
		return dir + "/.nova-friend." + version + ".new.1", nil
	}
	w.verifyBin = func(_ context.Context, staged string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return "nova-friend " + f.version + " darwin/arm64 go1.26", nil
	}
	w.swap = func(staged, target string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.swaps = append(f.swaps, staged+" -> "+target)
		return nil
	}
	w.reexec = func(bin string, argv []string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.execs = append(f.execs, bin+" "+strings.Join(argv, " "))
		return errors.New("the fake does not replace the test")
	}
}

// The row names a release this build is not: the daemon fetches it from --release-repo,
// verifies it, swaps it over its own binary and restarts itself with its own arguments; the
// beats before carry the build it runs, and its width is the row's.
func TestRunFollowsTheRowsReleaseAndRestartsUnderIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	f := &followWorld{version: "v2.0.0"}
	f.wire(&w, "v1.0.0")
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	beats := 0
	w.beat = func(_ context.Context, _, _ string, _ time.Time, words friend.BeatWords) (string, error) {
		f.mu.Lock()
		beats++
		f.beats = append(f.beats, strings.Join(proofArgs(words), " "))
		execs := len(f.execs)
		f.mu.Unlock()
		if beats == 12 || execs > 0 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=3 row_release=v2.0.0", nil
	}
	dir := t.TempDir()
	var out, errb strings.Builder
	args := []string{"run", "--as", "bob", "--harness", "opencode", "--dir", dir, "--width", "4", "--release-repo", "owner/name"}
	code := run(args, strings.NewReader(""), &out, &errb, w)
	assert.Equal(t, 0, code, errb.String())
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.fetches, "the release was fetched")
	assert.Equal(t, "owner/name v2.0.0 /opt/nova/bin", f.fetches[0], "from the repository given, into the running binary's directory")
	assert.Equal(t, []string{"/opt/nova/bin/.nova-friend.v2.0.0.new.1 -> /opt/nova/bin/nova-friend"}, f.swaps, "the verified binary over this one")
	require.Len(t, f.execs, 1)
	assert.Equal(t, "/opt/nova/bin/nova-friend "+strings.Join(args, " "), f.execs[0], "restarted with the same arguments")
	record := out.String()
	assert.Contains(t, record, "CONFIG release=v2.0.0 (was -) from the friend row")
	assert.Contains(t, record, "CONFIG width=3 (was 0) from the friend row")
	assert.Contains(t, record, "the row names v2.0.0 and this daemon runs v1.0.0: taking no new lanes; fetching it")
	assert.Contains(t, record, "v2.0.0 is in place; restarting under it with the same arguments")
	assert.Contains(t, record, "the restart under v2.0.0 failed: the fake does not replace the test")
	assert.Contains(t, f.beats[len(f.beats)-1], "--working 0 --width 3 --build v1.0.0", "every beat says the work, the row's width and the build")
	s, _, err := friend.ReadStatus(friend.StateDirIn(dir))
	require.NoError(t, err)
	assert.Equal(t, 3, s.Width, "the row's width is the only width once the beat answers")
}

// A fetched binary of another version is never put in place; and without a repository the
// release is said and not followed.
func TestRunNeverRunsAnUnverifiedReleaseAndNeedsARepository(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version string
		repo          []string
		want          string
	}{
		{"the wrong version", "v1.9.9", []string{"--release-repo", "owner/name"}, "the fetched binary answers version v1.9.9, not v2.0.0"},
		{"no repository", "v2.0.0", nil, "no release repository: run the daemon with --release-repo <owner/name>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			f := &followWorld{version: tc.version}
			f.wire(&w, "v1.0.0")
			var cancel context.CancelFunc
			w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel = context.WithCancel(ctx)
				return ctx, cancel
			}
			beats := 0
			w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
				beats++
				if beats == 8 {
					cancel()
				}
				return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=2 row_release=v2.0.0", nil
			}
			var out, errb strings.Builder
			code := run(append([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()}, tc.repo...), strings.NewReader(""), &out, &errb, w)
			assert.Equal(t, 0, code, errb.String())
			f.mu.Lock()
			defer f.mu.Unlock()
			assert.Empty(t, f.swaps, "nothing put in place")
			assert.Empty(t, f.execs, "nothing restarted")
			assert.Contains(t, out.String(), "v2.0.0 not followed: "+tc.want)
			assert.Contains(t, out.String(), "the running build v1.0.0 stays in place")
		})
	}
}

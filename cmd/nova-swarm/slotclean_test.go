package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A finished launch's checkout is removed (slotclean.go): the member done with a launch
// removes its directory and keeps the small files beside it and the results; a failed one
// keeps its directory, the newest keepFailed of the pool; the start sweeps what a crash
// left; nothing running, nothing that is not a launch directory, and nothing through a link
// is ever removed.

// pool is a temporary pool: its slots directory, its results, and a runner over them.
type pool struct {
	t       *testing.T
	slots   string
	results string
	r       *nativeRunner
	errb    *bytes.Buffer
}

func newPool(t *testing.T) *pool {
	t.Helper()
	root := t.TempDir()
	p := &pool{t: t, slots: filepath.Join(root, "slots"), results: filepath.Join(root, "results"), errb: &bytes.Buffer{}}
	require.NoError(t, os.MkdirAll(p.slots, 0o755))
	require.NoError(t, os.MkdirAll(p.results, 0o755))
	p.r = &nativeRunner{root: root, slots: p.slots, resultsRoot: p.results, stderr: p.errb}
	// what a test keeps read-only is made writable again before the temp dir is removed
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink == 0 {
				_ = os.Chmod(path, 0o755) // ignored: best effort; the temp dir's own removal reports what is left
			}
			return nil
		})
	})
	return p
}

// launch stages a fake launch of card at gen as the member would leave it: the directory
// with a checkout holding a module cache as Go leaves one (directories 0555, files 0444),
// the small files beside it and a result; its activity is at.
func (p *pool) launch(card string, gen int, at time.Time) string {
	p.t.Helper()
	name := launchName(member.Packet{Card: card, Kind: "work", Gen: gen, Epoch: 1})
	write(p.t, filepath.Join(p.slots, name, "jobs", card, "repo", "main.go"), "package main\n")
	mod := filepath.Join(p.slots, name, "jobs", card, "gomodcache", "example.com", "mod@v1.0.0")
	write(p.t, filepath.Join(mod, "mod.go"), "package mod\n")
	require.NoError(p.t, os.Chmod(filepath.Join(mod, "mod.go"), 0o444))
	for d := mod; d != filepath.Join(p.slots, name, "jobs", card); d = filepath.Dir(d) {
		require.NoError(p.t, os.Chmod(d, 0o555))
	}
	for _, sib := range []string{".native.log", ".card.md", ".frame.json"} {
		write(p.t, filepath.Join(p.slots, name+sib), "kept\n")
	}
	write(p.t, filepath.Join(p.results, name, "RESULT.md"), "kept\n")
	p.touch(name, at)
	return name
}

// touch sets a launch's last activity: its directory's and its native log's times.
func (p *pool) touch(name string, at time.Time) {
	p.t.Helper()
	for _, path := range []string{filepath.Join(p.slots, name), filepath.Join(p.slots, name+".native.log")} {
		require.NoError(p.t, os.Chtimes(path, at, at))
	}
}

func (p *pool) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(p.slots, name))
	return err == nil
}

// TestAnEndedLaunchLeavesNoCheckout pins the end of an ok launch: its directory is gone,
// a read-only part (as a module cache is) and all, and the files beside it and its results
// stay.
func TestAnEndedLaunchLeavesNoCheckout(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.launch("c1", 1, time.Now())
	ro := filepath.Join(p.slots, name, "jobs", "c1", "gomodcache")
	fi, err := os.Stat(ro)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o555), fi.Mode().Perm(), "the rig's module cache is read-only")
	p.r.started(name)
	p.r.Ended(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1}, false)
	assert.False(t, p.exists(name), "the launch's directory is removed")
	for _, sib := range []string{".native.log", ".card.md", ".frame.json"} {
		assert.FileExists(t, filepath.Join(p.slots, name+sib))
	}
	assert.FileExists(t, filepath.Join(p.results, name, "RESULT.md"))
	assert.Empty(t, p.errb.String())
	assert.Empty(t, p.r.live)
}

// TestFailedLaunchesKeepTheNewestFive pins the bound: each failed launch keeps its
// directory, and past keepFailed the oldest goes first.
func TestFailedLaunchesKeepTheNewestFive(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	base := time.Now().Add(-time.Minute)
	var names []string
	for i := range keepFailed + 2 {
		card := "c" + strconv.Itoa(i)
		names = append(names, p.launch(card, 1, base.Add(time.Duration(i)*time.Second)))
		p.r.started(names[i])
		p.r.Ended(member.Packet{Card: card, Kind: "work", Gen: 1, Epoch: 1}, true)
	}
	for i, name := range names {
		assert.Equal(t, i >= 2, p.exists(name), "%s: the newest %d failed are kept, the oldest removed", name, keepFailed)
	}
	assert.Len(t, p.r.kept, keepFailed)
}

// TestTheSweepRemovesWhatACrashLeftAndNothingElse pins the start's sweep over a pool a crash
// left: ended launches past the newest keepFailed go; a running one (its pid alive, or this
// process's own), one with recent activity (another process's, or one still starting), a
// directory that is not a launch's, a file, and a link are never touched, and a link's
// target is never reached.
func TestTheSweepRemovesWhatACrashLeftAndNothingElse(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	old := time.Now().Add(-time.Hour)
	var left []string
	for i := range keepFailed + 2 {
		left = append(left, p.launch("old"+strconv.Itoa(i), 1, old.Add(time.Duration(i)*time.Second)))
	}
	running := p.launch("running", 1, old.Add(-time.Hour))
	write(t, filepath.Join(p.slots, running+".pid"), strconv.Itoa(os.Getpid())+"\n")
	ours := p.launch("ours", 1, old.Add(-time.Hour))
	p.r.started(ours)
	recent := p.launch("recent", 1, time.Now())
	notALaunch := filepath.Join(p.slots, "cache")
	require.NoError(t, os.MkdirAll(notALaunch, 0o755))
	require.NoError(t, os.Chtimes(notALaunch, old, old))
	write(t, filepath.Join(p.slots, "file.g1.e1"), "a file named like a launch\n")
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "never removed\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(p.slots, "link.g1.e1")))

	removed, kept := p.r.prune(time.Now())
	assert.Equal(t, 2, removed)
	assert.Equal(t, keepFailed, kept)
	for i, name := range left {
		assert.Equal(t, i >= 2, p.exists(name), "%s: the newest %d ended are kept", name, keepFailed)
	}
	for _, name := range []string{running, ours, recent, "cache", "file.g1.e1", "link.g1.e1"} {
		assert.True(t, p.exists(name), "%s is never removed", name)
	}
	assert.FileExists(t, filepath.Join(outside, "precious"), "a link's target is never reached")
	assert.Empty(t, p.errb.String())
}

// TestRemoveLaunchRefusesAnythingButALaunchDirectory pins the removal by name: a name that is
// not a launch's, a path that leaves the slots, a link and a file are refused, and nothing
// is removed.
func TestRemoveLaunchRefusesAnythingButALaunchDirectory(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "never removed\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(p.slots, "link.g1.e1")))
	write(t, filepath.Join(p.slots, "file.g1.e1"), "a file\n")
	require.NoError(t, os.MkdirAll(filepath.Join(p.slots, "cache"), 0o755))
	for _, name := range []string{"cache", "../results", "..", "", "link.g1.e1", "file.g1.e1", "x/y.g1.e1"} {
		assert.Error(t, p.r.removeLaunch(name), "%q is refused", name)
	}
	assert.True(t, p.exists("link.g1.e1"))
	assert.True(t, p.exists("cache"))
	assert.FileExists(t, filepath.Join(outside, "precious"))
	assert.DirExists(t, p.results)
	assert.NoError(t, p.r.removeLaunch("gone.g1.e1"), "a launch already gone is nothing to remove")
}

// TestTheDiskFloorRefusesToStartAndSaysWhy pins the member's Room: under the floor, or a
// volume that cannot be read, starts nothing and says what was found and what to do.
func TestTheDiskFloorRefusesToStartAndSaysWhy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		free uint64
		err  error
		ok   bool
		says string
	}{
		{"above the floor", 120 * gib, nil, true, "is 120.0 GiB, above the floor of 100 GiB"},
		{"at the floor", 100 * gib, nil, true, "above the floor"},
		{"under the floor", 42 * gib, nil, false, "is 42.0 GiB, under the floor of 100 GiB; no card is started until it is above; run: free disk on that volume, or start the member with a lower --disk-floor"},
		{"unreadable", 0, errors.New("statfs: no such file"), false, "could not be read (statfs: no such file); no card is started until it can"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, why := diskRoom("/pool/slots", 100, func(string) (uint64, error) { return tc.free, tc.err })()
			assert.Equal(t, tc.ok, ok)
			assert.Contains(t, why, tc.says)
			assert.Contains(t, why, "/pool/slots")
		})
	}
	free, err := diskFree(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, free, "the real volume answers")
}

// TestMemberRefusesANegativeDiskFloor pins the flag's refusal, saying what it wants.
func TestMemberRefusesANegativeDiskFloor(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run(append(memberFull(t.TempDir()), "--disk-floor", "-1"), bytes.NewReader(nil), &out, &errb, time.Now())
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--disk-floor is the free GiB the slots' volume must keep for the member to start a card: 0 or more")
}

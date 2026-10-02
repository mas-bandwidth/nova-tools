//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// handoff_race_test.go is the contract that the handoff reads only the volume,
// even while something the card left running rewrites it. supervise kills the
// command's process group, and a setsid() child is outside it, so the copy runs
// beside a process that can swap any path on the volume between the check and
// the open. Each row makes that swap at the moment a real race would win,
// through the input's own hooks: the openat hook changes the filesystem and then
// makes the real call, so what is proved is the kernel's refusal, not a fake one.

// offVolume writes a file the caller could read off the volume, at <dir>/name:
// the thing a raced handoff would carry out.
func offVolume(t *testing.T, name string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	testkit.WriteFile(t, path, "PRIVATE KEY", 0o600)
	return dir, path
}

// swapOnOpen is an openat hook that, on the Nth open of name, runs swap and
// then makes the real call. The first open of a name is the measure; the second
// is the copy, which is where the reported race won.
func swapOnOpen(name string, nth int, swap func()) *handoffHooks {
	var mu sync.Mutex
	seen := 0
	return &handoffHooks{openat: func(dirfd int, n string, flags int) (int, error) {
		if n == name {
			mu.Lock()
			if seen++; seen == nth {
				swap()
			}
			mu.Unlock()
		}
		return unix.Openat(dirfd, n, flags, 0)
	}}
}

// noSecretIn fails when the off-volume bytes reached --out.
func noSecretIn(t *testing.T, out string) {
	t.Helper()
	_ = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			assert.NotContains(t, string(b), "PRIVATE KEY", "the off-volume file reached --out at %s", p)
		}
		return nil
	})
}

// Every row is refused, names each of says, carries no off-volume byte to --out and
// leaves no <out>/card1/RESULT.md. A row's setup lays the work directory down and
// returns the input; Out and Name are the test's.
func TestHandoffRefusesEveryRaceOffTheVolume(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		setup func(t *testing.T, work string) handoffInput
		says  []string
	}{
		// The reported race: RESULT.md passes every check as a regular file, then is
		// swapped for a symlink to a file the caller can read before the copy opens it.
		// The open follows no link.
		{"a file swapped to a symlink before the copy", func(t *testing.T, work string) handoffInput {
			testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "RESULT card1\n")
			_, secret := offVolume(t, "id_ed25519")
			return handoffInput{Work: work, hooks: swapOnOpen("RESULT.md", 2, func() {
				_ = os.Remove(filepath.Join(work, "RESULT.md"))
				assert.NoError(t, os.Symlink(secret, filepath.Join(work, "RESULT.md")))
			})}
		}, []string{"symlink", "RESULT.md"}},
		// The same swap one level up, to a directory off the volume holding a file of
		// the same name: every component is opened from its checked parent with no
		// link followed.
		{"a parent directory swapped to a symlink", func(t *testing.T, work string) handoffInput {
			testkit.WriteFile(t, filepath.Join(work, "art", "deep", "two.txt"), "22")
			elsewhere, _ := offVolume(t, "two.txt")
			return handoffInput{Work: work, Artifacts: []string{"art"}, Named: true, hooks: swapOnOpen("deep", 2, func() {
				_ = os.RemoveAll(filepath.Join(work, "art", "deep"))
				assert.NoError(t, os.Symlink(elsewhere, filepath.Join(work, "art", "deep")))
			})}
		}, []string{"symlink"}},
		// work/ itself a symlink: the mount is opened, and work/ from it with no link
		// followed.
		{"a work directory that is a symlink", func(t *testing.T, mount string) handoffInput {
			elsewhere, _ := offVolume(t, "RESULT.md")
			require.NoError(t, os.Symlink(elsewhere, filepath.Join(mount, "work")))
			return handoffInput{Mount: mount, Work: filepath.Join(mount, "work")}
		}, nil},
		// A FIFO cannot hold the handoff: the open does not wait for a writer, and the
		// descriptor is refused by its type. Without O_NONBLOCK this open blocks forever,
		// and the test binary's own -timeout is what reports it.
		{"a FIFO swapped in, without blocking", func(t *testing.T, work string) handoffInput {
			testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "RESULT card1\n")
			return handoffInput{Work: work, hooks: swapOnOpen("RESULT.md", 2, func() {
				_ = os.Remove(filepath.Join(work, "RESULT.md"))
				assert.NoError(t, syscall.Mkfifo(filepath.Join(work, "RESULT.md"), 0o644))
			})}
		}, []string{"neither a regular file nor a directory"}},
		// The fstat hook reports the next device number for RESULT.md's descriptor, which
		// is what a file on another filesystem reached by any route would show.
		{"a regular file on another device", func(t *testing.T, work string) handoffInput {
			testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "RESULT card1\n")
			var mu sync.Mutex
			resultFDs := map[int]bool{}
			return handoffInput{Work: work, hooks: &handoffHooks{
				openat: func(dirfd int, n string, flags int) (int, error) {
					fd, err := unix.Openat(dirfd, n, flags, 0)
					if err == nil && n == "RESULT.md" {
						mu.Lock()
						resultFDs[fd] = true
						mu.Unlock()
					}
					return fd, err
				},
				fstat: func(fd int, st *unix.Stat_t) error {
					if err := unix.Fstat(fd, st); err != nil {
						return err
					}
					mu.Lock()
					defer mu.Unlock()
					if resultFDs[fd] {
						st.Dev++
					}
					return nil
				},
			}}
		}, []string{"not on the volume"}},
		// The copy reads what it checked and no more than the cap: a file that grew after
		// it was measured is refused, and its partial copy removed.
		{"a file that grew past the cap after it was measured", func(t *testing.T, work string) handoffInput {
			testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "small\n")
			return handoffInput{Work: work, MaxBytes: 1024, hooks: swapOnOpen("RESULT.md", 2, func() {
				assert.NoError(t, os.WriteFile(filepath.Join(work, "RESULT.md"), []byte(strings.Repeat("x", 4096)), 0o644))
			})}
		}, []string{"--out-max-bytes"}},
		// The mount must hold the working directory, before anything under it is opened.
		{"a work directory off the mount", func(t *testing.T, mount string) handoffInput {
			work, _ := handoffDirs(t)
			testkit.WriteFile(t, filepath.Join(work, "RESULT.md"), "x\n")
			return handoffInput{Mount: mount, Work: work}
		}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			work, out := handoffDirs(t)
			in := c.setup(t, work)
			in.Out, in.Name = out, "card1"
			_, err := copyOut(in)
			require.Error(t, err)
			for _, want := range c.says {
				require.Contains(t, err.Error(), want)
			}
			noSecretIn(t, out)
			assert.Error(t, statErr(filepath.Join(out, "card1", "RESULT.md")), "a refused file was left in --out")
		})
	}
}

// A symlink inside a walked directory is skipped, as a handoff copies bytes, and a
// link inside the working directory named as the artifact is refused, even to a file on
// the volume.
func TestHandoffSkipsLinksInADirectoryAndRefusesANamedLink(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	testkit.WriteFile(t, filepath.Join(work, "art", "one.txt"), "1")
	_, secret := offVolume(t, "id_ed25519")
	require.NoError(t, os.Symlink(secret, filepath.Join(work, "art", "key")))
	require.NoError(t, os.Symlink(filepath.Join(work, "art", "one.txt"), filepath.Join(work, "inner.txt")))
	res, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", Artifacts: []string{"art"}, Named: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Files, "the one regular file")
	require.Equal(t, int64(1), res.Bytes, "the one regular file")
	noSecretIn(t, out)
	_, err = copyOut(handoffInput{Work: work, Out: out, Name: "card2", Artifacts: []string{"inner.txt"}, Named: true})
	require.Error(t, err)
}

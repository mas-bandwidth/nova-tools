//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// handoff_race_test.go is the contract that the handoff reads only the volume,
// even while something the card left running rewrites it. supervise kills the
// command's process group, and a setsid() child is outside it, so the copy runs
// beside a process that can swap any path on the volume between the check and
// the open. Each test here makes that swap at the moment a real race would win,
// through the input's own hooks: the openat hook changes the filesystem and then
// makes the real call, so what is proved is the kernel's refusal, not a fake one.

// secretOff writes a file off the volume that the caller could read, the thing
// a raced handoff would carry out.
func secretOff(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
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
			seen++
			if seen == nth {
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
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "PRIVATE KEY") {
				t.Errorf("the off-volume file reached --out at %s", p)
			}
		}
		return nil
	})
}

// 1. The reported race: RESULT.md passes every check as a regular file, then is
// swapped for a symlink to a file the caller can read before the copy opens it.
// The open follows no link, so the handoff refuses and nothing off the volume
// leaves.
func TestHandoffRefusesAFileSwappedToASymlinkBeforeTheCopy(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "RESULT card1\n")
	_, secret := secretOff(t)
	hooks := swapOnOpen("RESULT.md", 2, func() {
		_ = os.Remove(filepath.Join(work, "RESULT.md"))
		if err := os.Symlink(secret, filepath.Join(work, "RESULT.md")); err != nil {
			t.Error(err)
		}
	})
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", hooks: hooks})
	if err == nil {
		t.Fatal("a RESULT.md swapped to a symlink off the volume was copied, not refused")
	}
	if !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), "RESULT.md") {
		t.Errorf("the refusal does not name the file and the link: %v", err)
	}
	noSecretIn(t, out)
}

// 2. The same swap one level up: a parent directory of a walked file becomes a
// symlink to a directory off the volume that holds a file of the same name.
// Every component is opened from its checked parent with no link followed, so
// the swap of a parent is refused the same way as the swap of the file.
func TestHandoffRefusesAParentDirectorySwappedToASymlink(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "art/deep/two.txt", "22")
	elsewhere, _ := secretOff(t)
	if err := os.WriteFile(filepath.Join(elsewhere, "two.txt"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := swapOnOpen("deep", 2, func() {
		_ = os.RemoveAll(filepath.Join(work, "art", "deep"))
		if err := os.Symlink(elsewhere, filepath.Join(work, "art", "deep")); err != nil {
			t.Error(err)
		}
	})
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1",
		Artifacts: []string{"art"}, Named: true, hooks: hooks})
	if err == nil {
		t.Fatal("a parent directory swapped to a symlink off the volume carried the copy off it")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal does not say a link was refused: %v", err)
	}
	noSecretIn(t, out)
}

// 3. The work directory itself swapped for a symlink before the handoff opens
// it: the mount is opened, and work/ is opened from it with no link followed.
func TestHandoffRefusesAWorkDirectoryThatIsASymlink(t *testing.T) {
	t.Parallel()

	mount, out := handoffDirs(t)
	elsewhere, _ := secretOff(t)
	if err := os.WriteFile(filepath.Join(elsewhere, "RESULT.md"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(mount, "work")); err != nil {
		t.Fatal(err)
	}
	_, err := copyOut(handoffInput{Mount: mount, Work: filepath.Join(mount, "work"), Out: out, Name: "card1"})
	if err == nil {
		t.Fatal("a work/ that is a symlink off the volume was read")
	}
	noSecretIn(t, out)
}

// 4. A FIFO swapped in before the copy cannot hold the handoff: the open does
// not wait for a writer, and the descriptor is refused by its type. Without
// O_NONBLOCK this open blocks forever.
func TestHandoffRefusesAFIFOSwappedInWithoutBlocking(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "RESULT card1\n")
	hooks := swapOnOpen("RESULT.md", 2, func() {
		_ = os.Remove(filepath.Join(work, "RESULT.md"))
		if err := syscall.Mkfifo(filepath.Join(work, "RESULT.md"), 0o644); err != nil {
			t.Error(err)
		}
	})
	// Called directly: an open that waited on the FIFO would hang here, and the
	// test binary's own -timeout is what reports it.
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", hooks: hooks})
	if err == nil {
		t.Fatal("a FIFO in place of RESULT.md was not refused")
	}
	if !strings.Contains(err.Error(), "neither a regular file nor a directory") {
		t.Errorf("the refusal does not name the type: %v", err)
	}
}

// 5. A regular file whose descriptor reports a device that is not the volume's
// is refused: the handoff copies bytes from the volume and from nowhere else.
// The fstat hook reports the next device number for RESULT.md's descriptor,
// which is what a file on another filesystem reached by any route would show.
func TestHandoffRefusesARegularFileOnAnotherDevice(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "RESULT card1\n")
	var mu sync.Mutex
	resultFDs := map[int]bool{}
	hooks := &handoffHooks{
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
			other := resultFDs[fd]
			mu.Unlock()
			if other {
				st.Dev++
			}
			return nil
		},
	}
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", hooks: hooks})
	if err == nil {
		t.Fatal("a regular file on another device was copied")
	}
	if !strings.Contains(err.Error(), "not on the volume") {
		t.Errorf("the refusal does not say the file is off the volume: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "card1", "RESULT.md")); statErr == nil {
		t.Errorf("a refused file was written to --out")
	}
}

// 6. The copy reads what it checked, and no more than the cap: a file that grew
// after it was measured is refused rather than copied past --out-max-bytes.
func TestHandoffRefusesAFileThatGrewPastTheCapAfterItWasMeasured(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "small\n")
	hooks := swapOnOpen("RESULT.md", 2, func() {
		if err := os.WriteFile(filepath.Join(work, "RESULT.md"), []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
			t.Error(err)
		}
	})
	_, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", MaxBytes: 1024, hooks: hooks})
	if err == nil || !strings.Contains(err.Error(), "--out-max-bytes") {
		t.Fatalf("a file that grew past the cap after the measure was not refused by the cap: %v", err)
	}
}

// 7. The mount must hold the working directory: a Work outside Mount is refused
// before anything is opened under it.
func TestHandoffRefusesAWorkDirectoryOffTheMount(t *testing.T) {
	t.Parallel()

	mount, out := handoffDirs(t)
	work, _ := handoffDirs(t)
	writeOn(t, work, "RESULT.md", "x\n")
	if _, err := copyOut(handoffInput{Mount: mount, Work: work, Out: out, Name: "card1"}); err == nil {
		t.Fatal("a working directory that is not under the mount was read")
	}
}

// 8. A symlink inside a walked directory is skipped, as a handoff copies bytes,
// and a link inside the working directory named as the artifact is refused.
func TestHandoffSkipsLinksInADirectoryAndRefusesANamedLink(t *testing.T) {
	t.Parallel()

	work, out := handoffDirs(t)
	writeOn(t, work, "art/one.txt", "1")
	_, secret := secretOff(t)
	if err := os.Symlink(secret, filepath.Join(work, "art", "key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(work, "art", "one.txt"), filepath.Join(work, "inner.txt")); err != nil {
		t.Fatal(err)
	}
	res, err := copyOut(handoffInput{Work: work, Out: out, Name: "card1", Artifacts: []string{"art"}, Named: true})
	if err != nil || res.Files != 1 || res.Bytes != 1 {
		t.Fatalf("files=%d bytes=%d err=%v, want the one regular file", res.Files, res.Bytes, err)
	}
	noSecretIn(t, out)
	if _, err := copyOut(handoffInput{Work: work, Out: out, Name: "card2", Artifacts: []string{"inner.txt"}, Named: true}); err == nil {
		t.Fatal("an artifact that is a symlink, even to a file on the volume, was followed")
	}
}

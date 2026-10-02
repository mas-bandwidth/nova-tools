//go:build darwin

// The disposable volume's CREATION, with diskutil faked and the lock REAL.
//
// Measured on the Studio, 2026-09-18, in a 20-run soak of `nova-sandbox run`: four
// concurrent runs, and three of them then all four died before the card ran with
//
//	SANDBOX REFUSED reason=volume_failed: /Volumes/nova-conc-N/work could not be made
//	on the disposable volume: mkdir ...: permission denied
//
// The cause is outside this tool. A `diskutil apfs addVolume` that runs while another one
// is running leaves the NEW VOLUME'S ROOT `root:wheel drwxr-xr-x` instead of the caller's
// `glenn:staff drwxrwxr-x`, and it does not settle: still denied two seconds later.
// Uncontended the root is the caller's the moment `diskutil info` reports a mount point;
// staggered twelve seconds apart, four runs all pass WITH THEIR EXECUTION OVERLAPPING --
// so it is creation alone that is broken. So `Create` takes an inter-process lock and,
// after the mount, asks the question that was silently assumed: is this root mine, and can
// I write it? diskutil is faked; the lock is a real `flock` on a real file.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDiskutil stands in for /usr/sbin/diskutil. It answers the commands Create runs,
// records every argv in order, and counts how many callers are inside addVolume at once,
// which is the whole question the lock exists to answer.
type fakeDiskutil struct {
	mu    sync.Mutex
	calls []string // every diskutil argv, joined, in order
	// inAdd is how many callers are inside addVolume now; maxAdd > 1 is the contention
	// the soak measured.
	inAdd, maxAdd int
	nextDisk      int
	// mountDenied makes `info` report no mount point, which is what a caller inside an
	// OS sandbox sees; frameworkDenied makes every command fail as diskutil does when a
	// seatbelt wall keeps it from DiskArbitration -- the same cause, one step earlier.
	mountDenied, frameworkDenied bool
}

// diskutilFrameworkLine is diskutil's own sentence when DiskArbitration is out of reach,
// copied from a run nested inside the bare wall form. It blames single-user mode, which is
// not what happened and not where to look.
const diskutilFrameworkLine = "framework being unavailable due to being booted in single-user mode."

// ran is every argv so far, joined by spaces.
func (f *fakeDiskutil) ran() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, " ")
}

func (f *fakeDiskutil) run(args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	f.mu.Unlock()
	if f.frameworkDenied {
		return "", fmt.Errorf("diskutil %s: exit status 1: %s", strings.Join(args, " "), diskutilFrameworkLine)
	}
	switch {
	case len(args) >= 2 && args[0] == "apfs" && args[1] == "addVolume":
		f.mu.Lock()
		f.inAdd++
		f.maxAdd = max(f.maxAdd, f.inAdd)
		f.nextDisk++
		disk := fmt.Sprintf("disk3s%d", f.nextDisk)
		f.mu.Unlock()
		// A real addVolume takes seconds. Yielding is this fake's whole duration: with no
		// lock the goroutines interleave here every time, and with it none can. No clock.
		for i := 0; i < 200; i++ {
			runtime.Gosched()
		}
		f.mu.Lock()
		f.inAdd--
		f.mu.Unlock()
		return "Disk from APFS operation: " + disk + "\n", nil
	case len(args) == 2 && args[0] == "info":
		if f.mountDenied {
			// diskutil names the field and leaves it empty.
			return "   Mount Point:              \n", nil
		}
		return "   Mount Point:              /Volumes/nova-x\n", nil
	case len(args) >= 2 && args[0] == "apfs" && args[1] == "deleteVolume",
		len(args) == 2 && args[0] == "unmount":
		return "", nil
	}
	return "", fmt.Errorf("the fake diskutil was asked %q, which Create does not run", strings.Join(args, " "))
}

// benchDiskutil puts the fake diskutil and a lock file of the test's own in place.
func benchDiskutil(t *testing.T, usable func(string) error) *fakeDiskutil {
	t.Helper()
	f := &fakeDiskutil{}
	lock := filepath.Join(t.TempDir(), "volume-create.lock")
	swap(t, &diskutilRun, f.run)
	swap(t, &volumeRootUsable, usable)
	swap(t, &volumeLockPath, func() (string, error) { return lock, nil })
	return f
}

// alwaysUsable is the uncontended machine: the new root is the caller's once mounted.
func alwaysUsable(string) error { return nil }

// realApfsList is `diskutil apfs list` as the Studio prints it (macOS 26, arm64,
// 2026-09-18), copied out and not invented: the tree characters are the whole point.
// Measured dogfooding `reap` against a really leaked volume: `field`'s cutset `|+-< `
// holds no `>`, so `|   +-> Volume disk3s7 <uuid>` trimmed to `> Volume ...`, no line
// opened a record, and `reap` printed `SANDBOX REAP OK volumes=0` while
// /Volumes/nova-kill1 was mounted with three processes holding it open.
const realApfsList = `APFS Containers (2 found)
|
+-- Container disk3 0BE7A7F4-4E48-4A5F-8CA5-8B1F0F1C8C40
|   ====================================================
|   APFS Container Reference:     disk3
|   Size (Capacity Ceiling):      994662584320 B (994.7 GB)
|   Capacity In Use By Volumes:   410932387840 B (410.9 GB) (41.3% used)
|   |
|   +-< Physical Store disk0s2 9E0D2B70-0000-0000-0000-000000000000
|   |   -----------------------------------------------------------
|   |   APFS Physical Store Disk:   disk0s2
|   |
|   +-> Volume disk3s1 1A6C0A79-0000-0000-0000-000000000000
|   |   ---------------------------------------------------
|   |   APFS Volume Disk (Role):   disk3s1 (System)
|   |   Name:                      Macintosh HD (Case-insensitive)
|   |   Mount Point:               /
|   |   Capacity Consumed:         11534336000 B (11.5 GB)
|   |
|   +-> Volume disk3s7 6CD8025B-76B4-4336-918B-04FEE498F9BD
|       ---------------------------------------------------
|       APFS Volume Disk (Role):   disk3s7 (No specific role)
|       Name:                      nova-kill1 (Case-insensitive)
|       Mount Point:               /Volumes/nova-kill1
|       Capacity Consumed:         32768 B (32.8 KB)
|       Capacity Quota:            64000000 B (64.0 MB) (0.1% reached)
|       Sealed:                    No
|
+-- Container disk5 2F0B1111-0000-0000-0000-000000000000
    ====================================================
    APFS Container Reference:     disk5
    |
    +-> Volume disk5s1 7C0D2222-0000-0000-0000-000000000000
    |   ---------------------------------------------------
    |   APFS Volume Disk (Role):   disk5s1 (No specific role)
    |   Name:                      nova-old (Case-insensitive)
    |   Mount Point:               Not Mounted
    |   Capacity Consumed:         16384 B (16.4 KB)
`

// The reaper's eyes: every nova- volume of the real listing, with the disk to delete and
// the mount point to look for survivors under -- and nothing else, however the tree is
// drawn: `Macintosh HD` sits one record above the leaked one, and the prefix is the whole
// of this tool's authority to delete anything.
func TestListReadsTheRealDiskutilTree(t *testing.T) {
	// Only `apfs list` is answered, so the test cannot be reading something else.
	swap(t, &diskutilRun, func(args ...string) (string, error) {
		if strings.Join(args, " ") != "apfs list" {
			return "", fmt.Errorf("List ran `diskutil %s`", strings.Join(args, " "))
		}
		return realApfsList, nil
	})
	got, err := diskutilVolumes{}.List()
	require.NoError(t, err)
	require.Equal(t, []diskVolume{
		{Name: "nova-kill1", Disk: "disk3s7", Mount: "/Volumes/nova-kill1"},
		// `Not Mounted` is no mount point, and the volume is still one to delete.
		{Name: "nova-old", Disk: "disk5s1", Mount: ""},
	}, got)
	for _, v := range got {
		assert.True(t, strings.HasPrefix(v.Name, volumePrefix), "List returned %+v, which this tool did not make and may not touch", v)
	}
}

// Edge 1, the retry. A root that came up `root:wheel` is not the caller's place to work
// and no `chown` is available without root -- so it is DELETED and made again; the second
// volume is the one returned, and the first is gone rather than leaked.
func TestCreateRemakesAVolumeWhoseRootIsNotTheCallers(t *testing.T) {
	var asked int
	var mu sync.Mutex
	f := benchDiskutil(t, func(string) error {
		mu.Lock()
		defer mu.Unlock()
		if asked++; asked == 1 {
			return fmt.Errorf("owned by uid 0, not %d", os.Getuid())
		}
		return nil
	})
	vol, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.NoError(t, err)
	assert.Equal(t, "disk3s2", vol.Disk, "the volume returned is the one whose root the caller could write")
	assert.Equal(t, 2, strings.Count(f.ran(), "apfs addVolume"), f.ran())
	assert.Contains(t, f.ran(), "apfs deleteVolume disk3s1", "the refused volume was not deleted before the retry; that is the leak this verb exists to prevent")
}

// And when it never becomes the caller's, the tool REFUSES and names it: a run that went
// on to mkdir work/ would die with `permission denied` and a path, which says nothing
// about what went wrong; every volume made and refused is deleted.
func TestCreateRefusesAVolumeThatNeverBecomesWritable(t *testing.T) {
	f := benchDiskutil(t, func(string) error { return errors.New("owned by uid 0") })
	_, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.Error(t, err)
	for _, want := range []string{"nova-x", "writable", fmt.Sprint(volumeCreateAttempts)} {
		assert.Contains(t, err.Error(), want, "a reader cannot tell a busy machine from a broken one")
	}
	assert.Equal(t, volumeCreateAttempts, strings.Count(f.ran(), "apfs deleteVolume"), f.ran())
}

// A volume that comes up with no mount point was CREATED, and the mount is what failed.
// Every caller inside an OS sandbox meets this, so the refusal says which of the two
// happened, names the cause and the form that needs no volume, in the one sentence
// (sandboxedCallerRemedy) the denied-disk-service refusal carries too -- and never says the
// volume could not be created, which sends a reader to diskutil for a fault in neither.
func TestCreateSaysTheVolumeWasMadeAndTheMountDenied(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)
	f.mountDenied = true
	_, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.Error(t, err)
	for _, want := range []string{"disk3s1", "was created", "no mount point", volumesRoot, "OS sandbox", "--write", sandboxedCallerRemedy} {
		assert.Contains(t, err.Error(), want)
	}
	assert.NotContains(t, err.Error(), "could not be created")
	assert.ErrorIs(t, err, errVolumeNotMounted, "the run verb reads this sentinel to tell a denied mount from a failed create")
	// The volume that was made goes, whatever the mount did.
	assert.Contains(t, f.ran(), "apfs deleteVolume disk3s1")
}

// Under a seatbelt wall diskutil cannot reach DiskArbitration at all and blames
// single-user mode. Both refusals the run verb can reach that way say what really happened
// and what to do, and the `--container` advice is dropped, because naming the container by
// hand fails the same way one step later. The manager is the REAL diskutil manager over
// the fake diskutil, so what is under test is the sentence a caller reads.
func TestRunSaysWhoDeniedTheDiskServiceInsteadOfNamingTheContainerFlag(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)
	f.frameworkDenied = true
	swap[volumeManager](t, &runVolumes, diskutilVolumes{})
	for _, c := range []struct {
		name, reason string
		extra, says  []string
		not          string
	}{
		{"no container named", "reason=no_container", nil, []string{sandboxedCallerRemedy}, "--container disk3"},
		{"the listing fails", "reason=volume_failed", []string{"--container", "disk3"}, []string{"could not be listed"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := disposable(t, 0, runFlagsFor(t, c.extra...)...).ExitErr(125, c.reason)
			for _, want := range append(c.says, "OS sandbox", "--write", diskutilFrameworkLine) {
				assert.Contains(t, r.Stderr, want, "a reader is left with single-user mode")
			}
			if c.not != "" {
				assert.NotContains(t, r.Stderr, c.not, "--container cannot help when diskutil reaches nothing")
			}
		})
	}
}

// Edge 1, the lock. Eight callers at once, and diskutil sees ONE of them at a time. The
// fake yields inside addVolume, so without the lock this is over 1 on every run.
func TestConcurrentCreatesAreSerialized(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)
	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Go(func() {
			<-start
			_, errs[i] = diskutilVolumes{}.Create("disk3", fmt.Sprintf("nova-c%d", i), "64m")
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		assert.NoError(t, err, "caller %d", i)
	}
	f.mu.Lock()
	most := f.maxAdd
	f.mu.Unlock()
	require.Equal(t, 1, most, "callers inside `diskutil apfs addVolume` at once; concurrent addVolume is what leaves a new root owned by root:wheel")
	assert.Equal(t, callers, strings.Count(f.ran(), "apfs addVolume"), "the lock serializes the callers and drops none")
}

// The lock is a real one, and this is the half a fake cannot show: a SECOND PROCESS
// holding it keeps this one out. `flock` scopes to the open file description, so a second
// descriptor in this process is the same test the operating system runs.
func TestTheCreateLockIsAnExclusiveFlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume-create.lock")
	swap(t, &volumeLockPath, func() (string, error) { return path, nil })
	unlock, err := lockVolumeCreate()
	require.NoError(t, err)
	other, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	require.NoError(t, err)
	defer other.Close()
	require.Error(t, syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "a second holder took the create lock while the first held it; then two diskutil runs can overlap")
	unlock()
	require.NoError(t, syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "the create lock was not released")
	_ = syscall.Flock(int(other.Fd()), syscall.LOCK_UN)
}

// The lock file lives under the caller's own cache directory and nowhere else: a lock at
// a path a card could write is a lock a card can take, and `run`'s whole premise is that
// the contained command is not trusted with the machine.
func TestTheCreateLockLivesUnderTheCallersCacheDirectory(t *testing.T) {
	t.Parallel()
	path, err := defaultVolumeLockPath()
	require.NoError(t, err)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("this machine has no user cache directory: %v", err)
	}
	assert.True(t, strings.HasPrefix(path, cache+string(os.PathSeparator)), "the create lock is at %s, not under the caller's cache directory %s", path, cache)
	for _, shared := range []string{"/tmp/", "/var/tmp/"} {
		assert.False(t, strings.HasPrefix(path, shared), "the create lock is at %s, a path any process on this machine can write", path)
	}
}

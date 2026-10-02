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
// is running leaves the NEW VOLUME'S ROOT owned by `root:wheel` and mode `drwxr-xr-x`
// instead of the caller's `glenn:staff drwxrwxr-x`, and it does not settle: still denied
// two seconds later. Uncontended the root is the caller's and writable the moment
// `diskutil info` reports a mount point. Staggered twelve seconds apart, four runs all
// pass WITH THEIR EXECUTION OVERLAPPING -- so it is creation alone that is broken, not
// running two volumes at once.
//
// So `Create` takes an inter-process lock and, after the mount, asks the one question that
// was silently assumed: is this root mine, and can I write it? The fake below is diskutil;
// the lock is a real `flock` on a real file in a temp directory.
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

// fakeDiskutil stands in for /usr/sbin/diskutil. It answers the three commands Create
// runs -- `apfs addVolume`, `info <disk>` and `apfs deleteVolume` -- records every argv in
// order, and counts how many callers are inside addVolume at once, which is the whole
// question the lock exists to answer.
type fakeDiskutil struct {
	mu sync.Mutex
	// calls is every diskutil argv, joined, in order.
	calls []string
	// inAdd is how many callers are inside addVolume right now, and maxAdd the most
	// there ever were. maxAdd > 1 is the contention the soak measured.
	inAdd, maxAdd int
	// nextDisk numbers the volumes this fake hands out.
	nextDisk int
	// mountDenied makes `info` report no mount point at all, which is what a caller
	// that is itself inside an OS sandbox sees: addVolume succeeds and the volume comes
	// up unmounted.
	mountDenied bool
	// frameworkDenied makes every command fail the way diskutil fails when it cannot
	// reach DiskArbitration at all, which is what a seatbelt wall around the caller
	// does. It is the same cause as mountDenied, one step earlier.
	frameworkDenied bool
}

// diskutilFrameworkLine is diskutil's own sentence when DiskArbitration is out of reach,
// copied from a run nested inside the bare wall form and not invented. It blames
// single-user mode, which is not what happened and not where to look.
const diskutilFrameworkLine = "framework being unavailable due to being booted in single-user mode."

func (f *fakeDiskutil) record(args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
}

func (f *fakeDiskutil) argv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *fakeDiskutil) run(args ...string) (string, error) {
	f.record(args)
	if f.frameworkDenied {
		return "", fmt.Errorf("diskutil %s: exit status 1: %s", strings.Join(args, " "), diskutilFrameworkLine)
	}
	switch {
	case len(args) >= 2 && args[0] == "apfs" && args[1] == "addVolume":
		f.mu.Lock()
		f.inAdd++
		if f.inAdd > f.maxAdd {
			f.maxAdd = f.inAdd
		}
		f.nextDisk++
		disk := fmt.Sprintf("disk3s%d", f.nextDisk)
		f.mu.Unlock()
		// A real addVolume takes seconds. Yielding is this fake's whole duration: with
		// no lock the goroutines below interleave here every time, and with the lock
		// none of them can. No clock, no sleep -- the test asserts the event.
		for i := 0; i < 200; i++ {
			runtime.Gosched()
		}
		f.mu.Lock()
		f.inAdd--
		f.mu.Unlock()
		return "Disk from APFS operation: " + disk + "\n", nil
	case len(args) == 2 && args[0] == "info":
		if f.mountDenied {
			// diskutil names the field and leaves it empty, which is the output the
			// mount-denied refusal is read off.
			return "   Mount Point:              \n", nil
		}
		return "   Mount Point:              /Volumes/nova-x\n", nil
	case len(args) >= 2 && args[0] == "apfs" && args[1] == "deleteVolume":
		return "", nil
	case len(args) == 2 && args[0] == "unmount":
		return "", nil
	}
	return "", fmt.Errorf("the fake diskutil was asked %q, which Create does not run", strings.Join(args, " "))
}

// benchDiskutil puts the fake diskutil and a lock file of the test's own in place, and
// takes them out again.
func benchDiskutil(t *testing.T, usable func(string) error) *fakeDiskutil {
	t.Helper()
	f := &fakeDiskutil{}
	lock := filepath.Join(t.TempDir(), "volume-create.lock")

	swap(t, &diskutilRun, f.run)
	swap(t, &volumeRootUsable, usable)
	swap(t, &volumeLockPath, func() (string, error) { return lock, nil })
	return f
}

// alwaysUsable is the uncontended machine: the new volume's root is the caller's and
// writable the moment it is mounted.
func alwaysUsable(string) error { return nil }

// realApfsList is `diskutil apfs list` as the Studio actually prints it (macOS 26, arm64,
// 2026-09-18), copied out and not invented — the tree characters are the whole point.
//
// This is the fixture the reaper's eyes should have had from the start. Measured while
// dogfooding `reap` against a really leaked volume: `field`'s cutset `|+-< ` holds no `>`,
// so the line `|   +-> Volume disk3s7 <uuid>` trimmed to `> Volume disk3s7 <uuid>`, no
// line ever opened a record, and `reap` printed `SANDBOX REAP OK volumes=0` while
// /Volumes/nova-kill1 was mounted with three processes holding it open. A reaper that
// reports a dirty machine clean is worse than no reaper at all.
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

// fakeApfsList answers `apfs list` with the real listing and refuses every other command,
// so a test that thinks it is reading the tree cannot be reading something else.
func fakeApfsList(t *testing.T) {
	t.Helper()
	swap(t, &diskutilRun, func(args ...string) (string, error) {
		if strings.Join(args, " ") != "apfs list" {
			return "", fmt.Errorf("List ran `diskutil %s`", strings.Join(args, " "))
		}
		return realApfsList, nil
	})
}

func mustList(t *testing.T) []diskVolume {
	t.Helper()
	got, err := diskutilVolumes{}.List()
	require.NoError(t, err, "List: %v", err)
	return got
}

// The reaper's eyes: every nova- volume of the real listing, with the disk to delete and
// the mount point to look for survivors under.
func TestListReadsTheRealDiskutilTree(t *testing.T) {
	fakeApfsList(t)
	got := mustList(t)
	want := []diskVolume{
		{Name: "nova-kill1", Disk: "disk3s7", Mount: "/Volumes/nova-kill1"},
		// `Not Mounted` is diskutil saying there is no mount point, not a path — and the
		// volume is still one to delete, so it is still listed.
		{Name: "nova-old", Disk: "disk5s1", Mount: ""},
	}
	require.Len(t, got, len(want), "List found %d volumes in the real tree, want %d: %+v", len(got), len(want), got)
	for i := range want {
		assert.Equal(t, want[i], got[i], "volume %d is %+v, want %+v", i, got[i], want[i])
	}
}

// And nothing else is ever returned, however the tree is drawn: `Macintosh HD` sits in the
// same listing one record above the leaked one, and the prefix is the whole of this tool's
// authority to delete anything.
func TestListNeverReturnsAVolumeThisToolDidNotName(t *testing.T) {
	fakeApfsList(t)
	for _, v := range mustList(t) {
		assert.True(t, strings.HasPrefix(v.Name, volumePrefix), "List returned %+v, which this tool did not make and may not touch", v)
	}
}

// Edge 1, the retry. A volume that came up `root:wheel` is not the caller's place to work
// and no `chown` is available without root -- so it is DELETED and made again. The fake
// says "not mine" once and "mine" after that; the second volume is the one that is
// returned, and the first one is gone rather than leaked.
func TestCreateRemakesAVolumeWhoseRootIsNotTheCallers(t *testing.T) {
	var asked int
	var mu sync.Mutex
	f := benchDiskutil(t, func(string) error {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if asked == 1 {
			return fmt.Errorf("owned by uid 0, not %d", os.Getuid())
		}
		return nil
	})

	vol, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.NoError(t, err, "Create gave up on a volume that was writable on the second attempt: %v", err)
	assert.Equal(t, "disk3s2", vol.Disk, "Create returned %s; the volume it returns is the one whose root it could write, which is the second", vol.Disk)
	got := strings.Join(f.argv(), " | ")
	assert.Equal(t, 2, strings.Count(got, "apfs addVolume"), "Create made %d volumes; the first root was not the caller's, so it makes one more:\n%s",
		strings.Count(got, "apfs addVolume"), got)
	assert.Contains(t, got, "apfs deleteVolume disk3s1", "the volume whose root was not the caller's was not deleted before the retry; that is the leak this verb exists to prevent:\n%s", got)
}

// And when it never becomes the caller's, the tool REFUSES and names it. A run that went
// on to mkdir work/ would die with `permission denied` and a path, which says nothing
// about what went wrong or what to do.
func TestCreateRefusesAVolumeThatNeverBecomesWritable(t *testing.T) {
	f := benchDiskutil(t, func(string) error { return errors.New("owned by uid 0") })

	_, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.Error(t, err, "Create returned a volume the caller cannot write; the run would then fail at mkdir with `permission denied` and no cause")
	for _, want := range []string{"nova-x", "writable", fmt.Sprintf("%d", volumeCreateAttempts)} {
		assert.Contains(t, err.Error(), want, "the refusal does not carry %q, so a reader cannot tell a busy machine from a broken one:\n%s", want, err)
	}
	n := strings.Count(strings.Join(f.argv(), " "), "apfs deleteVolume")
	assert.Equal(t, volumeCreateAttempts, n, "%d of %d unusable volumes were deleted; every one that was made and refused goes", n, volumeCreateAttempts)
}

// A volume that comes up with no mount point was CREATED, and what failed is the mount.
// Every caller inside an OS sandbox meets this, so the refusal has to say which of the two
// happened, name the cause it almost always is, and name the form that needs no volume —
// and it must not say the volume could not be created, which sends a reader to diskutil
// and to the container for a fault in neither.
func TestCreateSaysTheVolumeWasMadeAndTheMountDenied(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)
	f.mountDenied = true

	_, err := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.Error(t, err, "Create returned a volume with no mount point; there is nowhere to work and the run would fail at mkdir with no cause")
	got := err.Error()
	for _, want := range []string{"disk3s1", "was created", "no mount point", volumesRoot, "OS sandbox", "--write"} {
		assert.Contains(t, got, want, "the refusal does not carry %q, so it does not say what happened or what to do:\n%s", want, got)
	}
	assert.NotContains(t, got, "could not be created", "the refusal says the volume could not be created; it WAS created, and the mount is what was denied:\n%s", got)
	assert.ErrorIs(t, err, errVolumeNotMounted, "the mount-denied error is not the sentinel the run verb reads, so the verb cannot tell it from a create that failed:\n%s", got)
	// And the volume that was made goes, whatever the mount did: the leak is the one
	// thing this path may not leave behind.
	assert.Contains(t, strings.Join(f.argv(), " "), "apfs deleteVolume disk3s1", "the unmounted volume was not deleted again:\n%s", strings.Join(f.argv(), " | "))
}

// The second face of the same cause, one step earlier than an unmounted volume: under a
// seatbelt wall diskutil cannot reach DiskArbitration at all, and it blames single-user
// mode. Both refusals the verb can reach that way say what really happened and what to do,
// and the `--container` advice is dropped on the container one, because naming the
// container by hand fails the same way one step later.
//
// The two seams compose here: the manager is the REAL diskutil manager and diskutil itself
// is the fake, so what is under test is the sentence a caller reads.
func withDeniedDiskService(t *testing.T) *fakeDiskutil {
	t.Helper()
	f := benchDiskutil(t, alwaysUsable)
	f.frameworkDenied = true
	swap[volumeManager](t, &runVolumes, diskutilVolumes{})
	return f
}

func TestRunSaysWhoDeniedTheDiskServiceInsteadOfNamingTheContainerFlag(t *testing.T) {
	withDeniedDiskService(t)

	r := disposable(t, 0, runFlagsFor(t)...)
	r.ExitErr(125, "reason=no_container", "a diskutil that cannot reach the disk service is not refused with reason=no_container: exit %d\n%s", r.Code, r.Stderr)
	for _, want := range []string{"OS sandbox", "--write", diskutilFrameworkLine} {
		assert.Contains(t, r.Stderr, want, "the refusal does not carry %q, so a reader is left with single-user mode:\n%s", want, r.Stderr)
	}
	assert.NotContains(t, r.Stderr, "--container disk3", "the refusal still advises --container, which cannot help when diskutil reaches nothing:\n%s", r.Stderr)
}

func TestRunSaysWhoDeniedTheDiskServiceWhenTheListingFails(t *testing.T) {
	withDeniedDiskService(t)

	r := disposable(t, 0, runFlagsFor(t, "--container", "disk3")...)
	r.ExitErr(125, "reason=volume_failed", "a listing that cannot reach the disk service is not refused with reason=volume_failed: exit %d\n%s", r.Code, r.Stderr)
	for _, want := range []string{"could not be listed", "OS sandbox", "--write", diskutilFrameworkLine} {
		assert.Contains(t, r.Stderr, want, "the refusal does not carry %q:\n%s", want, r.Stderr)
	}
}

// One sentence, one constant: the mount that never happened and the disk service out of
// reach are two faces of one cause, and a reader who meets the second must not be told
// something different from the first.
func TestTheSandboxedCallerSentenceIsWrittenOnce(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)
	f.mountDenied = true
	_, mountErr := diskutilVolumes{}.Create("disk3", "nova-x", "64m")
	require.Error(t, mountErr, "Create returned a volume with no mount point")
	assert.Contains(t, mountErr.Error(), sandboxedCallerRemedy, "the mount-denied refusal does not carry the shared sentence:\n%s", mountErr)

	withDeniedDiskService(t)
	r := disposable(t, 0, runFlagsFor(t)...)
	assert.Contains(t, r.Stderr, sandboxedCallerRemedy, "the denied-disk-service refusal does not carry the shared sentence:\n%s", r.Stderr)
}

// Edge 1, the lock. Eight callers at once, and diskutil sees ONE of them at a time. The
// fake yields inside addVolume, so without the lock this is over 1 on every run.
func TestConcurrentCreatesAreSerialized(t *testing.T) {
	f := benchDiskutil(t, alwaysUsable)

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = diskutilVolumes{}.Create("disk3", fmt.Sprintf("nova-c%d", i), "64m")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "caller %d could not create its volume: %v", i, err)
	}
	f.mu.Lock()
	most := f.maxAdd
	f.mu.Unlock()
	require.Equal(t, 1, most, "%d callers were inside `diskutil apfs addVolume` at once; concurrent addVolume is what leaves a new volume root owned by root:wheel, so Create takes a lock and exactly one caller is ever in there", most)
	n := strings.Count(strings.Join(f.argv(), " "), "apfs addVolume")
	assert.Equal(t, callers, n, "%d volumes were made for %d callers; the lock serializes them and drops none", n, callers)
}

// The lock is a real one, and this is the half a fake cannot show: a SECOND PROCESS
// holding it keeps this one out. Another process is another open file description, which
// is what `flock` scopes to -- so a second descriptor in this process is the same test the
// operating system runs, and it needs no second binary.
func TestTheCreateLockIsAnExclusiveFlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume-create.lock")
	swap(t, &volumeLockPath, func() (string, error) { return path, nil })

	unlock, err := lockVolumeCreate()
	require.NoError(t, err, "the create lock could not be taken: %v", err)

	other, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	require.NoError(t, err, "open the lock file a second time: %v", err)
	defer other.Close()
	err = syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	require.Error(t, err, "a second holder took the create lock while the first held it; then two diskutil runs can overlap and the lock is decoration")

	unlock()
	err = syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	require.NoError(t, err, "the create lock was not released: %v", err)
	_ = syscall.Flock(int(other.Fd()), syscall.LOCK_UN)
}

// The lock file lives under the caller's own cache directory and nowhere else. A lock at
// a path a card could write is a lock a card can take, and `run`'s whole premise is that
// the contained command is not trusted with the machine.
func TestTheCreateLockLivesUnderTheCallersCacheDirectory(t *testing.T) {
	t.Parallel()

	path, err := defaultVolumeLockPath()
	require.NoError(t, err, "the default lock path: %v", err)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("this machine has no user cache directory: %v", err)
	}
	assert.True(t, strings.HasPrefix(path, cache+string(os.PathSeparator)), "the create lock is at %s, which is not under the caller's cache directory %s; a world-writable lock path is one a contained command can take", path, cache)
	assert.False(t, strings.HasPrefix(path, "/tmp/"), "the create lock is at %s, a path any process on this machine can write", path)
	assert.False(t, strings.HasPrefix(path, "/var/tmp/"), "the create lock is at %s, a path any process on this machine can write", path)
}

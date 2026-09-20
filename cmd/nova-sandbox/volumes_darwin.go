//go:build darwin

// The production volume manager: diskutil, and nothing else. Every value that reaches a
// diskutil argument is either the caller's --name and --size (both checked against a
// narrow shape in run.go before anything is created) or a reference diskutil itself
// printed and this file checked again — no path from this file to a shell exists, because
// exec.Command takes an argv and never a command line.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// diskutilPath is where macOS ships the tool. It is looked up on the PATH first so a
// machine that moved it still works.
const diskutilPath = "/usr/sbin/diskutil"

// volumesRoot is where macOS mounts a named APFS volume.
const volumesRoot = "/Volumes"

// diskutilVolumes is the real manager.
type diskutilVolumes struct{}

func newPlatformVolumes() volumeManager { return diskutilVolumes{} }

// The two seams of this file. diskutilRun is the only way anything here reaches a
// process, and volumeRootUsable the only way it reaches the new volume's root — so
// volumes_darwin_test.go replaces both and Create's whole contract (a lock, a check, a
// retry, a refusal) is tested with no diskutil and no disk.
var (
	diskutilRun      = runDiskutil
	volumeRootUsable = rootOwnedAndWritable
)

// runDiskutil is the one place this file runs anything, and it folds stderr into the
// error so a refusal carries what diskutil actually said.
func runDiskutil(args ...string) (string, error) {
	bin := diskutilPath
	if found, err := exec.LookPath("diskutil"); err == nil {
		bin = found
	}
	cmd := exec.Command(bin, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("diskutil %s: %w: %s", strings.Join(args, " "), err, oneLineOf(errb.String()+" "+out.String()))
	}
	return out.String(), nil
}

// oneLineOf is the last non-empty line of a diskutil failure, which is the sentence that
// says why. The whole output would be a paragraph inside a one-line refusal.
func oneLineOf(s string) string {
	var last string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			last = t
		}
	}
	return last
}

// field reads one `Name:   value` field out of diskutil's human output. diskutil has a
// -plist form, and this is deliberately not it: the three fields wanted here are named
// the same way in every macOS this tool has run on, and a plist parser is a dependency
// and a second failure mode for three strings.
func field(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		// `diskutil apfs list` draws a tree, so a field line can carry | and + before it.
		trimmed = strings.TrimLeft(trimmed, "|+-< ")
		rest, ok := strings.CutPrefix(trimmed, name+":")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// Container is the APFS container the BOOT volume lives in, asked of the boot volume
// itself rather than guessed from a listing: a Mac with external APFS disks has many
// containers and only one of them is the one / is on.
func (diskutilVolumes) Container() (string, error) {
	out, err := diskutilRun("info", "/")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"APFS Container", "Part of Whole"} {
		if v := field(out, name); okContainer(v) {
			return v, nil
		}
	}
	return "", fmt.Errorf("diskutil info / names no APFS container")
}

// Exists asks whether a volume of this name is already on the machine, and asks it two
// ways: the container listing, and the mount point. Either one is enough to refuse —
// a stray directory at /Volumes/nova-<n> would be mounted over, and a run that mounted
// over someone's directory and then deleted the volume would look like it had eaten it.
func (diskutilVolumes) Exists(name string) (bool, error) {
	out, err := diskutilRun("apfs", "list")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), "|+-< ")
		rest, ok := strings.CutPrefix(trimmed, "Name:")
		if !ok {
			continue
		}
		got := strings.TrimSpace(rest)
		// `Name:  nova-j1 (Case-insensitive)` — the case note is the volume's, not part
		// of its name. APFS folds case by default, so the comparison does too: a volume
		// named NOVA-J1 would mount at the same place as nova-j1.
		if before, _, found := strings.Cut(got, " ("); found {
			got = before
		}
		if strings.EqualFold(got, name) {
			return true, nil
		}
	}
	if _, err := os.Lstat(filepath.Join(volumesRoot, name)); err == nil {
		return true, nil
	}
	return false, nil
}

// volumeCreateAttempts is how many volumes Create will make and throw away before it
// refuses. Measured on the Studio: a contended addVolume produced an unusable root every
// time and an uncontended one never did, so under the lock the first attempt is the
// answer — the retries are for the machine that is contended by something that is not
// this tool, and three is enough to say so without grinding.
const volumeCreateAttempts = 3

// Create exports one new volume with a quota and returns it mounted, WRITABLE, and the
// caller's. A volume that came up without a mount point, or with a root this user cannot
// work in, is deleted again before the error is returned: a half-made volume is the leak
// this verb exists to prevent, and the one moment it can be closed silently is before the
// caller has been told the volume exists.
//
// Two processes may not be in here at once. `diskutil apfs addVolume` run concurrently
// leaves the new volume's root `root:wheel drwxr-xr-x` rather than the caller's, and it
// never settles — measured in a 20-run soak, 2026-09-18, where three of four concurrent
// runs died at `mkdir /Volumes/nova-conc-N/work: permission denied` before their card ran.
// The same four runs staggered twelve seconds apart all passed with their EXECUTION
// overlapping, so it is creation alone that cannot be shared. There is no repair to apply
// after the fact: `chown` on someone else's directory needs root, which rule 2 does not
// have. So the lock, and then the question.
func (d diskutilVolumes) Create(container, name, size string) (diskVolume, error) {
	unlock, err := lockVolumeCreate()
	if err != nil {
		return diskVolume{}, err
	}
	defer unlock()

	var last error
	for attempt := 1; attempt <= volumeCreateAttempts; attempt++ {
		vol, err := d.createOnce(container, name, size)
		if err != nil {
			return diskVolume{}, err
		}
		last = volumeRootUsable(vol.Mount)
		if last == nil {
			return vol, nil
		}
		// Not this caller's place to work, and nothing here can make it one. It goes,
		// through the same delete every other exit uses, and the next attempt asks again.
		if derr := d.Delete(vol.Disk); derr != nil {
			return diskVolume{}, fmt.Errorf("the new volume %s came up with a root this user cannot write (%v) and could not be deleted again: %w", vol.Disk, last, derr)
		}
	}
	return diskVolume{}, fmt.Errorf("the new volume %s came up %d times with a root that is not this user's and not writable (%v), so there is nowhere to work; another `diskutil apfs addVolume` running at the same time is what does this, and this tool serializes its own — wait for the other one, or run `nova-sandbox reap` if it left volumes behind",
		name, volumeCreateAttempts, last)
}

// createOnce is one addVolume and the two questions that follow it: is it a volume, and
// where is it mounted. It holds no lock and knows nothing about retries.
func (d diskutilVolumes) createOnce(container, name, size string) (diskVolume, error) {
	out, err := diskutilRun("apfs", "addVolume", container, "APFS", name, "-quota", size)
	if err != nil {
		return diskVolume{}, err
	}
	disk := field(out, "Disk from APFS operation")
	if !okVolumeDisk(disk) {
		return diskVolume{}, fmt.Errorf("diskutil made a volume and named it %q, which is not a volume reference", disk)
	}
	info, err := diskutilRun("info", disk)
	if err != nil {
		_ = d.Delete(disk)
		return diskVolume{}, err
	}
	mount := field(info, "Mount Point")
	if mount == "" || !strings.HasPrefix(mount, volumesRoot+"/") {
		_ = d.Delete(disk)
		// The volume EXISTS at this point, and the mount is what did not happen, so the
		// error says which of the two it is: a reader told the create failed goes to
		// diskutil and to the container for a fault in neither. The cause is almost always
		// the caller's own: a process inside an OS sandbox may not mount a volume, and
		// every agent run under a harness that sandboxes its shell arrives here.
		return diskVolume{}, fmt.Errorf("%w: %s came up in %s with no mount point under %s (diskutil reports mount point %q), so there is nowhere to work, and it has been deleted again. %s",
			errVolumeNotMounted, disk, container, volumesRoot, mount, sandboxedCallerRemedy)
	}
	return diskVolume{Name: name, Disk: disk, Mount: mount}, nil
}

// rootOwnedAndWritable is the question the old Create assumed the answer to: a mount
// point is not the same thing as a place this user may work. It is asked twice, because
// the two answers differ — the OWNER is what went wrong (root:wheel instead of the
// caller), and a WRITE is what the run needs — and a check that only stats can be
// satisfied by a mode a group the caller is not in would need.
func rootOwnedAndWritable(mount string) error {
	fi, err := os.Stat(mount)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("the new volume's root at %s cannot be owner-checked on this machine", mount)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("the root of %s is owned by uid %d and this process is uid %d", mount, st.Uid, os.Getuid())
	}
	probe, err := os.CreateTemp(mount, ".nova-sandbox-create-")
	if err != nil {
		return fmt.Errorf("the root of %s is owned by this user and still not writable: %w", mount, err)
	}
	path := probe.Name()
	_ = probe.Close()
	// Removed through safepath, under the mount it was made in, like every other
	// deletion this repository performs.
	return safepath.RemoveUnder(mount, path)
}

// lockVolumeCreate takes the one lock that makes a concurrent `nova-sandbox run` safe:
// an exclusive `flock` on a file under the CALLER's cache directory. Not /tmp and not
// /var: a lock at a path any process can write is a lock any process can take, and the
// contained command of a run is exactly a process this tool does not trust.
//
// It polls rather than blocking, because a wait with no deadline is how a fleet ends up
// with idle lines holding a file (**wait loops need a deadline**). The bound is generous
// on purpose — a real addVolume is seconds and a queue of them is minutes — and it is
// production code's own wait, never a test's.
func lockVolumeCreate() (func(), error) {
	path, err := volumeLockPath()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the volume-creation lock at %s could not be opened: %w", path, err)
	}
	fd := int(f.Fd())
	deadline := time.Now().Add(volumeLockWait)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("the volume-creation lock at %s could not be taken: %w", path, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another nova-sandbox held the volume-creation lock at %s for %s; concurrent `diskutil apfs addVolume` is what this lock prevents, so this run waits rather than making a volume it could not write",
				path, volumeLockWait)
		}
		time.Sleep(volumeLockPoll)
	}
}

// volumeLockWait is how long a run waits for its turn to create, and volumeLockPoll how
// often it asks. Production's own numbers.
const (
	volumeLockWait = 15 * time.Minute
	volumeLockPoll = 50 * time.Millisecond
)

// volumeLockPath is where the lock lives, as a seam so a test locks a file of its own.
var volumeLockPath = defaultVolumeLockPath

// defaultVolumeLockPath is one file under the caller's own cache directory. The directory
// is created because it is this tool's own and holds nothing of the caller's — the rule
// that every path is yours and none is guessed is about the paths a CALLER names.
func defaultVolumeLockPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("this user has no cache directory to keep the volume-creation lock in: %w", err)
	}
	dir := filepath.Join(cache, "nova-sandbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("the volume-creation lock's directory %s could not be made: %w", dir, err)
	}
	return filepath.Join(dir, "volume-create.lock"), nil
}

// List is every volume on this machine whose name begins with the run verb's prefix. It
// reads the same `diskutil apfs list` Exists does, but keeps the whole record rather than
// the name — the reap verb needs the disk to delete and the mount point to look for
// survivors under.
//
// The tree `apfs list` draws opens each volume with `|   +-> Volume disk3s7 <uuid>`, and
// the fields that follow belong to it until the next one. A volume with no name or no disk
// reference is not one this tool made and is not returned: the prefix is the whole of the
// authority to touch anything here.
//
// treeChars is what that tree is DRAWN with, and it is this function's own rather than
// field's because it needs one character field never met. Measured, dogfooding `reap`
// against a really leaked volume: field's cutset holds no `>`, so `|   +-> Volume disk3s7`
// trimmed to `> Volume disk3s7`, nothing ever opened a record, and `reap` answered
// `SANDBOX REAP OK volumes=0` on a machine that was holding one. A reaper that reports a
// dirty machine clean is worse than no reaper — so the fixture this is tested against is
// the real `diskutil apfs list`, copied off the Studio, and never a shape assumed here.
const treeChars = "|+-<> "

func (diskutilVolumes) List() ([]diskVolume, error) {
	out, err := diskutilRun("apfs", "list")
	if err != nil {
		return nil, err
	}
	var found []diskVolume
	var cur diskVolume
	flush := func() {
		if okVolumeDisk(cur.Disk) && strings.HasPrefix(cur.Name, volumePrefix) {
			found = append(found, cur)
		}
		cur = diskVolume{}
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), treeChars)
		if rest, ok := strings.CutPrefix(trimmed, "Volume "); ok {
			flush()
			cur.Disk = strings.TrimSpace(strings.Fields(rest + " ")[0])
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "Name:"); ok {
			name := strings.TrimSpace(rest)
			if before, _, cut := strings.Cut(name, " ("); cut {
				name = before
			}
			cur.Name = name
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "Mount Point:"); ok {
			mount := strings.TrimSpace(rest)
			// `Not Mounted` is diskutil saying there is no mount point, not a path.
			if strings.HasPrefix(mount, volumesRoot+"/") {
				cur.Mount = mount
			}
		}
	}
	flush()
	return found, nil
}

// Used is the bytes the volume holds, which is what deleting it gives back. It is asked
// of the filesystem rather than of diskutil: one statfs, no process.
func (diskutilVolumes) Used(mount string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		return 0, err
	}
	return int64(st.Blocks-st.Bfree) * int64(st.Bsize), nil
}

// Delete unmounts and removes the volume. A volume whose last process has only just died
// can still be reported busy, so a first failure is followed by a forced unmount and one
// more attempt — and a second failure is a LEAK, reported, never swallowed.
func (diskutilVolumes) Delete(disk string) error {
	if !okVolumeDisk(disk) {
		return fmt.Errorf("%q is not a volume reference and this tool deletes nothing it cannot name", disk)
	}
	_, err := diskutilRun("apfs", "deleteVolume", disk)
	if err == nil {
		return nil
	}
	if _, uerr := diskutilRun("unmount", "force", disk); uerr != nil {
		return err
	}
	if _, second := diskutilRun("apfs", "deleteVolume", disk); second != nil {
		return second
	}
	return nil
}

// okVolumeDisk is the shape diskutil spells a VOLUME in: a container's disk and a slice
// on it. It guards the one argument this file ever passes to a destructive command.
func okVolumeDisk(s string) bool {
	rest, ok := strings.CutPrefix(s, "disk")
	if !ok {
		return false
	}
	before, after, found := strings.Cut(rest, "s")
	if !found || before == "" || after == "" {
		return false
	}
	for _, part := range []string{before, after} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

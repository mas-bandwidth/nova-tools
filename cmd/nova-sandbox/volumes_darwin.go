//go:build darwin

// The production volume manager: diskutil, and nothing else. Every value that reaches a
// diskutil argument is either the caller's --name and --size (both checked against a
// narrow shape in run.go before anything is created) or a reference diskutil itself
// printed and this file checked again — no path from this file to a shell exists, because
// exec.Command takes an argv and never a command line.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// diskutilPath is where macOS ships the tool. It is looked up on the PATH first so a
// machine that moved it still works.
const diskutilPath = "/usr/sbin/diskutil"

// volumesRoot is where macOS mounts a named APFS volume.
const volumesRoot = "/Volumes"

// diskutilVolumes is the real manager.
type diskutilVolumes struct{}

func newPlatformVolumes() volumeManager { return diskutilVolumes{} }

// diskutilRun is the one place this file runs anything, and it folds stderr into the
// error so a refusal carries what diskutil actually said.
func diskutilRun(args ...string) (string, error) {
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

// Create exports one new volume with a quota and returns it mounted. A volume that came
// up without a mount point is deleted again before the error is returned: a half-made
// volume is the leak this verb exists to prevent, and the one moment it can be closed
// silently is before the caller has been told the volume exists.
func (d diskutilVolumes) Create(container, name, size string) (diskVolume, error) {
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
		return diskVolume{}, fmt.Errorf("the new volume %s is not mounted under %s (mount point %q), so there is nowhere to work", disk, volumesRoot, mount)
	}
	return diskVolume{Name: name, Disk: disk, Mount: mount}, nil
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

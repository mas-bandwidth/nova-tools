package main

// The reap verb's tests, with the disk, the process table, the signals and the clock all
// REPLACED: nothing here lists a volume, finds a process or kills anything.
//
// Measured in the 20-run soak on the Studio, 2026-09-18: a `nova-sandbox run` killed with
// SIGKILL leaks BOTH halves of its containment. The volume stays mounted, and the command's
// `sleep 60`, reparented to PID 1 with its working directory there, holds it open against
// every unmount. Nothing survives to print `SANDBOX LEAK`, so the machine is left dirty
// and says nothing. `reap` is the answer, and the marker is its second half: a reap that
// cannot tell a working card from an orphan is a reap nobody dares run.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reapBench stands up the four seams reap reaches the machine through.
type reapBench struct {
	vols    *fakeVolumes
	procs   map[string][]int // mount -> the pids holding it
	alive   map[int]bool     // pid -> still running
	starts  map[int]string   // pid -> what `ps -o lstart=` says now
	signals []string         // every signal sent, in order, as "<pid>:<sig>"
}

func newReapBench(t *testing.T) *reapBench {
	t.Helper()
	b := &reapBench{
		procs:  map[string][]int{},
		alive:  map[int]bool{},
		starts: map[int]string{},
		vols:   &fakeVolumes{},
	}
	swap[volumeManager](t, &runVolumes, b.vols)
	swap(t, &reapProcs, func(mount string) ([]int, error) { return b.procs[mount], nil })
	swap(t, &reapSignal, func(pid int, sig syscall.Signal) error {
		b.signals = append(b.signals, fmt.Sprintf("%d:%d", pid, sig))
		if sig == syscall.SIGKILL {
			b.alive[pid] = false
		}
		return nil
	})
	swap(t, &reapAlive, func(pid int) bool { return b.alive[pid] })
	swap(t, &reapProcStart, func(pid int) (string, error) {
		if s, ok := b.starts[pid]; ok {
			return s, nil
		}
		return "", fmt.Errorf("no such process %d", pid)
	})
	// The grace is production code's own wait and never a test's.
	swap(t, &reapGraceSleep, func() {})
	return b
}

// volume adds one nova- volume to the fake machine, with a real directory playing its
// mount point so the owner marker is a real file.
func (b *reapBench) volume(t *testing.T, name, disk string) string {
	t.Helper()
	mount := t.TempDir()
	b.vols.listed = append(b.vols.listed, diskVolume{Name: volumePrefix + name, Disk: disk, Mount: mount})
	return mount
}

// What reap does with each kind of nova- volume, and the exit that tells a caller whether
// the machine is clean.
func TestReapKillsWhatHeldAnOrphanedVolumeAndDeletesIt(t *testing.T) {
	const started = "Fri Sep 18 11:26:37 2026"
	untouched := func(t *testing.T, b *reapBench) {
		assert.Empty(t, b.signals, "a process was signalled")
		assert.NotContains(t, strings.Join(b.vols.calls, " "), "delete:", "a volume was deleted")
	}
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, b *reapBench)
		dry   bool
		code  int
		says  []string
		then  func(t *testing.T, b *reapBench)
	}{
		// The SIGKILL case, whole: a volume nobody owns, with a process still holding it open.
		// TERM first, and only then KILL: a process with work in hand is asked before it is
		// taken, which is the same grace the run verb gives its own group.
		{name: "kills what held an orphaned volume and deletes it", setup: func(t *testing.T, b *reapBench) {
			b.procs[b.volume(t, "orphan", "disk3s9")] = []int{7001}
			b.alive[7001] = true
		}, says: []string{"SANDBOX REAP volume=nova-orphan procs=1 deleted=yes", "SANDBOX REAP OK volumes=1"}, then: func(t *testing.T, b *reapBench) {
			want := fmt.Sprintf("7001:%d", syscall.SIGTERM)
			if assert.NotEmpty(t, b.signals, "want %s first", want) {
				assert.Equal(t, want, b.signals[0], "the reap's first signal")
			}
			assert.Contains(t, strings.Join(b.vols.calls, " "), "delete:disk3s9", "the orphaned volume was not deleted")
		}},
		// The guard: a marker naming a LIVE run with the start time it was written with is
		// never touched; a reap that takes a volume from a working card destroys the work it
		// was called to protect.
		{name: "never takes a volume from a live run", setup: func(t *testing.T, b *reapBench) {
			mount := b.volume(t, "live", "disk3s8")
			b.starts[7100] = started
			testkit.WriteFile(t, filepath.Join(mount, ownerMarker), "pid=7100\nstart="+started+"\n", 0o600)
			b.alive[7100] = true
			b.procs[mount] = []int{7100}
		}, says: []string{"SANDBOX REAP volume=nova-live procs=1 deleted=no"}, then: untouched},
		// A pid is a small number the OS hands out again: a marker naming a pid that is alive
		// but STARTED AT A DIFFERENT TIME is a dead run's marker on a recycled number, and the
		// volume under it is an orphan. The guard is the pid AND the start time.
		{name: "reads the start time and not just the pid", setup: func(t *testing.T, b *reapBench) {
			mount := b.volume(t, "recycled", "disk3s7")
			testkit.WriteFile(t, filepath.Join(mount, ownerMarker), "pid=7200\nstart="+started+"\n", 0o600)
			b.alive[7200] = true
			b.starts[7200] = "Fri Sep 18 14:02:11 2026"
		}, says: []string{"SANDBOX REAP volume=nova-recycled procs=0 deleted=yes"}},
		// --dry-run touches nothing, and a dry run that FOUND an orphan is exitLeak because the
		// machine still holds it.
		{name: "dry run touches nothing", setup: func(t *testing.T, b *reapBench) {
			b.procs[b.volume(t, "orphan", "disk3s9")] = []int{7300}
			b.alive[7300] = true
		}, dry: true, code: exitLeak, says: []string{"SANDBOX REAP volume=nova-orphan procs=1 deleted=no"}, then: untouched},
		// A clean machine is one line and exit 0, which makes the dry run a gate a card can end on.
		{name: "on a clean machine is exit zero", setup: func(*testing.T, *reapBench) {}, dry: true, says: []string{"SANDBOX REAP OK volumes=0"}},
		// A delete that fails is exit 3, the status a leaked volume costs the run verb: a
		// caller that read 0 would believe the machine was clean.
		{name: "exits three when a volume remains", setup: func(t *testing.T, b *reapBench) {
			b.volume(t, "stuck", "disk3s6")
			b.vols.deleteErr = fmt.Errorf("Resource busy")
		}, code: exitLeak, says: []string{"SANDBOX REAP volume=nova-stuck procs=0 deleted=no"}},
		// A marker that is not a marker is an ORPHAN, never a live run: the other bias would
		// keep a volume nobody is using forever, the leak the verb exists to end.
		{name: "an unreadable marker is an orphan", setup: func(t *testing.T, b *reapBench) {
			testkit.WriteFile(t, filepath.Join(b.volume(t, "junk", "disk3s5"), ownerMarker), "not a marker\n", 0o600)
		}, says: []string{"SANDBOX REAP volume=nova-junk procs=0 deleted=yes"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newReapBench(t)
			c.setup(t, b)
			r := testkit.Streams(func(_ []string, _, stderr io.Writer) int { return reapAll(c.dry, stderr) }).Do(t).Exit(c.code)
			for _, s := range c.says {
				assert.Contains(t, r.Stderr, s, r)
			}
			if c.then != nil {
				c.then(t, b)
			}
		})
	}
}

// The marker is the run verb's, written before the command starts, so that a reap after a
// SIGKILL can tell that volume from one a working card is using; a pid alone is a number
// the OS hands out again, so it carries the start time too.
func TestTheRunVerbWritesAnOwnerMarkerAtTheVolumeRoot(t *testing.T) {
	b := newRunBench(t, 0)
	b.once(t, runFlagsFor(t)...).Exit(0)
	got := testkit.ReadFile(t, filepath.Join(b.vols.mount, ownerMarker))
	assert.Contains(t, got, fmt.Sprintf("pid=%d", os.Getpid()), "the marker does not name the running tool's pid")
	assert.Contains(t, got, "start=")
}

// Every other platform refuses the verb for the reason `run` refuses: there are no
// disposable volumes there to reap, and reap and run share one platform gate.
func TestReapRefusesWhereThereAreNoDisposableVolumes(t *testing.T) {
	t.Parallel()
	line, remedy, refused := noDisposableBody("linux")
	require.True(t, refused, "linux was not refused")
	require.Contains(t, line, "reason=no_sandbox")
	require.NotEmpty(t, remedy)
}

// `reap --help` answers the question rather than complaining about the argv that asked
// it, the way `run --help` does (ONBOARDING.md point 2).
func TestReapHelpIsExitZeroOnStdout(t *testing.T) {
	t.Parallel()
	r := testkit.Streams(reapVerb).Do(t, "--help").Exit(0)
	for _, want := range []string{"nova-sandbox reap", "--dry-run"} {
		assert.Contains(t, r.Stdout, want, r)
	}
}

// A flag the verb does not have is a refusal that names it, not a silent ignore.
func TestReapRefusesAFlagItDoesNotHave(t *testing.T) {
	t.Parallel()
	testkit.Streams(reapVerb).Do(t, "--force").ExitErr(125, "--force")
}

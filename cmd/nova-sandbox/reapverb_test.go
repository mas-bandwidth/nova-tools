package main

// The reap verb's tests, with the disk, the process table, the signals and the clock all
// REPLACED. Nothing here lists a volume, finds a process or kills anything.
//
// What they are about, measured in the 20-run soak on the Studio, 2026-09-18: a
// `nova-sandbox run` killed with SIGKILL leaks BOTH halves of its containment. The tool
// dies with no chance to delete, so the volume stays mounted; and the command's own
// `sleep 60` is reparented to PID 1 with its working directory on that volume, which
// holds it open against every unmount. No `SANDBOX LEAK` is printed, because nothing
// survives to print it, and `nova-sandbox check` only asks what the backend can enforce.
// The machine is left dirty and says nothing, which is the one failure the run verb's
// whole contract is about.
//
// `reap` is the answer, and the second half of it is the marker: a reap that cannot tell
// a working card from an orphan is a reap nobody dares run.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// reapBench stands up the four seams reap reaches the machine through.
type reapBench struct {
	vols    *fakeVolumes
	procs   map[string][]int  // mount -> the pids holding it
	alive   map[int]bool      // pid -> still running
	starts  map[int]string    // pid -> what `ps -o lstart=` says now
	signals []string          // every signal sent, in order, as "<pid>:<sig>"
	mounts  map[string]string // volume name -> the directory that plays its mount
}

func newReapBench(t *testing.T) *reapBench {
	t.Helper()
	b := &reapBench{
		procs:  map[string][]int{},
		alive:  map[int]bool{},
		starts: map[int]string{},
		mounts: map[string]string{},
		vols:   &fakeVolumes{},
	}
	oldVols, oldProcs, oldSignal, oldAlive, oldStart, oldGrace :=
		runVolumes, reapProcs, reapSignal, reapAlive, reapProcStart, reapGraceSleep
	t.Cleanup(func() {
		runVolumes, reapProcs, reapSignal, reapAlive, reapProcStart, reapGraceSleep =
			oldVols, oldProcs, oldSignal, oldAlive, oldStart, oldGrace
	})

	runVolumes = b.vols
	reapProcs = func(mount string) ([]int, error) { return b.procs[mount], nil }
	reapSignal = func(pid int, sig syscall.Signal) error {
		b.signals = append(b.signals, fmt.Sprintf("%d:%d", pid, sig))
		if sig == syscall.SIGKILL {
			b.alive[pid] = false
		}
		return nil
	}
	reapAlive = func(pid int) bool { return b.alive[pid] }
	reapProcStart = func(pid int) (string, error) {
		if s, ok := b.starts[pid]; ok {
			return s, nil
		}
		return "", fmt.Errorf("no such process %d", pid)
	}
	// The grace is production code's own wait and never a test's.
	reapGraceSleep = func() {}
	return b
}

// volume adds one nova- volume to the fake machine, with a real directory playing its
// mount point so the owner marker is a real file.
func (b *reapBench) volume(t *testing.T, name, disk string) string {
	t.Helper()
	mount := t.TempDir()
	b.mounts[name] = mount
	b.vols.listed = append(b.vols.listed, diskVolume{Name: volumePrefix + name, Disk: disk, Mount: mount})
	return mount
}

// owner writes the marker a live run leaves at its volume root.
func (b *reapBench) owner(t *testing.T, mount string, pid int, start string) {
	t.Helper()
	b.starts[pid] = start
	if err := os.WriteFile(filepath.Join(mount, ownerMarker), []byte(fmt.Sprintf("pid=%d\nstart=%s\n", pid, start)), 0o600); err != nil {
		t.Fatalf("write the owner marker: %v", err)
	}
}

func reapOnce(t *testing.T, dryRun bool) (int, string) {
	t.Helper()
	var errb bytes.Buffer
	return reapAll(dryRun, &errb), errb.String()
}

// The SIGKILL case, whole: a volume nobody owns, with a process still holding it open.
// The process is killed, the volume is deleted, and one line says what happened.
func TestReapKillsWhatHeldAnOrphanedVolumeAndDeletesIt(t *testing.T) {
	b := newReapBench(t)
	mount := b.volume(t, "orphan", "disk3s9")
	b.procs[mount] = []int{7001}
	b.alive[7001] = true

	code, errOut := reapOnce(t, false)
	if code != 0 {
		t.Fatalf("a reap that cleaned the machine is exit 0: got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP volume=nova-orphan procs=1 deleted=yes") {
		t.Errorf("the reap did not report the volume it took:\n%s", errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP OK volumes=1") {
		t.Errorf("the reap printed no closing count:\n%s", errOut)
	}
	// TERM first, and only then KILL: a process with work in hand is asked before it is
	// taken, which is the same grace the run verb gives its own group.
	want := fmt.Sprintf("7001:%d", syscall.SIGTERM)
	if len(b.signals) == 0 || b.signals[0] != want {
		t.Errorf("the reap's first signal was %v, want %s first", b.signals, want)
	}
	if !strings.Contains(strings.Join(b.vols.calls, " "), "delete:disk3s9") {
		t.Errorf("the orphaned volume was not deleted: %v", b.vols.calls)
	}
}

// And the guard. A volume whose owner marker names a LIVE run with the start time it was
// written with is never touched: a reap that takes a volume out from under a working card
// destroys the work it was called to protect.
func TestReapNeverTakesAVolumeFromALiveRun(t *testing.T) {
	b := newReapBench(t)
	mount := b.volume(t, "live", "disk3s8")
	b.owner(t, mount, 7100, "Fri Sep 18 11:26:37 2026")
	b.alive[7100] = true
	b.procs[mount] = []int{7100}

	code, errOut := reapOnce(t, false)
	if code != 0 {
		t.Fatalf("a live run is not a failure of the reap: got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP volume=nova-live procs=1 deleted=no") {
		t.Errorf("the reap did not report the live volume it left alone:\n%s", errOut)
	}
	if len(b.signals) != 0 {
		t.Errorf("the reap signalled a live run's processes: %v", b.signals)
	}
	if strings.Contains(strings.Join(b.vols.calls, " "), "delete:") {
		t.Errorf("the reap deleted a live run's volume: %v", b.vols.calls)
	}
}

// The other half of the marker, and the reason the start time is in it: a pid is a small
// number the operating system hands out again. A marker naming a pid that is alive but
// STARTED AT A DIFFERENT TIME is a dead run's marker on a recycled number, and the volume
// under it is an orphan.
func TestReapReadsTheStartTimeAndNotJustThePid(t *testing.T) {
	b := newReapBench(t)
	mount := b.volume(t, "recycled", "disk3s7")
	b.owner(t, mount, 7200, "Fri Sep 18 11:26:37 2026")
	b.alive[7200] = true
	// Same pid, a process that started later: the run that wrote the marker is gone.
	b.starts[7200] = "Fri Sep 18 14:02:11 2026"

	code, errOut := reapOnce(t, false)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP volume=nova-recycled procs=0 deleted=yes") {
		t.Errorf("a marker on a RECYCLED pid held the volume; the guard is the pid AND the start time:\n%s", errOut)
	}
}

// --dry-run prints and touches nothing, and says the machine is not clean.
func TestReapDryRunTouchesNothing(t *testing.T) {
	b := newReapBench(t)
	mount := b.volume(t, "orphan", "disk3s9")
	b.procs[mount] = []int{7300}
	b.alive[7300] = true

	code, errOut := reapOnce(t, true)
	if code != exitLeak {
		t.Fatalf("a dry run that FOUND an orphan is exit %d, because the machine still holds it: got %d\n%s", exitLeak, code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP volume=nova-orphan procs=1 deleted=no") {
		t.Errorf("the dry run did not report what a real one would take:\n%s", errOut)
	}
	if len(b.signals) != 0 {
		t.Errorf("--dry-run signalled a process: %v", b.signals)
	}
	if strings.Contains(strings.Join(b.vols.calls, " "), "delete:") {
		t.Errorf("--dry-run deleted a volume: %v", b.vols.calls)
	}
}

// A clean machine is one line and exit 0, which is what makes the dry run a gate a card
// can end on.
func TestReapOnACleanMachineIsExitZero(t *testing.T) {
	newReapBench(t)
	code, errOut := reapOnce(t, true)
	if code != 0 || !strings.Contains(errOut, "SANDBOX REAP OK volumes=0") {
		t.Fatalf("a machine with no nova- volumes is `SANDBOX REAP OK volumes=0` and exit 0: got %d\n%s", code, errOut)
	}
}

// A delete that fails is exit 3, the same status a leaked volume costs the run verb: the
// machine still holds it, and a caller that read 0 would believe it was clean.
func TestReapExitsThreeWhenAVolumeRemains(t *testing.T) {
	b := newReapBench(t)
	b.volume(t, "stuck", "disk3s6")
	b.vols.deleteErr = fmt.Errorf("Resource busy")

	code, errOut := reapOnce(t, false)
	if code != exitLeak {
		t.Fatalf("a volume that would not delete is exit %d: got %d\n%s", exitLeak, code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX REAP volume=nova-stuck procs=0 deleted=no") {
		t.Errorf("the failed delete was not reported on the volume's own line:\n%s", errOut)
	}
}

// The marker is the run verb's, written before the command starts, so that a reap after a
// SIGKILL can tell that volume from one a working card is using.
func TestTheRunVerbWritesAnOwnerMarkerAtTheVolumeRoot(t *testing.T) {
	b := newRunBench(t, 0)
	if code, errOut := runOnce(t, b, runFlagsFor(t)...); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(b.vols.mount, ownerMarker))
	if err != nil {
		t.Fatalf("the run left no %s at its volume root, so a reap cannot tell it from an orphan: %v", ownerMarker, err)
	}
	got := string(raw)
	if !strings.Contains(got, fmt.Sprintf("pid=%d", os.Getpid())) {
		t.Errorf("the marker does not name the running tool's pid:\n%s", got)
	}
	if !strings.Contains(got, "start=") {
		t.Errorf("the marker carries no start time, and a pid alone is a number the OS hands out again:\n%s", got)
	}
}

// A marker that is not there, or is not a marker, is an ORPHAN and never a live run. The
// bias has to be this way round: an unreadable marker on a volume nobody is using would
// otherwise keep that volume forever, which is the leak the verb exists to end.
func TestAnUnreadableMarkerIsAnOrphan(t *testing.T) {
	b := newReapBench(t)
	mount := b.volume(t, "junk", "disk3s5")
	if err := os.WriteFile(filepath.Join(mount, ownerMarker), []byte("not a marker\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, errOut := reapOnce(t, false)
	if code != 0 || !strings.Contains(errOut, "SANDBOX REAP volume=nova-junk procs=0 deleted=yes") {
		t.Fatalf("a volume with an unreadable marker was kept; it is an orphan: exit %d\n%s", code, errOut)
	}
}

// Every other platform refuses the verb for the reason `run` refuses: there are no
// disposable volumes there to reap.
func TestReapRefusesWhereThereAreNoDisposableVolumes(t *testing.T) {
	line, remedy, refused := noDisposableBody("linux")
	if !refused || !strings.Contains(line, "reason=no_sandbox") || remedy == "" {
		t.Fatalf("reap and run share one platform gate, and it did not refuse linux: %q %q %v", line, remedy, refused)
	}
}

// `reap --help` answers the question rather than complaining about the argv that asked
// it, the way `run --help` does (ONBOARDING.md point 2).
func TestReapHelpIsExitZeroOnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	if code := reapVerb([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("`reap --help` exited %d\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nova-sandbox reap") || !strings.Contains(out.String(), "--dry-run") {
		t.Errorf("`reap --help` does not print the verb's usage:\n%s", out.String())
	}
}

// A flag the verb does not have is a refusal that names it, not a silent ignore.
func TestReapRefusesAFlagItDoesNotHave(t *testing.T) {
	var out, errb bytes.Buffer
	code := reapVerb([]string{"--force"}, &out, &errb)
	if code != 125 || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("`reap --force` is a refusal naming the flag: got %d\n%s", code, errb.String())
	}
}

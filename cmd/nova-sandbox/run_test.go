package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// These tests run the run verb's whole logic with the disk, the child and the signals
// REPLACED: no volume is created, no diskutil is executed and no process is started. What
// is under test is the contract that cannot be proved by a single end-to-end run — that
// a created volume is deleted on EVERY path out, that a delete that fails is reported
// rather than swallowed, that a duplicate name is refused before anything is made, and
// that a timeout kills the whole process group rather than the leader. The one real run,
// which creates a real 64m volume and proves it is gone, is in run_e2e_darwin_test.go
// behind a build tag, because eight CI runners share this machine and a suite that made
// volumes on every `go test` would be a hazard rather than a test.

// fakeVolumes is the disk seam. It records the calls IN ORDER, because the order is the
// contract: nothing runs before a volume exists and nothing exits before it is gone.
type fakeVolumes struct {
	mount        string
	calls        []string
	exists       bool
	used         int64
	containerErr error
	existsErr    error
	createErr    error
	deleteErr    error
}

func (f *fakeVolumes) Container() (string, error) {
	f.calls = append(f.calls, "container")
	if f.containerErr != nil {
		return "", f.containerErr
	}
	return "disk3", nil
}

func (f *fakeVolumes) Exists(name string) (bool, error) {
	f.calls = append(f.calls, "exists:"+name)
	return f.exists, f.existsErr
}

func (f *fakeVolumes) Create(container, name, size string) (diskVolume, error) {
	f.calls = append(f.calls, "create:"+container+":"+name+":"+size)
	if f.createErr != nil {
		return diskVolume{}, f.createErr
	}
	return diskVolume{Name: name, Disk: "disk3s9", Mount: f.mount}, nil
}

func (f *fakeVolumes) Used(string) (int64, error) {
	f.calls = append(f.calls, "used")
	return f.used, nil
}

func (f *fakeVolumes) Delete(disk string) error {
	f.calls = append(f.calls, "delete:"+disk)
	return f.deleteErr
}

// runBench stands the three seams up around one temporary directory that plays the
// mounted volume, and puts them all back afterwards.
type runBench struct {
	vols   *fakeVolumes
	killed []syscall.Signal
	sigs   chan os.Signal
}

func newRunBench(t *testing.T, code int) *runBench {
	t.Helper()
	mount := t.TempDir()
	if r, err := filepath.EvalSymlinks(mount); err == nil {
		mount = r
	}
	b := &runBench{vols: &fakeVolumes{mount: mount, used: 4096}, sigs: make(chan os.Signal)}

	oldVols, oldExec, oldSigs := runVolumes, runExec, runSignals
	t.Cleanup(func() { runVolumes, runExec, runSignals = oldVols, oldExec, oldSigs })

	runVolumes = b.vols
	runSignals = func() (<-chan os.Signal, func()) { return b.sigs, func() {} }
	runExec = func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (<-chan int, func(syscall.Signal), error) {
		done := make(chan int, 1)
		done <- code
		return done, func(sig syscall.Signal) { b.killed = append(b.killed, sig) }, nil
	}
	return b
}

// shellOf is a command that resolves on this platform, because rule 5 resolves the
// command before any policy is built and a name that is on no PATH would refuse for the
// wrong reason. The fake executor never runs it.
func shellOf(t *testing.T) []string {
	t.Helper()
	return newJob(t).shell(t)
}

func runFlagsFor(t *testing.T, extra ...string) []string {
	t.Helper()
	args := append([]string{"--name", "j1", "--size", "64m"}, extra...)
	args = append(args, "--")
	return append(args, shellOf(t)...)
}

func runOnce(t *testing.T, b *runBench, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	f := parseRun(args)
	code := runDisposable(f, 0, nil, &out, &errb, []string{"PATH=" + os.Getenv("PATH")})
	return code, errb.String()
}

// The contract in one line: whatever the command did, the place it did it in is gone.
func TestRunCreatesTheVolumeRunsAndAlwaysDeletesIt(t *testing.T) {
	for _, code := range []int{0, 7, 137} {
		b := newRunBench(t, code)
		got, errOut := runOnce(t, b, runFlagsFor(t)...)
		if got != code {
			t.Fatalf("the run verb returned %d for a command that exited %d; the status belongs to the wrapped command\n%s", got, code, errOut)
		}
		order := strings.Join(b.vols.calls, " ")
		if !strings.HasPrefix(order, "container exists:nova-j1 create:disk3:nova-j1:64m") || !strings.HasSuffix(order, "delete:disk3s9") {
			t.Fatalf("the calls are not look-create-run-delete: %s", order)
		}
		if !strings.Contains(errOut, "SANDBOX DONE name=j1 exit="+strconv.Itoa(code)) || !strings.Contains(errOut, "freed=4096") {
			t.Fatalf("the receipt does not name the exit and what was freed:\n%s", errOut)
		}
		if !strings.Contains(errOut, "wall=") {
			t.Fatalf("the receipt does not carry wall=:\n%s", errOut)
		}
	}
}

// A refusal AFTER the volume exists still deletes it. This is the path a cleanup step
// would forget: the tool said no, so nothing ran, so nothing looks like it needs undoing.
func TestRunDeletesTheVolumeWhenTheWallItselfRefuses(t *testing.T) {
	b := newRunBench(t, 0)
	args := []string{"--name", "j1", "--size", "64m", "--read", "/no/such/directory", "--"}
	args = append(args, shellOf(t)...)
	code, errOut := runOnce(t, b, args...)
	if code != 125 {
		t.Fatalf("a --read that does not exist is the tool's own refusal, 125, and got %d\n%s", code, errOut)
	}
	if !strings.Contains(strings.Join(b.vols.calls, " "), "delete:disk3s9") {
		t.Fatalf("a refusal after the volume was made left it on the disk: %s", strings.Join(b.vols.calls, " "))
	}
	if !strings.Contains(errOut, "SANDBOX DONE name=j1") {
		t.Fatalf("a refused run printed no receipt:\n%s", errOut)
	}
}

// A name already on the machine is refused BEFORE anything is made: a run never joins a
// place it did not create, because it would delete that place on the way out.
func TestRunRefusesAVolumeNameThatIsAlreadyThere(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.exists = true
	code, errOut := runOnce(t, b, runFlagsFor(t)...)
	if code != 125 || !strings.Contains(errOut, "reason=volume_exists") {
		t.Fatalf("a duplicate name is not refused with reason=volume_exists: exit %d\n%s", code, errOut)
	}
	for _, call := range b.vols.calls {
		if strings.HasPrefix(call, "create:") || strings.HasPrefix(call, "delete:") {
			t.Fatalf("a refused run touched the disk: %s", strings.Join(b.vols.calls, " "))
		}
	}
}

// The one failure the verb cannot repair is the one it must never hide.
func TestRunReportsALeakAndPaysForItWithTheExitCode(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.deleteErr = errors.New("Unable to unmount volume for deletion")
	code, errOut := runOnce(t, b, runFlagsFor(t)...)
	if code != exitLeak {
		t.Fatalf("a volume that could not be deleted exited %d, want %d: a caller that reads 0 believes the machine is clean\n%s", code, exitLeak, errOut)
	}
	if !strings.Contains(errOut, `SANDBOX LEAK name=j1 volume=disk3s9 remedy="diskutil apfs deleteVolume disk3s9"`) {
		t.Fatalf("the leak line does not name the volume and the one command that removes it:\n%s", errOut)
	}
	if !strings.Contains(errOut, "freed=0") {
		t.Fatalf("a leaked volume freed nothing and the receipt should say so:\n%s", errOut)
	}
}

// A container that cannot be read is a refusal with a remedy, and nothing is made.
func TestRunRefusesWhenTheContainerCannotBeRead(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.containerErr = errors.New("no such thing")
	code, errOut := runOnce(t, b, runFlagsFor(t)...)
	if code != 125 || !strings.Contains(errOut, "reason=no_container") {
		t.Fatalf("an unreadable container is not refused with reason=no_container: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "--container disk3") {
		t.Fatalf("the refusal does not name the flag that answers it:\n%s", errOut)
	}
}

// Every independent problem in ONE run, and each one naming the form its flag wants.
func TestRunNamesEveryBadFlagAtOnce(t *testing.T) {
	var out, errb bytes.Buffer
	code := runVerb([]string{"--name", "a b", "--size", "lots", "--timeout", "soon", "--container", "sda1"},
		nil, &out, &errb, []string{"PATH=" + os.Getenv("PATH")})
	if code != 125 {
		t.Fatalf("bad flags are the tool's own refusal, 125, and got %d\n%s", code, errb.String())
	}
	for _, want := range []string{"reason=no_name", "reason=bad_size", "reason=bad_timeout", "reason=no_container", "reason=no_command"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("a run with five problems does not report %s; a first run must not be sequenced into as many runs as it has mistakes:\n%s", want, errb.String())
		}
	}
	if !strings.Contains(errb.String(), runRemedy) {
		t.Errorf("the refusal carries no remedy line:\n%s", errb.String())
	}
}

// The verb refuses where there is no disposable place, and says where the disposable
// place is on that platform instead.
func TestTheVerbRefusesWherethereIsNoDisposableBody(t *testing.T) {
	if _, _, refused := noDisposableBody("darwin"); refused {
		t.Fatalf("darwin has the body and must not refuse")
	}
	for _, goos := range []string{"linux", "windows"} {
		line, remedy, refused := noDisposableBody(goos)
		if !refused {
			t.Fatalf("%s has no disposable-volume body and must refuse rather than use an ordinary directory", goos)
		}
		if !strings.Contains(line, "reason=no_sandbox") || !strings.Contains(line, goos) {
			t.Errorf("the refusal on %s does not name the platform and the reason: %s", goos, line)
		}
		if !strings.Contains(remedy, "--write <dir>") || !strings.Contains(remedy, "image") {
			t.Errorf("the remedy on %s does not name the container path to use instead: %s", goos, remedy)
		}
	}
}

// The name and size shapes, both ways round: what is accepted and what is not.
func TestTheNameAndSizeShapesAreNarrow(t *testing.T) {
	for _, ok := range []string{"j1", "lane-sandbox", "a.b_c", "A1"} {
		if !okName(ok) {
			t.Errorf("--name %q is a name this verb should take", ok)
		}
	}
	for _, bad := range []string{"", "-lead", "a b", "a/b", "../x", "a;rm", strings.Repeat("x", 33), "naïve"} {
		if okName(bad) {
			t.Errorf("--name %q reaches a volume name, a path under /Volumes and a diskutil argument, and must be refused", bad)
		}
	}
	for _, ok := range []string{"64m", "8g", "1t", "512", "1.5g", "64mb", "8GB"} {
		if !okSize(ok) {
			t.Errorf("--size %q is a quota this verb should take", ok)
		}
	}
	for _, bad := range []string{"", "g", "50%", "-8g", "8 g", "8gx", "eight"} {
		if okSize(bad) {
			t.Errorf("--size %q is not a quota and must be refused", bad)
		}
	}
	for _, ok := range []string{"disk3", "disk12"} {
		if !okContainer(ok) {
			t.Errorf("--container %q is a container reference", ok)
		}
	}
	for _, bad := range []string{"", "disk", "disk3s1", "sda", "disk3;reboot"} {
		if okContainer(bad) {
			t.Errorf("--container %q is not a container reference and must be refused", bad)
		}
	}
}

// supervise is the part the volume's life hangs on: nothing of the run may be alive when
// the delete is attempted, or the unmount fails and a clean exit becomes a leak. These
// four assert the ORDER of the signals, with no process, no clock and no timer.
func TestSuperviseSweepsTheGroupAfterAnOrdinaryExit(t *testing.T) {
	done := make(chan int, 1)
	done <- 3
	var killed []syscall.Signal
	code, timedOut := supervise(done, nil, nil, nil, func(s syscall.Signal) { killed = append(killed, s) })
	if code != 3 || timedOut {
		t.Fatalf("an ordinary exit is the command's own status: got %d timedOut=%v", code, timedOut)
	}
	if len(killed) != 1 || killed[0] != syscall.SIGKILL {
		t.Fatalf("the group was not swept after the leader exited: %v; a forked child that outlives it holds the volume open", killed)
	}
}

func TestSuperviseKillsTheWholeGroupOnATimeout(t *testing.T) {
	done := make(chan int, 1)
	deadline := make(chan time.Time, 1)
	deadline <- time.Time{}
	grace := make(chan time.Time, 1)
	grace <- time.Time{}
	var killed []syscall.Signal
	kill := func(s syscall.Signal) {
		killed = append(killed, s)
		if s == syscall.SIGKILL {
			select {
			case done <- 137:
			default:
			}
		}
	}
	code, timedOut := supervise(done, deadline, grace, nil, kill)
	if code != exitTimeout || !timedOut {
		t.Fatalf("a deadline that passed is exit %d and timedOut: got %d %v", exitTimeout, code, timedOut)
	}
	want := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL, syscall.SIGKILL}
	if len(killed) != len(want) {
		t.Fatalf("the timeout did not term, kill and sweep the group: %v", killed)
	}
	for i := range want {
		if killed[i] != want[i] {
			t.Fatalf("the timeout signalled %v, want %v", killed, want)
		}
	}
}

func TestSuperviseStopsAtTheTermWhenTheGroupDies(t *testing.T) {
	done := make(chan int, 1)
	deadline := make(chan time.Time, 1)
	deadline <- time.Time{}
	grace := make(chan time.Time) // never fires: the group answered the SIGTERM
	var killed []syscall.Signal
	kill := func(s syscall.Signal) {
		killed = append(killed, s)
		if s == syscall.SIGTERM {
			done <- 143
		}
	}
	code, timedOut := supervise(done, deadline, grace, nil, kill)
	if code != exitTimeout || !timedOut {
		t.Fatalf("the deadline is what ended the run whatever signal did it: got %d %v", code, timedOut)
	}
	want := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	if len(killed) != len(want) || killed[0] != want[0] || killed[1] != want[1] {
		t.Fatalf("a group that answered the term was still hit with a kill it did not need, or not swept: %v", killed)
	}
}

func TestSuperviseForwardsATerminatingSignalToTheGroup(t *testing.T) {
	done := make(chan int, 1)
	sigs := make(chan os.Signal, 1)
	sigs <- syscall.SIGINT
	var killed []syscall.Signal
	kill := func(s syscall.Signal) {
		killed = append(killed, s)
		if s == syscall.SIGINT {
			done <- 130
		}
	}
	code, timedOut := supervise(done, nil, nil, sigs, kill)
	if code != 128+int(syscall.SIGINT) || timedOut {
		t.Fatalf("a caller's SIGINT is 128+N and not a timeout: got %d %v", code, timedOut)
	}
	if len(killed) != 2 || killed[0] != syscall.SIGINT || killed[1] != syscall.SIGKILL {
		t.Fatalf("the caller's signal did not reach the group and the group was not swept after it: %v", killed)
	}
}

// withHome is rule 9 for a home the TOOL made: the child sees one HOME and it is the one
// on the disposable volume, whatever the caller's own environment carried.
func TestTheChildsHomeIsTheOneOnTheVolume(t *testing.T) {
	got := withHome([]string{"HOME=/Users/someone", "PATH=/bin", "HOMEBREW_PREFIX=/opt/homebrew"}, "/Volumes/nova-j1/home")
	homes := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "HOME=") {
			homes++
			if kv != "HOME=/Volumes/nova-j1/home" {
				t.Errorf("the child's HOME is %q, not the one on the volume", kv)
			}
		}
	}
	if homes != 1 {
		t.Errorf("the child's environment carries %d HOME entries, want exactly 1: %v", homes, got)
	}
	if !contains(got, "HOMEBREW_PREFIX=/opt/homebrew") || !contains(got, "PATH=/bin") {
		t.Errorf("a variable that merely begins with HOME was dropped: %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

// The tmp directory the wall points TMPDIR at is on the volume, so it goes with it. This
// is the whole reason there is no cleanup step: the temp files are not on the boot disk to
// begin with.
func TestTheTempDirectoryIsOnTheVolume(t *testing.T) {
	b := newRunBench(t, 0)
	if _, errOut := runOnce(t, b, runFlagsFor(t)...); !strings.Contains(errOut, "SANDBOX OK") {
		t.Fatalf("no wall was reported:\n%s", errOut)
	}
	tmp := filepath.Join(b.vols.mount, ".nova-sandbox-tmp")
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("the one directory this tool makes is not on the volume: %s", err)
	}
}

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	listed       []diskVolume
	containerErr error
	existsErr    error
	createErr    error
	deleteErr    error
	listErr      error
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

func (f *fakeVolumes) List() ([]diskVolume, error) {
	f.calls = append(f.calls, "list")
	return f.listed, f.listErr
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

	swap[volumeManager](t, &runVolumes, b.vols)
	swap(t, &runSignals, func() (<-chan os.Signal, func()) { return b.sigs, func() {} })
	swap(t, &runExec, func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
		done := make(chan int, 1)
		done <- code
		return startedRun{
			done: done,
			kill: func(sig syscall.Signal) { b.killed = append(b.killed, sig) },
			pid:  4242,
		}, nil
	})
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

// disposable drives the darwin verb from the look onwards, with the argv parsed and the
// deadline given in the verb's own units; once is that with no deadline, on a bench.
func disposable(t *testing.T, deadline time.Duration, args ...string) testkit.Ran {
	t.Helper()
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return runDisposable(parseRun(args), deadline, stdin, stdout, stderr, hostPath())
	}).Do(t, args...)
}

func (b *runBench) once(t *testing.T, args ...string) testkit.Ran {
	t.Helper()
	return disposable(t, 0, args...)
}

// The contract in one line: whatever the command did, the place it did it in is gone.
func TestRunCreatesTheVolumeRunsAndAlwaysDeletesIt(t *testing.T) {
	for _, code := range []int{0, 7, 137} {
		b := newRunBench(t, code)
		r := b.once(t, runFlagsFor(t)...)
		require.Equal(t, code, r.Code, "the run verb returned %d for a command that exited %d; the status belongs to the wrapped command\n%s", r.Code, code, r.Stderr)
		order := strings.Join(b.vols.calls, " ")
		require.True(t, strings.HasPrefix(order, "container exists:nova-j1 create:disk3:nova-j1:64m"), "the calls are not look-create-run-delete: %s", order)
		require.True(t, strings.HasSuffix(order, "delete:disk3s9"), "the calls are not look-create-run-delete: %s", order)
		require.Contains(t, r.Stderr, "SANDBOX DONE name=j1 exit="+strconv.Itoa(code), "the receipt does not name the exit and what was freed:\n%s", r.Stderr)
		require.Contains(t, r.Stderr, "freed=4096", "the receipt does not name the exit and what was freed:\n%s", r.Stderr)
		require.Contains(t, r.Stderr, "wall=", "the receipt does not carry wall=:\n%s", r.Stderr)
	}
}

// A refusal AFTER the volume exists still deletes it. This is the path a cleanup step
// would forget: the tool said no, so nothing ran, so nothing looks like it needs undoing.
func TestRunDeletesTheVolumeWhenTheWallItselfRefuses(t *testing.T) {
	b := newRunBench(t, 0)
	args := []string{"--name", "j1", "--size", "64m", "--read", "/no/such/directory", "--"}
	args = append(args, shellOf(t)...)
	r := b.once(t, args...)
	require.Equal(t, 125, r.Code, "a --read that does not exist is the tool's own refusal, 125, and got %d\n%s", r.Code, r.Stderr)
	require.Contains(t, strings.Join(b.vols.calls, " "), "delete:disk3s9", "a refusal after the volume was made left it on the disk: %s", strings.Join(b.vols.calls, " "))
	require.Contains(t, r.Stderr, "SANDBOX DONE name=j1", "a refused run printed no receipt:\n%s", r.Stderr)
}

// A name already on the machine is refused BEFORE anything is made: a run never joins a
// place it did not create, because it would delete that place on the way out.
func TestRunRefusesAVolumeNameThatIsAlreadyThere(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.exists = true
	r := b.once(t, runFlagsFor(t)...)
	r.ExitErr(125, "reason=volume_exists", "a duplicate name is not refused with reason=volume_exists: exit %d\n%s", r.Code, r.Stderr)
	for _, call := range b.vols.calls {
		require.False(t, strings.HasPrefix(call, "create:"), "a refused run touched the disk: %s", strings.Join(b.vols.calls, " "))
		require.False(t, strings.HasPrefix(call, "delete:"), "a refused run touched the disk: %s", strings.Join(b.vols.calls, " "))
	}
}

// The one failure the verb cannot repair is the one it must never hide.
func TestRunReportsALeakAndPaysForItWithTheExitCode(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.deleteErr = errors.New("Unable to unmount volume for deletion")
	r := b.once(t, runFlagsFor(t)...)
	require.Equal(t, exitLeak, r.Code, "a volume that could not be deleted exited %d, want %d: a caller that reads 0 believes the machine is clean\n%s", r.Code, exitLeak, r.Stderr)
	require.Contains(t, r.Stderr, `SANDBOX LEAK name=j1 volume=disk3s9 remedy="diskutil apfs deleteVolume disk3s9"`, "the leak line does not name the volume and the one command that removes it:\n%s", r.Stderr)
	require.Contains(t, r.Stderr, "freed=0", "a leaked volume freed nothing and the receipt should say so:\n%s", r.Stderr)
}

// A volume that was made and not mounted is not a volume that could not be made, and the
// printed line has to be the one the caller can act on: the verb's own prefix would tell a
// reader the create failed, which sends them to diskutil and to the container for a fault
// in neither. The words come from the manager, which is the half that knows which of the
// two happened.
func TestRunSaysTheMountWasDeniedRatherThanTheCreateFailed(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.createErr = fmt.Errorf("%w: the volume disk3s7 was created in disk3 and is not mounted under /Volumes", errVolumeNotMounted)
	r := b.once(t, runFlagsFor(t)...)
	r.ExitErr(125, "reason=volume_failed", "a volume that came up unmounted is not refused with reason=volume_failed: exit %d\n%s", r.Code, r.Stderr)
	require.NotContains(t, r.Stderr, "could not be created", "the refusal says the volume could not be created, over an error that says it was:\n%s", r.Stderr)
	require.Contains(t, r.Stderr, "was created in disk3 and is not mounted under /Volumes", "the refusal drops what the manager said happened:\n%s", r.Stderr)
	require.Contains(t, r.Stderr, runRemedy, "the refusal carries no remedy line:\n%s", r.Stderr)
}

// Every other create failure keeps the verb's own prefix: the container is where the
// volume would have been made, and a reader of that line needs to know which one.
func TestRunNamesTheContainerWhenTheCreateItselfFails(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.createErr = errors.New("diskutil apfs addVolume: exit status 1: quota too small")
	r := b.once(t, runFlagsFor(t)...)
	r.ExitErr(125, "reason=volume_failed", "a create that failed is not refused with reason=volume_failed: exit %d\n%s", r.Code, r.Stderr)
	require.Contains(t, r.Stderr, "the disposable volume could not be created in disk3", "the refusal does not name the container the volume would have been made in:\n%s", r.Stderr)
}

// A container that cannot be read is a refusal with a remedy, and nothing is made.
func TestRunRefusesWhenTheContainerCannotBeRead(t *testing.T) {
	b := newRunBench(t, 0)
	b.vols.containerErr = errors.New("no such thing")
	r := b.once(t, runFlagsFor(t)...)
	r.ExitErr(125, "reason=no_container", "an unreadable container is not refused with reason=no_container: exit %d\n%s", r.Code, r.Stderr)
	require.Contains(t, r.Stderr, "--container disk3", "the refusal does not name the flag that answers it:\n%s", r.Stderr)
}

// Every independent problem in ONE run, and each one naming the form its flag wants.
//
// The CONTRACT is the count of runs, not the vocabulary: a first run is never sequenced
// into as many runs as it has mistakes. The vocabulary is the PLATFORM's — `--size lots`
// is `bad_size` on darwin and `size_unenforceable` on windows (W6), where no `--size` can
// be enforced at all, and windows adds the `bad_scratch` of W5 because the place there has
// no default. So the reason set is asked of `validateRun` with the platform NAMED, which
// is what that parameter is for, and both platforms are checked from either machine.
//
// Measured red on the windows CI leg (run 35367602664, job test-windows-pr): this test
// asserted darwin's set and darwin's remedy whatever platform it ran on, and the windows
// verb — which named every one of its own six problems in one refusal, correctly — failed
// it.
func TestRunNamesEveryBadFlagAtOnce(t *testing.T) {
	t.Parallel()

	badArgv := []string{"--name", "a b", "--size", "lots", "--timeout", "soon", "--container", "sda1"}
	for _, tc := range []struct {
		goos string
		want []string
	}{
		{"darwin", []string{"no_name", "bad_size", "bad_timeout", "no_container", "no_command"}},
		{"windows", []string{"no_name", "size_unenforceable", "bad_scratch", "bad_timeout", "no_container", "no_command"}},
	} {
		f := parseRun(badArgv)
		_, bad := validateRun(&f, tc.goos)
		for _, want := range tc.want {
			assert.True(t, hasReason(bad, want), "on %s, an argv with a problem per flag does not report reason=%s; every independent problem is named in ONE refusal (all: %v)",
				tc.goos, want, reasonsOf(bad))
		}
	}

	// And end to end, on the platform this test is actually running on: one refusal, exit
	// 125, and the remedy line is that platform's own — a windows reader handed the darwin
	// argv would type the very flag the next line refuses.
	r := withEnv(runVerb, hostPath()).Do(t, badArgv...)
	require.Equal(t, 125, r.Code, "bad flags are the tool's own refusal, 125, and got %d\n%s", r.Code, r.Stderr)
	for _, want := range []string{"reason=no_name", "reason=bad_timeout", "reason=no_container", "reason=no_command"} {
		assert.Contains(t, r.Stderr, want, "the refusal does not carry %s, which every platform shares:\n%s", want, r.Stderr)
	}
	assert.Contains(t, r.Stderr, remedyFor(runtime.GOOS), "the refusal carries no remedy line for %s:\n%s", runtime.GOOS, r.Stderr)
}

// The verb refuses where there is no disposable place, and says where the disposable
// place is on that platform instead.
//
// windows LEFT this list on 2026-09-18: docs/SPEC-SANDBOX.md's "Windows — the disposable
// place" (W1..W12) is built in runwin.go as a Job Object plus a per-run scratch directory,
// so the verb no longer refuses there for want of a PLACE. It still refuses there for want
// of the WALL, which is a different refusal in a different line — see
// TestWindowsRefusesWhileTheWallIsNotBuilt.
func TestTheVerbRefusesWherethereIsNoDisposableBody(t *testing.T) {
	t.Parallel()

	for _, built := range []string{"darwin", "windows"} {
		_, _, refused := noDisposableBody(built)
		require.False(t, refused, "%s has the body and must not refuse for want of a place", built)
	}
	for _, goos := range []string{"linux"} {
		line, remedy, refused := noDisposableBody(goos)
		require.True(t, refused, "%s has no disposable-volume body and must refuse rather than use an ordinary directory", goos)
		assert.Contains(t, line, "reason=no_sandbox", "the refusal on %s does not name the platform and the reason: %s", goos, line)
		assert.Contains(t, line, goos, "the refusal on %s does not name the platform and the reason: %s", goos, line)
		assert.Contains(t, remedy, "--write <dir>", "the remedy on %s does not name the container path to use instead: %s", goos, remedy)
		assert.Contains(t, remedy, "image", "the remedy on %s does not name the container path to use instead: %s", goos, remedy)
	}
}

// The name and size shapes, both ways round: what is accepted and what is not.
func TestTheNameAndSizeShapesAreNarrow(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"j1", "lane-sandbox", "a.b_c", "A1"} {
		assert.True(t, okName(ok), "--name %q is a name this verb should take", ok)
	}
	for _, bad := range []string{"", "-lead", "a b", "a/b", "../x", "a;rm", strings.Repeat("x", 33), "naïve"} {
		assert.False(t, okName(bad), "--name %q reaches a volume name, a path under /Volumes and a diskutil argument, and must be refused", bad)
	}
	for _, ok := range []string{"64m", "8g", "1t", "512", "1.5g", "64mb", "8GB"} {
		assert.True(t, okSize(ok), "--size %q is a quota this verb should take", ok)
	}
	for _, bad := range []string{"", "g", "50%", "-8g", "8 g", "8gx", "eight"} {
		assert.False(t, okSize(bad), "--size %q is not a quota and must be refused", bad)
	}
	for _, ok := range []string{"disk3", "disk12"} {
		assert.True(t, okContainer(ok), "--container %q is a container reference", ok)
	}
	for _, bad := range []string{"", "disk", "disk3s1", "sda", "disk3;reboot"} {
		assert.False(t, okContainer(bad), "--container %q is not a container reference and must be refused", bad)
	}
}

// supervise is the part the volume's life hangs on: nothing of the run may be alive when
// the delete is attempted, or the unmount fails and a clean exit becomes a leak. These
// four assert the ORDER of the signals, with no process, no clock and no timer.
func TestSuperviseSweepsTheGroupAfterAnOrdinaryExit(t *testing.T) {
	t.Parallel()

	done := make(chan int, 1)
	done <- 3
	var killed []syscall.Signal
	code, timedOut := supervise(done, nil, nil, nil, func(s syscall.Signal) { killed = append(killed, s) })
	require.Equal(t, 3, code, "an ordinary exit is the command's own status: got %d timedOut=%v", code, timedOut)
	require.False(t, timedOut, "an ordinary exit is the command's own status: got %d timedOut=%v", code, timedOut)
	require.Equal(t, []syscall.Signal{syscall.SIGKILL}, killed, "the group was not swept after the leader exited: %v; a forked child that outlives it holds the volume open", killed)
}

func TestSuperviseKillsTheWholeGroupOnATimeout(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, exitTimeout, code, "a deadline that passed is exit %d and timedOut: got %d %v", exitTimeout, code, timedOut)
	require.True(t, timedOut, "a deadline that passed is exit %d and timedOut: got %d %v", exitTimeout, code, timedOut)
	want := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL, syscall.SIGKILL}
	require.Len(t, killed, len(want), "the timeout did not term, kill and sweep the group: %v", killed)
	for i := range want {
		require.Equal(t, want[i], killed[i], "the timeout signalled %v, want %v", killed, want)
	}
}

func TestSuperviseStopsAtTheTermWhenTheGroupDies(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, exitTimeout, code, "the deadline is what ended the run whatever signal did it: got %d %v", code, timedOut)
	require.True(t, timedOut, "the deadline is what ended the run whatever signal did it: got %d %v", code, timedOut)
	want := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	require.Equal(t, want, killed, "a group that answered the term was still hit with a kill it did not need, or not swept: %v", killed)
}

func TestSuperviseForwardsATerminatingSignalToTheGroup(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, 128+int(syscall.SIGINT), code, "a caller's SIGINT is 128+N and not a timeout: got %d %v", code, timedOut)
	require.False(t, timedOut, "a caller's SIGINT is 128+N and not a timeout: got %d %v", code, timedOut)
	require.Equal(t, []syscall.Signal{syscall.SIGINT, syscall.SIGKILL}, killed, "the caller's signal did not reach the group and the group was not swept after it: %v", killed)
}

// withHome is rule 9 for a home the TOOL made: the child sees one HOME and it is the one
// on the disposable volume, whatever the caller's own environment carried.
func TestTheChildsHomeIsTheOneOnTheVolume(t *testing.T) {
	t.Parallel()

	got := withHome([]string{"HOME=/Users/someone", "PATH=/bin", "HOMEBREW_PREFIX=/opt/homebrew"}, "/Volumes/nova-j1/home")
	homes := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "HOME=") {
			homes++
			assert.Equal(t, "HOME=/Volumes/nova-j1/home", kv, "the child's HOME is %q, not the one on the volume", kv)
		}
	}
	assert.Equal(t, 1, homes, "the child's environment carries %d HOME entries, want exactly 1: %v", homes, got)
	assert.Contains(t, got, "HOMEBREW_PREFIX=/opt/homebrew", "a variable that merely begins with HOME was dropped: %v", got)
	assert.Contains(t, got, "PATH=/bin", "a variable that merely begins with HOME was dropped: %v", got)
}

// The tmp directory the wall points TMPDIR at is on the volume, so it goes with it. This
// is the whole reason there is no cleanup step: the temp files are not on the boot disk to
// begin with.
func TestTheTempDirectoryIsOnTheVolume(t *testing.T) {
	b := newRunBench(t, 0)
	r := b.once(t, runFlagsFor(t)...)
	require.Contains(t, r.Stderr, "SANDBOX OK", "no wall was reported:\n%s", r.Stderr)
	tmp := filepath.Join(b.vols.mount, ".nova-sandbox-tmp")
	_, err := os.Stat(tmp)
	require.NoError(t, err, "the one directory this tool makes is not on the volume: %s", err)
}

// `nova-sandbox run --help` printed FOUR REFUSALS — one for the missing --name, one for
// the missing --size, one for --help itself not being a flag, one for the missing -- —
// and exit 125. Measured 2026-09-18 by a non-author dogfooding the verb. Asking a tool how
// to use it is not a mistake, and a tool that answers a question with four complaints
// teaches the reader to stop asking.
func TestRunAnswersHelpWithItsUsage(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--help", "-h", "help"} {
		r := withEnv(runVerb, hostPath()).Do(t, flag)
		assert.Equal(t, 0, r.Code, "`run %s` exited %d, want 0: asking how to use a verb is not a mistake\nstderr:\n%s", flag, r.Code, r.Stderr)
		assert.NotContains(t, r.Stderr, "REFUSED", "`run %s` refused instead of answering:\n%s", flag, r.Stderr)
		assert.Contains(t, r.Stdout, "nova-sandbox run", "`run %s` did not print the verb's usage on stdout:\n%s", flag, r.Stdout)
		assert.Contains(t, r.Stdout, "--size", "`run %s` did not print the verb's usage on stdout:\n%s", flag, r.Stdout)
	}
}

// --go is the flag the measured failure asks for: every card that builds Go needs the
// toolchain root and the module cache, and naming them by hand in every argv is a step
// that will be forgotten.
func TestGoAddsTheToolchainRootAndTheModuleCache(t *testing.T) {
	root, mod := t.TempDir(), t.TempDir()
	swap(t, &runGoEnv, func() (goDirs, error) { return goDirs{Root: root, ModCache: mod}, nil })

	f := runFlags{useGo: true, reads: []string{"/usr"}}
	var errb bytes.Buffer
	r := applyGoReads(&f, &errb)
	if r != nil {
		require.Nil(t, r, "--go refused with a real toolchain: %s", r.Text)
	}
	require.Contains(t, f.reads, root, "--go did not add GOROOT and GOMODCACHE to the reads: %v", f.reads)
	require.Contains(t, f.reads, mod, "--go did not add GOROOT and GOMODCACHE to the reads: %v", f.reads)
	assert.Contains(t, f.reads, "/usr", "--go dropped a --read the caller named: %v", f.reads)
	assert.Contains(t, errb.String(), "SANDBOX NOTE", "--go added two read roots and said nothing about it:\n%s", errb.String())
}

// A module cache that is not there yet is SKIPPED, not refused: it is a path the tool
// derived, not one the caller named, and rule 5's refusal-for-absence is about the
// caller's own paths.
func TestGoSkipsAToolchainPathThatIsNotThere(t *testing.T) {
	root := t.TempDir()
	swap(t, &runGoEnv, func() (goDirs, error) {
		return goDirs{Root: root, ModCache: filepath.Join(root, "not", "there")}, nil
	})
	f := runFlags{useGo: true}
	var errb bytes.Buffer
	r := applyGoReads(&f, &errb)
	if r != nil {
		require.Nil(t, r, "--go refused because a derived path was absent: %s", r.Text)
	}
	require.Equal(t, []string{root}, f.reads, "--go added %v, want just the toolchain root", f.reads)
	assert.True(t, strings.Contains(errb.String(), "not there") || strings.Contains(errb.String(), "skipped"), "--go skipped a path without saying which:\n%s", errb.String())
}

// No go on the PATH is a refusal naming the flag, not a run that fails later inside the
// wall for a reason nothing explains.
func TestGoRefusesWhenThereIsNoGoToAsk(t *testing.T) {
	swap(t, &runGoEnv, func() (goDirs, error) {
		return goDirs{}, errors.New("exec: \"go\": executable file not found in $PATH")
	})
	f := runFlags{useGo: true}
	var errb bytes.Buffer
	r := applyGoReads(&f, &errb)
	require.NotNil(t, r, "--go with no go on the PATH did not refuse")
	assert.Equal(t, "bad_read", r.Reason, "the refusal does not name the flag: %+v", r)
	assert.Contains(t, r.Text, "--go", "the refusal does not name the flag: %+v", r)
}

// The whole point of the line: a command that failed is told what the wall refused.
func TestAFailedRunIsToldWhatTheWallDenied(t *testing.T) {
	b := newRunBench(t, 2)
	// /opt is a directory of the machine the DENIAL came from, not of the machine reading
	// this test: the windows leg has none and the remedy came out as `--read \` there.
	posixDirs(t, "/opt")
	swap(t, &runDenials, func(int, int) []deniedPath {
		return []deniedPath{{Path: "/opt", Op: "read", PID: 999}}
	})
	r := b.once(t, runFlagsFor(t)...)
	require.Equal(t, 2, r.Code, "the command's status is still the command's: got %d", r.Code)
	assert.Contains(t, r.Stderr, `SANDBOX DENIED path=/opt op=read remedy="--read /opt"`, "a failed run did not say what the wall denied:\n%s", r.Stderr)
}

// Edge 3 of the 20-run soak, measured on the Studio 2026-09-18. A run that hit its
// --timeout spent two seconds asking the OS what it had denied and then printed
//
//	SANDBOX NOTE the command failed and this OS reported no seatbelt denials for it;
//	... add a --read, or --go
//
// A TIMEOUT IS NOT A DENIAL. Nothing was refused: the command was still working when its
// deadline passed, and a hint pointing at the read set sends the reader to widen a wall
// that was never in the way. The probe is skipped and the one true sentence is printed.
func TestATimeoutNeverAsksWhatWasDeniedAndSaysItTimedOut(t *testing.T) {
	newRunBenchNeverFinishes(t, 137)
	asked := false
	swap(t, &runDenials, func(int, int) []deniedPath { asked = true; return nil })

	// The deadline this verb was given, in the units the verb takes it. The command
	// under it never finishes, so the deadline is the only thing that can end this run.
	r := disposable(t, time.Nanosecond, runFlagsFor(t, "--timeout", "1ns")...)

	require.Equal(t, exitTimeout, r.Code, "a run that passed its deadline is exit %d: got %d\n%s", exitTimeout, r.Code, r.Stderr)
	assert.False(t, asked, "a timed-out run asked the operating system what it had denied; that is a bounded two-second query spent on a question nobody asked -- nothing was refused, the deadline passed")
	assert.Contains(t, r.Stderr, "SANDBOX TIMEOUT after=", "a timed-out run did not say so in one line:\n%s", r.Stderr)
	assert.NotContains(t, r.Stderr, "add a --read", "a timed-out run printed the no-denials hint; the wall denied nothing and the remedy it names is not the one:\n%s", r.Stderr)
	assert.Contains(t, r.Stderr, "SANDBOX DONE name=j1", "the timed-out run left no receipt, so its volume's fate is unstated:\n%s", r.Stderr)
}

// newRunBenchNeverFinishes is newRunBench for the one case it cannot express: a command
// that does not exit on its own, so that the deadline is the only thing that can end the
// run. Its status arrives when the group is KILLED, which is what a real one does.
func newRunBenchNeverFinishes(t *testing.T, code int) *runBench {
	t.Helper()
	b := newRunBench(t, code)
	runExec = func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
		done := make(chan int, 1)
		return startedRun{
			done: done,
			kill: func(sig syscall.Signal) {
				b.killed = append(b.killed, sig)
				select {
				case done <- code:
				default:
				}
			},
			pid: 4242,
		}, nil
	}
	return b
}

// A run that SUCCEEDED asks the OS nothing: the reader costs a process, and a clean run
// has no question to answer.
func TestACleanRunNeverAsksWhatWasDenied(t *testing.T) {
	b := newRunBench(t, 0)
	asked := false
	swap(t, &runDenials, func(int, int) []deniedPath { asked = true; return nil })
	r := b.once(t, runFlagsFor(t)...)
	assert.NotContains(t, r.Stderr, "SANDBOX DENIED", "a clean run printed a denial:\n%s", r.Stderr)
	assert.False(t, asked, "a clean run asked the operating system what it had denied; that is a process spent on a question nobody has")
}

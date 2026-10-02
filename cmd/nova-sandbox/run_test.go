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
// REPLACED: no volume is created, no diskutil runs and no process starts. What is under test
// is the contract one end-to-end run cannot prove: a created volume is deleted on EVERY path
// out, a failed delete is reported rather than swallowed, a duplicate name is refused before
// anything is made, and a timeout kills the whole process group. The one real run (a real
// 64m volume) is in run_e2e_darwin_test.go behind a build tag: eight CI runners share this
// machine, and a suite that made volumes on every `go test` would be a hazard.

// fakeVolumes is the disk seam. It records the calls IN ORDER, because the order is the
// contract: nothing runs before a volume exists and nothing exits before it is gone.
type fakeVolumes struct {
	mount  string
	calls  []string
	exists bool
	used   int64
	listed []diskVolume

	containerErr, existsErr, createErr, deleteErr, listErr error
}

func (f *fakeVolumes) rec(call string) { f.calls = append(f.calls, call) }

func (f *fakeVolumes) Container() (string, error) {
	f.rec("container")
	if f.containerErr != nil {
		return "", f.containerErr
	}
	return "disk3", nil
}

func (f *fakeVolumes) Exists(name string) (bool, error) {
	f.rec("exists:" + name)
	return f.exists, f.existsErr
}

func (f *fakeVolumes) List() ([]diskVolume, error) { f.rec("list"); return f.listed, f.listErr }

func (f *fakeVolumes) Create(container, name, size string) (diskVolume, error) {
	f.rec("create:" + container + ":" + name + ":" + size)
	if f.createErr != nil {
		return diskVolume{}, f.createErr
	}
	return diskVolume{Name: name, Disk: "disk3s9", Mount: f.mount}, nil
}

func (f *fakeVolumes) Used(string) (int64, error) { f.rec("used"); return f.used, nil }

func (f *fakeVolumes) Delete(disk string) error { f.rec("delete:" + disk); return f.deleteErr }

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
		return startedRun{done: done, kill: func(sig syscall.Signal) { b.killed = append(b.killed, sig) }, pid: 4242}, nil
	})
	return b
}

// shellOf is a command that resolves on this platform, because rule 5 resolves the
// command before any policy is built and a name on no PATH would refuse for the wrong
// reason. The fake executor never runs it.
func shellOf(t *testing.T) []string {
	t.Helper()
	return newJob(t).shell(t)
}

func runFlagsFor(t *testing.T, extra ...string) []string {
	t.Helper()
	args := append([]string{"--name", "j1", "--size", "64m"}, extra...)
	return append(append(args, "--"), shellOf(t)...)
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

// The contract in one line: whatever the command did, the place it did it in is gone. The
// rows after the loop are every other way out, and each one still deletes or never made.
func TestRunCreatesTheVolumeRunsAndAlwaysDeletesIt(t *testing.T) {
	for _, code := range []int{0, 7, 137} {
		t.Run("exit "+strconv.Itoa(code), func(t *testing.T) {
			b := newRunBench(t, code)
			r := b.once(t, runFlagsFor(t)...)
			require.Equal(t, code, r.Code, "the status belongs to the wrapped command\n%s", r)
			order := strings.Join(b.vols.calls, " ")
			require.True(t, strings.HasPrefix(order, "container exists:nova-j1 create:disk3:nova-j1:64m"), "not look-create-run-delete: %s", order)
			require.True(t, strings.HasSuffix(order, "delete:disk3s9"), "not look-create-run-delete: %s", order)
			r.Err("SANDBOX DONE name=j1 exit="+strconv.Itoa(code), "freed=4096", "wall=", "SANDBOX OK")
			// The TMPDIR the wall points at is on the volume, so it goes with it: that is
			// the whole reason there is no cleanup step.
			require.NoError(t, statErr(filepath.Join(b.vols.mount, ".nova-sandbox-tmp")))
		})
	}

	asked := false // set by the runDenials swap of the rows that watch the denial reader
	askDenials := func(t *testing.T, _ *runBench) {
		asked = false
		swap(t, &runDenials, func(int, int) []deniedPath { asked = true; return nil })
	}
	for _, tc := range []struct {
		name  string
		code  int
		extra []string
		vols  fakeVolumes // the failures the disk answers with
		setup func(*testing.T, *runBench)
		check func(*testing.T, *runBench, testkit.Ran)
	}{
		// This is the path a cleanup step would forget: the tool said no, so nothing ran, so
		// nothing looks like it needs undoing.
		{name: "a refusal after the volume exists still deletes it", extra: []string{"--read", "/no/such/directory"},
			check: func(t *testing.T, b *runBench, r testkit.Ran) {
				r.Exit(125).Err("SANDBOX DONE name=j1")
				require.Contains(t, strings.Join(b.vols.calls, " "), "delete:disk3s9", "a refusal after the volume was made left it on the disk")
			}},
		// A run never joins a place it did not create, because it would delete that place
		// on the way out.
		{name: "a duplicate name is refused before anything is made", vols: fakeVolumes{exists: true},
			check: func(t *testing.T, b *runBench, r testkit.Ran) {
				r.ExitErr(125, "reason=volume_exists")
				require.NotContains(t, strings.Join(b.vols.calls, " "), "create:", "a refused run touched the disk")
				require.NotContains(t, strings.Join(b.vols.calls, " "), "delete:", "a refused run touched the disk")
			}},
		// The one failure the verb cannot repair is the one it must never hide.
		{name: "a leak is reported and paid for with the exit code", vols: fakeVolumes{deleteErr: errors.New("Unable to unmount volume for deletion")},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				require.Equal(t, exitLeak, r.Code, "a caller that reads 0 believes the machine is clean\n%s", r)
				r.Err(`SANDBOX LEAK name=j1 volume=disk3s9 remedy="diskutil apfs deleteVolume disk3s9"`, "freed=0")
			}},
		// A volume made and not mounted is not one that could not be made: the verb's own
		// prefix would send the reader to diskutil and the container for a fault in neither.
		// The words come from the manager, the half that knows which happened.
		{name: "an unmounted volume says so rather than that the create failed",
			vols: fakeVolumes{createErr: fmt.Errorf("%w: the volume disk3s7 was created in disk3 and is not mounted under /Volumes", errVolumeNotMounted)},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				r.ExitErr(125, "reason=volume_failed").NotErr("could not be created")
				r.Err("was created in disk3 and is not mounted under /Volumes", runRemedy)
			}},
		// Every other create failure keeps the verb's prefix, naming the container the
		// volume would have been made in.
		{name: "a failed create names the container", vols: fakeVolumes{createErr: errors.New("diskutil apfs addVolume: exit status 1: quota too small")},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				r.ExitErr(125, "reason=volume_failed").Err("the disposable volume could not be created in disk3")
			}},
		{name: "an unreadable container is a refusal with a remedy", vols: fakeVolumes{containerErr: errors.New("no such thing")},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				r.ExitErr(125, "reason=no_container").Err("--container disk3")
			}},
		// /opt is a directory of the machine the DENIAL came from, not of the machine
		// reading this test: the windows leg has none and the remedy came out as `--read \`.
		{name: "a failed run is told what the wall denied", code: 2,
			setup: func(t *testing.T, _ *runBench) {
				posixDirs(t, "/opt")
				swap(t, &runDenials, func(int, int) []deniedPath { return []deniedPath{{Path: "/opt", Op: "read", PID: 999}} })
			},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				require.Equal(t, 2, r.Code, "the command's status is still the command's\n%s", r)
				assert.Contains(t, r.Stderr, `SANDBOX DENIED path=/opt op=read remedy="--read /opt"`, r)
			}},
		// The denial reader costs a process, and a clean run has no question to answer.
		{name: "a clean run never asks what was denied", setup: askDenials,
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				assert.NotContains(t, r.Stderr, "SANDBOX DENIED", r)
				assert.False(t, asked, "a clean run asked the operating system what it had denied")
			}},
		// Edge 3 of the 20-run soak, measured on the Studio 2026-09-18: a run that hit its
		// --timeout spent two seconds asking what was denied, then printed the no-denials
		// hint (`... add a --read, or --go`). A TIMEOUT IS NOT A DENIAL: the hint sends the
		// reader to widen a wall that was never in the way. The command here never finishes
		// (its status arrives when the group is KILLED), so the deadline ends the run.
		{name: "a timeout never asks what was denied and says it timed out", code: 137,
			extra: []string{"--timeout", "1ns"},
			setup: func(t *testing.T, b *runBench) {
				askDenials(t, b)
				runExec = func(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (startedRun, error) {
					done := make(chan int, 1)
					return startedRun{done: done, pid: 4242, kill: func(sig syscall.Signal) {
						b.killed = append(b.killed, sig)
						select {
						case done <- 137:
						default:
						}
					}}, nil
				}
			},
			check: func(t *testing.T, _ *runBench, r testkit.Ran) {
				require.Equal(t, exitTimeout, r.Code, r)
				assert.False(t, asked, "a timed-out run spent a bounded two-second query on a question nobody asked")
				assert.Contains(t, r.Stderr, "SANDBOX TIMEOUT after=", r)
				assert.NotContains(t, r.Stderr, "add a --read", "the wall denied nothing; the remedy it names is not the one\n%s", r)
				assert.Contains(t, r.Stderr, "SANDBOX DONE name=j1", "no receipt, so the volume's fate is unstated\n%s", r)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newRunBench(t, tc.code)
			b.vols.exists, b.vols.containerErr, b.vols.createErr, b.vols.deleteErr = tc.vols.exists, tc.vols.containerErr, tc.vols.createErr, tc.vols.deleteErr
			if tc.setup != nil {
				tc.setup(t, b)
			}
			args := runFlagsFor(t, tc.extra...)
			deadline, _ := time.ParseDuration(parseRun(args).timeout) // the row's --timeout in the verb's units; none is 0
			tc.check(t, b, disposable(t, deadline, args...))
		})
	}
}

// Every independent problem in ONE run, each naming the form its flag wants. The CONTRACT is
// the count of runs, not the vocabulary: the vocabulary is the PLATFORM's (`--size lots` is
// bad_size on darwin and size_unenforceable on windows, W6; windows adds W5's bad_scratch),
// so validateRun is asked with the platform NAMED and both are checked from either machine.
// Measured red on the windows CI leg (run 35367602664, job test-windows-pr): this test
// asserted darwin's set and remedy wherever it ran, and the windows verb, which named its
// own six problems in one refusal correctly, failed it.
func TestRunNamesEveryBadFlagAtOnce(t *testing.T) {
	t.Parallel()

	badArgv := []string{"--name", "a b", "--size", "lots", "--timeout", "soon", "--container", "sda1"}
	for goos, want := range map[string][]string{
		"darwin":  {"no_name", "bad_size", "bad_timeout", "no_container", "no_command"},
		"windows": {"no_name", "size_unenforceable", "bad_scratch", "bad_timeout", "no_container", "no_command"},
	} {
		f := parseRun(badArgv)
		_, bad := validateRun(&f, goos)
		for _, w := range want {
			assert.Contains(t, reasonsOf(bad), w, "on %s every independent problem is named in ONE refusal", goos)
		}
	}

	// End to end on this platform: one refusal, and the remedy is this platform's own -- a
	// windows reader handed the darwin argv would type the very flag the next line refuses.
	r := withEnv(runVerb, hostPath()).Do(t, badArgv...).Exit(125)
	for _, want := range []string{"reason=no_name", "reason=bad_timeout", "reason=no_container", "reason=no_command", remedyFor(runtime.GOOS)} {
		assert.Contains(t, r.Stderr, want, r)
	}
}

// The verb refuses where there is no disposable place, and says where it is on that platform
// instead. windows LEFT this list on 2026-09-18 (W1..W12, runwin.go: a Job Object plus a
// per-run scratch): it still refuses there for want of the WALL, a different refusal (see
// TestWindowsRunCreatesJobAndScratchRunsAndAlwaysDeletes/the_wall_is_not_built).
func TestTheVerbRefusesWherethereIsNoDisposableBody(t *testing.T) {
	t.Parallel()

	for _, built := range []string{"darwin", "windows"} {
		_, _, refused := noDisposableBody(built)
		require.False(t, refused, "%s has the body and must not refuse for want of a place", built)
	}
	line, remedy, refused := noDisposableBody("linux")
	require.True(t, refused, "linux has no disposable-volume body and must refuse rather than use an ordinary directory")
	assert.Contains(t, line, "reason=no_sandbox")
	assert.Contains(t, line, "linux")
	assert.Contains(t, remedy, "--write <dir>", "the remedy does not name the container path to use instead")
	assert.Contains(t, remedy, "image", "the remedy does not name the container path to use instead")
}

// The name, size and container shapes, both ways round. A --name reaches a volume name, a
// path under /Volumes and a diskutil argument.
func TestTheNameAndSizeShapesAreNarrow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		flag     string
		ok       func(string) bool
		good, no []string
	}{
		{"--name", okName, []string{"j1", "lane-sandbox", "a.b_c", "A1"},
			[]string{"", "-lead", "a b", "a/b", "../x", "a;rm", strings.Repeat("x", 33), "naïve"}},
		{"--size", okSize, []string{"64m", "8g", "1t", "512", "1.5g", "64mb", "8GB"},
			[]string{"", "g", "50%", "-8g", "8 g", "8gx", "eight"}},
		{"--container", okContainer, []string{"disk3", "disk12"}, []string{"", "disk", "disk3s1", "sda", "disk3;reboot"}},
	} {
		for _, v := range tc.good {
			assert.True(t, tc.ok(v), "%s %q should be taken", tc.flag, v)
		}
		for _, v := range tc.no {
			assert.False(t, tc.ok(v), "%s %q must be refused", tc.flag, v)
		}
	}
}

// supervise is the part the volume's life hangs on: nothing of the run may be alive when the
// delete is attempted, or the unmount fails and a clean exit becomes a leak. Each row asserts
// the ORDER of the signals, with no process, no clock and no timer.
func TestSuperviseSignalsTheGroupInOrder(t *testing.T) {
	t.Parallel()

	fired := func() chan time.Time { c := make(chan time.Time, 1); c <- time.Time{}; return c }
	for _, tc := range []struct {
		name            string
		exited          int // a status already there: the leader exited on its own
		deadline, grace chan time.Time
		sig             os.Signal
		diesOn          syscall.Signal // the signal the group dies of, with status dieCode
		dieCode, code   int
		timedOut        bool
		want            []syscall.Signal
		why             string
	}{
		{name: "an ordinary exit is swept", exited: 3, code: 3, want: []syscall.Signal{syscall.SIGKILL},
			why: "a forked child that outlives the leader holds the volume open"},
		{name: "a timeout terms, kills and sweeps the whole group", deadline: fired(), grace: fired(), diesOn: syscall.SIGKILL, dieCode: 137,
			code: exitTimeout, timedOut: true, want: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL, syscall.SIGKILL}},
		// grace never fires: the group answered the SIGTERM, and the deadline is what ended
		// the run whatever signal did it.
		{name: "a group that answers the term is not killed again", deadline: fired(), grace: make(chan time.Time), diesOn: syscall.SIGTERM, dieCode: 143,
			code: exitTimeout, timedOut: true, want: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
		{name: "a caller's SIGINT reaches the group and is 128+N", sig: syscall.SIGINT, diesOn: syscall.SIGINT, dieCode: 130,
			code: 128 + int(syscall.SIGINT), want: []syscall.Signal{syscall.SIGINT, syscall.SIGKILL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan int, 1)
			if tc.exited != 0 {
				done <- tc.exited
			}
			var sigs chan os.Signal
			if tc.sig != nil {
				sigs = make(chan os.Signal, 1)
				sigs <- tc.sig
			}
			var killed []syscall.Signal
			code, timedOut := supervise(done, tc.deadline, tc.grace, sigs, func(s syscall.Signal) {
				killed = append(killed, s)
				if tc.diesOn != 0 && s == tc.diesOn {
					select {
					case done <- tc.dieCode:
					default:
					}
				}
			})
			require.Equal(t, tc.code, code)
			require.Equal(t, tc.timedOut, timedOut)
			require.Equal(t, tc.want, killed, tc.why)
		})
	}
}

// withHome is rule 9 for a home the TOOL made: the child sees one HOME and it is the one on
// the disposable volume, whatever the caller's own environment carried.
func TestTheChildsHomeIsTheOneOnTheVolume(t *testing.T) {
	t.Parallel()

	got := withHome([]string{"HOME=/Users/someone", "PATH=/bin", "HOMEBREW_PREFIX=/opt/homebrew"}, "/Volumes/nova-j1/home")
	homes := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "HOME=") {
			homes++
			assert.Equal(t, "HOME=/Volumes/nova-j1/home", kv)
		}
	}
	assert.Equal(t, 1, homes, "HOME entries in %v", got)
	assert.Contains(t, got, "HOMEBREW_PREFIX=/opt/homebrew", "a variable that merely begins with HOME was dropped")
	assert.Contains(t, got, "PATH=/bin")
}

// `nova-sandbox run --help` printed FOUR REFUSALS (no --name, no --size, --help not a flag,
// no --) and exit 125, measured 2026-09-18 by a non-author dogfooding the verb. A tool that
// answers a question with four complaints teaches the reader to stop asking. On windows the
// help says what --size does there, the first thing a reader from the darwin docs will try.
func TestRunAnswersHelpWithItsUsage(t *testing.T) {
	t.Parallel()

	for label, env := range map[string][]string{"PATH": hostPath(), "no env": nil} {
		for _, flag := range []string{"--help", "-h", "help"} {
			t.Run(flag+" with "+label, func(t *testing.T) {
				withEnv(runVerb, env).Do(t, flag).Exit(0).NotErr("REFUSED").
					Out("nova-sandbox run", "--size", "--scratch", "--memory", "--cpu", "--place", "REFUSED on windows")
			})
		}
	}
}

// --go is the flag the measured failure asks for: every card that builds Go needs the
// toolchain root and the module cache, and naming them by hand in every argv will be
// forgotten.
func TestGoAddsTheToolchainRootAndTheModuleCache(t *testing.T) {
	apply := func(t *testing.T, dirs goDirs, err error, reads ...string) (runFlags, *sandbox.Refusal, string) {
		swap(t, &runGoEnv, func() (goDirs, error) { return dirs, err })
		f := runFlags{useGo: true, reads: reads}
		var errb bytes.Buffer
		r := applyGoReads(&f, &errb)
		return f, r, errb.String()
	}
	t.Run("both are added to the reads", func(t *testing.T) {
		root, mod := t.TempDir(), t.TempDir()
		f, r, stderr := apply(t, goDirs{Root: root, ModCache: mod}, nil, "/usr")
		require.Nil(t, r)
		require.Contains(t, f.reads, root)
		require.Contains(t, f.reads, mod)
		assert.Contains(t, f.reads, "/usr", "--go dropped a --read the caller named")
		assert.Contains(t, stderr, "SANDBOX NOTE", "--go added two read roots and said nothing about it")
	})
	// A derived path is SKIPPED, not refused: rule 5's refusal-for-absence is about the
	// caller's own paths.
	t.Run("a module cache that is not there is skipped", func(t *testing.T) {
		root := t.TempDir()
		f, r, stderr := apply(t, goDirs{Root: root, ModCache: filepath.Join(root, "not", "there")}, nil)
		require.Nil(t, r)
		require.Equal(t, []string{root}, f.reads)
		assert.True(t, strings.Contains(stderr, "not there") || strings.Contains(stderr, "skipped"), "--go skipped a path without saying which:\n%s", stderr)
	})
	// Not a run that fails later inside the wall for a reason nothing explains.
	t.Run("no go on the PATH is a refusal naming the flag", func(t *testing.T) {
		_, r, _ := apply(t, goDirs{}, errors.New("exec: \"go\": executable file not found in $PATH"))
		require.NotNil(t, r)
		assert.Equal(t, "bad_read", r.Reason)
		assert.Contains(t, r.Text, "--go")
	})
}

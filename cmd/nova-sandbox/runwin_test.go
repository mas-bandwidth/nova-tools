package main

// The WINDOWS half of the run verb, tested on whatever host this runs on.
//
// THERE IS NO WINDOWS BENCH IN THE ESTATE (2026-09-18). docs/SPEC-SANDBOX.md's windows
// section was written before one arrives so it is not designed under fire, and these tests
// are the same move for the code: every Win32 call is behind runwin.go's `winPlacer`, a fake
// stands in for it, and what is asserted is the SEQUENCE, which is where the contract lives
// (W1 both or neither, W2 kill before delete, W3 child in the job at creation, W4 caps on the
// job, W5 one writable place, W6 refusal, W7 retry and leak, W8-W10 wsb, W11 tripwire, W12).
// They CANNOT prove that CreateJobObjectW, the extended limits, PROC_THREAD_ATTRIBUTE_JOB_LIST
// and WindowsSandbox.exe behave on real Windows as the rules say; the first bench proves that.
// As in internal/sandbox/winpath_test.go the platform is a PARAMETER, never runtime.GOOS:
// a test that only ever walks the darwin path calls a windows bug green.

import (
	"errors"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWinPlace is the Windows seam. It records the calls IN ORDER, because the order is the
// contract: nothing runs before the job and the scratch exist, nothing is deleted before the
// job is closed, and nothing exits before the scratch is gone.
type fakeWinPlace struct {
	// mu guards every field below: the verb calls the placer on whatever goroutine runs it,
	// and a test watching a run IN FLIGHT (TestWindowsHasNo128PlusN) reads the record from
	// its own. Same shape, and reason, as fakeDiskutil in volumes_darwin_test.go.
	mu    sync.Mutex
	calls []string

	exists, wsbOK, wsbRun bool
	used                  int64
	wsbEd, wsbBusy        string
	exitCode              int

	existsErr, makeErr, jobErr, startErr, closeErr, removeErr error
	wsbAvailErr, wsbRunErr, wsbStartErr                       error

	// removeOKAfter is W7's bounded retry as a fake: the first N removals fail with a
	// transient hold and the one after that works.
	removeOKAfter, removes int

	gotLimits winLimits
	gotSpec   winStartSpec
	gotXML    string
	jobsOpen  int
}

// lock takes mu and records the call; the method defers what it returns.
func (f *fakeWinPlace) lock(call string) func() {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	return f.mu.Unlock
}

func (f *fakeWinPlace) Exists(dir string) (bool, error) {
	defer f.lock("exists:" + filepath.Base(dir))()
	return f.exists, f.existsErr
}

func (f *fakeWinPlace) MakeScratch(dir string) error {
	defer f.lock("scratch:" + filepath.Base(dir))()
	if f.makeErr != nil {
		return f.makeErr
	}
	// A real MakeScratch makes work/ and home/ too, and sandbox.Build resolves the cwd, so
	// they have to be there for the policy to build on this host.
	for _, d := range []string{"", "work", "home"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeWinPlace) CreateJob(l winLimits) (winJob, error) {
	defer f.lock("job:mem=" + strconv.FormatInt(l.MemoryBytes, 10) + ",cpu=" + strconv.Itoa(l.CPUPercent))()
	if f.jobErr != nil {
		return nil, f.jobErr
	}
	f.gotLimits = l
	f.jobsOpen++
	return "job-1", nil
}

func (f *fakeWinPlace) Start(job winJob, spec winStartSpec) (winStarted, error) {
	defer f.lock("start:" + job.(string))()
	if f.startErr != nil {
		return winStarted{}, f.startErr
	}
	f.gotSpec = spec
	done := make(chan int, 1)
	done <- f.exitCode
	return winStarted{done: done, pid: 4242}, nil
}

func (f *fakeWinPlace) CloseJob(job winJob) error {
	defer f.lock("close:" + job.(string))()
	f.jobsOpen--
	return f.closeErr
}

func (f *fakeWinPlace) Used(string) (int64, error) { defer f.lock("used")(); return f.used, nil }

func (f *fakeWinPlace) RemoveTree(root, dir string, _ time.Duration) error {
	defer f.lock("remove:" + filepath.Base(dir))()
	f.removes++
	if f.removes <= f.removeOKAfter {
		return errors.New("ERROR_SHARING_VIOLATION: the file is in use by another process")
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	return os.RemoveAll(dir)
}

func (f *fakeWinPlace) WSBAvailable() (string, bool, error) {
	defer f.lock("wsb-available")()
	return f.wsbEd, f.wsbOK, f.wsbAvailErr
}

func (f *fakeWinPlace) WSBRunning() (string, bool, error) {
	defer f.lock("wsb-running")()
	return f.wsbBusy, f.wsbRun, f.wsbRunErr
}

func (f *fakeWinPlace) StartWSB(file, xml string) error {
	defer f.lock("wsb-start:" + filepath.Base(file))()
	f.gotXML = xml
	return f.wsbStartErr
}

// snapshot, openJobs, startSpec and jobLimits read the record under the lock, so a test may
// read it while a run is still in flight.
func (f *fakeWinPlace) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *fakeWinPlace) openJobs() int { f.mu.Lock(); defer f.mu.Unlock(); return f.jobsOpen }

func (f *fakeWinPlace) startSpec() winStartSpec { f.mu.Lock(); defer f.mu.Unlock(); return f.gotSpec }

func (f *fakeWinPlace) jobLimits() winLimits { f.mu.Lock(); defer f.mu.Unlock(); return f.gotLimits }

// winBench stands the windows seams up around one temporary directory that plays --scratch,
// and puts them all back afterwards. The TOOL believes it is on windows for the duration,
// which is the only way a Mac can run these at all.
type winBench struct {
	place   *fakeWinPlace
	scratch string
	sigs    chan os.Signal
}

func newWinBench(t *testing.T, code int) *winBench {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	b := &winBench{
		place:   &fakeWinPlace{used: 4096, exitCode: code, wsbEd: "Professional", wsbOK: true},
		scratch: root,
		sigs:    make(chan os.Signal),
	}
	swap[winPlacer](t, &runWinPlace, b.place)
	swap(t, &runGOOS, "windows")
	swap(t, &runSignals, func() (<-chan os.Signal, func()) { return b.sigs, func() {} })
	// The wall is not built (runwin_other.go / wrap_other.go) and these tests are about the
	// PLACE, so the wall says yes here and exactly one row turns it off again.
	swap(t, &runWinWall, func() (string, bool) { return "appcontainer", true })
	return b
}

// winArgv is the argv a windows caller writes: --scratch instead of --size, the rest as
// darwin's. The command is the host's, because sandbox.Build resolves it before any policy
// is built and a name on no PATH would refuse for the wrong reason.
func winArgv(t *testing.T, scratch string, extra ...string) []string {
	t.Helper()
	args := append([]string{"--name", "j1", "--scratch", scratch}, extra...)
	return append(append(args, "--"), shellOf(t)...)
}

// exec drives the windows verb FROM THE LOOK ONWARDS with the argv parsed, as darwin's
// disposable does: what is under test is the sequence over the placer, and the argv's
// --scratch (the HOST's temp directory, where the fake makes a real place) cannot also be
// judged as a windows path on a host whose idea of absolute is the other platform's.
func (b *winBench) exec(t *testing.T, args ...string) testkit.Ran {
	t.Helper()
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		f := parseRun(args)
		if f.place == "" {
			f.place = placeJob
		}
		var d time.Duration
		if f.timeout != "" {
			d, _ = time.ParseDuration(f.timeout)
		}
		return runDisposableWindows(f, d, stdin, stdout, stderr, hostPath())
	}).Do(t, args...)
}

func (b *winBench) order() string { return strings.Join(b.place.snapshot(), " ") }

// The contract in one line, the SAME line as darwin's: whatever the command did, the place it
// did it in is gone. The rows after the loop are every other way out, and the wsb place.
func TestWindowsRunCreatesJobAndScratchRunsAndAlwaysDeletes(t *testing.T) {
	for _, code := range []int{0, 7, 1} {
		t.Run("exit "+strconv.Itoa(code), func(t *testing.T) {
			b := newWinBench(t, code)
			r := b.exec(t, winArgv(t, b.scratch)...)
			require.Equal(t, code, r.Code, "the status belongs to the wrapped command\n%s", r)
			require.Equal(t, "exists:nova-j1 scratch:nova-j1 job:mem=0,cpu=0 start:job-1 close:job-1 used remove:nova-j1", b.order(), "not look-create-job-run-kill-delete")
			assert.Contains(t, r.Stderr, "SANDBOX DONE name=j1 exit="+strconv.Itoa(code), r)
			assert.Contains(t, r.Stderr, "freed=4096", r)
			assert.ErrorIs(t, statErr(filepath.Join(b.scratch, "nova-j1")), fs.ErrNotExist, "the scratch is still on the disk; the whole verb is that it is not")
		})
	}

	reads := t.TempDir()
	var window time.Duration // what RemoveTree was handed, recorded by the retry row
	for _, tc := range []struct {
		name  string
		code  int
		verb  bool // the whole verb, argv and all: a refusal BEFORE anything is made
		extra []string
		setup func(*testing.T, *winBench)
		check func(*testing.T, *winBench, testkit.Ran)
	}{
		// W2 and W7 as an ORDERING: a running image inside the scratch cannot be removed or
		// renamed aside (NTFS raises a sharing violation; there is no replace-the-inode trick),
		// so a delete attempted before the kill is a leak by construction.
		{name: "the job is closed before the scratch is removed", check: func(t *testing.T, b *winBench, _ testkit.Ran) {
			calls := b.place.snapshot()
			closed := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "close:") })
			removed := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "remove:") })
			require.GreaterOrEqual(t, closed, 0, b.order())
			require.GreaterOrEqual(t, removed, 0, b.order())
			require.LessOrEqual(t, closed, removed, "the scratch was removed before the job was closed: %s", b.order())
			assert.Equal(t, 0, b.place.openJobs(), "the kill IS the close, and a job this tool still holds is a tree still running")
		}},
		// W1, `a-windows-run-creates-both-the-job-and-the-scratch-or-neither`: a scratch with
		// no job leaves a survivor holding a handle to the directory about to be deleted.
		{name: "a job that fails unmakes the scratch beside it",
			setup: func(_ *testing.T, b *winBench) { b.place.jobErr = errors.New("CreateJobObjectW: access denied") },
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.Exit(125)
				require.NotContains(t, b.order(), "start:", "the command was started with no job to hold it")
				require.Contains(t, b.order(), "remove:nova-j1")
				assert.ErrorIs(t, statErr(filepath.Join(b.scratch, "nova-j1")), fs.ErrNotExist, "both or neither is the rule")
				assert.Contains(t, r.Stderr, "reason=volume_failed", r)
			}},
		// W5: <scratch>/nova-<n> is the ONLY --write, with the cwd, HOME and the temp
		// directory inside it; a second writable root would be a place the delete does not
		// reach. --read passes through UNCHANGED: a shared toolchain is read in place.
		{name: "the scratch is the only write", extra: []string{"--read", reads}, check: func(t *testing.T, b *winBench, _ testkit.Ran) {
			p := b.place.startSpec().Policy
			require.NotNil(t, p, "the command was never started")
			require.Len(t, p.Writes, 1)
			dir := filepath.Join(b.scratch, "nova-j1")
			for _, in := range []string{p.Writes[0], p.Cwd, p.Home, p.Tmp} {
				assert.True(t, strings.HasPrefix(in, dir), "%q is off the scratch %q", in, dir)
			}
			assert.Equal(t, "work", filepath.Base(p.Cwd))
			assert.Equal(t, "home", filepath.Base(p.Home))
			assert.NotEmpty(t, p.Reads, "--read did not reach the policy")
		}},
		// W5 the other way: a run never joins a place it did not make.
		{name: "an existing scratch is refused, not joined", setup: func(_ *testing.T, b *winBench) { b.place.exists = true },
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.ExitErr(125, "reason=volume_exists")
				assert.NotContains(t, b.order(), "scratch:")
				assert.NotContains(t, b.order(), "job:")
			}},
		// W4: the caps reach the JOB in its own units, and are on it at creation (the fake
		// records them at CreateJob): a limit applied to a job already holding a running
		// tree has been escaped once already.
		{name: "memory and cpu reach the job", extra: []string{"--memory", "4g", "--cpu", "50"}, check: func(t *testing.T, b *winBench, _ testkit.Ran) {
			assert.Equal(t, int64(4)<<30, b.place.jobLimits().MemoryBytes)
			assert.Equal(t, 50, b.place.jobLimits().CPUPercent)
			assert.Contains(t, b.order(), "job:mem="+strconv.FormatInt(int64(4)<<30, 10)+",cpu=50")
		}},
		// W6, `size-on-windows-is-refused-not-approximated`: a ceiling the tool only MEASURES
		// is not a ceiling (rule 7's net_unenforceable precedent). Two remedies, both real:
		// --place wsb, whose whole disk is discarded, and a --scratch on a volume already sized.
		{name: "size is refused, not approximated", verb: true, extra: []string{"--size", "8g"}, check: func(t *testing.T, b *winBench, r testkit.Ran) {
			r.Exit(125).Err("reason=size_unenforceable")
			assert.Contains(t, r.Stderr, "--place wsb", r)
			assert.Contains(t, r.Stderr, "--scratch", r)
			assert.NotContains(t, b.order(), "scratch:", "the refusal came after something was made")
		}},
		// W7, `a-windows-leak-exits-3-and-names-the-one-command-that-removes-it`: a caller
		// that read 0 would believe the machine was clean. The remedy is rmdir /s /q, not
		// `rm -rf` and not a sentence, and the note says a running .exe resists removal.
		{name: "a leak exits 3 and names the one command",
			setup: func(_ *testing.T, b *winBench) { b.place.removeErr = errors.New("ERROR_SHARING_VIOLATION") },
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.Exit(exitLeak)
				dir := filepath.Join(b.scratch, "nova-j1")
				for _, want := range []string{"SANDBOX LEAK name=j1", "volume=" + dir, `remedy="rmdir /s /q ` + dir + `"`, "SANDBOX DONE name=j1 exit=0", "running .exe"} {
					assert.Contains(t, r.Stderr, want, r)
				}
			}},
		// `a-held-handle-is-retried-before-it-is-a-leak`: the retry is inside the placer, and
		// the verb hands it a WINDOW rather than treating the first failure as final --
		// Defender and the search indexer hold transient handles on files a run just wrote.
		{name: "the removal is given a retry window", setup: func(t *testing.T, b *winBench) {
			swap[winPlacer](t, &runWinPlace, &windowRecordingPlace{winPlacer: b.place, window: &window})
		}, check: func(t *testing.T, _ *winBench, r testkit.Ran) {
			r.Exit(0)
			require.Positive(t, window, "a leak declared on the first sharing violation names a machine dirty that a second's patience would have left clean")
			assert.Equal(t, winRemoveWindow, window)
		}},
		// 124 on --timeout, and the job closed, which IS the kill and what reaches a grandchild
		// the harness abandoned. No SIGTERM, no grace, no SIGKILL: no signal to escalate from.
		{name: "a timeout closes the job and exits 124", extra: []string{"--timeout", "20ms"},
			setup: func(t *testing.T, b *winBench) {
				swap[winPlacer](t, &runWinPlace, &hangingPlace{fakeWinPlace: b.place})
			},
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.Exit(exitTimeout)
				require.Contains(t, b.order(), "close:job-1")
				assert.Contains(t, r.Stderr, "SANDBOX DONE name=j1 exit=124", r)
				assert.Contains(t, r.Stderr, "the whole tree", r)
			}},
		// The PLACE is built and the WALL is not, and a place without a wall is hygiene, not
		// containment. Rule 1 is OS-ENFORCED OR REFUSED, and the refusal names the half that
		// is missing rather than `the sandbox failed`.
		{name: "the wall is not built", setup: func(_ *testing.T, _ *winBench) { runWinWall = func() (string, bool) { return "appcontainer", false } },
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.ExitErr(125, "reason=no_sandbox")
				assert.Contains(t, r.Stderr, "appcontainer", r)
				assert.Empty(t, b.order(), "a machine with no wall had something made on it")
			}},
		// The W-preamble: one verb, one receipt grammar, one set of exit codes for three
		// platforms; and the remedy a windows refusal carries is the WINDOWS argv, since a
		// reader handed darwin's would type --size, which the next line refuses.
		{name: "the receipt is the same grammar as darwin's", code: 3, check: func(t *testing.T, _ *winBench, r testkit.Ran) {
			for _, want := range []string{"SANDBOX OK ", "SANDBOX STEP ", "SANDBOX DONE name=j1 exit=3 wall=", " freed="} {
				assert.Contains(t, r.Stderr, want, r)
			}
			assert.Contains(t, remedyFor("windows"), "--scratch")
			assert.NotContains(t, remedyFor("windows"), "--size")
			assert.Contains(t, remedyFor("darwin"), "--size")
		}},
		// W8-W10, Windows Sandbox. `wsb-refuses-on-an-edition-that-has-no-windows-sandbox`:
		// Pro and Enterprise only, the optional feature enabled; the refusal names both
		// rather than saying "unavailable".
		{name: "wsb refuses on an edition without it", extra: []string{"--place", "wsb", "--timeout", "30m"},
			setup: func(_ *testing.T, b *winBench) { b.place.wsbOK, b.place.wsbEd = false, "Home" },
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.ExitErr(125, "reason=no_wsb")
				assert.Contains(t, r.Stderr, "Home", r)
				assert.Contains(t, r.Stderr, "Containers-DisposableClientVM", r)
				assert.NotContains(t, b.order(), "scratch:", "a mapped folder was made before the refusal")
			}},
		// `a-second-wsb-run-refuses-rather-than-queues`: ONE running instance per machine, so a
		// pool of workers each wanting one is a queue of one; hence the default --place job.
		{name: "a second wsb run refuses rather than queues", extra: []string{"--place", "wsb", "--timeout", "30m"},
			setup: func(_ *testing.T, b *winBench) { b.place.wsbRun, b.place.wsbBusy = true, "windowssandbox.exe pid=904" },
			check: func(t *testing.T, _ *winBench, r testkit.Ran) {
				r.ExitErr(125, "reason=wsb_busy")
				assert.Contains(t, r.Stderr, "pid=904", r)
			}},
		// `wsb-refuses-a-run-with-no-timeout`: a guest that never writes the status file is a
		// wait with no end, and this verb never waits without one.
		{name: "wsb refuses a run with no timeout", verb: true, extra: []string{"--place", "wsb"}, check: func(t *testing.T, b *winBench, r testkit.Ran) {
			r.ExitErr(125, "reason=bad_timeout")
			assert.NotContains(t, b.order(), "wsb-start", "a VM was started for a run with no deadline")
		}},
		// `a-wsb-run-with-no-status-file-times-out-at-124`, the one place the contract bends:
		// WindowsSandbox.exe returns once the VM is up and carries no guest status.
		{name: "a wsb run with no status file times out at 124", extra: []string{"--place", "wsb", "--timeout", "30ms"},
			setup: func(t *testing.T, _ *winBench) {
				tick := make(chan time.Time)
				swap(t, &runWinPoll, func(time.Duration) <-chan time.Time { return tick })
			},
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.Exit(exitTimeout)
				assert.Contains(t, r.Stderr, wsbExitFile, r)
				assert.Contains(t, b.order(), "remove:nova-j1", "the mapped folder was not removed after the VM closed")
			}},
		// And the status that DOES come back through the file is the command's own.
		{name: "a wsb run reads the guest's status out of the scratch", extra: []string{"--place", "wsb", "--timeout", "30m"},
			setup: func(t *testing.T, _ *winBench) {
				swap(t, &runWinReadExit, func(path string) (int, bool) {
					assert.Equal(t, wsbExitFile, filepath.Base(path), "the host must wait on the status file in the mapped writable folder")
					return 7, true
				})
			},
			check: func(t *testing.T, b *winBench, r testkit.Ran) {
				r.Exit(7)
				assert.Equal(t, "exists:nova-j1 wsb-available wsb-running scratch:nova-j1 wsb-start:run.wsb used remove:nova-j1", b.order())
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newWinBench(t, tc.code)
			if tc.setup != nil {
				tc.setup(t, b)
			}
			if tc.verb {
				// `C:\nova` is absolute where the tool believes it is, and never reaches the placer.
				tc.check(t, b, withEnv(runVerb, hostPath()).Do(t, winArgv(t, `C:\nova`, tc.extra...)...))
				return
			}
			tc.check(t, b, b.exec(t, winArgv(t, b.scratch, tc.extra...)...))
		})
	}
}

// windowRecordingPlace forwards everything and remembers the one argument under test.
type windowRecordingPlace struct {
	winPlacer
	window *time.Duration
}

func (p *windowRecordingPlace) RemoveTree(root, dir string, w time.Duration) error {
	*p.window = w
	return p.winPlacer.RemoveTree(root, dir, w)
}

// hangingPlace starts a command whose status never arrives until the job is closed, which
// is what a real job's kill-on-close does.
type hangingPlace struct {
	*fakeWinPlace
	done chan int
}

func (p *hangingPlace) Start(job winJob, spec winStartSpec) (winStarted, error) {
	defer p.lock("start:" + job.(string))()
	p.gotSpec, p.done = spec, make(chan int, 1)
	return winStarted{done: p.done, pid: 4242}, nil
}

func (p *hangingPlace) CloseJob(job winJob) error {
	defer p.lock("close:" + job.(string))()
	p.jobsOpen--
	// KILL_ON_JOB_CLOSE: the tree dies with the handle, so the status arrives now.
	select {
	case p.done <- 1:
	default:
	}
	return nil
}

// "There is no 128+N." Windows has no signals, so a caller reading >128 as "killed by a
// signal" reads a unix convention on a platform that has none: the receipt carries the status
// TerminateProcess gave the child and says how the run ended.
func TestWindowsHasNo128PlusN(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	b := newWinBench(t, 0)
	swap[winPlacer](t, &runWinPlace, &hangingPlace{fakeWinPlace: b.place})

	done := make(chan struct{})
	var r testkit.Ran
	go func() {
		r = b.exec(t, winArgv(t, b.scratch)...)
		close(done)
	}()
	// Let the run reach its wait, then ask the tool to stop.
	for i := 0; i < 200 && !strings.Contains(b.order(), "start:"); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	b.sigs <- os.Interrupt
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the verb did not return after the tool was asked to stop; a wait without an end is the thing this verb never does")
	}
	assert.LessOrEqual(t, r.Code, 128, "windows has no signals and there is no 128+N status to give")
	assert.Contains(t, r.Stderr, "windows has none", "the note does not say the status is TerminateProcess's and not a signal's")
	assert.True(t, strings.Contains(b.order(), "close:job-1") || strings.Contains(b.order(), "remove:nova-j1"), "an interrupted run left the place behind: %s", b.order())
}

// validateRun, asked with the platform NAMED, for the flags whose answer is per platform.
func TestValidateRunJudgesEachPlatformsFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		goos, argv, reason string
		refused            bool
	}{
		// W4: --memory and --cpu are accepted and IGNORED off windows, as --name is, so one
		// caller builds one argv for three platforms...
		{"darwin", "--name j1 --size 8g --memory 4g --cpu 50 -- /bin/sh", "bad_memory", false},
		{"darwin", "--name j1 --size 8g --memory 4g --cpu 50 -- /bin/sh", "bad_cpu", false},
		{"linux", "--name j1 --size 8g --memory 4g --cpu 50 -- /bin/sh", "bad_memory", false},
		{"linux", "--name j1 --size 8g --memory 4g --cpu 50 -- /bin/sh", "bad_cpu", false},
		// ...but their SHAPES are checked everywhere: a typo ignored on a Mac and refused on
		// a bench is a bug found on the wrong machine.
		{"darwin", "--name j1 --size 8g --memory 0 -- /bin/sh", "bad_memory", true},
		{"darwin", "--name j1 --size 8g --memory lots -- /bin/sh", "bad_memory", true},
		{"darwin", "--name j1 --size 8g --cpu 0 -- /bin/sh", "bad_cpu", true},
		{"darwin", "--name j1 --size 8g --cpu 101 -- /bin/sh", "bad_cpu", true},
		{"darwin", "--name j1 --size 8g --cpu half -- /bin/sh", "bad_cpu", true},
		// W6: on darwin --size stays REQUIRED, because there the APFS volume quota is real.
		{"darwin", "--name j1 -- /bin/sh", "bad_size", true},
		// W5: --scratch is required on windows and absolute there, and is not a darwin flag.
		{"windows", "--name j1 -- /bin/sh", "bad_scratch", true},                  // no default: not TEMP, not the profile
		{"windows", "--name j1 --scratch nova -- /bin/sh", "bad_scratch", true},   // relative
		{"windows", "--name j1 --scratch C:nova -- /bin/sh", "bad_scratch", true}, // drive-relative: a directory per drive
		{"windows", "--name j1 --scratch /nova -- /bin/sh", "bad_scratch", true},  // a unix root is not a windows absolute
		{"windows", `--name j1 --scratch C:\nova -- /bin/sh`, "bad_scratch", false},
		{"windows", `--name j1 --scratch \\s\share -- /bin/sh`, "bad_scratch", false},       // a UNC share is absolute
		{"darwin", `--name j1 --size 8g --scratch C:\nova -- /bin/sh`, "bad_scratch", true}, // darwin makes its own volume
		{"darwin", "--name j1 --size 8g -- /bin/sh", "bad_scratch", false},
		// W9: no --place is not refused on windows (the default is job, below).
		{"windows", `--name j1 --scratch C:\nova -- cmd.exe`, "bad_place", false},
	} {
		f := parseRun(strings.Fields(tc.argv))
		_, bad := validateRun(&f, tc.goos)
		assert.Equal(t, tc.refused, hasReason(bad, tc.reason), "%s on %s: %s refused, want %v (all: %v)", tc.argv, tc.goos, tc.reason, tc.refused, reasonsOf(bad))
	}

	f := parseRun(strings.Fields(`--name j1 --scratch C:\nova -- cmd.exe`))
	validateRun(&f, "windows")
	require.Equal(t, placeJob, f.place, "Windows Sandbox is one instance per machine, and a pool of workers each wanting one is a queue of one")
}

// absolutePathFor is the pure half of --scratch, and what internal/sandbox's winpath tests
// exist for: filepath.IsAbs answers for the HOST, and a Mac gets `C:\nova` and `/nova`
// exactly backwards.
func TestAbsolutePathForNamesThePlatform(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		goos, path string
		abs        bool
	}{
		{"windows", `C:\nova`, true}, {"windows", `c:/nova`, true}, {"windows", `\\server\share`, true},
		{"windows", `C:nova`, false}, {"windows", `nova`, false}, {"windows", `/nova`, false}, {"windows", "", false},
		{"darwin", "/nova", true}, {"darwin", `C:\nova`, false}, {"darwin", "nova", false},
	} {
		assert.Equal(t, tc.abs, absolutePathFor(tc.goos, tc.path), "absolutePathFor(%q, %q)", tc.goos, tc.path)
	}
}

// W8's document is a pure function of its input, asserted line by line.
func TestTheWSBDocumentIsW8sFile(t *testing.T) {
	t.Parallel()

	xml := wsbDocument(wsbInput{
		Reads:   []string{`C:\go`, `C:\src & co`},
		Scratch: `C:\nova\nova-j1`,
		Argv:    []string{"cmd.exe", "/c", "build"},
		MemMB:   4096,
	})
	for _, want := range []string{
		"<HostFolder>C:\\go</HostFolder>", "<ReadOnly>true</ReadOnly>", "<HostFolder>C:\\nova\\nova-j1</HostFolder>",
		"<ReadOnly>false</ReadOnly>", "<Networking>Disable</Networking>", "<MemoryInMB>4096</MemoryInMB>", "<LogonCommand>", wsbExitFile,
		"C:\\src &amp; co", // an & is an ordinary NTFS path character and must not make the document malformed
	} {
		assert.Contains(t, xml, want)
	}
	assert.Equal(t, 2, strings.Count(xml, "<ReadOnly>true</ReadOnly>"), "one read-only mapping PER --read")
	assert.Equal(t, 1, strings.Count(xml, "<ReadOnly>false</ReadOnly>"), "exactly one writable mapping, the scratch")
	// Networking is Disable UNLESS the run allows it; and <MemoryInMB>0</MemoryInMB> is a
	// document Windows Sandbox refuses, so an unset --memory leaves the element out.
	on := wsbDocument(wsbInput{Scratch: `C:\nova\nova-j1`, Argv: []string{"cmd.exe"}, Net: true})
	assert.Contains(t, on, "<Networking>Default</Networking>")
	assert.NotContains(t, on, "<MemoryInMB>")
}

// W10's mechanism: the <LogonCommand> ENDS by writing the command's own %ERRORLEVEL% to the
// status file, with `&` and not `&&`, because an absent file is 124 and a failing command is
// not a timeout.
func TestTheWSBLogonCommandWritesTheStatusWhateverHappened(t *testing.T) {
	t.Parallel()

	cmd := wsbLogonCommand(wsbInput{Scratch: `C:\nova\nova-j1`, Argv: []string{"cmd.exe", "/c", "exit 3"}})
	require.Contains(t, cmd, "%ERRORLEVEL%")
	require.Contains(t, cmd, wsbExitFile)
	assert.NotContains(t, cmd, "&& echo")
}

// W11 `no-windows-path-reaches-wsl`, the runtime half: no argv this tool composes names wsl,
// wsl.exe or a \\wsl$\ path. Containment that only holds inside WSL is containment on another
// machine, and the tripwire matches the PROGRAM, not a substring.
func TestNoWindowsPathReachesWSL(t *testing.T) {
	t.Parallel()

	for _, argv := range [][]string{
		{"wsl", "-e", "bash"}, {`C:\Windows\System32\wsl.exe`, "--", "make"}, {"WSL.EXE"},
		{`\\wsl$\Ubuntu\home\me\build.sh`}, {`\\wsl.localhost\Ubuntu\bin\sh`},
	} {
		_, found := wslInArgv(argv)
		assert.True(t, found, "%v is not caught by the WSL tripwire", argv)
	}
	for _, argv := range [][]string{{"cmd.exe", "/c", "build"}, {`C:\Go\bin\go.exe`, "build", "./..."}, {"newsletter.exe"}} {
		bad, found := wslInArgv(argv)
		assert.False(t, found, "%v was refused as WSL on the strength of %q", argv, bad)
	}
}

// W11, the SOURCE half: a tripwire on every exec site, reading this package rather than
// trusting the runtime check, because a future exec site that forgot wslInArgv would leave
// that check green and the rule broken.
func TestNoExecSiteInThisToolSpellsWSL(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		// The tripwire's own file and this test name WSL on purpose: they are what refuses it.
		if e.IsDir() || !strings.HasSuffix(name, ".go") || name == "runwin.go" || name == "runwin_test.go" {
			continue
		}
		for i, line := range strings.Split(goCodeOnly(t, name), "\n") {
			for _, spelling := range []string{`"wsl`, `wsl.exe`, `\\wsl$`} {
				assert.NotContains(t, strings.ToLower(line), spelling, "%s:%d names WSL: %s\nW11: a build that reaches for WSL when AppContainer, the Job Object or Windows Sandbox is unavailable must REFUSE instead", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// goCodeOnly is one .go file with its COMMENTS DROPPED, so a tripwire judges what the tool
// DOES and not what a comment says about it. Both source tripwires went red on their own prose
// the first time they ran (measured 2026-09-18: runwin_windows.go explains in words why it
// never calls AssignProcessToJobObject or imports golang.org/x/sys), and a tripwire a comment
// can trip teaches deleting the comment. Parsing WITHOUT parser.ParseComments leaves the
// printer no comments to print.
func goCodeOnly(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	var b strings.Builder
	require.NoError(t, printer.Fprint(&b, fset, f))
	return b.String()
}

// W2's second red test, `the-job-never-permits-breakaway`, read off the Win32 body's SOURCE:
// the assertion is about a line that is NOT there, and no fake can stand in for an absence.
func TestTheWin32BodyNeverPermitsBreakaway(t *testing.T) {
	t.Parallel()

	src := goCodeOnly(t, "runwin_windows.go")
	for _, flag := range []string{"jobObjectLimitBreakawayOK", "jobObjectLimitSilentBreakawayOK"} {
		assert.Equal(t, 1, strings.Count(src, flag), "%s must be named once, in its declaration: a second mention is an assignment, and breakaway is how a tree escapes the kill", flag)
	}
	assert.Contains(t, src, "LimitFlags = jobObjectLimitKillOnJobClose", "without KILL_ON_JOB_CLOSE as the base limit, closing the handle leaves the tree running and the scratch cannot be removed")
	assert.NotContains(t, src, "AssignProcessToJobObject", "W3 puts the child in the job AT CREATION: CreateProcess-then-assign leaves a window in which a child spawned outside the job survives the kill")
	assert.Contains(t, src, "procThreadAttributeJobList", "PROC_THREAD_ATTRIBUTE_JOB_LIST is the whole of W3")
	assert.NotContains(t, src, "golang.org/x/sys", "go.mod carries the standard library and nothing else, and three class tests in internal/ci read the tree on that premise")
}

// The \\?\ prefix, which every grant and every removal goes through: a scratch under a deep
// profile plus a Go module cache reaches MAX_PATH in ORDINARY use.
func TestWinLongPathPrefixesOnlyWhatItShould(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		`C:\nova\nova-j1`:     `\\?\C:\nova\nova-j1`,
		`c:/nova/nova-j1`:     `\\?\c:\nova\nova-j1`,
		`\\?\C:\already`:      `\\?\C:\already`,
		`\\.\pipe\x`:          `\\.\pipe\x`,
		`\\server\share\nova`: `\\?\UNC\server\share\nova`,
		`nova-j1`:             `nova-j1`, // relative: \\?\ turns off normalisation, so it is not a path at all
		`C:nova`:              `C:nova`,
		``:                    ``,
	} {
		assert.Equal(t, want, winLongPath(in), "winLongPath(%q)", in)
	}
}

// The command line is one STRING on windows, not an argv: joined on a space,
// `C:\Program Files\Go\bin\go.exe` (the ordinary path) would reach the child as two arguments.
func TestWinCommandLineQuotesTheWayCommandLineToArgvWUnquotes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"cmd.exe", "/c", "build"}, `cmd.exe /c build`},
		{[]string{`C:\Program Files\Go\bin\go.exe`, "build"}, `"C:\Program Files\Go\bin\go.exe" build`},
		{[]string{`a b`, `c"d`}, `"a b" "c\"d"`},
		// A trailing backslash inside quotes is DOUBLED, or it would escape the closing quote
		// and swallow the next argument.
		{[]string{`C:\dir with space\`, "next"}, `"C:\dir with space\\" next`},
		{[]string{""}, `""`},
	} {
		assert.Equal(t, tc.want, winCommandLine(tc.in), "winCommandLine(%q)", tc.in)
	}
}

// --memory's number, in the spellings --size already takes; a cap this tool cannot read is
// a cap it must refuse.
func TestParseBytesTakesTheSizeSpellings(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]int64{"1024": 1024, "64m": 64 << 20, "4g": 4 << 30, "4G": 4 << 30, "4gb": 4 << 30, "1.5g": 1536 << 20, "2t": 2 << 40} {
		got, ok := parseBytes(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "0", "-1", "lots", "50%", "g"} {
		_, ok := parseBytes(bad)
		assert.False(t, ok, bad)
	}
}

// The status file's three answers: not yet (absent is not an error), a number, and something
// that is not one -- a guest that ran something this host cannot account for, which is 126.
func TestReadWSBExit(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), wsbExitFile)
	_, ok := readWSBExit(path)
	assert.False(t, ok, "absent is `not yet`")
	for body, want := range map[string]int{" 7 \r\n": 7, "ECHO is off.": 126} {
		testkit.WriteFile(t, path, body, 0o600)
		got, ok := readWSBExit(path)
		assert.True(t, ok, body)
		assert.Equal(t, want, got, body)
	}
}

func hasReason(bad []sandbox.Refusal, reason string) bool {
	for _, r := range bad {
		if r.Reason == reason {
			return true
		}
	}
	return false
}

func reasonsOf(bad []sandbox.Refusal) []string {
	out := make([]string, 0, len(bad))
	for _, r := range bad {
		out = append(out, r.Reason)
	}
	return out
}

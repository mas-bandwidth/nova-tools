package main

// The WINDOWS half of the run verb, tested on whatever host this runs on.
//
// THERE IS NO WINDOWS BENCH IN THE ESTATE (2026-09-18). docs/SPEC-SANDBOX.md's windows
// section was written before one arrives so that it is not designed under fire, and these
// tests are the same move for the code: every Win32 call is behind runwin.go's `winPlacer`,
// a fake stands in for it, and what is asserted is the SEQUENCE -- which is where the
// contract lives. W1's "both or neither", W2's kill before the delete, W3's child-in-the-job
// at creation, W4's caps on the job, W5's one writable place, W6's refusal, W7's retry and
// its leak, W8/W9/W10's wsb path, W11's tripwire and W12's shape.
//
// WHAT THESE CANNOT PROVE, and do not claim to: that CreateJobObjectW, the extended limit
// information, PROC_THREAD_ATTRIBUTE_JOB_LIST and WindowsSandbox.exe behave on a real
// Windows machine as the rules say. The first Windows bench proves that. These prove the
// shape that machine will be asked for, and they go red the moment the shape changes.
//
// They follow internal/sandbox/winpath_test.go's pattern exactly: the platform is a
// PARAMETER, never runtime.GOOS, because a test that only ever walks the darwin path calls a
// windows bug green.

import (
	"bytes"
	"errors"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// fakeWinPlace is the Windows seam. It records the calls IN ORDER, because the order is the
// contract: nothing runs before the job and the scratch exist, nothing is deleted before the
// job is closed, and nothing exits before the scratch is gone.
type fakeWinPlace struct {
	calls []string

	exists   bool
	used     int64
	wsbEd    string
	wsbOK    bool
	wsbBusy  string
	wsbRun   bool
	exitCode int

	existsErr, makeErr, jobErr, startErr, closeErr, removeErr error
	wsbAvailErr, wsbRunErr, wsbStartErr                       error

	// removeOKAfter is W7's bounded retry expressed as a fake: the first N removals fail
	// with a transient hold and the one after that works.
	removeOKAfter int
	removes       int

	gotLimits winLimits
	gotSpec   winStartSpec
	gotXML    string
	jobsOpen  int
}

func (f *fakeWinPlace) Exists(dir string) (bool, error) {
	f.calls = append(f.calls, "exists:"+filepath.Base(dir))
	return f.exists, f.existsErr
}

func (f *fakeWinPlace) MakeScratch(dir string) error {
	f.calls = append(f.calls, "scratch:"+filepath.Base(dir))
	if f.makeErr != nil {
		return f.makeErr
	}
	// A real MakeScratch makes work/ and home/ too, and sandbox.Build below resolves the
	// cwd, so they have to be there for the policy to build on this host.
	for _, d := range []string{"", "work", "home"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeWinPlace) CreateJob(l winLimits) (winJob, error) {
	f.calls = append(f.calls, "job:"+limitsWord(l))
	if f.jobErr != nil {
		return nil, f.jobErr
	}
	f.gotLimits = l
	f.jobsOpen++
	return "job-1", nil
}

func (f *fakeWinPlace) Start(job winJob, spec winStartSpec) (winStarted, error) {
	f.calls = append(f.calls, "start:"+job.(string))
	if f.startErr != nil {
		return winStarted{}, f.startErr
	}
	f.gotSpec = spec
	done := make(chan int, 1)
	done <- f.exitCode
	return winStarted{done: done, pid: 4242}, nil
}

func (f *fakeWinPlace) CloseJob(job winJob) error {
	f.calls = append(f.calls, "close:"+job.(string))
	f.jobsOpen--
	return f.closeErr
}

func (f *fakeWinPlace) Used(string) (int64, error) {
	f.calls = append(f.calls, "used")
	return f.used, nil
}

func (f *fakeWinPlace) RemoveTree(root, dir string, _ time.Duration) error {
	f.removes++
	f.calls = append(f.calls, "remove:"+filepath.Base(dir))
	if f.removes <= f.removeOKAfter {
		return errors.New("ERROR_SHARING_VIOLATION: the file is in use by another process")
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	return os.RemoveAll(dir)
}

func (f *fakeWinPlace) WSBAvailable() (string, bool, error) {
	f.calls = append(f.calls, "wsb-available")
	return f.wsbEd, f.wsbOK, f.wsbAvailErr
}

func (f *fakeWinPlace) WSBRunning() (string, bool, error) {
	f.calls = append(f.calls, "wsb-running")
	return f.wsbBusy, f.wsbRun, f.wsbRunErr
}

func (f *fakeWinPlace) StartWSB(file, xml string) error {
	f.calls = append(f.calls, "wsb-start:"+filepath.Base(file))
	f.gotXML = xml
	return f.wsbStartErr
}

func limitsWord(l winLimits) string {
	return "mem=" + strconv.FormatInt(l.MemoryBytes, 10) + ",cpu=" + strconv.Itoa(l.CPUPercent)
}

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
	oldPlace, oldSigs, oldGOOS, oldWall := runWinPlace, runSignals, runGOOS, runWinWall
	t.Cleanup(func() { runWinPlace, runSignals, runGOOS, runWinWall = oldPlace, oldSigs, oldGOOS, oldWall })

	runWinPlace = b.place
	runGOOS = "windows"
	runSignals = func() (<-chan os.Signal, func()) { return b.sigs, func() {} }
	// The wall is not built (runwin_other.go / wrap_other.go), and every test below is about
	// the PLACE, so the wall says yes here and exactly one test below turns it off again.
	runWinWall = func() (string, bool) { return "appcontainer", true }
	return b
}

// winScratchArg is the --scratch a VERB-level test types. It is a windows path because
// validateRun judges it as one, and it never reaches the placer: every test that uses it
// refuses before anything is made.
const winScratchArg = `C:\nova`

// args is the argv a windows caller writes: --scratch instead of --size, everything else the
// same as darwin's. --scratch here is the HOST's temp directory, because the fake has to make
// a real place in it; the flag's own shape is judged by validateRun and is asserted against
// that function directly, which is the only way both answers can be right at once on a Mac.
// The command is the host's, because sandbox.Build resolves it before any policy is built and
// a name on no PATH would refuse for the wrong reason.
func (b *winBench) args(t *testing.T, extra ...string) []string {
	t.Helper()
	args := append([]string{"--name", "j1", "--scratch", b.scratch}, extra...)
	args = append(args, "--")
	return append(args, shellOf(t)...)
}

// exec drives the windows verb FROM THE LOOK ONWARDS, with the argv already parsed. It is
// the same seam darwin's runOnce uses on runDisposable, and it is here for the same reason:
// what is under test below is the sequence over the placer, not the argv, and the two cannot
// both be judged on a host whose idea of an absolute path is the other platform's.
func (b *winBench) exec(t *testing.T, args ...string) (int, string) {
	t.Helper()
	f := parseRun(args)
	if f.place == "" {
		f.place = placeJob
	}
	var d time.Duration
	if f.timeout != "" {
		d, _ = time.ParseDuration(f.timeout)
	}
	var out, errb bytes.Buffer
	code := runDisposableWindows(f, d, nil, &out, &errb, []string{"PATH=" + os.Getenv("PATH")})
	return code, errb.String()
}

// verb is the whole verb, argv and all, for the refusals that happen BEFORE anything is
// made. Those use winScratchArg, which is absolute on the platform the tool believes it is on.
func (b *winBench) verb(t *testing.T, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"--name", "j1", "--scratch", winScratchArg}, extra...)
	args = append(args, "--")
	args = append(args, shellOf(t)...)
	var out, errb bytes.Buffer
	return runVerb(args, nil, &out, &errb, []string{"PATH=" + os.Getenv("PATH")}), errb.String()
}

func (b *winBench) order() string { return strings.Join(b.place.calls, " ") }

// ---------------------------------------------------------------------------------------
// W1, W2, W5, W7: the sequence.
// ---------------------------------------------------------------------------------------

// The contract in one line, and it is the SAME line as darwin's: whatever the command did,
// the place it did it in is gone.
func TestWindowsRunCreatesJobAndScratchRunsAndAlwaysDeletes(t *testing.T) {
	for _, code := range []int{0, 7, 1} {
		b := newWinBench(t, code)
		got, errOut := b.exec(t, b.args(t)...)
		if got != code {
			t.Fatalf("the windows run verb returned %d for a command that exited %d; the status belongs to the wrapped command\n%s", got, code, errOut)
		}
		want := "exists:nova-j1 scratch:nova-j1 job:mem=0,cpu=0 start:job-1 close:job-1 used remove:nova-j1"
		if b.order() != want {
			t.Fatalf("the windows call order is not look-create-job-run-kill-delete:\n got: %s\nwant: %s", b.order(), want)
		}
		if !strings.Contains(errOut, "SANDBOX DONE name=j1 exit="+strconv.Itoa(code)) || !strings.Contains(errOut, "freed=4096") {
			t.Errorf("the receipt is not the contract's SANDBOX DONE line:\n%s", errOut)
		}
		if _, err := os.Stat(filepath.Join(b.scratch, "nova-j1")); !os.IsNotExist(err) {
			t.Errorf("the scratch is still on the disk after the run; the whole verb is that it is not")
		}
	}
}

// W2 and W7, as an ORDERING and not as a pair of calls: the job is closed BEFORE the
// removal, every time. A running image inside the scratch cannot be removed and cannot be
// renamed aside -- NTFS raises a sharing violation and there is no replace-the-inode trick --
// so a delete attempted before the kill is a leak by construction.
func TestWindowsTheJobIsClosedBeforeTheScratchIsRemoved(t *testing.T) {
	b := newWinBench(t, 0)
	b.exec(t, b.args(t)...)
	closed, removed := -1, -1
	for i, c := range b.place.calls {
		if strings.HasPrefix(c, "close:") && closed < 0 {
			closed = i
		}
		if strings.HasPrefix(c, "remove:") && removed < 0 {
			removed = i
		}
	}
	if closed < 0 || removed < 0 {
		t.Fatalf("the run neither closed the job nor removed the scratch: %s", b.order())
	}
	if closed > removed {
		t.Fatalf("the scratch was removed before the job was closed: %s. A directory holding a running .exe cannot be removed, and a rename over a running image raises ERROR_SHARING_VIOLATION -- there is no unix replace-the-inode trick on NTFS", b.order())
	}
	if b.place.jobsOpen != 0 {
		t.Errorf("%d job handle(s) are still open after the verb returned; the kill IS the close, and a job this tool still holds is a tree still running", b.place.jobsOpen)
	}
}

// W1's red test, by name: `a-windows-run-creates-both-the-job-and-the-scratch-or-neither`.
// A scratch with no job leaves a survivor holding a handle to the directory the tool is
// about to delete, so the scratch made beside a job that failed is unmade before the verb
// returns and NOTHING is run.
func TestWindowsAJobThatFailsUnmakesTheScratchBesideIt(t *testing.T) {
	b := newWinBench(t, 0)
	b.place.jobErr = errors.New("CreateJobObjectW: access denied")
	code, errOut := b.exec(t, b.args(t)...)
	if code != 125 {
		t.Fatalf("a job that could not be made must refuse at 125, got %d\n%s", code, errOut)
	}
	if strings.Contains(b.order(), "start:") {
		t.Fatalf("the command was started with no job to hold it: %s", b.order())
	}
	if !strings.Contains(b.order(), "remove:nova-j1") {
		t.Fatalf("the scratch made beside the failed job was not removed: %s", b.order())
	}
	if _, err := os.Stat(filepath.Join(b.scratch, "nova-j1")); !os.IsNotExist(err) {
		t.Errorf("the scratch survived a failed job: both or neither is the rule")
	}
	if !strings.Contains(errOut, "reason=volume_failed") {
		t.Errorf("the refusal does not name the reason:\n%s", errOut)
	}
}

// W5: <scratch>/nova-<n> is the run's ONLY --write, and the working directory and HOME are
// inside it. A second writable root would be a place the delete does not reach.
func TestWindowsTheScratchIsTheOnlyWrite(t *testing.T) {
	b := newWinBench(t, 0)
	reads := t.TempDir()
	b.exec(t, b.args(t, "--read", reads)...)
	p := b.place.gotSpec.Policy
	if p == nil {
		t.Fatal("the command was never started, so there is no policy to read")
	}
	if len(p.Writes) != 1 {
		t.Fatalf("the run has %d writable roots, want exactly 1 -- the scratch: %v", len(p.Writes), p.Writes)
	}
	dir := filepath.Join(b.scratch, "nova-j1")
	if !strings.HasPrefix(p.Writes[0], dir) {
		t.Errorf("the one writable root is %q and not the scratch %q", p.Writes[0], dir)
	}
	if !strings.HasPrefix(p.Cwd, dir) || filepath.Base(p.Cwd) != "work" {
		t.Errorf("the working directory is %q, want <scratch>/work", p.Cwd)
	}
	if !strings.HasPrefix(p.Home, dir) || filepath.Base(p.Home) != "home" {
		t.Errorf("HOME is %q, want <scratch>/home", p.Home)
	}
	if !strings.HasPrefix(p.Tmp, dir) {
		t.Errorf("the temp directory is %q, which is off the scratch: TEMP and TMP must be on the place that is deleted", p.Tmp)
	}
	// --read passes through UNCHANGED: a shared toolchain or reference checkout is read in
	// place and never copied into the disposable place.
	if len(p.Reads) == 0 {
		t.Errorf("--read did not reach the policy; a shared toolchain is read in place, never copied")
	}
}

// W5 again, the other way: a run never joins a place it did not make.
func TestWindowsAnExistingScratchIsRefusedNotJoined(t *testing.T) {
	b := newWinBench(t, 0)
	b.place.exists = true
	code, errOut := b.exec(t, b.args(t)...)
	if code != 125 || !strings.Contains(errOut, "reason=volume_exists") {
		t.Fatalf("an existing <scratch>/nova-<n> must refuse with volume_exists at 125; got %d\n%s", code, errOut)
	}
	if strings.Contains(b.order(), "scratch:") || strings.Contains(b.order(), "job:") {
		t.Errorf("something was made before the refusal: %s", b.order())
	}
}

// ---------------------------------------------------------------------------------------
// W4: the caps are the job's.
// ---------------------------------------------------------------------------------------

// The caps reach the JOB, in the job's own units, and they are set on the job -- which the
// production body does before any process is in it.
func TestWindowsMemoryAndCPUReachTheJob(t *testing.T) {
	b := newWinBench(t, 0)
	b.exec(t, b.args(t, "--memory", "4g", "--cpu", "50")...)
	if got, want := b.place.gotLimits.MemoryBytes, int64(4)<<30; got != want {
		t.Errorf("--memory 4g reached the job as %d bytes, want %d", got, want)
	}
	if got := b.place.gotLimits.CPUPercent; got != 50 {
		t.Errorf("--cpu 50 reached the job as %d, want 50", got)
	}
	// The job is made with the caps already on it: the fake records them at CreateJob, so a
	// body that created the job bare and set the limits afterwards would show mem=0 here.
	if !strings.Contains(b.order(), "job:mem="+strconv.FormatInt(int64(4)<<30, 10)+",cpu=50") {
		t.Errorf("the caps were not on the job at creation: %s. A limit applied to a job that already holds a running tree has been escaped once already", b.order())
	}
}

// W4's second half: accepted and IGNORED off windows, the way --name already is, so one
// caller builds one argv for three platforms.
func TestWindowsMemoryAndCPUAreAcceptedAndIgnoredOffWindows(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		f := parseRun([]string{"--name", "j1", "--size", "8g", "--memory", "4g", "--cpu", "50", "--", "/bin/sh"})
		_, bad := validateRun(&f, goos)
		for _, r := range bad {
			if r.Reason == "bad_memory" || r.Reason == "bad_cpu" {
				t.Errorf("--memory and --cpu are refused on %s: %s. They are accepted and ignored off windows, so one caller writes one argv for three platforms", goos, r.Text)
			}
		}
	}
	// Their SHAPES are still checked everywhere: a typo silently ignored on a Mac and
	// refused on a bench is a bug found on the wrong machine.
	for _, tc := range []struct{ flag, value, reason string }{
		{"--memory", "0", "bad_memory"},
		{"--memory", "lots", "bad_memory"},
		{"--cpu", "0", "bad_cpu"},
		{"--cpu", "101", "bad_cpu"},
		{"--cpu", "half", "bad_cpu"},
	} {
		f := parseRun([]string{"--name", "j1", "--size", "8g", tc.flag, tc.value, "--", "/bin/sh"})
		_, bad := validateRun(&f, "darwin")
		if !hasReason(bad, tc.reason) {
			t.Errorf("%s %s is not refused with %s on darwin: %v", tc.flag, tc.value, tc.reason, reasonsOf(bad))
		}
	}
}

// ---------------------------------------------------------------------------------------
// W6: --size is refused, not approximated.
// ---------------------------------------------------------------------------------------

// `size-on-windows-is-refused-not-approximated`. A ceiling the tool only MEASURES is not a
// ceiling, and the precedent is rule 7's net_unenforceable: a promise this tool cannot
// enforce is a refusal, never a note.
func TestWindowsSizeIsRefusedNotApproximated(t *testing.T) {
	b := newWinBench(t, 0)
	code, errOut := b.verb(t, "--size", "8g")
	if code != 125 {
		t.Fatalf("--size on windows must refuse at 125, got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "reason=size_unenforceable") {
		t.Fatalf("--size on windows does not refuse with reason=size_unenforceable:\n%s", errOut)
	}
	// Two remedies on the line, both of them real.
	if !strings.Contains(errOut, "--place wsb") || !strings.Contains(errOut, "--scratch") {
		t.Errorf("the refusal does not carry both remedies -- --place wsb, whose whole disk is discarded, and a --scratch on a volume already sized:\n%s", errOut)
	}
	if strings.Contains(b.order(), "scratch:") {
		t.Errorf("the refusal came after something was made: %s", b.order())
	}
	// And on darwin the flag stays REQUIRED, because there the APFS volume quota is real.
	f := parseRun([]string{"--name", "j1", "--", "/bin/sh"})
	_, bad := validateRun(&f, "darwin")
	if !hasReason(bad, "bad_size") {
		t.Errorf("--size is no longer required on darwin: %v. There the volume quota is real and the ceiling holds", reasonsOf(bad))
	}
}

// W5's flag half, both directions: --scratch is required on windows, absolute, and is not a
// darwin flag.
func TestWindowsScratchIsRequiredAndAbsolute(t *testing.T) {
	for _, tc := range []struct {
		goos, scratch string
		refused       bool
	}{
		{"windows", "", true},           // no default: not the TEMP variable, not the user profile
		{"windows", `nova`, true},       // relative
		{"windows", `C:nova`, true},     // drive-relative: a different directory per drive
		{"windows", `/nova`, true},      // a unix root is not an absolute windows path
		{"windows", `C:\nova`, false},   //
		{"windows", `\\s\share`, false}, // a UNC share is absolute
		{"darwin", `C:\nova`, true},     // darwin makes its own volume; there is nothing to make it under
		{"darwin", "", false},           // and on darwin the flag is simply absent
	} {
		args := []string{"--name", "j1"}
		if tc.goos != "windows" {
			args = append(args, "--size", "8g")
		}
		if tc.scratch != "" {
			args = append(args, "--scratch", tc.scratch)
		}
		args = append(args, "--", "/bin/sh")

		f := parseRun(args)
		_, bad := validateRun(&f, tc.goos)
		if got := hasReason(bad, "bad_scratch"); got != tc.refused {
			t.Errorf("--scratch %q on %s: bad_scratch=%v, want %v (all: %v)", tc.scratch, tc.goos, got, tc.refused, reasonsOf(bad))
		}
	}
}

// absolutePathFor is the pure half of that, and it is the one internal/sandbox's winpath
// tests exist for: filepath.IsAbs answers for the HOST, and the host here is a Mac, which
// gets `C:\nova` and `/nova` exactly backwards.
func TestAbsolutePathForNamesThePlatform(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
		abs        bool
	}{
		{"windows", `C:\nova`, true},
		{"windows", `c:/nova`, true},
		{"windows", `\\server\share`, true},
		{"windows", `C:nova`, false},
		{"windows", `nova`, false},
		{"windows", `/nova`, false},
		{"windows", "", false},
		{"darwin", "/nova", true},
		{"darwin", `C:\nova`, false},
		{"darwin", "nova", false},
	} {
		if got := absolutePathFor(tc.goos, tc.path); got != tc.abs {
			t.Errorf("absolutePathFor(%q, %q) = %v, want %v", tc.goos, tc.path, got, tc.abs)
		}
	}
}

// ---------------------------------------------------------------------------------------
// W7: the retry, and the leak.
// ---------------------------------------------------------------------------------------

// `a-windows-leak-exits-3-and-names-the-one-command-that-removes-it`. A caller that read 0
// would believe the machine was clean.
func TestWindowsALeakExitsThreeAndNamesTheOneCommand(t *testing.T) {
	b := newWinBench(t, 0)
	b.place.removeErr = errors.New("ERROR_SHARING_VIOLATION")
	code, errOut := b.exec(t, b.args(t)...)
	if code != exitLeak {
		t.Fatalf("a scratch that could not be removed must exit %d whatever the command did, got %d\n%s", exitLeak, code, errOut)
	}
	dir := filepath.Join(b.scratch, "nova-j1")
	if !strings.Contains(errOut, "SANDBOX LEAK name=j1") || !strings.Contains(errOut, "volume="+dir) {
		t.Errorf("the leak line does not name the run and the directory:\n%s", errOut)
	}
	if !strings.Contains(errOut, `remedy="rmdir /s /q `+dir+`"`) {
		t.Errorf("the leak line does not carry the ONE command that removes it -- rmdir /s /q, not `rm -rf`, and not a sentence:\n%s", errOut)
	}
	if !strings.Contains(errOut, "SANDBOX DONE name=j1 exit=0") {
		t.Errorf("the receipt is missing: a leak still reports what the command did:\n%s", errOut)
	}
	// The note names the one windows fact that explains it.
	if !strings.Contains(errOut, "running .exe") {
		t.Errorf("the leak note does not say why a windows scratch resists removal -- a running image cannot be removed and cannot be replaced in place:\n%s", errOut)
	}
}

// `a-held-handle-is-retried-before-it-is-a-leak`. The retry is inside the placer, and what
// is asserted here is that the verb hands it a WINDOW to retry within rather than treating
// the first failure as final -- Defender and the search indexer hold transient handles on
// files a run has just written.
func TestWindowsTheRemovalIsGivenARetryWindow(t *testing.T) {
	var got time.Duration
	b := newWinBench(t, 0)
	b.place.removeOKAfter = 0
	old := runWinPlace
	runWinPlace = &windowRecordingPlace{winPlacer: b.place, window: &got}
	t.Cleanup(func() { runWinPlace = old })

	if code, errOut := b.exec(t, b.args(t)...); code != 0 {
		t.Fatalf("a clean run returned %d\n%s", code, errOut)
	}
	if got <= 0 {
		t.Fatalf("the removal was given no retry window; a leak declared on the first sharing violation names a machine dirty that a second's patience would have left clean")
	}
	if got != winRemoveWindow {
		t.Errorf("the removal window is %s, want the constant %s", got, winRemoveWindow)
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

// ---------------------------------------------------------------------------------------
// The exit codes, and the two that windows does not have.
// ---------------------------------------------------------------------------------------

// 124 on --timeout, and the job closed -- which is the kill. There is no SIGTERM, no grace
// and no SIGKILL, because there is no signal to escalate from.
func TestWindowsATimeoutClosesTheJobAndExits124(t *testing.T) {
	b := newWinBench(t, 0)
	// A start that never finishes: the done channel is never written.
	old := runWinPlace
	hang := &hangingPlace{fakeWinPlace: b.place}
	runWinPlace = hang
	t.Cleanup(func() { runWinPlace = old })

	code, errOut := b.exec(t, b.args(t, "--timeout", "20ms")...)
	if code != exitTimeout {
		t.Fatalf("a command that outlived --timeout must exit %d, got %d\n%s", exitTimeout, code, errOut)
	}
	if !strings.Contains(b.order(), "close:job-1") {
		t.Fatalf("the job was not closed on the deadline: %s. The close IS the kill, and it is what reaches a grandchild the harness abandoned", b.order())
	}
	if !strings.Contains(errOut, "SANDBOX DONE name=j1 exit=124") {
		t.Errorf("the receipt does not carry 124:\n%s", errOut)
	}
	if !strings.Contains(errOut, "the whole tree") {
		t.Errorf("the timeout note does not say the tree went with the job:\n%s", errOut)
	}
}

// hangingPlace starts a command whose status never arrives until the job is closed, which
// is what a real job's kill-on-close does.
type hangingPlace struct {
	*fakeWinPlace
	done chan int
}

func (p *hangingPlace) Start(job winJob, spec winStartSpec) (winStarted, error) {
	p.calls = append(p.calls, "start:"+job.(string))
	p.gotSpec = spec
	p.done = make(chan int, 1)
	return winStarted{done: p.done, pid: 4242}, nil
}

func (p *hangingPlace) CloseJob(job winJob) error {
	p.calls = append(p.calls, "close:"+job.(string))
	p.jobsOpen--
	// KILL_ON_JOB_CLOSE: the tree dies with the handle, so the status arrives now.
	select {
	case p.done <- 1:
	default:
	}
	return nil
}

// "There is no 128+N." Windows has no signals, so a caller reading >128 as "killed by a
// signal" is reading a unix convention on a platform that has none. The status the job's
// termination gave the child is what the receipt carries, and the receipt is what says how
// the run ended.
func TestWindowsHasNo128PlusN(t *testing.T) {
	b := newWinBench(t, 0)
	old := runWinPlace
	hang := &hangingPlace{fakeWinPlace: b.place}
	runWinPlace = hang
	t.Cleanup(func() { runWinPlace = old })

	done := make(chan struct{})
	var code int
	var errOut string
	go func() {
		code, errOut = b.exec(t, b.args(t)...)
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
	if code > 128 {
		t.Errorf("the windows verb returned %d, which reads as 128+N; windows has no signals and there is no such status to give", code)
	}
	if !strings.Contains(errOut, "windows has none") {
		t.Errorf("the note does not say that the status is TerminateProcess's and not a signal's:\n%s", errOut)
	}
	if !strings.Contains(b.order(), "close:job-1") && !strings.Contains(b.order(), "remove:nova-j1") {
		t.Errorf("an interrupted run left the place behind: %s", b.order())
	}
}

// ---------------------------------------------------------------------------------------
// W8, W9, W10: Windows Sandbox.
// ---------------------------------------------------------------------------------------

// `wsb-refuses-on-an-edition-that-has-no-windows-sandbox`. It is Pro and Enterprise only and
// the optional feature must already be enabled; the refusal names the edition and the
// feature rather than saying "unavailable".
func TestWSBRefusesOnAnEditionThatHasNoWindowsSandbox(t *testing.T) {
	b := newWinBench(t, 0)
	b.place.wsbOK, b.place.wsbEd = false, "Home"
	code, errOut := b.exec(t, b.args(t, "--place", "wsb", "--timeout", "30m")...)
	if code != 125 || !strings.Contains(errOut, "reason=no_wsb") {
		t.Fatalf("--place wsb on an edition without it must refuse with no_wsb at 125; got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "Home") {
		t.Errorf("the refusal does not name the edition:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Containers-DisposableClientVM") {
		t.Errorf("the refusal does not name the optional feature, so a reader cannot act on it:\n%s", errOut)
	}
	if strings.Contains(b.order(), "scratch:") {
		t.Errorf("a mapped folder was made before the refusal: %s", b.order())
	}
}

// `a-second-wsb-run-refuses-rather-than-queues`. Windows Sandbox permits ONE running
// instance per machine, so a pool of workers each wanting one is a queue of one -- which is
// why the default is --place job and wsb is the review place.
func TestASecondWSBRunRefusesRatherThanQueues(t *testing.T) {
	b := newWinBench(t, 0)
	b.place.wsbRun, b.place.wsbBusy = true, "windowssandbox.exe pid=904"
	code, errOut := b.exec(t, b.args(t, "--place", "wsb", "--timeout", "30m")...)
	if code != 125 || !strings.Contains(errOut, "reason=wsb_busy") {
		t.Fatalf("a second --place wsb must refuse with wsb_busy at 125, never wait; got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "pid=904") {
		t.Errorf("the refusal does not name the running instance:\n%s", errOut)
	}
}

// W9's other half: the DEFAULT on windows is --place job, because a single-instance resource
// is not a pool's.
func TestTheWindowsDefaultPlaceIsTheJob(t *testing.T) {
	f := parseRun([]string{"--name", "j1", "--scratch", `C:\nova`, "--", "cmd.exe"})
	if _, bad := validateRun(&f, "windows"); hasReason(bad, "bad_place") {
		t.Fatalf("a run with no --place is refused: %v", reasonsOf(bad))
	}
	if f.place != placeJob {
		t.Fatalf("the windows default --place is %q, want %q: Windows Sandbox is one instance per machine and a pool of workers each wanting one is a queue of one", f.place, placeJob)
	}
}

// `wsb-refuses-a-run-with-no-timeout`. Without a deadline a guest that never writes the
// status file is a wait with no end, and this verb never waits without one.
func TestWSBRefusesARunWithNoTimeout(t *testing.T) {
	b := newWinBench(t, 0)
	code, errOut := b.verb(t, "--place", "wsb")
	if code != 125 || !strings.Contains(errOut, "reason=bad_timeout") {
		t.Fatalf("--place wsb with no --timeout must refuse with bad_timeout at 125; got %d\n%s", code, errOut)
	}
	if strings.Contains(b.order(), "wsb-start") {
		t.Errorf("a VM was started for a run with no deadline: %s", b.order())
	}
}

// `a-wsb-run-with-no-status-file-times-out-at-124`. This is the one place the contract
// bends: WindowsSandbox.exe returns as soon as the VM is up and carries no guest status.
func TestAWSBRunWithNoStatusFileTimesOutAt124(t *testing.T) {
	b := newWinBench(t, 0)
	tick := make(chan time.Time)
	oldPoll := runWinPoll
	runWinPoll = func(time.Duration) <-chan time.Time { return tick }
	t.Cleanup(func() { runWinPoll = oldPoll })

	code, errOut := b.exec(t, b.args(t, "--place", "wsb", "--timeout", "30ms")...)
	if code != exitTimeout {
		t.Fatalf("a wsb run whose guest wrote no status file must exit %d, got %d\n%s", exitTimeout, code, errOut)
	}
	if !strings.Contains(errOut, wsbExitFile) {
		t.Errorf("the note does not name the status file the host waited for:\n%s", errOut)
	}
	if !strings.Contains(b.order(), "remove:nova-j1") {
		t.Errorf("the mapped folder was not removed after the VM closed: %s", b.order())
	}
}

// And the status that DOES come back through the file is the command's own.
func TestAWSBRunReadsTheGuestsStatusOutOfTheScratch(t *testing.T) {
	b := newWinBench(t, 0)
	oldRead := runWinReadExit
	runWinReadExit = func(path string) (int, bool) {
		if filepath.Base(path) != wsbExitFile {
			t.Errorf("the host waited on %q, want the status file in the mapped writable folder", path)
		}
		return 7, true
	}
	t.Cleanup(func() { runWinReadExit = oldRead })

	code, errOut := b.exec(t, b.args(t, "--place", "wsb", "--timeout", "30m")...)
	if code != 7 {
		t.Fatalf("the guest's own status did not come back: got %d, want 7\n%s", code, errOut)
	}
	want := "exists:nova-j1 wsb-available wsb-running scratch:nova-j1 wsb-start:run.wsb used remove:nova-j1"
	if b.order() != want {
		t.Errorf("the wsb sequence is wrong:\n got: %s\nwant: %s", b.order(), want)
	}
}

// W8's document. It is a pure function of its input, so it is asserted line by line here and
// nothing about it waits for a bench.
func TestTheWSBDocumentIsW8sFile(t *testing.T) {
	xml := wsbDocument(wsbInput{
		Reads:   []string{`C:\go`, `C:\src & co`},
		Scratch: `C:\nova\nova-j1`,
		Argv:    []string{"cmd.exe", "/c", "build"},
		MemMB:   4096,
	})
	for _, want := range []string{
		"<HostFolder>C:\\go</HostFolder>",
		"<ReadOnly>true</ReadOnly>",
		"<HostFolder>C:\\nova\\nova-j1</HostFolder>",
		"<ReadOnly>false</ReadOnly>",
		"<Networking>Disable</Networking>",
		"<MemoryInMB>4096</MemoryInMB>",
		"<LogonCommand>",
		wsbExitFile,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("the .wsb document has no %q:\n%s", want, xml)
		}
	}
	// One read-only mapping PER --read, plus exactly one writable one.
	if got := strings.Count(xml, "<ReadOnly>true</ReadOnly>"); got != 2 {
		t.Errorf("the document has %d read-only mappings for 2 --read paths", got)
	}
	if got := strings.Count(xml, "<ReadOnly>false</ReadOnly>"); got != 1 {
		t.Errorf("the document has %d writable mappings, want exactly 1 -- the scratch", got)
	}
	// A host path carrying an ampersand is an ordinary NTFS path and must not make the
	// document malformed.
	if !strings.Contains(xml, "C:\\src &amp; co") {
		t.Errorf("an & in a host path was not escaped, so the document is not XML:\n%s", xml)
	}
	// Networking is Disable UNLESS the run allows it, and the allowing is explicit.
	on := wsbDocument(wsbInput{Scratch: `C:\nova\nova-j1`, Argv: []string{"cmd.exe"}, Net: true})
	if !strings.Contains(on, "<Networking>Default</Networking>") {
		t.Errorf("a run that allows the network still gets Disable:\n%s", on)
	}
	// <MemoryInMB>0</MemoryInMB> is a document Windows Sandbox refuses, so an unset --memory
	// leaves the element out rather than writing a zero.
	if strings.Contains(on, "<MemoryInMB>") {
		t.Errorf("an unset --memory wrote a <MemoryInMB> element:\n%s", on)
	}
}

// W10's mechanism, in the one line that carries it: the <LogonCommand> ENDS by writing the
// command's own %ERRORLEVEL% to the status file, with `&` and not `&&`, because an absent
// file is 124 and a failing command is not a timeout.
func TestTheWSBLogonCommandWritesTheStatusWhateverHappened(t *testing.T) {
	cmd := wsbLogonCommand(wsbInput{Scratch: `C:\nova\nova-j1`, Argv: []string{"cmd.exe", "/c", "exit 3"}})
	if !strings.Contains(cmd, "%ERRORLEVEL%") {
		t.Fatalf("the logon command does not write the command's own status: %s", cmd)
	}
	if !strings.Contains(cmd, wsbExitFile) {
		t.Fatalf("the logon command does not name the status file: %s", cmd)
	}
	if strings.Contains(cmd, "&& echo") {
		t.Errorf("the status is written only when the command SUCCEEDS: %s. An absent file is 124 and a failing command is not a timeout", cmd)
	}
}

// ---------------------------------------------------------------------------------------
// W11: no windows path reaches WSL.
// ---------------------------------------------------------------------------------------

// `no-windows-path-reaches-wsl`, the runtime half: no argv this tool composes names wsl,
// wsl.exe or a \\wsl$\ path.
func TestNoWindowsPathReachesWSL(t *testing.T) {
	for _, argv := range [][]string{
		{"wsl", "-e", "bash"},
		{`C:\Windows\System32\wsl.exe`, "--", "make"},
		{"WSL.EXE"},
		{`\\wsl$\Ubuntu\home\me\build.sh`},
		{`\\wsl.localhost\Ubuntu\bin\sh`},
	} {
		if _, found := wslInArgv(argv); !found {
			t.Errorf("%v is not caught by the WSL tripwire; containment that only holds inside WSL is containment on another machine, and it is never the answer", argv)
		}
	}
	for _, argv := range [][]string{
		{"cmd.exe", "/c", "build"},
		{`C:\Go\bin\go.exe`, "build", "./..."},
		{"newsletter.exe"}, // holds "wsl" as a substring and is not WSL
	} {
		if bad, found := wslInArgv(argv); found {
			t.Errorf("%v was refused as WSL on the strength of %q; the tripwire matches the PROGRAM, not a substring", argv, bad)
		}
	}
}

// `no-windows-path-reaches-wsl`, the SOURCE half: a tripwire on every exec site. It reads
// this package rather than trusting the runtime check, because a future exec site that
// forgot to call wslInArgv would leave that check green and the rule broken.
func TestNoExecSiteInThisToolSpellsWSL(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("this package has to be readable for the tripwire to read it: %s", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// The tripwire's own file and this test name WSL on purpose: they are what refuses
		// it. Everything else in the package must not.
		if name == "runwin.go" || name == "runwin_test.go" {
			continue
		}
		for i, line := range strings.Split(goCodeOnly(t, name), "\n") {
			low := strings.ToLower(line)
			if strings.Contains(low, `"wsl`) || strings.Contains(low, `wsl.exe`) || strings.Contains(low, `\\wsl$`) {
				t.Errorf("%s:%d names WSL: %s\nW11: a build that reaches for WSL when AppContainer, the Job Object or Windows Sandbox is unavailable must REFUSE instead", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// goCodeOnly is one .go file with its COMMENTS DROPPED, so that a tripwire reading the
// source judges what the tool DOES and not what a comment says about it.
//
// It is here because both source tripwires above went red on their own prose the first time
// they ran, measured 2026-09-18: runwin_windows.go explains in words why it never calls
// AssignProcessToJobObject and why it does not import golang.org/x/sys, and a `strings.Contains`
// over the raw bytes cannot tell an explanation from a call. A tripwire that a comment can
// trip is a tripwire whose remedy is to delete the comment, which is the wrong lesson.
//
// Parsing WITHOUT parser.ParseComments leaves ast.File.Comments nil, and the printer then has
// no comments to print -- the same go/ast toolkit internal/ci's class tests already read the
// tree with.
func goCodeOnly(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s has to parse for a tripwire to read it: %s", path, err)
	}
	var b strings.Builder
	if err := printer.Fprint(&b, fset, f); err != nil {
		t.Fatalf("%s could not be printed back: %s", path, err)
	}
	return b.String()
}

// ---------------------------------------------------------------------------------------
// Rule 1 on windows, and the two constants the Win32 body must never set.
// ---------------------------------------------------------------------------------------

// The PLACE is built and the WALL is not, and a place without a wall is a directory that
// gets deleted -- which is hygiene, not containment. Rule 1 is OS-ENFORCED OR REFUSED, so
// the verb refuses and the refusal names the half that is missing.
func TestWindowsRefusesWhileTheWallIsNotBuilt(t *testing.T) {
	b := newWinBench(t, 0)
	runWinWall = func() (string, bool) { return "appcontainer", false }
	code, errOut := b.exec(t, b.args(t)...)
	if code != 125 || !strings.Contains(errOut, "reason=no_sandbox") {
		t.Fatalf("a windows run with no wall must refuse with no_sandbox at 125; got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "appcontainer") {
		t.Errorf("the refusal does not name the missing half, so a reader learns only that `the sandbox failed`:\n%s", errOut)
	}
	if b.order() != "" {
		t.Errorf("a machine with no wall had something made on it: %s", b.order())
	}
}

// W2's second red test, `the-job-never-permits-breakaway`, read off the Win32 body's SOURCE
// -- because the assertion is about a line that is NOT there, and no fake can stand in for
// an absence. Breakaway is exactly how a tree escapes the kill.
func TestTheWin32BodyNeverPermitsBreakaway(t *testing.T) {
	src := goCodeOnly(t, "runwin_windows.go")
	for _, flag := range []string{"jobObjectLimitBreakawayOK", "jobObjectLimitSilentBreakawayOK"} {
		// Named once in the const block, and never used. A second mention is an assignment.
		if n := strings.Count(src, flag); n != 1 {
			t.Errorf("%s is named %d times in runwin_windows.go, want exactly 1 (its declaration). JOB_OBJECT_LIMIT_BREAKAWAY_OK and JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK are how a tree escapes the kill, and this tool never sets either", flag, n)
		}
	}
	// And the flag that MUST be set is set.
	if !strings.Contains(src, "LimitFlags = jobObjectLimitKillOnJobClose") {
		t.Errorf("runwin_windows.go does not set JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE as the job's base limit; without it, closing the handle leaves the tree running and the scratch cannot be removed")
	}
	// W3: one CreateProcessW with the job on the attribute list, never an
	// AssignProcessToJobObject afterwards.
	if strings.Contains(src, "AssignProcessToJobObject") {
		t.Errorf("runwin_windows.go calls AssignProcessToJobObject; W3 puts the child in the job AT CREATION, because CreateProcess-then-assign leaves a window in which the child is alive and outside the job, and a child that spawns inside that window is a survivor the kill never reaches")
	}
	if !strings.Contains(src, "procThreadAttributeJobList") {
		t.Errorf("runwin_windows.go does not use PROC_THREAD_ATTRIBUTE_JOB_LIST; that attribute is the whole of W3")
	}
	// No new dependency: go.mod carries the standard library and nothing else.
	if strings.Contains(src, "golang.org/x/sys") {
		t.Errorf("runwin_windows.go imports golang.org/x/sys; go.mod carries the standard library and nothing else, and three class tests in internal/ci read the tree on that premise")
	}
}

// ---------------------------------------------------------------------------------------
// The pure functions the windows body stands on, asserted on this host.
// ---------------------------------------------------------------------------------------

// The \\?\ prefix, which every grant and every removal goes through: a scratch under a deep
// profile plus a Go module cache reaches MAX_PATH in ORDINARY use.
func TestWinLongPathPrefixesOnlyWhatItShould(t *testing.T) {
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
		if got := winLongPath(in); got != want {
			t.Errorf("winLongPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The command line, which is one STRING on windows and not an argv. A tool that joined on a
// space would hand `C:\Program Files\Go\bin\go.exe` to the child as two arguments, and that
// is the ordinary path on windows, not an unusual one.
func TestWinCommandLineQuotesTheWayCommandLineToArgvWUnquotes(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"cmd.exe", "/c", "build"}, `cmd.exe /c build`},
		{[]string{`C:\Program Files\Go\bin\go.exe`, "build"}, `"C:\Program Files\Go\bin\go.exe" build`},
		{[]string{`a b`, `c"d`}, `"a b" "c\"d"`},
		// A trailing backslash inside quotes is DOUBLED, or it would escape the closing
		// quote and swallow the next argument.
		{[]string{`C:\dir with space\`, "next"}, `"C:\dir with space\\" next`},
		{[]string{""}, `""`},
	} {
		if got := winCommandLine(tc.in); got != tc.want {
			t.Errorf("winCommandLine(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// --memory's number, in the same spellings --size already takes: a caller who learned
// `--size 8g` should not have to learn a second one.
func TestParseBytesTakesTheSizeSpellings(t *testing.T) {
	for in, want := range map[string]int64{
		"1024": 1024,
		"64m":  64 << 20,
		"4g":   4 << 30,
		"4G":   4 << 30,
		"4gb":  4 << 30,
		"1.5g": 1536 << 20,
		"2t":   2 << 40,
	} {
		got, ok := parseBytes(in)
		if !ok || got != want {
			t.Errorf("parseBytes(%q) = %d,%v; want %d,true", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "lots", "50%", "g"} {
		if got, ok := parseBytes(bad); ok {
			t.Errorf("parseBytes(%q) = %d,true; a cap this tool cannot read is a cap it must refuse", bad, got)
		}
	}
}

// The status file's three answers: not yet, a number, and something that is not one.
func TestReadWSBExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, wsbExitFile)
	if _, ok := readWSBExit(path); ok {
		t.Errorf("a status file that is not there yet answered; absent is `not yet`, not an error")
	}
	if err := os.WriteFile(path, []byte(" 7 \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := readWSBExit(path); !ok || got != 7 {
		t.Errorf("readWSBExit = %d,%v; the guest wrote 7", got, ok)
	}
	if err := os.WriteFile(path, []byte("ECHO is off."), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := readWSBExit(path); !ok || got != 126 {
		t.Errorf("readWSBExit = %d,%v; a guest that wrote something that is not a status ran something this host cannot account for, and that is 126", got, ok)
	}
}

// ---------------------------------------------------------------------------------------
// The contract that does NOT change across the three.
// ---------------------------------------------------------------------------------------

// The W-preamble in one test: the same verb, the same receipt grammar, the same exit codes.
// A caller writes one argv for three platforms and reads one grammar back.
func TestTheWindowsReceiptIsTheSameGrammarAsDarwins(t *testing.T) {
	b := newWinBench(t, 3)
	_, errOut := b.exec(t, b.args(t)...)
	for _, want := range []string{"SANDBOX OK ", "SANDBOX STEP ", "SANDBOX DONE name=j1 exit=3 wall=", " freed="} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the windows run does not print %q; the grammar is one grammar for three platforms:\n%s", want, errOut)
		}
	}
	// And the remedy a windows refusal carries is the WINDOWS argv: a reader handed the
	// darwin one would type --size, which the next line refuses.
	if got := remedyFor("windows"); !strings.Contains(got, "--scratch") || strings.Contains(got, "--size") {
		t.Errorf("the windows remedy line is %q; it must name --scratch and must not name --size", got)
	}
	if got := remedyFor("darwin"); !strings.Contains(got, "--size") {
		t.Errorf("the darwin remedy line lost --size: %q", got)
	}
}

// The help, on windows, answers the question rather than complaining about the argv that did
// not ask it -- and it says what --size does here, because that is the first thing a windows
// reader coming from the darwin docs will try.
func TestTheWindowsHelpNamesTheWindowsFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runVerb([]string{"help"}, nil, &out, &errb, nil); code != 0 {
		t.Fatalf("`run help` exited %d", code)
	}
	for _, want := range []string{"--scratch", "--memory", "--cpu", "--place", "REFUSED on windows"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the run banner does not mention %q:\n%s", want, out.String())
		}
	}
}

// ---------------------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------------------

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

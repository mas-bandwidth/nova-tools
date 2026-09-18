// The run verb's WINDOWS half: docs/SPEC-SANDBOX.md, "Windows — the disposable place",
// rules W1..W12.
//
// The contract does not change. `nova-sandbox run --name <n> ...` on windows is the same
// verb with the same five steps, the same receipt, the same leak line and the same exit
// codes as the darwin section: look, create, run, KILL, delete;
// `SANDBOX DONE name=<n> exit=<code> wall=<s> freed=<bytes>` on the way out;
// `SANDBOX LEAK ...` and exit 3 when the place could not be removed; 124 on --timeout; 125
// for every refusal of the tool's own. What differs is named here and nowhere else.
//
// WHAT THE PLACE IS (W1): a Job Object plus a per-run scratch directory, and the two are
// ONE unit. The job is the windows answer to the darwin section's process group -- step 4,
// the kill -- and the scratch is the disposable place itself. A job with no scratch leaves
// the run's files on the profile; a scratch with no job leaves a survivor holding a handle
// to the directory the tool is about to delete. So they are made together and, when the
// second of them fails, the first is unmade before the verb returns.
//
// WHY THIS FILE HAS NO BUILD TAG. Everything the verb reaches Windows through is the
// `winPlacer` interface below, and the SEQUENCE over that interface is the contract: create
// the job before the process, put the child in the job at creation, close the job before
// the delete, delete on every path out. That sequence is what runwin_test.go asserts, with
// a fake placer, ON ANY HOST -- there is no Windows bench in the estate (2026-09-18), and a
// contract that could only be tested where it runs would be tested nowhere. The Win32 calls
// themselves are in runwin_windows.go behind `//go:build windows`, and runwin_other.go is
// the same interface refusing off windows so this file compiles everywhere.
//
// WHAT IS NOT PROVEN HERE, and is not claimed to be: that CreateJobObjectW, the extended
// limit information, PROC_THREAD_ATTRIBUTE_JOB_LIST and WindowsSandbox.exe behave on a real
// Windows machine as the rules say. The first Windows bench proves that. This file proves
// the shape that machine will be asked for.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// winScratchPrefix is the one directory-name shape this verb makes under --scratch and the
// one it removes. A directory whose name does not begin with it was not made here and is
// never touched -- the same rule the darwin volume name carries, for the same reason.
const winScratchPrefix = volumePrefix

// wsbExitFile is W10's status file: `WindowsSandbox.exe` returns as soon as the VM is up
// and carries no guest status, so the <LogonCommand> ends by writing the command's own
// %ERRORLEVEL% here, in the mapped writable folder, and the host waits for it.
const wsbExitFile = ".nova-sandbox-exit"

// wsbPoll is how often the host looks for the status file. It is production code's own
// wait, never a test's: a test replaces the clock and the placer, not this.
const wsbPoll = 250 * time.Millisecond

// winRemoveWindow is W7's bounded retry. Defender and the search indexer hold transient
// handles on files a run has just written, so a removal that fails with
// ERROR_SHARING_VIOLATION, ERROR_ACCESS_DENIED or ERROR_DIR_NOT_EMPTY is retried for this
// long, and ONLY then is it a leak. A leak declared on the first sharing violation would
// name a machine dirty that a second's patience would have left clean.
const winRemoveWindow = 10 * time.Second

// winLimits is W4's caps as the JOB carries them, in the job's own units. Zero means the
// flag was not given and the limit is not set: an unset limit is the machine's, and a limit
// set to "everything" is a number this tool would have had to invent.
type winLimits struct {
	// MemoryBytes goes into JOBOBJECT_EXTENDED_LIMIT_INFORMATION.ProcessMemoryLimit AND
	// JobMemoryLimit: the first caps one process, the second caps the tree, and a run that
	// forks its way past a per-process cap is exactly the runaway the cap is for.
	MemoryBytes int64
	// CPUPercent goes into JOBOBJECT_CPU_RATE_CONTROL_INFORMATION with
	// JOB_OBJECT_CPU_RATE_CONTROL_ENABLE|JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP, as a
	// percentage of ONE MACHINE's total cycles. 1..100.
	CPUPercent int
}

// winStartSpec is one CreateProcessW: the wall and the place applied by a single call
// (W3), which is why there is no ordering between them to get wrong.
type winStartSpec struct {
	// Policy is the wall: the resolved command, the read roots, the one writable root,
	// the cwd. The windows body turns it into PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES
	// on the same attribute list that carries PROC_THREAD_ATTRIBUTE_JOB_LIST.
	Policy *sandbox.Policy
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// winStarted is what the placer hands back: the channel the status arrives on and the
// leader's pid. There is NO kill function here, unlike the darwin startedRun, and the
// absence is the point -- on windows the kill is closing the job handle (W2), which is the
// placer's `CloseJob`, so there is exactly one way to end a run and it is the one that
// reaches a grandchild the harness abandoned.
type winStarted struct {
	done <-chan int
	pid  int
}

// winJob is an opaque handle to the Job Object. The production body is a Win32 HANDLE; the
// fake's is a counter. Nothing outside the placer ever looks inside it.
type winJob any

// winPlacer is the WHOLE of this verb's contact with Windows. Every Win32 call the windows
// half makes is behind one of these methods, so runwin_test.go replaces the lot with a fake
// and asserts the ORDER of the calls -- which is where the contract lives.
//
// The methods are in the order the verb calls them, and that order is W1, W5, W2, W3, W7.
type winPlacer interface {
	// Exists reports whether <scratch>/nova-<n> is already on the machine. W5: a run never
	// joins a place it did not make.
	Exists(dir string) (bool, error)

	// MakeScratch creates <scratch>/nova-<n> and the two directories the place is born
	// with -- work/ (the working directory) and home/ (rule 9's HOME) -- and grants the
	// AppContainer SID read+write on it and on nothing else the run created.
	MakeScratch(dir string) error

	// CreateJob makes the Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and the caps
	// set through SetInformationJobObject BEFORE any process is in it (W2, W4).
	// JOB_OBJECT_LIMIT_BREAKAWAY_OK and JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK are NEVER
	// set: breakaway is exactly how a tree escapes the kill.
	CreateJob(limits winLimits) (winJob, error)

	// Start is ONE CreateProcessW with EXTENDED_STARTUPINFO_PRESENT, whose attribute list
	// carries PROC_THREAD_ATTRIBUTE_JOB_LIST with this job (W3). The child is therefore in
	// the job before its first instruction, and there is no window in which it is alive
	// and outside it.
	Start(job winJob, spec winStartSpec) (winStarted, error)

	// CloseJob closes the tool's last handle to the job, which terminates every process
	// still in it (W2). It is the kill, and W7's delete comes after it.
	CloseJob(job winJob) error

	// Used is the bytes the scratch holds, read by WALKING the tree immediately before the
	// removal -- there is no statfs for a directory (W7).
	Used(dir string) (int64, error)

	// RemoveTree removes <scratch>/nova-<n>, retrying a transient hold for a bounded
	// window before it is a leak (W7). The ROOT is the caller's --scratch and it is passed
	// separately on purpose: deletion in this repository is a verb over a validated path
	// BELOW A ROOT (Glenn, 2026-09-17, "it is just one mistake away from deleting the whole
	// disk"), and the class test in internal/ci holds every os.RemoveAll of a computed path
	// to safepath.RemoveUnder. A removal that only knew the leaf could not be checked
	// against anything but its own parent, which is no check at all.
	RemoveTree(root, dir string, window time.Duration) error

	// WSBAvailable reports whether Windows Sandbox is on this machine at all: it is Pro
	// and Enterprise only and the optional feature must already be enabled (W8). The
	// string is the edition, for the refusal to name.
	WSBAvailable() (edition string, ok bool, err error)

	// WSBRunning names the running Windows Sandbox instance, if there is one. Windows
	// Sandbox permits a SINGLE running instance per machine (W9), so a second run refuses
	// rather than queues.
	WSBRunning() (who string, running bool, err error)

	// StartWSB writes the .wsb file and starts WindowsSandbox.exe on it. It returns as
	// soon as the VM is up and carries no guest status, which is why W10 exists.
	StartWSB(file, xml string) error
}

// The windows seams. Each is a var so a test can replace it, and none is reachable from
// caller input.
var (
	runWinPlace winPlacer = newPlatformWinPlace()
	// runWinPoll is the wsb status-file poll seam: a test hands back a channel it controls
	// rather than waiting a real 250ms per tick.
	runWinPoll = func(d time.Duration) <-chan time.Time { return time.Tick(d) }
	// runWinReadExit is how the host reads W10's status file. A test replaces it; the
	// production body reads the file the guest wrote in the mapped writable folder.
	runWinReadExit = readWSBExit
	// runWinWall is rule 1 on windows: OS-ENFORCED OR REFUSED. The PLACE is built (this
	// file); the WALL is the AppContainer body of the section above, and it is not. A place
	// without a wall is a directory that gets deleted, which is hygiene and not containment,
	// so the verb refuses under --place job until the wall lands -- and the refusal names the
	// half that is missing rather than saying "the sandbox failed".
	//
	// Under --place wsb the boundary is the VM, not the AppContainer, so this is not asked.
	runWinWall = winWallAvailable
)

// winRemedy is the one remedy line every windows refusal of this verb carries. It is not
// the darwin runRemedy: --size is refused here (W6) and --scratch is required (W5), so a
// remedy printing the darwin argv would send a windows reader to type the one flag the
// next line refuses.
const winRemedy = "run: nova-sandbox run --name <n> --scratch <C:\\nova> [--timeout <30m>] [--memory <4g>] [--cpu <50>] [--read <dir>]... -- <command> <args...>"

// runDisposableWindows is the windows verb from the look onwards. Like runDisposable it is
// one function on purpose: from the moment the place exists there is exactly ONE path to
// the exit, and that path deletes it.
func runDisposableWindows(f runFlags, deadline time.Duration, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	started := runNow()
	refuse := func(reason, format string, a ...any) int {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n",
			oneline.Field(reason), oneline.Escape(fmt.Sprintf(format, a...)), winRemedy)
		return sandbox.ExitRefused
	}

	// W11's tripwire, at the top of the only windows path there is: no argv this tool
	// composes names wsl. A build that reached for WSL when AppContainer, the Job Object or
	// Windows Sandbox was unavailable would be containment SOMEWHERE ELSE, on a machine the
	// card was not sent to.
	if bad, found := wslInArgv(f.argv); found {
		return refuse("no_sandbox",
			"%s names WSL, and WSL is never the answer on windows -- not as the wall, not as the place, not as a fallback: containment that only holds inside WSL is containment on another machine. Run the command itself, or refuse", oneline.Escape(bad))
	}

	// Rule 1, before anything is made: a place with no wall is not this verb. The check is
	// here rather than inside Start so that a machine with no AppContainer body never gets a
	// scratch directory made on it and removed again for nothing.
	if f.place == placeJob {
		if missing, ok := runWinWall(); !ok {
			return refuse("no_sandbox",
				"the windows wall (%s) is not built in this binary: the disposable PLACE is -- the Job Object and the per-run scratch -- and a place without a wall is a directory that gets deleted, which is hygiene and not containment. This tool does not run a command it cannot contain. Two remedies: --place wsb, where the boundary is the VM and not the AppContainer, or run the card on darwin", oneline.Escape(missing))
		}
	}

	dir := filepath.Join(f.scratch, winScratchPrefix+f.name)

	exists, err := step(stderr, "look", func() (bool, error) { return runWinPlace.Exists(dir) })
	if err != nil {
		return refuse("volume_failed", "the scratch directories under %s could not be read: %s", oneline.Escape(f.scratch), oneline.Err(err))
	}
	if exists {
		return refuse("volume_exists",
			"%s is already on this machine; a run never joins a place it did not make. Pick another --name, or remove it: rmdir /s /q %s", oneline.Escape(dir), oneline.Escape(dir))
	}

	if f.place == placeWSB {
		return runWSB(f, dir, deadline, stderr, started)
	}

	// W1: BOTH or NEITHER. The scratch is made first because the job's kill-on-close means
	// an orphaned job handle dies with this process anyway, while an orphaned directory
	// does not -- so the half that can leak is the half that is unmade by hand below.
	if err := stepErr(stderr, "create", func() error { return runWinPlace.MakeScratch(dir) }); err != nil {
		return refuse("volume_failed", "the disposable scratch %s could not be created: %s", oneline.Escape(dir), oneline.Err(err))
	}

	job, err := step(stderr, "job", func() (winJob, error) { return runWinPlace.CreateJob(f.limits()) })
	if err != nil {
		// W1's red test is exactly this path: a windows run creates BOTH or NEITHER. A
		// scratch with no job leaves a survivor holding the directory this verb deletes, so
		// the half that was made is unmade -- through finishWindows, so that the removal,
		// the receipt and a removal that fails all read the same as they do on every other
		// way out of this verb.
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n", oneline.Field("volume_failed"),
			oneline.Escape(fmt.Sprintf("the Job Object could not be created, so the scratch made beside it is being removed and nothing was run: %s", oneline.Err(err))), winRemedy)
		return finishWindows(stderr, f.name, f.scratch, dir, sandbox.ExitRefused, started)
	}

	// ONE exit from here. Whatever the run does, the job is closed and the scratch goes.
	code := runInWinPlace(f, dir, job, deadline, stdin, stdout, stderr, env)
	return finishWindows(stderr, f.name, f.scratch, dir, code, started)
}

// runInWinPlace builds the wall around the scratch and runs the command inside the job.
// Its answer is the status the run earned; the place's fate is finishWindows's business.
func runInWinPlace(f runFlags, dir string, job winJob, deadline time.Duration, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	refuse := func(reason, format string, a ...any) int {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n",
			oneline.Field(reason), oneline.Escape(fmt.Sprintf(format, a...)), winRemedy)
		return sandbox.ExitRefused
	}
	work := filepath.Join(dir, "work")
	home := filepath.Join(dir, "home")

	// The wall: the scratch is the ONE --write (W5), so the only place on this machine the
	// command may write is the place that is about to be deleted. --read passes through
	// unchanged, so a shared toolchain or reference checkout is read IN PLACE and never
	// copied. Rule 8's temp directory defaults inside the scratch, which is what puts TEMP
	// and TMP on it too.
	p, bad := sandbox.Build(sandbox.Input{
		Reads:  f.reads,
		Writes: []string{dir},
		Cwd:    work,
		Argv:   f.argv,
		Home:   home,
		Name:   f.name,
	})
	if len(bad) > 0 {
		code := refuseAll(stderr, bad)
		fmt.Fprintln(stderr, winRemedy)
		return code
	}
	// W11 again, on the RESOLVED command: the tripwire above reads what the caller typed
	// and this one reads what the tool is about to execute, because a `wsl.exe` reached
	// through a PATH lookup is the same escape by a longer road.
	if bad, found := wslInArgv(append([]string{p.Command}, p.Argv...)); found {
		return refuse("no_sandbox",
			"the command resolved to %s, which is WSL; containment that only holds inside WSL is not containment on windows", oneline.Escape(bad))
	}

	childEnv := withHome(sandbox.ChildEnv(env, p.Tmp), home)

	fmt.Fprintf(stderr, "SANDBOX OK backend=%s abi=%s read=%d write=%d net=%s cwd=%s cwdb64=%s ancestors=%d cmd=%s gpu=%s\n",
		oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI()), len(p.Reads), len(p.Writes),
		oneline.Field(p.Net()), oneline.Field(p.Cwd), base64Cwd(p.Cwd), p.AncestorCount(),
		oneline.Field(p.CmdName()), oneline.Field(string(p.GPUMode)))

	started, err := runWinPlace.Start(job, winStartSpec{Policy: p, Env: childEnv, Stdin: stdin, Stdout: stdout, Stderr: stderr})
	if err != nil {
		var r sandbox.Refusal
		if asRefusal(err, &r) {
			code := refuseAll(stderr, []sandbox.Refusal{r})
			fmt.Fprintln(stderr, winRemedy)
			return code
		}
		return refuse("sandbox_failed", "the contained command could not be started in the job: %s", oneline.Err(err))
	}

	var deadlineC <-chan time.Time
	if deadline > 0 {
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		deadlineC = timer.C
	}
	sigs, stop := runSignals()
	defer stop()

	closeJob := func() {
		// W2: closing the tool's last handle terminates every process still in the job,
		// including a grandchild a harness spawned and abandoned. There is no grace and no
		// escalation, because there is no signal to escalate FROM: windows has none.
		_ = stepErr(stderr, "kill", func() error { return runWinPlace.CloseJob(job) })
	}
	code, timedOut, interrupted := superviseWindows(started.done, deadlineC, sigs, closeJob)
	if timedOut {
		fmt.Fprintf(stderr, "SANDBOX NOTE the command did not finish inside --timeout %s; the job was closed, which terminated the whole tree, and the scratch goes with it\n", oneline.Field(f.timeout))
	}
	if interrupted {
		fmt.Fprintf(stderr, "SANDBOX NOTE this tool was asked to stop; the job was closed and the tree terminated. exit=%d is the status TerminateProcess gave the child, not a signal: windows has none, so there is no 128+N to read here\n", code)
	}
	_ = started.pid
	return code
}

// superviseWindows waits for whichever of three things happens first -- the command
// finished, the deadline passed, this tool was asked to stop -- and in EVERY case closes
// the job before it returns. The job is the unit because a command that forked a background
// child leaves that child holding a handle inside the scratch, and a directory that is held
// open cannot be removed: the survivor would turn a clean exit into a leak.
//
// It is a function over channels rather than over a real job so that the ordering -- wait,
// close, then always the status -- is testable without Windows, a real handle or a real
// clock. It is deliberately SHORTER than the darwin supervise: there is no SIGTERM, no
// grace and no SIGKILL, because closing the job is not a request.
func superviseWindows(done <-chan int, deadline <-chan time.Time, sigs <-chan os.Signal, closeJob func()) (code int, timedOut, interrupted bool) {
	select {
	case code := <-done:
		// The leader is gone; the job may still hold a grandchild. Close it anyway -- this
		// is the sweep, and on an empty job it costs one handle close.
		closeJob()
		return code, false, false
	case <-deadline:
		closeJob()
		// The status after a kill says only what TerminateProcess gave it, and the thing
		// the caller needs to know is that the deadline is what ended the run.
		<-done
		return exitTimeout, true, false
	case <-sigs:
		closeJob()
		return <-done, false, true
	}
}

// finishWindows is the one exit: it measures what the scratch holds, removes it, and prints
// the receipt. A removal that fails prints SANDBOX LEAK with the directory and the one
// command that removes it, and costs exit 3 whatever the command's own status was -- a
// caller that read 0 would believe the machine was clean.
//
// The order is W7's, and the order is the rule: the job is ALREADY closed by the time this
// is reached (runInWinPlace's supervise closes it on every path), because a running image
// inside the scratch cannot be removed and cannot be renamed aside either. Unix's
// replace-the-inode trick has no equivalent on NTFS: a rename over a running image raises
// ERROR_SHARING_VIOLATION, so the only way to remove a directory holding a running .exe is
// to stop the .exe first.
func finishWindows(stderr io.Writer, name, root, dir string, code int, started time.Time) int {
	freed, err := runWinPlace.Used(dir)
	if err != nil {
		freed = 0
	}
	rmErr := stepErr(stderr, "delete", func() error { return runWinPlace.RemoveTree(root, dir, winRemoveWindow) })
	wall := runNow().Sub(started).Seconds()
	if rmErr != nil {
		fmt.Fprintf(stderr, "SANDBOX DONE name=%s exit=%d wall=%.3f freed=%d\n", oneline.Field(name), code, wall, 0)
		fmt.Fprintf(stderr, "SANDBOX LEAK name=%s volume=%s remedy=%q\n",
			oneline.Field(name), oneline.Field(dir), "rmdir /s /q "+dir)
		fmt.Fprintf(stderr, "SANDBOX NOTE the scratch could not be removed after %s of retries: %s. A running .exe inside it cannot be removed and cannot be replaced in place -- NTFS raises a sharing violation and there is no replace-the-inode trick -- so something in the job outlived the close, or Defender still holds a handle\n",
			winRemoveWindow, oneline.Err(rmErr))
		return exitLeak
	}
	fmt.Fprintf(stderr, "SANDBOX DONE name=%s exit=%d wall=%.3f freed=%d\n", oneline.Field(name), code, wall, freed)
	return code
}

// runWSB is W8, W9 and W10: the one-off review place, not the swarm's.
//
// Windows Sandbox is the only FULL disposability windows offers -- the guest's disk is
// discarded when the window closes, so "nothing of the run survives" is the platform's
// guarantee rather than a delete this tool must get right -- and it permits a SINGLE
// running instance per machine, which is why it is never the pool's: a pool of workers each
// wanting one is a queue of one.
func runWSB(f runFlags, dir string, deadline time.Duration, stderr io.Writer, started time.Time) int {
	refuse := func(reason, format string, a ...any) int {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n",
			oneline.Field(reason), oneline.Escape(fmt.Sprintf(format, a...)), winRemedy)
		return sandbox.ExitRefused
	}
	edition, ok, err := runWinPlace.WSBAvailable()
	if err != nil {
		return refuse("no_wsb", "whether this machine has Windows Sandbox could not be read: %s", oneline.Err(err))
	}
	if !ok {
		return refuse("no_wsb",
			"this machine is Windows %s and Windows Sandbox is not available on it: the feature is Pro and Enterprise only and the optional feature Containers-DisposableClientVM must already be enabled. Enable it, or run under the default --place job", oneline.Escape(edition))
	}
	if who, running, err := runWinPlace.WSBRunning(); err != nil {
		return refuse("no_wsb", "whether a Windows Sandbox is already running could not be read: %s", oneline.Err(err))
	} else if running {
		return refuse("wsb_busy",
			"a Windows Sandbox is already running on this machine (%s) and windows permits one instance at a time; this verb refuses rather than waits, because a silent wait on a single-instance resource is a queue nobody can see. Close it, or run under --place job", oneline.Escape(who))
	}

	if err := stepErr(stderr, "create", func() error { return runWinPlace.MakeScratch(dir) }); err != nil {
		return refuse("volume_failed", "the mapped writable folder %s could not be created: %s", oneline.Escape(dir), oneline.Err(err))
	}

	file := filepath.Join(dir, "run.wsb")
	xml := wsbDocument(wsbInput{
		Reads:   f.reads,
		Scratch: dir,
		Argv:    f.argv,
		MemMB:   megabytesOf(f.limits().MemoryBytes),
		Net:     false,
	})
	if err := stepErr(stderr, "wsb", func() error { return runWinPlace.StartWSB(file, xml) }); err != nil {
		code := refuse("no_wsb", "WindowsSandbox.exe could not be started on %s: %s", oneline.Escape(file), oneline.Err(err))
		return finishWindows(stderr, f.name, f.scratch, dir, code, started)
	}

	code := waitForWSBExit(dir, deadline, stderr)
	if code == exitTimeout {
		fmt.Fprintf(stderr, "SANDBOX NOTE the guest wrote no %s inside --timeout %s; the VM is closed and its disk discarded, and the command's own status is not knowable from here\n",
			wsbExitFile, oneline.Field(f.timeout))
	}
	return finishWindows(stderr, f.name, f.scratch, dir, code, started)
}

// waitForWSBExit is the one place the contract bends (W10). WindowsSandbox.exe returns as
// soon as the VM is up and carries no guest status, so the status comes back through the
// mapped writable folder or not at all: a run whose status file is absent when --timeout
// passes is 124 with the VM closed. A run with no --timeout under wsb is refused before
// this is reached (validateRun), because without one a guest that never writes the file is
// a wait with no end.
func waitForWSBExit(dir string, deadline time.Duration, stderr io.Writer) int {
	path := filepath.Join(dir, wsbExitFile)
	var deadlineC <-chan time.Time
	if deadline > 0 {
		t := time.NewTimer(deadline)
		defer t.Stop()
		deadlineC = t.C
	}
	tick := runWinPoll(wsbPoll)
	for {
		if code, ok := runWinReadExit(path); ok {
			return code
		}
		select {
		case <-deadlineC:
			return exitTimeout
		case <-tick:
		}
	}
}

// readWSBExit reads the guest's %ERRORLEVEL% out of the status file. A file that is not
// there yet is "not yet", not an error; a file holding something that is not a status is
// 126, the tool's "could not be executed", because a guest that wrote nonsense there ran
// something this host cannot account for.
func readWSBExit(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return sandbox.ExitNotExecuted, true
	}
	return n, true
}

// wsbInput is everything the .wsb document is built from. It is a struct so that
// wsbDocument is a PURE function of it and can be asserted line by line on any host.
type wsbInput struct {
	Reads   []string
	Scratch string
	Argv    []string
	MemMB   int
	Net     bool
}

// wsbDocument is W8's file: one <MappedFolder> per --read with <ReadOnly>true</ReadOnly>,
// one for the scratch writable, <Networking>Disable</Networking> unless the run allows it,
// <MemoryInMB> from --memory, and a <LogonCommand> that runs the command and ENDS BY
// WRITING %ERRORLEVEL% to the status file -- which is the whole of W10's mechanism.
func wsbDocument(in wsbInput) string {
	var b strings.Builder
	b.WriteString("<Configuration>\n")
	b.WriteString("  <MappedFolders>\n")
	for _, r := range in.Reads {
		fmt.Fprintf(&b, "    <MappedFolder>\n      <HostFolder>%s</HostFolder>\n      <ReadOnly>true</ReadOnly>\n    </MappedFolder>\n", xmlText(r))
	}
	fmt.Fprintf(&b, "    <MappedFolder>\n      <HostFolder>%s</HostFolder>\n      <ReadOnly>false</ReadOnly>\n    </MappedFolder>\n", xmlText(in.Scratch))
	b.WriteString("  </MappedFolders>\n")
	if in.Net {
		b.WriteString("  <Networking>Default</Networking>\n")
	} else {
		b.WriteString("  <Networking>Disable</Networking>\n")
	}
	if in.MemMB > 0 {
		fmt.Fprintf(&b, "  <MemoryInMB>%d</MemoryInMB>\n", in.MemMB)
	}
	b.WriteString("  <LogonCommand>\n")
	fmt.Fprintf(&b, "    <Command>%s</Command>\n", xmlText(wsbLogonCommand(in)))
	b.WriteString("  </LogonCommand>\n")
	b.WriteString("</Configuration>\n")
	return b.String()
}

// wsbLogonCommand is the one command the guest runs. The guest sees the scratch at its own
// mapped path under C:\Users\WDAGUtilityAccount\Desktop, which is where Windows Sandbox
// maps a <MappedFolder> by its host base name, and the status file is written there so the
// HOST can read it through the same folder.
func wsbLogonCommand(in wsbInput) string {
	guest := `C:\Users\WDAGUtilityAccount\Desktop\` + filepath.Base(in.Scratch)
	var cmd strings.Builder
	cmd.WriteString(`cmd.exe /c "`)
	for i, a := range in.Argv {
		if i > 0 {
			cmd.WriteString(" ")
		}
		cmd.WriteString(a)
	}
	// & not && : the status file is written whatever the command did, because an absent
	// file is 124 and a failing command is not a timeout.
	fmt.Fprintf(&cmd, ` & echo %%ERRORLEVEL%% > %s\%s"`, guest, wsbExitFile)
	return cmd.String()
}

// xmlText escapes the five characters that are not text in an XML element body. A host path
// carrying an ampersand is an ordinary NTFS path and must not become a malformed document.
func xmlText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// megabytesOf is <MemoryInMB>'s number. It rounds DOWN and floors at zero, because a
// <MemoryInMB>0</MemoryInMB> is a document Windows Sandbox refuses and an unset limit is
// the machine's default.
func megabytesOf(bytes int64) int {
	if bytes < 1<<20 {
		return 0
	}
	return int(bytes >> 20)
}

// wslInArgv is W11's tripwire in code: no argv this tool composes names wsl, wsl.exe or a
// \\wsl$\ path. It is checked on what the caller typed AND on what the command resolved to.
// The source-level half of the same rule -- that no exec site in this tool spells wsl at all
// -- is runwin_test.go's.
func wslInArgv(argv []string) (string, bool) {
	for _, a := range argv {
		lower := strings.ToLower(a)
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(a, `\`, "/")))
		if base == "wsl" || base == "wsl.exe" {
			return a, true
		}
		if strings.HasPrefix(lower, `\\wsl$\`) || strings.HasPrefix(lower, `\\wsl.localhost\`) {
			return a, true
		}
	}
	return "", false
}

// winCommandLine is argv as CreateProcessW wants it: one string, quoted by the rule
// CommandLineToArgvW un-quotes by. A tool that built this by joining on a space would hand a
// path with a space in it to the child as TWO arguments -- and `C:\Program Files` is not an
// unusual path on windows, it is the ordinary one. It lives here, beside the platform-
// independent half, because a quoting rule is a pure function of a string and a pure
// function of a string is testable on a Mac.
func winCommandLine(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(winEscapeArg(a))
	}
	return b.String()
}

// winEscapeArg is one argument, quoted. The BACKSLASH rule is the awkward one and it is the
// documented one: a run of backslashes is literal, except that it is doubled when the thing
// that follows it is a quote -- one in the argument, or the closing one. So `C:\dir\` inside
// quotes ends `\\"`, and a tool that wrote `\"` there would have ended the quoted string with
// a backslash and swallowed the next argument.
func winEscapeArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
			slashes = 0
			b.WriteByte('"')
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
			slashes = 0
			b.WriteByte(c)
		}
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}

// winLongPath is the \\?\ prefix every grant and every removal goes through. A scratch under
// a deep profile plus a Go module cache reaches MAX_PATH in ORDINARY use -- not in a
// pathological one -- and a path that is one component too long fails at the Win32 call with
// an error naming nothing about length.
//
// It is a pure function of the string so that it is asserted on any host, like winDir in
// internal/sandbox. A path that already carries the prefix is left alone, and a UNC path
// takes \\?\UNC\ with the leading two backslashes replaced, which is the one form of it that
// is not a simple prefix.
func winLongPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return p
	}
	p = strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
	}
	// Only an absolute path takes the prefix: \\?\ turns off all path normalisation, so a
	// relative path prefixed with it is not a path at all.
	if len(p) >= 3 && p[1] == ':' && p[2] == '\\' {
		return `\\?\` + p
	}
	return p
}

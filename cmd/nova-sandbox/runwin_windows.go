//go:build windows

// The Win32 body of docs/SPEC-SANDBOX.md's "Windows — the disposable place". Every call
// this tool makes into Windows is in this file and behind runwin.go's `winPlacer`, so the
// verb's SEQUENCE is unit-tested with a fake on any host and this file is the only thing a
// Windows bench has left to prove.
//
// THERE IS NO NEW DEPENDENCY. go.mod carries the standard library and nothing else, and
// three test classes in internal/ci read the tree on that premise, so this is `syscall` and
// `syscall.NewLazyDLL` with every constant NAMED and its value written beside the name --
// golang.org/x/sys/windows would have been fewer lines and would have been the first
// dependency in the module.
//
// NOT MEASURED. Not one line below has run on a Windows machine: the estate has none
// (2026-09-18). It cross-compiles, it vets, and its caller's sequence is proven against a
// fake. The first Windows bench proves the rest, and until it does the wall check at
// winWallAvailable keeps the verb from claiming containment it has not got.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// The Win32 constants, each with its value, because a constant referred to by name from a
// dependency is a constant nobody in this repository can check against the documentation.
const (
	// Job Object information classes (JOBOBJECTINFOCLASS).
	jobObjectExtendedLimitInformation  = 9
	jobObjectCPURateControlInformation = 15
	jobObjectBasicProcessIdList        = 3

	// JOBOBJECT_BASIC_LIMIT_INFORMATION.LimitFlags.
	//
	// W2: KILL_ON_JOB_CLOSE is the whole mechanism -- closing the tool's last handle to the
	// job terminates every process still in it, including a grandchild a harness spawned and
	// abandoned. BREAKAWAY_OK and SILENT_BREAKAWAY_OK are named here ONLY so that the test
	// that asserts they are never set has something to name: breakaway is exactly how a tree
	// escapes the kill, and this file never sets either.
	jobObjectLimitProcessMemory     = 0x00000100
	jobObjectLimitJobMemory         = 0x00000200
	jobObjectLimitBreakawayOK       = 0x00000800
	jobObjectLimitSilentBreakawayOK = 0x00001000
	jobObjectLimitKillOnJobClose    = 0x00002000

	// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION.ControlFlags. HARD_CAP is the one that makes
	// the number a ceiling rather than a share: without it the rate is a weight, and a
	// weight is not the promise --cpu makes.
	jobObjectCPURateControlEnable  = 0x00000001
	jobObjectCPURateControlHardCap = 0x00000004

	// CreateProcess flags.
	extendedStartupInfoPresent = 0x00080000
	createUnicodeEnvironment   = 0x00000400
	createSuspended            = 0x00000004
	createNoWindow             = 0x08000000

	// STARTUPINFO.dwFlags.
	startfUseStdHandles = 0x00000100

	// PROC_THREAD_ATTRIBUTE_JOB_LIST (W3) and the slot the WALL fills
	// (PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES, rule 3 of the AppContainer section).
	// They are on ONE attribute list and therefore one CreateProcessW, which is why there is
	// no ordering between the wall and the place to get wrong.
	procThreadAttributeJobList              = 0x0002000D
	procThreadAttributeSecurityCapabilities = 0x00020009

	// The removal errors W7 retries rather than calls a leak: Defender and the search
	// indexer hold transient handles on files a run has just written.
	errorAccessDenied     = 5
	errorSharingViolation = 32
	errorDirNotEmpty      = 145

	// TOOLHELP32, for W9's "is a Windows Sandbox already running".
	th32csSnapProcess = 0x00000002
	maxPath           = 260
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateJobObjectW              = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject       = kernel32.NewProc("SetInformationJobObject")
	procIsProcessInJob                = kernel32.NewProc("IsProcessInJob")
	procCreateProcessW                = kernel32.NewProc("CreateProcessW")
	procInitializeProcThreadAttrList  = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute     = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList = kernel32.NewProc("DeleteProcThreadAttributeList")
	procQueryFullProcessImageNameW    = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetProductInfo                = kernel32.NewProc("GetProductInfo")
	procCreateToolhelp32Snapshot      = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW               = kernel32.NewProc("Process32FirstW")
	procProcess32NextW                = kernel32.NewProc("Process32NextW")
	// ResumeThread is not in the standard syscall package on windows, so it is named here
	// like the rest: CREATE_SUSPENDED is only usable with something to undo it.
	procResumeThread = kernel32.NewProc("ResumeThread")
)

// JOBOBJECT_BASIC_LIMIT_INFORMATION.
type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// IO_COUNTERS.
type jobIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// JOBOBJECT_EXTENDED_LIMIT_INFORMATION. W4's two memory caps are its last two writable
// fields: ProcessMemoryLimit caps ONE process and JobMemoryLimit caps the TREE, and a run
// that forks its way past a per-process cap is exactly the runaway the cap is for.
type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION. The second field is a union; with
// ENABLE|HARD_CAP it is CpuRate, in units of 1/100 of one percent of ONE MACHINE's cycles.
type jobCPURateControlInformation struct {
	ControlFlags uint32
	CPURate      uint32
}

// STARTUPINFOEX. The attribute list is what carries W3's job list and the wall's security
// capabilities, and EXTENDED_STARTUPINFO_PRESENT is what makes CreateProcessW read it.
type startupInfoEx struct {
	StartupInfo   syscall.StartupInfo
	AttributeList *byte
}

// PROCESSENTRY32W, for W9.
type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

// winPlace is the production placer.
type winPlace struct{}

func newPlatformWinPlace() winPlacer { return winPlace{} }

// winJobHandle is the opaque winJob the verb passes back in. It carries the HANDLE and
// nothing else, and nothing outside this file looks inside it.
type winJobHandle struct {
	h       syscall.Handle
	limits  winLimits
	created time.Time
}

// winWallAvailable is rule 1 on windows, and today it says NO.
//
// The PLACE is this file. The WALL is the AppContainer body of the section above, and it is
// not built: internal/sandbox/wrap_other.go is what compiles on windows and its Available
// answers false. A place without a wall is a disposable directory, not containment, and a
// tool that ran the command anyway would be the silent sandbox rule 1 exists to prevent --
// so the verb refuses, and the refusal names the half that is missing rather than saying
// "the sandbox failed".
//
// When the AppContainer body lands, sandbox.Available answers on windows and this says yes
// with no edit here: the security-capabilities slot in the attribute list below is already
// reserved for it, because W3 puts the wall and the place on ONE CreateProcessW.
func winWallAvailable() (string, bool) {
	backend, ok := sandbox.Available()
	if !ok {
		return "appcontainer", false
	}
	return backend, true
}

func (winPlace) Exists(dir string) (bool, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !fi.IsDir() {
		return true, nil
	}
	return true, nil
}

// MakeScratch is W5's place: <scratch>/nova-<n> with work/ and home/ inside it.
//
// os.Mkdir and not MkdirAll: the directory must NOT already exist -- a run never joins a
// place it did not make -- and MkdirAll is silent about one that does. The parent is the
// caller's --scratch, which validateRun has already required to be absolute and which must
// already exist, because a tool that creates the parent of a disposable place is a tool that
// can be pointed at a typo.
//
// The paths here are not long-path-prefixed: Go's os package applies the \\?\ prefix itself
// for a path near MAX_PATH. winLongPath is for the raw Win32 calls in Start, where nothing
// does it for us.
//
// NOT BUILT HERE: the AppContainer SID's read+write grant on this directory. That grant
// belongs to the wall, which is not built (winWallAvailable), and the verb refuses before
// reaching this on a machine with no wall. When the wall lands, the grant is one SetEntriesInAcl
// on this path and nothing else in this function changes.
func (winPlace) MakeScratch(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for _, d := range []string{"work", "home"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// CreateJob is W2 and W4: the job, its kill-on-close, and its caps -- all set BEFORE any
// process is in it, because a limit applied to a job that already holds a running tree has
// already been escaped once.
func (winPlace) CreateJob(limits winLimits) (winJob, error) {
	h, _, errno := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return nil, fmt.Errorf("CreateJobObjectW: %w", errno)
	}
	job := winJobHandle{h: syscall.Handle(h), limits: limits, created: time.Now()}

	var ext jobExtendedLimitInformation
	// W2. KILL_ON_JOB_CLOSE and NOTHING that permits breakaway. The two breakaway flags are
	// not set here and are not set anywhere else in this file; the class test in
	// runwin_test.go reads this source and holds that shut.
	ext.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if limits.MemoryBytes > 0 {
		ext.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessMemory | jobObjectLimitJobMemory
		ext.ProcessMemoryLimit = uintptr(limits.MemoryBytes)
		ext.JobMemoryLimit = uintptr(limits.MemoryBytes)
	}
	if err := setJobInfo(job.h, jobObjectExtendedLimitInformation, unsafe.Pointer(&ext), unsafe.Sizeof(ext)); err != nil {
		_ = syscall.CloseHandle(job.h)
		return nil, fmt.Errorf("SetInformationJobObject(JobObjectExtendedLimitInformation): %w", err)
	}

	if limits.CPUPercent > 0 {
		rate := jobCPURateControlInformation{
			ControlFlags: jobObjectCPURateControlEnable | jobObjectCPURateControlHardCap,
			// 1/100 of one percent: a hard cap of 50% is 5000.
			CPURate: uint32(limits.CPUPercent) * 100,
		}
		if err := setJobInfo(job.h, jobObjectCPURateControlInformation, unsafe.Pointer(&rate), unsafe.Sizeof(rate)); err != nil {
			_ = syscall.CloseHandle(job.h)
			return nil, fmt.Errorf("SetInformationJobObject(JobObjectCpuRateControlInformation): %w", err)
		}
	}
	return job, nil
}

func setJobInfo(h syscall.Handle, class uint32, p unsafe.Pointer, size uintptr) error {
	r, _, errno := procSetInformationJobObject.Call(uintptr(h), uintptr(class), uintptr(p), size)
	if r == 0 {
		return errno
	}
	return nil
}

// Start is W3: ONE CreateProcessW with EXTENDED_STARTUPINFO_PRESENT, whose attribute list
// carries PROC_THREAD_ATTRIBUTE_JOB_LIST. The child is in the job BEFORE ITS FIRST
// INSTRUCTION.
//
// CreateProcess followed by AssignProcessToJobObject -- which is what Go's own
// os/exec offers on windows, and the reason this function exists rather than a SysProcAttr
// -- leaves a window in which the child is alive and outside the job, and a child that
// spawns inside that window is a survivor the kill never reaches.
func (winPlace) Start(j winJob, spec winStartSpec) (winStarted, error) {
	job, ok := j.(winJobHandle)
	if !ok {
		return winStarted{}, fmt.Errorf("the job handed to Start is not this placer's")
	}
	if _, ok := winWallAvailable(); !ok {
		// Belt and braces: the verb refuses before reaching this, and this refuses again,
		// because a future caller of the placer must not be able to run a command with no
		// wall by taking a shorter road to it.
		return winStarted{}, sandbox.Refusal{Reason: "no_sandbox",
			Text: "the windows WALL (AppContainer) is not built in this binary: the disposable place is, and a place without a wall is a directory that gets deleted, not containment. This tool does not run a command it cannot contain"}
	}

	// The attribute list: two slots reserved, one filled. The second is the wall's
	// PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES, and reserving it here is what makes the
	// wall a one-line addition rather than a restructure.
	attrs, free, err := newAttributeList(2)
	if err != nil {
		return winStarted{}, err
	}
	defer free()
	h := job.h
	if err := updateAttribute(attrs, procThreadAttributeJobList, unsafe.Pointer(&h), unsafe.Sizeof(h)); err != nil {
		return winStarted{}, fmt.Errorf("UpdateProcThreadAttribute(PROC_THREAD_ATTRIBUTE_JOB_LIST): %w", err)
	}

	stdin, stdout, stderr, closeStdio, err := stdioHandles(spec)
	if err != nil {
		return winStarted{}, err
	}

	var si startupInfoEx
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.StartupInfo.Flags = startfUseStdHandles
	si.StartupInfo.StdInput = stdin
	si.StartupInfo.StdOutput = stdout
	si.StartupInfo.StdErr = stderr
	si.AttributeList = attrs

	appName, err := syscall.UTF16PtrFromString(winLongPath(spec.Policy.Command))
	if err != nil {
		return winStarted{}, err
	}
	cmdLine, err := syscall.UTF16PtrFromString(winCommandLine(spec.Policy.Argv))
	if err != nil {
		return winStarted{}, err
	}
	cwd, err := syscall.UTF16PtrFromString(winLongPath(spec.Policy.Cwd))
	if err != nil {
		return winStarted{}, err
	}
	envBlock, err := winEnvBlock(spec.Env)
	if err != nil {
		return winStarted{}, err
	}

	var pi syscall.ProcessInformation
	r, _, errno := procCreateProcessW.Call(
		uintptr(unsafe.Pointer(appName)),
		uintptr(unsafe.Pointer(cmdLine)),
		0, // lpProcessAttributes
		0, // lpThreadAttributes
		1, // bInheritHandles: rule 12's inherited handle is this and the STARTUPINFOEX handle list, never fd 3
		uintptr(extendedStartupInfoPresent|createUnicodeEnvironment|createNoWindow|createSuspended),
		uintptr(unsafe.Pointer(&envBlock[0])),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	closeStdio()
	if r == 0 {
		// A file that passed the executability pre-flight and is not a valid PE image fails
		// HERE, and it is 126 -- "the command could not be executed and the tool was still
		// there" -- the way it already is on the other two platforms.
		return winStarted{}, sandbox.Refusal{Reason: "sandbox_failed",
			Text: fmt.Sprintf("CreateProcessW(%s) failed: %v", filepath.Base(spec.Policy.Command), errno)}
	}

	// W12's second half, asserted on the tool's own child before it runs an instruction: the
	// child IS in the job the tool created. CREATE_SUSPENDED is what makes that assertion
	// worth making -- ask before the first instruction, and there is no window in which the
	// answer could have been arranged.
	if err := assertInJob(pi.Process, job.h); err != nil {
		_ = syscall.TerminateProcess(pi.Process, 1)
		_ = syscall.CloseHandle(pi.Thread)
		_ = syscall.CloseHandle(pi.Process)
		return winStarted{}, sandbox.Refusal{Reason: "sandbox_failed", Text: err.Error()}
	}
	if _, err := resumeThread(pi.Thread); err != nil {
		_ = syscall.TerminateProcess(pi.Process, 1)
		_ = syscall.CloseHandle(pi.Thread)
		_ = syscall.CloseHandle(pi.Process)
		return winStarted{}, fmt.Errorf("ResumeThread: %w", err)
	}
	_ = syscall.CloseHandle(pi.Thread)

	done := make(chan int, 1)
	go func() {
		defer syscall.CloseHandle(pi.Process)
		_, _ = syscall.WaitForSingleObject(pi.Process, syscall.INFINITE)
		var code uint32
		if err := syscall.GetExitCodeProcess(pi.Process, &code); err != nil {
			done <- sandbox.ExitNotExecuted
			return
		}
		// There is no 128+N here and there must not be: windows has no signals, and a
		// process the job terminated reports the status TerminateProcess gave it. A caller
		// reading >128 as "killed by a signal" is reading a unix convention on a platform
		// that has none; the SANDBOX DONE line is what says how the run ended.
		done <- int(int32(code))
	}()
	return winStarted{done: done, pid: int(pi.ProcessId)}, nil
}

// assertInJob is IsProcessInJob, W12's second guard.
func assertInJob(process, job syscall.Handle) error {
	var in int32
	r, _, errno := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&in)))
	if r == 0 {
		return fmt.Errorf("IsProcessInJob: %w", errno)
	}
	if in == 0 {
		return fmt.Errorf("the child is not in the job this tool created; the place did not take, and a tree outside the job is a tree the kill never reaches")
	}
	return nil
}

// resumeThread lets the child take its first instruction, and it is called ONLY after
// IsProcessInJob has answered yes. -1 is the failure value; a count of 0 would mean the
// thread was not suspended, which cannot happen for a CREATE_SUSPENDED child.
func resumeThread(t syscall.Handle) (uint32, error) {
	n, _, errno := procResumeThread.Call(uintptr(t))
	if int32(n) == -1 {
		return 0, errno
	}
	return uint32(n), nil
}

// CloseJob is W2 and the whole of step 4: the tool's last handle goes, and every process
// still in the job goes with it.
func (winPlace) CloseJob(j winJob) error {
	job, ok := j.(winJobHandle)
	if !ok {
		return fmt.Errorf("the job handed to CloseJob is not this placer's")
	}
	return syscall.CloseHandle(job.h)
}

// Used is `freed=`, and it WALKS -- there is no statfs for a directory on NTFS, and the
// volume's free space is the volume's, not this run's.
func (winPlace) Used(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // a file that vanished under the walk is not this measurement's problem
		}
		if d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total, err
}

// RemoveTree is W7's delete, with the bounded retry that separates a transient hold from a
// leak. Defender and the search indexer hold handles on files a run has just written for a
// short while after it stops writing them, and a leak declared on the first sharing
// violation would name a machine dirty that a second's patience would have left clean.
//
// It is only ever reached AFTER the job is closed, because a directory holding a running
// image cannot be removed: a rename over a running .exe raises ERROR_SHARING_VIOLATION and
// unix's replace-the-inode trick has no equivalent on NTFS.
// The removal itself is safepath.RemoveUnder(root, dir) and never a bare os.RemoveAll:
// deletion in this repository is a verb over a path VALIDATED BELOW A ROOT (Glenn,
// 2026-09-17, "it is just one mistake away from deleting the whole disk"), and the class test
// in internal/ci refuses every other spelling. The root is the caller's --scratch, so a
// --name that somehow escaped okName still cannot reach a directory outside the place the
// caller named.
//
// The paths are NOT long-path-prefixed here. Go's os package applies the \\?\ prefix itself
// for a path near MAX_PATH; winLongPath is for the RAW Win32 calls -- CreateProcessW's
// application name and current directory -- where nothing does it for us.
func (winPlace) RemoveTree(root, dir string, window time.Duration) error {
	deadline := time.Now().Add(window)
	var last error
	for {
		err := safepath.RemoveUnder(root, dir)
		if err == nil {
			return nil
		}
		last = err
		// An unsafe path is refused on the FIRST try and never retried: retrying it would
		// turn a refusal into a slow refusal, and the answer will not change.
		if errors.Is(err, safepath.ErrUnsafe) || !transientHold(err) || time.Now().After(deadline) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// transientHold is the three errors W7 names, and only those three. Anything else is a
// leak on the first try: a retry loop that retried everything would turn a permanent
// failure into a slow one.
func transientHold(err error) bool {
	var errno syscall.Errno
	for e := err; e != nil; {
		if pe, ok := e.(*os.PathError); ok {
			e = pe.Err
			continue
		}
		if en, ok := e.(syscall.Errno); ok {
			errno = en
		}
		break
	}
	switch uintptr(errno) {
	case errorAccessDenied, errorSharingViolation, errorDirNotEmpty:
		return true
	}
	return false
}

// WSBAvailable is W8's pre-flight: Windows Sandbox is Pro and Enterprise only and the
// optional feature Containers-DisposableClientVM must already be enabled. The binary being
// on the machine is what "enabled" looks like from here, and the edition is named so the
// refusal can say which one this is.
func (winPlace) WSBAvailable() (string, bool, error) {
	edition := windowsEdition()
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	if _, err := os.Stat(filepath.Join(root, "System32", "WindowsSandbox.exe")); err != nil {
		return edition, false, nil
	}
	return edition, true, nil
}

// windowsEdition is GetProductInfo's number turned into the three words a refusal needs.
// The full table is large and most of it is not a machine this fleet would ever run on, so
// the ones that matter are named and the rest is the number: a refusal that says
// "product=48" is still a refusal a reader can act on.
func windowsEdition() string {
	var product uint32
	r, _, _ := procGetProductInfo.Call(10, 0, 0, 0, uintptr(unsafe.Pointer(&product)))
	if r == 0 {
		return "unknown"
	}
	switch product {
	case 0x00000030, 0x00000031, 0x00000067, 0x00000068:
		return "Professional"
	case 0x00000004, 0x0000001B, 0x00000048, 0x00000054:
		return "Enterprise"
	case 0x00000065, 0x00000062, 0x00000063, 0x00000064:
		return "Home"
	}
	return fmt.Sprintf("product=%d", product)
}

// WSBRunning is W9: windows permits ONE Windows Sandbox at a time, so a second run refuses
// rather than waits. A silent wait on a single-instance resource is a queue nobody can see.
func (winPlace) WSBRunning() (string, bool, error) {
	snap, _, errno := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return "", false, fmt.Errorf("CreateToolhelp32Snapshot: %w", errno)
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var e processEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	r, _, errno := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e)))
	for r != 0 {
		name := strings.ToLower(syscall.UTF16ToString(e.ExeFile[:]))
		// Both halves of it: the client window and the VM's own container process. Either
		// one present means the single instance is taken.
		if name == "windowssandbox.exe" || name == "windowssandboxclient.exe" {
			return fmt.Sprintf("%s pid=%d", name, e.ProcessID), true, nil
		}
		r, _, errno = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e)))
	}
	return "", false, nil
}

// StartWSB writes the .wsb document and starts WindowsSandbox.exe on it. It returns as soon
// as the VM is up and carries NO GUEST STATUS, which is the whole reason W10 exists and the
// one place the contract bends.
func (winPlace) StartWSB(file, xml string) error {
	if err := os.WriteFile(file, []byte(xml), 0o600); err != nil {
		return err
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	exe := filepath.Join(root, "System32", "WindowsSandbox.exe")
	appName, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	cmdLine, err := syscall.UTF16PtrFromString(winCommandLine([]string{exe, file}))
	if err != nil {
		return err
	}
	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi syscall.ProcessInformation
	r, _, errno := procCreateProcessW.Call(
		uintptr(unsafe.Pointer(appName)), uintptr(unsafe.Pointer(cmdLine)),
		0, 0, 0, uintptr(createUnicodeEnvironment), 0, 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)),
	)
	if r == 0 {
		return fmt.Errorf("CreateProcessW(%s): %w", exe, errno)
	}
	_ = syscall.CloseHandle(pi.Thread)
	_ = syscall.CloseHandle(pi.Process)
	return nil
}

// newAttributeList allocates and initialises a PROC_THREAD_ATTRIBUTE_LIST for n attributes.
// The two-call shape -- once with a nil list to learn the size, once to initialise -- is
// the documented one, and the first call is EXPECTED to fail with ERROR_INSUFFICIENT_BUFFER.
func newAttributeList(n int) (*byte, func(), error) {
	var size uintptr
	_, _, _ = procInitializeProcThreadAttrList.Call(0, uintptr(n), 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil, nil, fmt.Errorf("InitializeProcThreadAttributeList named no size for %d attributes", n)
	}
	buf := make([]byte, size)
	r, _, errno := procInitializeProcThreadAttrList.Call(
		uintptr(unsafe.Pointer(&buf[0])), uintptr(n), 0, uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return nil, nil, fmt.Errorf("InitializeProcThreadAttributeList: %w", errno)
	}
	list := &buf[0]
	return list, func() {
		_, _, _ = procDeleteProcThreadAttributeList.Call(uintptr(unsafe.Pointer(list)))
		// The list holds pointers INTO this allocation until CreateProcessW has read it, and
		// the only thing referring to the allocation by then is this closure. Without the
		// keep-alive the collector may move or free it between UpdateProcThreadAttribute and
		// CreateProcessW, and the failure would be a rare corrupt attribute list rather than
		// an error anyone could read.
		runtime.KeepAlive(buf)
	}, nil
}

func updateAttribute(list *byte, attr uintptr, value unsafe.Pointer, size uintptr) error {
	r, _, errno := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(list)), 0, attr, uintptr(value), size, 0, 0)
	if r == 0 {
		return errno
	}
	return nil
}

// stdioHandles gives the child its three standard handles. A caller's *os.File is used
// directly -- which is what production does, with this process's own stdio -- and anything
// else gets a pipe with a pump, which is what a test and the probe need.
func stdioHandles(spec winStartSpec) (in, out, errh syscall.Handle, closeAll func(), err error) {
	var toClose []*os.File
	closeAll = func() {
		for _, f := range toClose {
			_ = f.Close()
		}
	}

	inh, f, err := readerHandle(spec.Stdin)
	if err != nil {
		closeAll()
		return 0, 0, 0, nil, err
	}
	if f != nil {
		toClose = append(toClose, f)
	}
	outh, f2, err := writerHandle(spec.Stdout)
	if err != nil {
		closeAll()
		return 0, 0, 0, nil, err
	}
	if f2 != nil {
		toClose = append(toClose, f2)
	}
	errhh, f3, err := writerHandle(spec.Stderr)
	if err != nil {
		closeAll()
		return 0, 0, 0, nil, err
	}
	if f3 != nil {
		toClose = append(toClose, f3)
	}
	return inh, outh, errhh, closeAll, nil
}

// readerHandle is the child's stdin. The second return is a handle THIS process must close
// after CreateProcessW, and is nil when the caller's own file was used.
func readerHandle(r io.Reader) (syscall.Handle, *os.File, error) {
	if r == nil {
		return 0, nil, nil
	}
	if f, ok := r.(*os.File); ok {
		h := syscall.Handle(f.Fd())
		return h, nil, inheritable(h)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, nil, err
	}
	go func() {
		defer pw.Close()
		_, _ = io.Copy(pw, r)
	}()
	h := syscall.Handle(pr.Fd())
	return h, pr, inheritable(h)
}

// writerHandle is the child's stdout or stderr, under the same rule as readerHandle.
func writerHandle(w io.Writer) (syscall.Handle, *os.File, error) {
	if w == nil {
		return 0, nil, nil
	}
	if f, ok := w.(*os.File); ok {
		h := syscall.Handle(f.Fd())
		return h, nil, inheritable(h)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, nil, err
	}
	go func() {
		defer pr.Close()
		_, _ = io.Copy(w, pr)
	}()
	h := syscall.Handle(pw.Fd())
	return h, pw, inheritable(h)
}

func inheritable(h syscall.Handle) error {
	return syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT)
}

// winEnvBlock is the child's environment as CREATE_UNICODE_ENVIRONMENT wants it: UTF-16,
// NUL after each entry and a second NUL at the end. An empty environment is still two NULs,
// never a nil pointer, because a nil there means "inherit mine" and the child's environment
// is the tool's to set.
func winEnvBlock(env []string) ([]uint16, error) {
	var out []uint16
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			return nil, fmt.Errorf("an environment entry holds a NUL byte")
		}
		u, err := syscall.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		out = append(out, u...) // UTF16FromString already ends in a NUL
	}
	out = append(out, 0)
	return out, nil
}

// parentExecutableWindows is W12's first half, and it is here rather than in a parent_*.go
// so that the two halves of the guard -- the image and the job -- are read in one place.
// QueryFullProcessImageNameW on a handle opened to the parent pid, compared with this
// binary's own image path. There is no proc_pidpath, no inode and no device number, and a
// check written against any of those is a check that compiles and answers nothing.
func parentExecutableWindows(pid int) (string, error) {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		// A limited-information handle is enough for the image name and is what a process
		// at a different integrity level will give.
		const processQueryLimitedInformation = 0x1000
		h, err = syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
		if err != nil {
			return "", fmt.Errorf("OpenProcess(%d): %w", pid, err)
		}
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	r, _, errno := procQueryFullProcessImageNameW.Call(
		uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return "", fmt.Errorf("QueryFullProcessImageNameW(%d): %w", pid, errno)
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

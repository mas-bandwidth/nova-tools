//go:build windows

// The windows body (wallwin.go has the sequence and why): a low-integrity, privilege-free
// copy of this process's token for the child, low mandatory labels on the write roots, and
// a kill-on-close Job Object this process joins before it starts the child.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// Backend is what the SANDBOX OK line names on this platform.
const Backend = "lowil-job"

// ABI is the abi= field, which only linux fills.
func ABI() string { return "-" }

// ClampedABI is linux's alone.
func ClampedABI() (int, bool) { return 0, false }

// available is the test seam for the backend refusal, as on darwin.
var available = func() (string, bool) { return Backend, true }

// Available reports the backend: every supported Windows has integrity levels and jobs.
func Available() (string, bool) { return available() }

// NetEnforceable: the windows body does not enforce a network denial.
func NetEnforceable() bool { return false }

// Note is the one clause the check verb prints about this backend.
func Note() string {
	return "windows: writes are confined to the write roots (low integrity) and the tree to a kill-on-close job; reads and the network are NOT confined"
}

// winOps is the real winWallOps.
type winOps struct{}

var (
	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
)

const disableMaxPrivilege = 0x1 // DISABLE_MAX_PRIVILEGE

// lowLabelSDDL is a SACL holding one mandatory label: LOW, inherited by files and
// directories, no write up.
const lowLabelSDDL = "S:(ML;OICI;NW;;;LW)"

func (winOps) LabelLow(dir string) error {
	sd, err := windows.SecurityDescriptorFromString(lowLabelSDDL)
	if err != nil {
		return fmt.Errorf("the low label: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("the low label's SACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl)
}

func (winOps) LowToken() (uintptr, func(), error) {
	var self windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY, &self); err != nil {
		return 0, nil, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer self.Close() // ignored: closing our own handle to our own token
	// Every privilege but SeChangeNotify removed. Administrators is NOT made deny-only:
	// measured on captain 2026-10-20, a deny-only Administrators SID crashed the cmake
	// configure step (0xC0000005, CMake 4.3 and 4.4 alike), and the integrity level is what
	// confines the writes either way.
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(self), disableMaxPrivilege, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		return 0, nil, fmt.Errorf("CreateRestrictedToken: %w", e)
	}
	low, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		restricted.Close() // ignored: the token is discarded on this path
		return 0, nil, fmt.Errorf("the low label SID: %w", err)
	}
	tml := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: low, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&tml)), tml.Size()); err != nil {
		restricted.Close() // ignored: the token is discarded on this path
		return 0, nil, fmt.Errorf("SetTokenInformation(TokenIntegrityLevel, low): %w", err)
	}
	return uintptr(restricted), func() { restricted.Close() }, nil // ignored: closing the child's token after the child has it
}

// winJob is held for the life of this process: the kernel closes it when the process
// ends, and kill-on-close then ends the tree.
var winJob windows.Handle

func (winOps) JoinJob(activeProcesses uint32, memoryBytes int64) error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if activeProcesses > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = activeProcesses
	}
	if memoryBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(memoryBytes)
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h) // ignored: the job is discarded on this path
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}
	if err := windows.AssignProcessToJobObject(h, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(h) // ignored: the job is discarded on this path
		return fmt.Errorf("AssignProcessToJobObject(self): %w", err)
	}
	winJob = h
	return nil
}

// wallOps is the seam a test replaces.
var wallOps winWallOps = winOps{}

// Run applies the policy and runs the command under it, and returns the status the tool
// exits with. A Refusal is the tool saying NO before the command ran.
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	availFn := Available
	if p.Available != nil {
		availFn = p.Available
	}
	if _, ok := availFn(); !ok {
		return ExitRefused, refuse("no_sandbox", "the windows wall is not available here; this tool does not run a command it cannot contain")
	}
	if p.NetDeny {
		return ExitRefused, refuse("sandbox_failed", "the windows wall cannot enforce a network denial; run without --net-deny or on a body that enforces it")
	}
	tok, release, ref := prepareWinWall(wallOps, p)
	if ref != nil {
		return ExitRefused, *ref
	}
	defer release()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := subproc.Long(ctx, p.Command, p.Argv[1:]...)
	cmd.Dir = p.Cwd
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(tok)}
	if okLine != nil {
		okLine()
	}
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started under the low-integrity token: %v", p.Command, err)
	}
	waitErr := cmd.Wait()
	return statusOf(waitErr, cmd.ProcessState), nil
}

// statusOf is the child's exit code; a child that never reported one is not executed.
func statusOf(waitErr error, st *os.ProcessState) int {
	if st != nil {
		return st.ExitCode()
	}
	if waitErr != nil {
		return ExitNotExecuted
	}
	return 0
}

// GroupUsage has no process group to count on windows; the job carries the caps.
func GroupUsage(pgid int) (Usage, error) {
	return Usage{}, fmt.Errorf("windows has no process groups; the job object carries the caps")
}

var _ = exec.ErrNotFound

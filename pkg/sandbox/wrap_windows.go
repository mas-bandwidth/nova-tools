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
	"path/filepath"
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
	return "windows: a low-integrity child in a kill-on-close job; it writes only low-labelled places (its roots, and every other low object: LocalLow, AppDataLow, other cards' roots), and reads and the network are NOT confined"
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

func (winOps) LabelLow(root string) error {
	sd, err := windows.SecurityDescriptorFromString(lowLabelSDDL)
	if err != nil {
		return fmt.Errorf("the low label: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("the low label's SACL: %w", err)
	}
	rootFinal, err := finalPath(root)
	if err != nil {
		return err
	}
	_, err = labelLowTree(root, isReparsePoint, func(path string) error { return labelOne(path, rootFinal, sacl) })
	return err
}

// finalPath is the path the kernel resolved an open of path to, every junction along it
// followed: the root's own, against which every labelled entry is checked.
func finalPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	defer windows.CloseHandle(h) // ignored: closing our own handle after the read
	return handleFinalPath(h)
}

// handleFinalPath is GetFinalPathNameByHandle of an open handle.
func handleFinalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", fmt.Errorf("GetFinalPathNameByHandle: %w", err)
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

// isReparsePoint reads path's attributes without following it.
func isReparsePoint(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

// labelOne sets the low label on path itself: through a handle opened on the entry (never
// what a reparse point names), and with a handle-based set, which propagates to nothing.
//
// AND WHAT THE HANDLE OPENED MUST BE INSIDE THE ROOT (the second security read of #6037).
// The walk checks a path and opens it later by its whole string; a folder along it swapped
// for a junction in that gap -- another card's low child can do it -- would send the open,
// and the label, outside. So the handle's own final path is read back and the label is set
// only when it is the root or under it.
func labelOne(path, rootFinal string, sacl *windows.ACL) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.WRITE_OWNER|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer windows.CloseHandle(h) // ignored: closing our own handle after the set
	got, err := handleFinalPath(h)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !coversPath(rootFinal, got) {
		return fmt.Errorf("%s opened %s, outside the write root %s: a junction was swapped in along the path; nothing was labelled", path, got, rootFinal)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// windowsGuard is what no write root may be or hold: the user's profile and its temp, and
// the system's own directories. Each is read from the token and the known folders, never
// from the environment: the caller hands this process the CHILD's environment, whose
// USERPROFILE and TMP are the job's own data home and tmp.
func windowsGuard() winGuard {
	var g winGuard
	if prof, err := windows.GetCurrentProcessToken().GetUserProfileDirectory(); err == nil && prof != "" {
		g.Protected = append(g.Protected, prof, filepath.Join(prof, "AppData", "Local", "Temp"))
	}
	if win, err := windows.GetSystemWindowsDirectory(); err == nil && win != "" {
		g.Protected = append(g.Protected, win)
	}
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_ProgramData} {
		if dir, err := windows.KnownFolderPath(id, 0); err == nil && dir != "" {
			g.Protected = append(g.Protected, dir)
		}
	}
	return g
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
	tok, release, ref := prepareWinWall(wallOps, p, windowsGuard())
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

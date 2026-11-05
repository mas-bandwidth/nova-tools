//go:build linux

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

// Landlock is the LSM an unprivileged process applies to itself; the kernel keeps it
// across fork and execve, so the command cannot lift it. The tool restricts its own
// thread and then execs the command in place: no tool process stays behind, signals go
// straight to the command, and its exit status is the tool's.
const backendName = "landlock"

// maxABI is the highest Landlock ABI this tool knows; a newer kernel accepts a ruleset
// built for an older one.
const maxABI = 6

const (
	sysCreateRuleset = 444
	sysAddRule       = 445
	sysRestrictSelf  = 446
	ruleTypePath     = 1
	ruleTypeNetPort  = 2
)

type rulesetAttr struct{ fs, net, scope uint64 } // grew twice: send the size the ABI knows

type pathBeneath struct {
	access uint64
	fd     int32
	_      [4]byte // the kernel's struct is packed to 12 bytes and reads only those
}

type netPort struct{ access, port uint64 }

func attrSize(abi int) uintptr {
	switch {
	case abi >= 6:
		return 24
	case abi >= 4:
		return 16
	}
	return 8
}

// landlockABI is the kernel's Landlock version; not there at all is one answer: no.
func landlockABI() (int, bool) {
	n, _, errno := syscall.Syscall(sysCreateRuleset, 0, 0, 1)
	return int(n), errno == 0 && int(n) >= 1
}

// wallCheck says whether the wall can be built here: abi, whether --net-deny is
// enforceable, a note, and a non-nil error if the backend is not usable.
func wallCheck() (abi string, netOK bool, note string, err error) {
	n, ok := landlockABI()
	if !ok {
		return "-", false, "", fmt.Errorf("no landlock in this kernel: below 5.13, not compiled in, or not in the boot-time lsm= list")
	}
	return strconv.Itoa(n), n >= 4, "landlock abi " + strconv.Itoa(n) + ": net is TCP bind/connect only (abi 4+); udp is unrestricted", nil
}

// execWall applies the wall and execs the command. It returns only on failure.
func execWall(w *wall, env []string) *refusal {
	fail := func(reason, format string, a ...any) *refusal { r := refuse(reason, format, a...); return &r }
	abi, ok := landlockABI()
	if !ok {
		return fail("no_sandbox", "this kernel has no landlock; this tool does not run a command it cannot contain")
	}
	used := min(abi, maxABI)
	if w.netDeny && used < 4 {
		return fail("net_unenforceable", "--net-deny needs landlock abi 4 (kernel 6.7) and this kernel reports abi %d", abi)
	}
	rs := buildRuleset(w, used, resolverDirOf("/etc/resolv.conf"))
	attr := rulesetAttr{rs.fs, rs.net, rs.scope}
	fd, _, errno := syscall.Syscall(sysCreateRuleset, uintptr(unsafe.Pointer(&attr)), attrSize(used), 0)
	if errno != 0 {
		return fail("sandbox_failed", "the landlock ruleset could not be created at abi %d: %v", used, errno)
	}
	defer syscall.Close(int(fd))
	for _, ru := range rs.rules {
		if err := addPath(int(fd), ru, used); err != nil && ru.must {
			return fail("sandbox_failed", "%s could not be added to the landlock ruleset: %v", ru.path, err)
		}
	}
	for _, port := range rs.connectPorts {
		a := netPort{netConnectTCP, uint64(port)}
		if _, _, errno := syscall.Syscall6(sysAddRule, fd, ruleTypeNetPort, uintptr(unsafe.Pointer(&a)), 0, 0, 0); errno != 0 {
			return fail("sandbox_failed", "--net-allow port %d could not be added: %v", port, errno)
		}
	}
	// Restriction is per thread and Go moves goroutines between threads: pin this one
	// to the thread that is restricted, so the exec happens on it. It is never
	// unlocked; the thread dies with the exec.
	runtime.LockOSThread()
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, 38, 1, 0); errno != 0 { // PR_SET_NO_NEW_PRIVS
		return fail("sandbox_failed", "no_new_privs: %v", errno)
	}
	if _, _, errno := syscall.Syscall(sysRestrictSelf, fd, 0, 0); errno != 0 {
		return fail("sandbox_failed", "landlock_restrict_self at abi %d: %v", used, errno)
	}
	err := syscall.Exec(w.command, w.argv, env)
	return fail("sandbox_failed", "%s could not be started inside the wall: %v", w.command, err)
}

// addPath grants mask beneath ru.path. The descriptor is O_PATH, which needs no read
// permission on the directory itself.
func addPath(rulesetFd int, ru rule, abi int) error {
	fd, err := syscall.Open(ru.path, 0x200000|syscall.O_CLOEXEC, 0) // O_PATH
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	mask := ru.mask
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) == nil && st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		mask &= fileMask(abi)
	}
	a := pathBeneath{access: mask, fd: int32(fd)}
	if _, _, errno := syscall.Syscall6(sysAddRule, uintptr(rulesetFd), ruleTypePath, uintptr(unsafe.Pointer(&a)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// resolverDirOf is the directory the resolver config at conf resolves to, or "": some
// distributions keep it outside every static root.
func resolverDirOf(conf string) string {
	resolved, err := filepath.EvalSymlinks(conf)
	if err != nil {
		return ""
	}
	if dir := filepath.Dir(resolved); dir != "/" && dir != "." {
		return dir
	}
	return ""
}

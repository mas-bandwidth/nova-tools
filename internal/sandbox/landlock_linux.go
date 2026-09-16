//go:build linux

// The Landlock ABI, as data and in one place. This file knows the kernel's numbers and
// nothing about this tool's policy; wrap_linux.go turns a Policy into a ruleset with it.
//
// docs/SPEC-SANDBOX.md, "Linux — Landlock, no root" is normative for every table here.
// The one rule that governs the whole file: an access this tool does not HANDLE is an
// access the kernel does not CHECK, and that is a hole with no line in any log. So the
// handled set is the whole set the discovered ABI defines, and an ABI above the highest
// row of the table below is REFUSED rather than guessed at.
package sandbox

import (
	"syscall"
	"unsafe"
)

// The three syscalls, x86-64 and arm64 alike: Landlock's numbers are the same on every
// architecture Linux has shipped it on, because they were added in one series.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446
)

// createRulesetVersion is the flag that turns create_ruleset into the version query.
const createRulesetVersion = 1 << 0

// The rule types. Only PATH_BENEATH is added by this tool: a net DENIAL is the absence of
// every net rule, so LANDLOCK_RULE_NET_PORT is named here for the reader and never used.
const (
	ruleTypePathBeneath = 1
	_                   = 2 // LANDLOCK_RULE_NET_PORT: not used; see netHandled below
)

// The filesystem access bits of uapi/linux/landlock.h, in ABI order.
const (
	fsExecute    = 1 << 0
	fsWriteFile  = 1 << 1
	fsReadFile   = 1 << 2
	fsReadDir    = 1 << 3
	fsRemoveDir  = 1 << 4
	fsRemoveFile = 1 << 5
	fsMakeChar   = 1 << 6
	fsMakeDir    = 1 << 7
	fsMakeReg    = 1 << 8
	fsMakeSock   = 1 << 9
	fsMakeFifo   = 1 << 10
	fsMakeBlock  = 1 << 11
	fsMakeSym    = 1 << 12
	fsRefer      = 1 << 13 // ABI 2 (5.19)
	fsTruncate   = 1 << 14 // ABI 3 (6.2)
	fsIoctlDev   = 1 << 15 // ABI 5 (6.10)
)

// The network access bits, ABI 4 (6.7). TCP only: UDP is not restricted at any ABI, and
// that gap is stated on the SANDBOX OK line's net= field only as "denied", so it is
// stated in the spec's limits list instead.
const (
	netBindTCP    = 1 << 0
	netConnectTCP = 1 << 1
)

// The scopes, ABI 6 (6.12): below this a walled process can reach an abstract unix socket
// outside its domain and signal a process outside it.
const (
	scopeAbstractUnixSocket = 1 << 0
	scopeSignal             = 1 << 1
)

// maxKnownABI is the highest row of the spec's ABI table. A kernel that reports more than
// this defines accesses this tool has never heard of, so the tool REFUSES rather than
// advertise a wall with an unchecked hole in it (reason=landlock_abi_unknown). The fix is
// one row here, one row in the spec, and a release.
const maxKnownABI = 6

// fsABI1 is the ABI 1 set: every bit through MAKE_SYM.
const fsABI1 = fsExecute | fsWriteFile | fsReadFile | fsReadDir | fsRemoveDir | fsRemoveFile |
	fsMakeChar | fsMakeDir | fsMakeReg | fsMakeSock | fsMakeFifo | fsMakeBlock | fsMakeSym

// fsReadSubset is what a root and every --read gets: the spec's
// "the read set and the roots get EXECUTE|READ_FILE|READ_DIR".
const fsReadSubset = fsExecute | fsReadFile | fsReadDir

// fsFileSubset is what a rule on a FILE may carry, and it is not an optimisation: the
// kernel REJECTS a path_beneath rule whose descriptor is not a directory and whose
// allowed_access holds a directory-only right (MAKE_*, REMOVE_*, READ_DIR, REFER), with
// EINVAL. The roots table's two writable device files are files, so handing them the
// directory mask adds NO rule at all -- measured on space: with the directory mask,
// `sh -c "cmd > /dev/null"` inside the wall is "cannot create /dev/null: Permission
// denied", because the rule the tool thought it had added was never there.
const fsFileSubset = fsExecute | fsReadFile | fsWriteFile | fsTruncate | fsIoctlDev

// handledFS is the spec's ABI table, as code: the whole set the discovered ABI defines.
// A ruleset that handles a bit the running kernel does not know is rejected with EINVAL,
// so the set is masked DOWN to the ABI and never up.
func handledFS(abi int) uint64 {
	fs := uint64(fsABI1)
	if abi >= 2 {
		fs |= fsRefer
	}
	if abi >= 3 {
		fs |= fsTruncate
	}
	if abi >= 5 {
		fs |= fsIoctlDev
	}
	return fs
}

// writeSubset is what every --write gets: the read subset plus the bits the spec grants
// to the write set alone, masked to the ABI.
func writeSubset(abi int) uint64 { return handledFS(abi) }

// fileWriteSubset is writeSubset for a rule whose target is a FILE rather than a
// directory, masked to the ABI. See fsFileSubset for why the two cannot be the same.
func fileWriteSubset(abi int) uint64 { return fsFileSubset & handledFS(abi) }

// netHandled is rule 7 for this platform. The handled net set is non-empty ONLY under
// --net-deny: handling an access and adding no rule for it is how Landlock denies, and
// handling nothing is how it leaves the network alone. There is no middle setting, which
// is why --net-listen (inbound allowed) handles nothing here.
func netHandled(abi int, netDeny bool) uint64 {
	if abi < 4 || !netDeny {
		return 0
	}
	return netBindTCP | netConnectTCP
}

// scopedFor is ABI 6's two scopes, set whenever the kernel has them.
func scopedFor(abi int) uint64 {
	if abi < 6 {
		return 0
	}
	return scopeAbstractUnixSocket | scopeSignal
}

// rulesetAttr is struct landlock_ruleset_attr. It GREW twice, so the size passed to the
// syscall is the size the discovered ABI knows (rulesetAttrSize), never unsafe.Sizeof:
// a kernel at ABI 3 handed a 24-byte attr rejects it with E2BIG.
type rulesetAttr struct {
	HandledAccessFS  uint64
	HandledAccessNet uint64 // ABI 4
	Scoped           uint64 // ABI 6
}

// rulesetAttrSize is how many bytes of rulesetAttr the discovered ABI defines.
func rulesetAttrSize(abi int) uintptr {
	switch {
	case abi >= 6:
		return 24
	case abi >= 4:
		return 16
	default:
		return 8
	}
}

// pathBeneathAttr is struct landlock_path_beneath_attr. The kernel declares it
// __attribute__((packed)) — 12 bytes — and Go pads it to 16. That is harmless and is
// deliberate rather than overlooked: the kernel reads exactly its own sizeof (12) from
// this pointer, so the first 12 bytes are what it takes and Go's trailing pad is never
// looked at. The alternative, a hand-built [12]byte, buys nothing and loses the names.
type pathBeneathAttr struct {
	AllowedAccess uint64
	ParentFd      int32
	_             [4]byte
}

// landlockABI asks the kernel its Landlock version. A failure here is every one of the
// three ways Landlock can be absent — kernel below 5.13, not compiled in, not in the
// boot-time lsm= list — and all three are one answer: no.
func landlockABI() (int, bool) {
	n, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, createRulesetVersion)
	if errno != 0 || int(n) < 1 {
		return 0, false
	}
	return int(n), true
}

// createRuleset opens a ruleset handling everything the ABI defines.
func createRuleset(abi int, netDeny bool) (int, error) {
	attr := rulesetAttr{
		HandledAccessFS:  handledFS(abi),
		HandledAccessNet: netHandled(abi, netDeny),
		Scoped:           scopedFor(abi),
	}
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset,
		uintptr(unsafe.Pointer(&attr)), rulesetAttrSize(abi), 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

// addPathRule grants `allowed` beneath `path`. A path that is ABSENT is skipped and is
// not an error: the roots table is "skip if absent" (rule 5 refuses a CALLER's missing
// path, and Build has already resolved those, so anything missing here is a root).
// The descriptor is O_PATH, which needs no read permission on the directory itself.
func addPathRule(rulesetFd int, path string, allowed uint64) error {
	const oPath = 0x200000 // O_PATH: not in syscall on every GOARCH, so it is spelled here
	fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	attr := pathBeneathAttr{AllowedAccess: allowed, ParentFd: int32(fd)}
	_, _, errno := syscall.Syscall6(sysLandlockAddRule, uintptr(rulesetFd), ruleTypePathBeneath,
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// restrictSelf applies the ruleset to the CALLING THREAD. PR_SET_NO_NEW_PRIVS must
// succeed first or this fails with EPERM. Both are irreversible, and both are inherited
// across fork(2) and execve(2) — which is the whole mechanism: the command cannot lift
// what the tool applied to itself before starting it.
func restrictSelf(rulesetFd int) error {
	const prSetNoNewPrivs = 38
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return errno
	}
	if _, _, errno := syscall.Syscall(sysLandlockRestrictSelf, uintptr(rulesetFd), 0, 0); errno != 0 {
		return errno
	}
	return nil
}

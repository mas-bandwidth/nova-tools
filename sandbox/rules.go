package main

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The Landlock access bits of uapi/linux/landlock.h, in ABI order.
const (
	fsExecute         = 1 << 0
	fsWriteFile       = 1 << 1
	fsReadFile        = 1 << 2
	fsReadDir         = 1 << 3
	fsMakeAll         = 0x1FF0 // REMOVE_DIR through MAKE_SYM, bits 4-12
	fsRefer           = 1 << 13
	fsTruncate        = 1 << 14
	fsIoctlDev        = 1 << 15
	netBindTCP        = 1 << 0
	netConnectTCP     = 1 << 1
	scopeAbstractUnix = 1 << 0
	scopeSignal       = 1 << 1
)

// linuxReadRoots is what a command needs to run, read-only, skipped if absent. /proc
// and not /proc/self: that resolves at open time to the tool's own pid.
var linuxReadRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/run/systemd/resolve", "/opt", "/dev", "/proc"}

// linuxWriteFiles are the writable exceptions inside /dev.
var linuxWriteFiles = []string{"/dev/null", "/dev/tty"}

// linuxWriteDirs are writable system directories, skipped if absent. /dev/shm is shared
// with every process on the host: inside the wall the command can read, overwrite and
// delete what any same-uid process keeps there, and they can do the same to its files.
// It stays writable because POSIX shared memory (Python multiprocessing and others)
// lives there.
var linuxWriteDirs = []string{"/dev/shm"}

// readMask is what a root and every --read gets; a --write gets every handled right.
const readMask = fsExecute | fsReadFile | fsReadDir

// handledFS is every filesystem access the ABI defines. An access the ruleset does not
// handle is an access the kernel does not check, so the whole set is handled and the
// rules grant back what is allowed.
func handledFS(abi int) uint64 {
	fs := uint64(fsExecute | fsWriteFile | fsReadFile | fsReadDir | fsMakeAll)
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

// fileMask is what a rule on a file may carry; the kernel rejects directory-only
// rights on a non-directory.
func fileMask(abi int) uint64 {
	return (fsExecute | fsReadFile | fsWriteFile | fsTruncate | fsIoctlDev) & handledFS(abi)
}

// rule is one Landlock path grant. must says a failure to add it is a refusal: the
// system roots are skip-if-absent, the caller's paths are not.
type rule struct {
	path string
	mask uint64
	must bool
}

// ruleset is the whole wall: what to handle and what to grant back.
type ruleset struct {
	fs, net, scope uint64
	rules          []rule
	connectPorts   []uint16
}

// buildRuleset turns a wall into the ruleset for abi. resolverDir is the directory
// /etc/resolv.conf resolves to when it lies outside every static root, or "".
// Network is handled only under --net-deny: handling an access and adding no rule for
// it is how Landlock denies.
func buildRuleset(w *wall, abi int, resolverDir string) *ruleset {
	rs := &ruleset{fs: handledFS(abi)}
	if abi >= 4 && w.netDeny {
		rs.net, rs.connectPorts = netBindTCP|netConnectTCP, w.netPorts
	}
	if abi >= 6 {
		rs.scope = scopeAbstractUnix | scopeSignal
	}
	roots := slices.Clone(linuxReadRoots)
	if resolverDir != "" && !slices.Contains(roots, resolverDir) {
		roots = append(roots, resolverDir)
	}
	for _, r := range roots {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask})
	}
	for _, f := range linuxWriteFiles {
		rs.rules = append(rs.rules, rule{path: f, mask: fileMask(abi)})
	}
	for _, d := range linuxWriteDirs {
		rs.rules = append(rs.rules, rule{path: d, mask: rs.fs})
	}
	for _, r := range w.reads {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask, must: true})
	}
	for _, d := range w.writes {
		rs.rules = append(rs.rules, rule{path: d, mask: rs.fs, must: true})
	}
	return rs
}

// maxABI is the highest Landlock ABI this tool knows; a newer kernel accepts a ruleset
// built for an older one.
const maxABI = 6

// linuxHost is what the kernel offers the wall.
type linuxHost struct {
	landlock    bool
	abi         int // the kernel's Landlock ABI
	seccomp     bool
	resolverDir string
}

// linuxPlan is the wall as it will be applied: the ruleset at the ABI used, and the
// seccomp filter if one is installed.
type linuxPlan struct {
	abi     int // the ABI the ruleset is built for: the kernel's, capped at maxABI
	rs      *ruleset
	seccomp bool
	filter  filterOpts
}

// planLinux decides the wall for host, or refuses: it never plans a wall weaker than
// the flags say. It is pure, so execWall and show apply and print the same plan.
func planLinux(w *wall, h linuxHost) (*linuxPlan, []refusal) {
	if !h.landlock {
		return nil, []refusal{refuse("no_sandbox", "this kernel has no landlock; this tool does not run a command it cannot contain")}
	}
	var bad []refusal
	used := min(h.abi, maxABI)
	if w.netDeny && used < 4 {
		bad = append(bad, refuse("net_unenforceable", "--net-deny needs landlock abi 4 (kernel 6.7) and this kernel reports abi %d", h.abi))
	}
	// Landlock fences TCP only; UDP, raw and every other family are closed by the
	// seccomp filter, so --net-deny without it would be weaker than it says.
	if w.netDeny && !h.seccomp {
		bad = append(bad, refuse("net_unenforceable", "net-deny needs seccomp on this kernel: landlock fences TCP only, and without the seccomp filter UDP and raw sockets would stay open"))
	}
	// The unix-socket escape (Landlock does not path-check a unix connect) is closed by
	// the seccomp filter. Without seccomp the wall refuses rather than stand weaker than
	// it says, unless --unix opts into leaving the socket open.
	if !w.unix && !h.seccomp {
		bad = append(bad, refuse("no_seccomp", "this kernel has no seccomp filter mode, so the same-uid unix-socket escape cannot be closed; pass --unix to run without that fence"))
	}
	if len(bad) > 0 {
		return nil, bad
	}
	// With seccomp, --unix still installs the filter, minus the AF_UNIX rules, so ptrace
	// and io_uring stay refused. Without seccomp only --unix without --net-deny gets
	// here, and nothing is installed: the caller accepted a wall without those fences.
	return &linuxPlan{abi: used, rs: buildRuleset(w, used, h.resolverDir), seccomp: h.seccomp, filter: filterOptsOf(w)}, nil
}

// portList is the --net-allow ports, comma-separated.
func portList(ports []uint16) string {
	var s []string
	for _, p := range ports {
		s = append(s, strconv.Itoa(int(p)))
	}
	return strings.Join(s, ",")
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

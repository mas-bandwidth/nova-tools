package main

import "slices"

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
	for _, r := range w.reads {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask, must: true})
	}
	for _, d := range w.writes {
		rs.rules = append(rs.rules, rule{path: d, mask: rs.fs, must: true})
	}
	return rs
}

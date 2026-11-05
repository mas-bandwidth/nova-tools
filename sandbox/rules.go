package main

import (
	"fmt"
	"net"
	"slices"
	"strconv"
)

// The Landlock filesystem access bits of uapi/linux/landlock.h, in ABI order.
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

// netHandled is non-empty only under --net-deny: handling an access and adding no rule
// for it is how Landlock denies. Net needs ABI 4.
func netHandled(abi int, netDeny bool) uint64 {
	if abi < 4 || !netDeny {
		return 0
	}
	return netBindTCP | netConnectTCP
}

func scoped(abi int) uint64 {
	if abi < 6 {
		return 0
	}
	return scopeAbstractUnix | scopeSignal
}

// rule is one Landlock path grant. must says a failure to add it is a refusal; the
// roots table is skip-if-absent, the caller's paths are not. file rules are masked
// to fileMask.
type rule struct {
	path string
	mask uint64
	must bool
	file bool
}

// ruleset is the whole wall: what to handle and what to grant back.
type ruleset struct {
	fs, net, scope uint64
	rules          []rule
	connectPorts   []uint16 // --net-allow ports under --net-deny
}

// readMask is what a root and every --read gets, and writeMask what a --write gets.
const readMask = fsExecute | fsReadFile | fsReadDir

// buildRuleset turns a policy into the ruleset for abi. resolverDir is the directory
// /etc/resolv.conf resolves to (WSL keeps it outside every static root), or "".
func buildRuleset(p *policy, abi int, resolverDir string) (*ruleset, error) {
	rs := &ruleset{fs: handledFS(abi), net: netHandled(abi, p.netDeny), scope: scoped(abi)}
	roots := slices.Clone(linuxReadRoots)
	if resolverDir != "" && !slices.Contains(roots, resolverDir) {
		roots = append(roots, resolverDir)
	}
	for _, r := range roots {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask})
	}
	for _, f := range linuxWriteFiles {
		rs.rules = append(rs.rules, rule{path: f, mask: fileMask(abi), file: true})
	}
	for _, r := range p.optRoots {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask})
	}
	for _, r := range p.reads {
		rs.rules = append(rs.rules, rule{path: r, mask: readMask, must: true})
	}
	writes := slices.Clone(p.writes)
	for _, extra := range []string{p.tmp, p.cwd} {
		if extra != "" && !slices.Contains(writes, extra) {
			writes = append(writes, extra)
		}
	}
	for _, w := range writes {
		rs.rules = append(rs.rules, rule{path: w, mask: rs.fs, must: true})
	}
	// Rules are a union, not ordered: a no-exec grant cannot take execute back from a
	// parent that gives it, so a --read-noexec under an execute root is refused rather
	// than built into a wall that does not mean what it says.
	for _, n := range p.readsNoExec {
		for _, r := range rs.rules {
			if r.mask&fsExecute != 0 && !r.file && inside(n, r.path) {
				return nil, fmt.Errorf("--read-noexec %s sits under %s, which the wall grants execute; landlock rules are a union, so name a directory no execute root covers", n, r.path)
			}
		}
		rs.rules = append(rs.rules, rule{path: n, mask: fsReadFile | fsReadDir, must: true})
	}
	if rs.net != 0 {
		for _, hp := range p.netAllow {
			if _, port, err := net.SplitHostPort(hp); err == nil {
				if n, err := strconv.ParseUint(port, 10, 16); err == nil {
					rs.connectPorts = append(rs.connectPorts, uint16(n))
				}
			}
		}
	}
	return rs, nil
}

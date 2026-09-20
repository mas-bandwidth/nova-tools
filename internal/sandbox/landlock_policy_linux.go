//go:build linux

// The landlock ruleset as printable text: what `policy` writes on this platform
// instead of the darwin profile, which no linux run uses (issue #1469).
package sandbox

import (
	"fmt"
	"os"
	"strings"
)

// LandlockPolicyText renders the ruleset the linux body would build from this
// policy, in the order addRules adds it: the read-only roots, this run's
// optional roots, the caller's --read and --read-noexec, then the write set.
// Landlock takes rules, not a profile document, so this text IS the generated
// policy rule 15 asks the `policy` verb to print — and like the verb, it runs
// nothing and applies nothing.
//
// A path holding a control character cannot reach here: Build refuses one on
// every platform, so one line stays one rule. The POLICY OK line carries the
// counts; this body carries the sets.
func LandlockPolicyText(p *Policy) (string, error) {
	if p == nil || len(p.Writes) == 0 {
		return "", fmt.Errorf("a policy with no --write has no ruleset")
	}
	var b strings.Builder
	b.WriteString("# nova-sandbox — landlock ruleset, generated (rule 15).\n")
	b.WriteString("#\n")
	b.WriteString("# Landlock has no profile text: the kernel takes rules, not a document.\n")
	b.WriteString("# This is the ruleset a wrapped run would build from the same flags\n")
	b.WriteString("# (docs/SPEC-SANDBOX.md, \"Linux — Landlock, no root\"), and it runs nothing.\n")
	fmt.Fprintf(&b, "backend=%s\n", Backend)
	abi, ok := available()
	if !ok {
		b.WriteString("abi=-\n")
		b.WriteString("# note: this kernel has no landlock, so a wrapped run would refuse (no_sandbox)\n")
	} else if used, clamped := wallABI(abi); clamped {
		fmt.Fprintf(&b, "abi=%d used=%d\n", abi, used)
		b.WriteString("# note: the wall is built at the table's maximum; the rights the newer abi added are not handled until the table grows\n")
	} else {
		fmt.Fprintf(&b, "abi=%d\n", abi)
		if p.NetDeny && abi < 4 {
			b.WriteString("# note: --net-deny needs abi 4 and this wall is built below it, so a wrapped run would refuse (net_unenforceable)\n")
		}
	}
	fmt.Fprintf(&b, "net=%s\n", p.Net())
	// The roots, read-only, skipped if absent — the same skip addRules applies,
	// so the printed set is the set the wall would hold.
	b.WriteString("# read-only roots: the linux roots table, then this run's optional roots\n")
	for _, root := range linuxReadRoots {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		fmt.Fprintf(&b, "read=%s\n", root)
	}
	for _, root := range p.OptRoots {
		fmt.Fprintf(&b, "read=%s\n", root)
	}
	b.WriteString("# the caller's own sets, exactly as named\n")
	for _, r := range p.Reads {
		fmt.Fprintf(&b, "read=%s\n", r)
	}
	for _, r := range p.ReadsNoExec {
		fmt.Fprintf(&b, "read-noexec=%s\n", r)
	}
	for _, w := range writePaths(p) {
		fmt.Fprintf(&b, "write=%s\n", w)
	}
	// The two writable device files of the roots table. They are FILES, so they
	// are their own grant, not a recursive write beneath a directory.
	for _, dev := range linuxWriteFiles {
		if _, err := os.Stat(dev); err != nil {
			continue
		}
		fmt.Fprintf(&b, "writefile=%s\n", dev)
	}
	fmt.Fprintf(&b, "gpu=%s\n", string(p.GPUMode))
	return b.String(), nil
}

package swarm

import (
	"os"
	"path/filepath"
)

// THE BENCH TOOLCHAIN ROOTS (the schema dogfood loop, 2026-09-18).
//
// TWO CONTRACTS CONTRADICTED EACH OTHER, and every Go card on hulk died in the gap. The
// bench provisioning standard puts the toolchain in a USER directory -- Go and sbcl under
// `~/sdk`, the standard's own PATH entry `~/go/bin`, the module cache at `~/go/pkg/mod` --
// while the native wall named NO toolchain root at all and pinned `GOTOOLCHAIN=local`. So
// the wall denied EXECUTION of `~/sdk/go1.26.5/bin/go` and of `~/go/bin/go` (`Permission
// denied`), the only reachable Go inside the wall was the distribution's `/usr/bin/go`
// 1.22.2 under the `/usr` root, and `go.mod` refused it in one line:
//
//	go: go.mod requires go >= 1.26 (running go 1.22.2; GOTOOLCHAIN=local)
//
// `GOTOOLCHAIN=local` is RIGHT and stays: a card must not fetch a toolchain behind the
// bench's back (card 8963). What was wrong is that the bench's own toolchain was outside
// every named path. The wall's implicit worker description now names these roots the way an
// explicit description names `read_roots` -- a toolchain in a user directory is exactly a
// caller-supplied read-only root (SPEC-SANDBOX, "the two callers") -- and a `--read` root
// carries EXECUTE on both bodies, so naming it is all that is required.
//
// ONE PLACE. The list lives HERE and nowhere else: the wall reads it to build its argv, and
// the provisioning standard's own check (`tools/bench-standard.sh`) reads the same names
// through the class test in internal/ci, which fails when the two lists drift apart. A
// literal in two places is how the two contracts came to disagree in the first place.
//
// NOTHING ELSE IN HOME. These three names are the toolchain and only the toolchain. The key
// store (`~/.config/nova-secrets`), `~/.ssh`, the bus, the caller's checkouts and every
// other path under HOME stay outside the wall, exactly as they were.

// toolchainRootNames is the standard's toolchain directories, relative to a bench HOME and
// written in slash form so the one list reads the same on every platform.
//
//	sdk   the toolchain tree the standard installs into: go<ver>/ and sbcl-<ver>/. Read
//	      AND execute -- this is the Go the card must run. It is the ONLY home directory
//	      the wall grants execute on, and the card's `go` and `gofmt` come from it.
//
// TWO ROOTS ARE DELIBERATELY NOT HERE (Johnny's security read of #1364), because a
// `--read` root CARRIES EXECUTE on both bodies -- landlock's read subset is
// EXECUTE|READ_FILE|READ_DIR, and the darwin profile grants process-exec* globally:
//
//	go/bin      GOPATH/bin. Every `go install` on the bench lands there, including the
//	            stale nova-* binaries being retired, and the bench user can write to it.
//	            Granting it would let a card EXECUTE bench-user tools inside the wall.
//	            Nothing is lost: on a provisioned bench ~/go/bin/go is a SYMLINK into the
//	            sdk tree, and the kernel checks the resolved target, so a card whose PATH
//	            finds ~/go/bin/go first still runs the granted toolchain -- while a real
//	            binary sitting in that directory is Permission denied. Measured on hulk.
//	go/pkg/mod  the bench's module cache. It wants READ WITHOUT EXECUTE, and the wall has
//	            no argv form for that yet: Policy.ReadsNoExec and both bodies carry the
//	            grant (internal/sandbox), but `nova-sandbox` has no `--read-noexec` flag
//	            to reach it. Until that flag lands the cache is NOT granted at all, which
//	            costs a card nothing -- its GOMODCACHE is the per-bench writable cache
//	            under <root>/cache (card 8963), never this path. Granting it read+exec to
//	            save a download is not a trade this wall makes.
var toolchainRootNames = []string{"sdk"}

// ToolchainRootNames is the list itself, HOME-relative and in slash form: the provisioning
// standard's side of the agreement, and what the class test compares against. A copy, so no
// caller can edit the one source by holding it.
func ToolchainRootNames() []string {
	out := make([]string, len(toolchainRootNames))
	copy(out, toolchainRootNames)
	return out
}

// ToolchainRoots is the wall's side: the same list under one home, absolute, and SKIPPED IF
// ABSENT. Absent is the machine's shape and not the caller's mistake -- a darwin bench has
// no `~/sdk` -- while rule 5 of the wall REFUSES a `--read` naming a path that is not there,
// so a root that does not exist must never reach the argv. An empty home names nothing at
// all, because a relative root is a refusal and a root at the filesystem's top is not a
// toolchain.
func ToolchainRoots(home string) []string {
	if home == "" {
		return nil
	}
	var out []string
	for _, name := range toolchainRootNames {
		root := filepath.Join(home, filepath.FromSlash(name))
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, root)
	}
	return out
}

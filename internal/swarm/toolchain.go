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
//	sdk         the toolchain tree the standard installs into: go<ver>/ and sbcl-<ver>/.
//	            Read AND execute -- this is the Go the card must run.
//	go/bin      the standard's own PATH entry (the unit files carry `go/bin`), which on a
//	            provisioned bench is where `go` and `gofmt` are found first.
//	go/pkg/mod  the bench's module cache, READ. A card's WRITABLE caches are the per-bench
//	            pair under `<root>/cache` (GOMODCACHE and GOCACHE, card 8963), which the
//	            wall already grants as a `--write`; this root is the bench's own copy, so a
//	            card that is pointed at it by an inherited GOPATH/GOMODCACHE reads the
//	            downloads the bench already made instead of dying on a denial.
var toolchainRootNames = []string{"sdk", "go/bin", "go/pkg/mod"}

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

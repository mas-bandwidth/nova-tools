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
// caller-supplied read-only root (SPEC-SANDBOX, "the two callers") -- and it names each root
// under ONE OF TWO KINDS: `--read`, which carries EXECUTE on both bodies, for the tree whose
// `go` the card must run, and `--read-noexec`, which does not, for the tree the card only
// reads.
//
// ONE PLACE. The list lives HERE and nowhere else: the wall reads it to build its argv, and
// the provisioning standard's own check (`tools/bench-standard.sh`) reads the same names
// through the class test in internal/ci, which fails when the two lists drift apart. A
// literal in two places is how the two contracts came to disagree in the first place.
//
// NOTHING ELSE IN HOME. These three names are the toolchain and only the toolchain. The key
// store (`~/.config/nova-secrets`), `~/.ssh`, the bus, the caller's checkouts and every
// other path under HOME stay outside the wall, exactly as they were.

// ToolchainRoot is one entry of the one list: a directory under the bench HOME and THE KIND
// OF GRANT it gets. The kind is the security decision, not a detail of the argv -- a
// `--read` root CARRIES EXECUTE on both bodies (landlock's read subset is
// EXECUTE|READ_FILE|READ_DIR, and the darwin profile grants process-exec* globally), so a
// tree the bench user can write to must be named as the read-only kind or a card can run
// whatever lands in it (Johnny's security read of #1364).
type ToolchainRoot struct {
	// Name is HOME-relative and in slash form, so the one list reads the same on every
	// platform. It is what the provisioning standard's own copy carries.
	Name string
	// Path is the absolute directory under one home. It is set only by ToolchainRoots and
	// is empty in the declaration.
	Path string
	// Exec is the kind: true means `--read`, readable AND executable; false means
	// `--read-noexec`, readable and NOT executable.
	Exec bool
}

// toolchainRoots is the standard's toolchain directories, ONE LIST WITH TWO KINDS.
//
//	sdk         the toolchain tree the standard installs into: go<ver>/ and sbcl-<ver>/.
//	            READ AND EXECUTE -- this is the Go the card must run. It is the ONLY home
//	            directory the wall grants execute on, and the card's `go` and `gofmt` come
//	            from it.
//	go/pkg/mod  the bench's module cache. READ WITHOUT EXECUTE: the card reads a module's
//	            sources out of it, and nothing under it is ever a program the card runs.
//	            Every `go mod download` on the bench lands there and the bench user can
//	            write to it, so the exec-carrying kind would put a dependency's own files
//	            one exec away from running inside the wall.
//
// ONE ROOT IS DELIBERATELY NOT HERE UNDER EITHER KIND (Johnny's security read of #1364):
//
//	go/bin      GOPATH/bin. Every `go install` on the bench lands there, including the
//	            stale nova-* binaries being retired, and the bench user can write to it.
//	            Granting it exec would let a card EXECUTE bench-user tools inside the wall,
//	            and granting it read-without-exec would buy a card nothing -- a directory of
//	            binaries is worth reading only to run them. Nothing is lost: on a
//	            provisioned bench ~/go/bin/go is a SYMLINK into the sdk tree, and the kernel
//	            checks the resolved target, so a card whose PATH finds ~/go/bin/go first
//	            still runs the granted toolchain -- while a real binary sitting in that
//	            directory is Permission denied. Measured on hulk.
var toolchainRoots = []ToolchainRoot{
	{Name: "sdk", Exec: true},
	{Name: "go/pkg/mod", Exec: false},
}

// ToolchainRootList is the declaration itself, kinds and all: what a reader of the wall and
// the class test in internal/ci ask when the question is not "which paths" but "granted
// HOW". A copy, so no caller can edit the one source by holding it.
func ToolchainRootList() []ToolchainRoot {
	out := make([]ToolchainRoot, len(toolchainRoots))
	copy(out, toolchainRoots)
	return out
}

// ToolchainRootNames is the same list as names, HOME-relative and in slash form: the
// provisioning standard's side of the agreement, which is about what a bench HAS rather
// than about how the wall grants it, and what the class test compares against.
func ToolchainRootNames() []string {
	out := make([]string, 0, len(toolchainRoots))
	for _, r := range toolchainRoots {
		out = append(out, r.Name)
	}
	return out
}

// ToolchainRoots is the wall's side: the same list under one home, absolute, carrying each
// root's kind, and SKIPPED IF ABSENT. Absent is the machine's shape and not the caller's
// mistake -- a darwin bench has no `~/sdk` -- while rule 5 of the wall REFUSES a `--read` or
// a `--read-noexec` naming a path that is not there, so a root that does not exist must
// never reach the argv. An empty home names nothing at all, because a relative root is a
// refusal and a root at the filesystem's top is not a toolchain.
func ToolchainRoots(home string) []ToolchainRoot {
	if home == "" {
		return nil
	}
	var out []ToolchainRoot
	for _, r := range toolchainRoots {
		r.Path = filepath.Join(home, filepath.FromSlash(r.Name))
		if fi, err := os.Stat(r.Path); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, r)
	}
	return out
}

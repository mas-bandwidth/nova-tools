package swarm

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// The bench toolchain roots are the directories the wall grants to toolchain processes.
//
// The wall and the provisioning standard share one list of roots, so a card's toolchain runs. The
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
// The same runtime lookup applies on Darwin. A Darwin bench's
// toolchains are INSTALLED and on `PATH`, and three of them still died inside the bare wall,
// because each resolves its own runtime FROM THE DIRECTORY OF THE LAUNCHER THAT RAN IT and
// that launcher is a symlink OUT of any granted tree:
//
//	go: cannot find GOROOT directory: 'go' binary is trimmed
//	dotnet: Failed to resolve full path of the current executable []
//	java: Unable to locate a Java Runtime
//
// `/opt/homebrew/bin/go` is a symlink into `/opt/homebrew/Cellar/go/1.27.1/libexec`, and the
// measured remedy was to name the CELLAR tree: `--read /opt/homebrew/Cellar/go/1.27.1`. The
// version is the machine's and not ours, so a darwin root that lives under a versioned
// prefix is RESOLVED AT RUNTIME from the launcher the bench's own PATH finds, exactly the
// way `readlink -f "$(command -v go)"` resolves it by hand.
//
// ONE PLACE, PER GOOS. The list lives HERE and nowhere else, and each operating system's
// entries are that OS's provisioning standard read back: the wall reads them to build its
// argv, and the standard's own check names the same roots -- `tools/benchstandard`'s
// marked block for a linux bench, the pulse package's darwin check table for a Mac one --
// held together by the class test in internal/ci, which fails IN BOTH DIRECTIONS PER OS when
// the lists drift apart. A literal in two places is how the two contracts came to disagree
// in the first place.
//
// NOTHING ELSE IN HOME. The home-relative names here are the toolchain and only the
// toolchain. The key store (`~/.config/nova-secrets`), `~/.ssh`, the bus, the caller's
// checkouts and every other path under HOME stay outside the wall, exactly as they were.

// ToolchainRoot is one entry of the one list: a directory the bench's toolchain lives in and
// THE KIND OF GRANT it gets. The kind is the security decision, not a detail of the argv --
// a `--read` root CARRIES EXECUTE on both bodies (landlock's read subset is
// EXECUTE|READ_FILE|READ_DIR, and the darwin profile grants process-exec* globally), so a
// tree the bench user can write to must be named as the read-only kind or a card can run
// untrusted files placed there by the bench user.
type ToolchainRoot struct {
	// Name is the token BOTH lists carry, in slash form. A name that begins with "/" is a
	// SYSTEM root and is that absolute directory -- or, when Tool is set, the versioned
	// prefix under which one directory is resolved; any other name is HOME-RELATIVE and is
	// joined to one bench home.
	Name string
	// Path is the absolute directory on one machine, resolved through its symlinks. It is
	// set only by ToolchainRoots and is empty in the declaration.
	Path string
	// Exec is the kind: true means `--read`, readable AND executable; false means
	// `--read-noexec`, readable and NOT executable.
	Exec bool
	// Tool, when set, is the program on the bench's PATH whose resolved launcher names the
	// ONE versioned directory under Name that this bench actually runs -- `go` resolving to
	// /opt/homebrew/Cellar/go/1.27.1/libexec/bin/go names /opt/homebrew/Cellar/go/1.27.1.
	// The version belongs to the machine, so naming the prefix itself would grant every
	// version ever brewed and hard-coding one would be stale on the next `brew upgrade`.
	Tool string
	// Optional says the provisioning standard does not DEMAND this root, only grants it when
	// the bench has it. A linux bench's roots are the standard's own installs and a missing
	// one is drift; a Mac bench's toolchains are installed by hand and by brew, so its roots
	// are reported and never drifted on -- a Mac with no .NET is a Mac with no .NET, and the
	// bench still has to have a Go, which the standard's own `go` check is what asserts.
	Optional bool
	// PathDirs are the directories under a SYSTEM root, in slash form ("." for the root
	// itself), that go on a card's PATH (BenchToolBins). A home root's PATH entry is always its
	// `bin` and names none here. Only the windows list sets it: a Windows toolchain is
	// installed under Program Files, never linked into ~/sdk/bin, and a bin directory is never
	// a root of its own (the grant is on the tree), so the tree names the directories of it a
	// card searches.
	PathDirs []string
}

// Home says whether this root is found under a bench home rather than at an absolute path.
// An absolute path is "/..." or, on Windows, a drive-letter path in slash form ("C:/...").
func (r ToolchainRoot) Home() bool { return !absRootName(r.Name) }

// absRootName says whether a root's name is an absolute path, in either spelling the list
// uses: "/opt/..." or a drive letter, "C:/Program Files/Git".
func absRootName(name string) bool {
	if strings.HasPrefix(name, "/") {
		return true
	}
	return len(name) >= 3 && name[1] == ':' && name[2] == '/' &&
		(name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z')
}

// toolchainRoots is the standard's toolchain directories PER GOOS, one list with two kinds.
//
// LINUX is the provisioned bench: what the standard installs, it installs under HOME.
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
// DARWIN is a Mac bench, where the toolchains are INSTALLED rather than unpacked into a
// home. It keeps both home roots -- a Mac provisioned to the darwin standard has the Go SDK
// and sbcl under `~/sdk`, and every Mac that has ever built Go has the module cache -- and
// adds the system trees the Air measured, each granted EXECUTE because each is a runtime the
// card RUNS and none of them is a directory a card can write:
//
//	/opt/homebrew/Cellar/go    brew's Go, resolved through `go`: the launcher on PATH is a
//	                           symlink into <ver>/libexec/bin, and without the Cellar tree
//	                           the runtime it looks for beside itself is not there at all
//	                           ("'go' binary is trimmed").
//	/opt/homebrew/Cellar/sbcl  brew's sbcl, resolved through `sbcl` the same way: the core
//	                           file lives beside the binary inside the Cellar tree.
//	/opt/homebrew/opt/openjdk  brew's JDK, itself a symlink into the Cellar (on the Air it
//	                           resolves to /opt/homebrew/Cellar/openjdk/27), and
//	/Library/Java/JavaVirtualMachines
//	                           the install location the system's own launcher reads, where
//	                           brew links its JDK. With the tree granted, that JDK RUNS
//	                           inside the wall -- measured. The `/usr/bin/java` STUB still
//	                           says "Unable to locate a Java Runtime", because it asks
//	                           `/usr/libexec/java_home`, which needs a system service the
//	                           wall denies and not a path anyone can grant; a Java card sets
//	                           JAVA_HOME, and then the stub works too (measured on the Air).
//	/usr/local/share/dotnet    the .NET install. `dotnet` resolves its own full path and
//	                           fails with an EMPTY one ("Failed to resolve full path of the
//	                           current executable []") when the tree is denied.
//
// The list excludes one writable binary directory on every OS:
//
//	go/bin      GOPATH/bin. Every `go install` on the bench lands there, including the
//	            stale nova-* binaries being retired, and the bench user can write to it.
//	            Granting it exec would let a card EXECUTE bench-user tools inside the wall,
//	            and granting it read-without-exec would buy a card nothing -- a directory of
//	            binaries is worth reading only to run them. Nothing is lost: on a
//	            provisioned bench ~/go/bin/go is a SYMLINK into the sdk tree, and the kernel
//	            checks the resolved target, so a card whose PATH finds ~/go/bin/go first
//	            still runs the granted toolchain -- while a real binary sitting in that
//	            directory is Permission denied, so only the resolved toolchain target runs.
//
// The same rule is why no bin directory appears on the darwin side either: every grant there
// is on a toolchain TREE (`/opt/homebrew/Cellar/go/<ver>`) and never on `/opt/homebrew/bin`,
// which is a writable directory holding a launcher for every formula on the machine.
//
// WINDOWS is a native Windows member (captain, titan: the members that take only GATE-NEEDS
// windows cards). It keeps both home roots, under %USERPROFILE% -- `~/sdk/bin` is where the
// member play puts the tools it installs for cards (sqlite3, which native reads a card's
// token budget with) -- and adds the installed toolchains the rocketnet Windows cards build
// with, each a TREE whose PathDirs name what a card searches, because Windows has no wall
// body yet (pkg/sandbox, wrap_other.go) and these roots are, for now, what makes the
// toolchain FINDABLE (BenchPath) rather than what a wall grants:
//
//	C:/Program Files/Git        Git for Windows: cmd/ holds git.exe, bin/ holds git, bash and
//	                            sh, so a card's bare `bash` is Git's and never the WSL launcher
//	                            in System32.
//	C:/Program Files/CMake      cmake, ctest, cpack in bin/; the Visual Studio generator finds
//	                            MSVC itself through the Visual Studio setup API.
//	C:/Program Files/Go         the installed Go (go.exe, gofmt.exe in bin/).
//	C:/Program Files (x86)/Microsoft Visual Studio/Installer
//	                            vswhere.exe, at its documented fixed location, so a card can
//	                            find Visual Studio; native itself runs it and vcvars64.bat to
//	                            put cl, link and the Windows SDK on the child's PATH, INCLUDE and
//	                            LIB (msvc.go, MSVCEnv).
//
// The member's own `C:/nova-swarm/bin` (nova-swarm, sqlite3, sops) is NOT a root: the tree
// above it holds the seat's key and the secrets store, and the member's supervisor already
// puts that bin first on the member's PATH, which a card inherits.
var toolchainRoots = map[string][]ToolchainRoot{
	"linux": {
		{Name: "sdk", Exec: true},
		{Name: "go/pkg/mod", Exec: false},
	},
	"darwin": {
		{Name: "sdk", Exec: true, Optional: true},
		{Name: "go/pkg/mod", Exec: false, Optional: true},
		{Name: "/opt/homebrew/Cellar/go", Exec: true, Tool: "go", Optional: true},
		{Name: "/opt/homebrew/Cellar/sbcl", Exec: true, Tool: "sbcl", Optional: true},
		{Name: "/opt/homebrew/opt/openjdk", Exec: true, Optional: true},
		{Name: "/Library/Java/JavaVirtualMachines", Exec: true, Optional: true},
		{Name: "/usr/local/share/dotnet", Exec: true, Optional: true},
	},
	"windows": {
		{Name: "sdk", Exec: true, Optional: true},
		{Name: "go/pkg/mod", Exec: false, Optional: true},
		{Name: "C:/Program Files/Git", Exec: true, Optional: true, PathDirs: []string{"cmd", "bin"}},
		{Name: "C:/Program Files/CMake", Exec: true, Optional: true, PathDirs: []string{"bin"}},
		{Name: "C:/Program Files/Go", Exec: true, Optional: true, PathDirs: []string{"bin"}},
		{Name: "C:/Program Files (x86)/Microsoft Visual Studio/Installer", Exec: true, Optional: true, PathDirs: []string{"."}},
	},
}

// ToolchainRootList is the declaration itself for one operating system, kinds and all: what
// a reader of the wall and the class test in internal/ci ask when the question is not "which
// paths" but "granted HOW". An OS the list does not speak for names nothing at all, which is
// the behaviour of every run before the roots existed and never a guess at another OS's
// layout. A copy, so no caller can edit the one source by holding it.
func ToolchainRootList(goos string) []ToolchainRoot {
	decl := toolchainRoots[goos]
	out := make([]ToolchainRoot, len(decl))
	copy(out, decl)
	for i := range out {
		out[i].PathDirs = slices.Clone(decl[i].PathDirs)
	}
	return out
}

// ToolchainRootNames is the same list as names, in slash form: the provisioning standard's
// side of the agreement, which is about what a bench HAS rather than about how the wall
// grants it, and what the class test compares against.
func ToolchainRootNames(goos string) []string {
	decl := toolchainRoots[goos]
	out := make([]string, 0, len(decl))
	for _, r := range decl {
		out = append(out, r.Name)
	}
	return out
}

// ToolchainRootOSes is every operating system the one list speaks for, sorted, so the class
// test walks the declaration itself rather than a list of its own that could fall behind it.
func ToolchainRootOSes() []string {
	return slices.Sorted(maps.Keys(toolchainRoots))
}

// ToolchainRoots is the wall's side: this machine's list, absolute, carrying each root's
// kind, and SKIPPED IF ABSENT. Absent is the machine's shape and not the caller's mistake --
// a Darwin bench may lack `~/sdk` or dotnet, and the wall refuses a
// `--read` or a `--read-noexec` naming a path that is not there, so a root that does not
// exist must never reach the argv. An empty home names no HOME-relative root, because a
// relative root is a refusal and a root at the filesystem's top is not a toolchain; a system
// root needs no home and is still named.
//
// Every path is RESOLVED THROUGH ITS SYMLINKS before it is named, because the grant is
// checked against the resolved target on both bodies: `/opt/homebrew/opt/openjdk` is itself
// a symlink into the Cellar, and granting the link would name a tree the card's `java` is
// not under.
func ToolchainRoots(goos, home string) []ToolchainRoot {
	var out []ToolchainRoot
	for _, r := range toolchainRoots[goos] {
		path, ok := toolchainRootPath(r, home)
		if !ok {
			continue
		}
		r.Path = path
		out = append(out, r)
	}
	return out
}

// toolchainRootPath resolves one declaration on this machine, or says the root is not here.
func toolchainRootPath(r ToolchainRoot, home string) (string, bool) {
	path := r.Name
	switch {
	case r.Home():
		if home == "" {
			return "", false
		}
		path = filepath.Join(home, filepath.FromSlash(r.Name))
	case r.Tool != "":
		// The version under the prefix belongs to the machine: read it off the launcher the
		// bench's own PATH finds, the way `readlink -f "$(command -v go)"` does.
		v, ok := toolchainVersionDir(r.Name, r.Tool, exec.LookPath)
		if !ok {
			return "", false
		}
		path = v
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return "", false
	}
	return resolved, true
}

// toolchainVersionDir is `readlink -f "$(command -v <tool>)"` reduced to the one directory
// directly under prefix: /opt/homebrew/Cellar/go plus `go` is /opt/homebrew/Cellar/go/1.27.1,
// from a launcher whose real path is /opt/homebrew/Cellar/go/1.27.1/libexec/bin/go. A tool
// that is not on PATH, or whose real path is under some other prefix -- a Go unpacked into
// ~/sdk, a tool from /usr/bin -- names nothing: this entry is brew's copy and only brew's.
//
// lookPath is how the launcher is found: exec.LookPath in the binary, and a test's own
// lookup over a directory of its own in a test -- the per-test seam the serial-tests
// ledger names ("a field on the value under test"), in place of t.Setenv("PATH", dir).
func toolchainVersionDir(prefix, tool string, lookPath func(string) (string, error)) (string, bool) {
	launcher, err := lookPath(tool)
	if err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		return "", false
	}
	// BOTH SIDES RESOLVED, or the comparison is between two spellings of one directory: on a
	// Mac /var is a symlink to /private/var, so a resolved launcher under a prefix spelled
	// the other way would look like a tool from somewhere else entirely.
	base := prefix
	if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
		base = resolved
	}
	rest, under := strings.CutPrefix(filepath.ToSlash(real), filepath.ToSlash(base)+"/")
	if !under {
		return "", false
	}
	version, _, _ := strings.Cut(rest, "/")
	if version == "" {
		return "", false
	}
	return filepath.Join(filepath.FromSlash(base), version), true
}

// THE BENCH'S TOOLCHAIN ON THE CHILD'S PATH (the mechanical sprint, 2026-10-02; the fleet
// tooling probe, 2026-10-04). Granting the sdk tree made the bench's toolchain EXECUTABLE
// inside the wall; nothing made it FINDABLE. A loop unit starts the member with the PATH
// fleet/loops.yml writes (~/.local/bin, /opt/homebrew/bin, /usr/local/bin, /usr/bin, /bin),
// the child inherits it, and the provisioning standard's own PATH entries live in
// ~/sdk/env.sh, which only an interactive shell of the bench user reads. So a card's bare
// `go` and `gofmt` were "command not found" on every bench whose Go is not on the unit's
// PATH: a worker on one bench reported it, and one on another downloaded a Go tarball into
// its job to get past it. Putting GOROOT/bin alone on the child's PATH closed that for Go and
// nothing else: the probe of 2026-10-04 found every up member's `~/sdk/bin` (go, dotnet,
// cargo, java, node, dart, elixir, bats) still off the child's PATH, and a card on a bench
// whose /usr/local/bin held a stale Go found that one, which go.mod refused.
//
// So the child's PATH carries, ahead of the member's own, the `bin` of every home root the
// wall EXECUTES and then the bench's GOROOT/bin: BenchPath. The directories are read off
// toolchainRoots, the one list the wall builds its argv from, so a tree the wall grants and a
// directory the child searches cannot drift apart -- a second literal is how the sdk came to
// be executable and unfindable at once. GOPATH/bin is searched for the Go (on a linux bench
// ~/go/bin/go is the standard's link into the sdk tree) and never put on the PATH: it holds
// every `go install` on the bench (the go/bin note above).

// goPathBin is GOPATH/bin, home-relative: searched for the bench's `go`, never a PATH entry.
const goPathBin = "go/bin"

// BenchToolBins is the `bin` of each home root the wall grants EXECUTE on, under home and in
// the list's order, then each system root's PathDirs (the windows list's installed
// toolchains), all skipped if absent: on a provisioned bench `~/sdk/bin`, the standard's
// links to every sdk tool. It reads the filesystem only and runs nothing.
func BenchToolBins(goos, home string) []string {
	return benchToolBinsIn(toolchainRoots[goos], home)
}

// benchToolBinsIn is BenchToolBins over one declaration, so a test can hand it system roots
// that exist under its own temporary directory.
func benchToolBinsIn(decl []ToolchainRoot, home string) []string {
	var out []string
	for _, r := range decl {
		if !r.Exec {
			continue
		}
		if r.Home() {
			if home == "" {
				continue
			}
			out = appendDir(out, filepath.Join(home, filepath.FromSlash(r.Name), "bin"))
			continue
		}
		for _, d := range r.PathDirs {
			out = appendDir(out, filepath.Clean(filepath.Join(filepath.FromSlash(r.Name), filepath.FromSlash(d))))
		}
	}
	return out
}

// appendDir appends dir when it is a directory on this machine.
func appendDir(out []string, dir string) []string {
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return append(out, dir)
	}
	return out
}

// BenchGoBin is the directory of the Go the bench's card environment runs: the first `go`
// in BenchToolBins, then GOPATH/bin under home, then pathEnv, resolved through its symlinks
// to the directory it REALLY lives in -- GOROOT/bin, holding `go` and `gofmt` and nothing
// else -- or "" when the bench has none. A `go` that resolves outside every root the wall
// executes is the standard's drift to report (tools/benchstandard, the wall check), not a
// root to widen here. It reads the filesystem only and runs nothing.
func BenchGoBin(goos, home, pathEnv string) string {
	dirs := BenchToolBins(goos, home)
	if home != "" {
		dirs = append(dirs, filepath.Join(home, filepath.FromSlash(goPathBin)))
	}
	dirs = append(dirs, filepath.SplitList(pathEnv)...)
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(d, name))
		if err != nil {
			continue
		}
		fi, err := os.Stat(real)
		if err != nil || !fi.Mode().IsRegular() || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0) {
			continue
		}
		return filepath.Dir(real)
	}
	return ""
}

// BenchPath is what native puts on a card's PATH ahead of the member's own, in order:
// BenchToolBins, then BenchGoBin when the bench has a Go (`gofmt` lives beside `go` in
// GOROOT/bin, and `~/sdk/bin` may link `go` alone). Empty names nothing and leaves the
// child's PATH as it was.
func BenchPath(goos, home, pathEnv string) []string {
	out := BenchToolBins(goos, home)
	if goBin := BenchGoBin(goos, home, pathEnv); goBin != "" {
		out = append(out, goBin)
	}
	return out
}

// ThisOS is the operating system whose list the wall is built from: this process's own,
// because the wall contains a card on THIS bench.
func ThisOS() string { return runtime.GOOS }

// DotnetSharedDir is the one directory outside a card's write root that the .NET runtime
// writes on Linux. Its named mutexes (NuGet's "NuGet-Migrations" on every restore of a fresh
// HOME) are files under /tmp/.dotnet/shm, a path the runtime builds from a fixed /tmp and
// never from TMPDIR, so no temp mapping moves it (measured on hetzner2, 2026-10-19: strace of
// `dotnet build` with TMPDIR set opens /tmp/.dotnet/shm/session<n>/NuGet-Migrations; inside
// the wall the open fails EACCES and the restore dies). It is a machine-wide rendezvous by
// design, mode 0777, and it is the only part of /tmp the wall grants.
const DotnetSharedDir = "/tmp/.dotnet"

// RuntimeSharedDirs is the fixed directories outside the job a toolchain's runtime must write
// on goos: DotnetSharedDir on linux, nothing elsewhere (darwin's runtime is not measured).
func RuntimeSharedDirs(goos string) []string {
	if goos == "linux" {
		return []string{DotnetSharedDir}
	}
	return nil
}

// EnsureRuntimeSharedDirs makes each of RuntimeSharedDirs(goos) that is absent, mode 0777 as
// the .NET runtime makes it itself, and returns the ones that are directories: the wall
// refuses a --write naming a path that is not there, so only those reach the argv. It runs
// outside the wall, before the launch.
func EnsureRuntimeSharedDirs(goos string) []string {
	var out []string
	for _, d := range RuntimeSharedDirs(goos) {
		if err := os.Mkdir(d, 0o777); err == nil {
			_ = os.Chmod(d, 0o777) // ignored: a umask-narrowed mode the runtime widens itself when it owns the directory
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

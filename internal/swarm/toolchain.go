package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
// under ONE OF THREE KINDS: `--read`, which carries EXECUTE on both bodies, for the tree
// whose `go` the card must run; `--read-noexec`, which does not, for the tree the card only
// reads; and `--write`, added 2026-09-20 for the ONE root that is not read-only, the .NET
// runtime's named-mutex directory, whose whole cost is written out at the row itself.
//
// THE SAME HURT HAS A DARWIN FACE, measured on the M2 Air 2026-09-18. A Mac bench's
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
// argv, and the standard's own check names the same roots -- `tools/bench-standard.sh`'s
// marked block for a linux bench, `internal/pulse`'s darwin check table for a Mac one --
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
// whatever lands in it (Johnny's security read of #1364).
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
	// `--read-noexec`, readable and NOT executable. It is ignored when Write is set, which
	// is its own kind and carries both.
	Exec bool
	// Write is THE THIRD KIND and the only one that is not read-only: `--write`. It exists
	// for exactly one measured need -- a .NET card cannot take a named mutex without it --
	// and it is the kind to reach for last, because a `--write` root carries READ AND
	// EXECUTE as well (policy.go: "--write carries read AND execute, so the no-exec grant
	// would buy nothing"), so there is no narrowing available in the KIND. THE ONLY
	// NARROWING IS THE PATH, which is why the one row that has it names the deepest
	// directory that was measured to work rather than the tree above it.
	//
	// AND IT IS SHARED. A read root is shared harmlessly: every card sees the same bytes
	// and none of them changes. A write root is shared by every card on the bench at once,
	// so what card A leaves there, card B's runtime opens. The row below says what that
	// costs and why no per-job alternative exists; docs/SPEC-SWARM.md carries the same
	// analysis for a reader who is not reading the code.
	Write bool
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
}

// Home says whether this root is found under a bench home rather than at an absolute path.
func (r ToolchainRoot) Home() bool { return !strings.HasPrefix(r.Name, "/") }

// Flag is the nova-sandbox flag this root's kind maps to, and it is the ONE place the
// mapping is written: the wall builds its argv from it and the class test reads it, so a
// kind cannot mean one thing in the declaration and another in the argv.
func (r ToolchainRoot) Flag() string {
	switch {
	case r.Write:
		return "--write"
	case r.Exec:
		return "--read"
	default:
		return "--read-noexec"
	}
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
// AND ONE ROOT ON BOTH OSES IS NOT READ-ONLY AT ALL: `/tmp/.dotnet`, the .NET runtime's
// named-mutex root, granted `--write` (Glenn, 2026-09-19: "I grant you permission to admin
// dotnet, and the readlonly roots. do that"). This is the third kind and the costly one, so
// here is the whole of what was measured, on hulk (linux) and batman (darwin), 2026-09-20.
//
// WHAT BREAKS WITHOUT IT. Every `dotnet build` and `dotnet test` dies in NuGet's restore,
// which takes a named mutex called `NuGet-Migrations` before it does anything:
//
//	NuGet.targets(198,5): error MSB4018: System.IO.IOException: The system cannot open the
//	device or file specified. : 'NuGet-Migrations'. One or more system calls failed:
//	open("/tmp/.dotnet/shm", 0x80000, 0x0) == -1; errno == EACCES        (hulk)
//	stat("/tmp/.dotnet", ...) == -1; errno == EPERM                      (batman)
//
// WHY IT CANNOT BE PER-JOB, which is the question this kind has to answer and the reason
// the row is not simply "dotnet needs /tmp":
//
//   - THE PATH IS HARD-CODED AND TMPDIR IS IGNORED. `TMPDIR` pointed at an existing,
//     writable, granted directory and the runtime still went to `/tmp/.dotnet/shm`. The
//     receipt is an strace of the production failure, not a reading of the source:
//     `stat("/tmp/.dotnet", {st_mode=S_IFDIR|0777, ...}) = 0` then
//     `openat(AT_FDCWD, "/tmp/.dotnet/shm", O_RDONLY|O_CLOEXEC) = -1 EACCES`, with
//     `TMPDIR` set to the job's own tmp throughout. `strings libcoreclr.so` names `/tmp`
//     as a bare literal and mentions `TMPDIR` in exactly one message, about the debugger
//     pipe -- the PAL's shared-memory root (`SHARED_MEMORY_TEMP_DIRECTORY_PATH`) is the
//     literal and takes no environment.
//   - NO DOTNET_ OR NUGET_ SETTING MOVES IT. The SDK's NuGet assemblies carry no
//     `NUGET_*` name that turns the migration mutex off (`strings` over
//     NuGet.Build.Tasks, NuGet.Common and NuGet.Configuration: nothing).
//   - THE WALL CANNOT REDIRECT IT EITHER. A per-job directory made to APPEAR at
//     /tmp/.dotnet would need a mount namespace, and neither body has one: linux is
//     Landlock over the caller's own namespace and darwin is a seatbelt profile. There is
//     no bind anywhere in internal/sandbox.
//   - AND READ IS NOT ENOUGH: `--read /tmp/.dotnet` fails the same way. The runtime
//     creates its session directory there. Measured.
//
// SO THE GRANT IS SHARED, AND HERE IS WHAT THAT COSTS, PLAINLY. Every card on a bench runs
// as the SAME bench user, so /tmp/.dotnet is one directory shared by all of them and the
// usual protection does not apply: the sticky bit stops one UNIX USER deleting another's
// entries, and between two cards there is only one user, so sticky buys nothing BETWEEN
// CARDS. It is still set (provisioning creates the directory 1777, measured tolerated by
// .NET 10 on both OSes and not rewritten by it), because it does stop every OTHER user on
// the machine. What card A can therefore do to card B: hold `NuGet-Migrations` and stall
// B's restore; pre-create a file under a name B's runtime will open; fill the directory.
// What it CANNOT do is reach anything else -- the grant is this one directory, the runtime
// keeps nothing of value in it (a run leaves it empty; the session directories are removed
// when the last user exits), and nothing under it is ever read as code by anything but the
// PAL's own mutex layer. A bench that wants more than that wants one UID per card, which is
// a different change and is not this one. tools/bench-standard.sh checks the directory's
// MODE AND OWNER so a wrong one is drift and not a surprise.
//
// THE PATH IS THE ONLY NARROWING AVAILABLE, so it was measured rather than assumed. On
// linux `--write /tmp/.dotnet/shm` alone is enough for `dotnet build` AND for `dotnet
// test`'s mutex; on darwin it is NOT -- the runtime also stats `/tmp/.dotnet/lockfiles`,
// a SIBLING of shm, and dies `errno == EPERM` when only shm is granted. The row is
// therefore the parent on both, because a per-OS split here would be narrower only for the
// workloads that happened to be measured: the runtime plainly creates siblings under its
// own root, and the linux row would be right today and wrong on the next one. `/tmp` itself
// is never granted.
//
// ONE ROOT IS DELIBERATELY NOT HERE UNDER EITHER KIND, ON EITHER OS (Johnny's security read
// of #1364):
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
//
// The same rule is why no bin directory appears on the darwin side either: every grant there
// is on a toolchain TREE (`/opt/homebrew/Cellar/go/<ver>`) and never on `/opt/homebrew/bin`,
// which is a writable directory holding a launcher for every formula on the machine.
var toolchainRoots = map[string][]ToolchainRoot{
	"linux": {
		{Name: "sdk", Exec: true},
		{Name: "go/pkg/mod", Exec: false},
		{Name: "/tmp/.dotnet", Write: true},
	},
	"darwin": {
		{Name: "sdk", Exec: true, Optional: true},
		{Name: "go/pkg/mod", Exec: false, Optional: true},
		{Name: "/opt/homebrew/Cellar/go", Exec: true, Tool: "go", Optional: true},
		{Name: "/opt/homebrew/Cellar/sbcl", Exec: true, Tool: "sbcl", Optional: true},
		{Name: "/opt/homebrew/opt/openjdk", Exec: true, Optional: true},
		{Name: "/Library/Java/JavaVirtualMachines", Exec: true, Optional: true},
		{Name: "/usr/local/share/dotnet", Exec: true, Optional: true},
		{Name: "/tmp/.dotnet", Write: true, Optional: true},
	},
}

// THE ONE DARWIN GRANT THAT TURNED OUT NOT TO BE A GRANT (measured on batman and superman,
// 2026-09-20). `cc` and `rustc` could not LINK inside the wall on batman:
//
//	xcode-select: error: unable to read data link at '/var/db/xcode_select_link',
//	expected symbolic link (Operation not permitted)
//	warning: failed running `"xcrun" "--sdk" "macosx" "--show-sdk-path"`   (rustc)
//
// The obvious reading is two missing read roots -- /Library/Developer/CommandLineTools and
// /var/db -- and BOTH are wrong:
//
//   - /Library/Developer/CommandLineTools IS ALREADY READABLE inside the wall, with no
//     grant at all: `/Library` is one of profiles/darwin.sb.tmpl's own fixed subpaths
//     (internal/sandbox.fixedDarwinPrefixes). Measured: a card with no extra root at all
//     lists CommandLineTools/usr/bin/ld happily. A row for it would grant NOTHING and
//     would read to the next person as if it were load-bearing. `--read
//     /Library/Developer/CommandLineTools` on its own leaves `cc` RED.
//   - /var/db WOULD work, and it is the whole of /var/db -- the local user database,
//     sudo's state, every other system store -- to buy the readlink() of ONE symlink.
//     The narrower spelling is not available either: `--read /var/db/xcode_select_link` is
//     rule 5 running os.Stat, which FOLLOWS the link, so that flag silently grants
//     CommandLineTools again and `cc` stays RED. Measured, and worth knowing: a caller who
//     names a symlink to a directory gets the target, not the link.
//
// So the answer is NO NEW ROOT. The only thing denied is xcode-select's readlink, and
// xcode-select is asked only when nothing has said where the developer directory is.
// DEVELOPER_DIR says it, the bench's own `xcode-select -p` is where its value comes from
// (read OUTSIDE the wall, where reading the link is allowed, exactly as toolchainVersionDir
// reads a brew version off a launcher), and with it set `cc`, `c++` and `rustc` all build
// AND link and run inside the wall on batman and on superman. Zero paths granted.
const developerDirEnv = "DEVELOPER_DIR"

// ToolchainEnv is the toolchain half of a card's environment that the ROOTS cannot express:
// names whose value is a fact about this machine, read outside the wall and handed in, so
// that a tool inside does not have to go and read a path the wall denies. It is `NAME=value`
// entries, in the order the child should see them, and an OS with nothing to say says
// nothing at all.
//
// Today it is one name, and the comment above is why. It belongs HERE rather than in the
// runner for the reason the file's header gives: the wall's knowledge of the bench's
// toolchain is ONE PLACE, and "which developer directory" is the same class of fact as
// "which Cellar version" -- the machine's, not ours, and stale the moment it is hard-coded.
func ToolchainEnv(goos string) []string {
	if goos != "darwin" {
		return nil
	}
	// The bench's own answer, and no default: a Mac that will not say is a Mac where
	// xcode-select's own search is the best answer there is (superman has no
	// /var/db/xcode_select_link at all and `cc` links inside the wall without help). An
	// inherited DEVELOPER_DIR is not re-read -- a caller who set one meant it.
	if v, ok := os.LookupEnv(developerDirEnv); ok && strings.TrimSpace(v) != "" {
		return []string{developerDirEnv + "=" + v}
	}
	out, err := exec.Command("xcode-select", "-p").Output()
	if err != nil {
		return nil
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	return []string{developerDirEnv + "=" + dir}
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
	out := make([]string, 0, len(toolchainRoots))
	for goos := range toolchainRoots {
		out = append(out, goos)
	}
	sortNames(out)
	return out
}

// ToolchainRoots is the wall's side: this machine's list, absolute, carrying each root's
// kind, and SKIPPED IF ABSENT. Absent is the machine's shape and not the caller's mistake --
// a Mac bench has no `~/sdk` and may have no dotnet -- while rule 5 of the wall REFUSES a
// `--read`, a `--read-noexec` or a `--write` naming a path that is not there, so a root that
// does not exist must never reach the argv. An empty home names no HOME-relative root,
// because a relative root is a refusal and a root at the filesystem's top is not a
// toolchain; a system root needs no home and is still named.
//
// SKIP-IF-ABSENT IS ALSO WHY NOTHING HERE CREATES A DIRECTORY, and it matters most for the
// one `--write` root. /tmp/.dotnet is in /tmp, which is cleared on a reboot, so it is
// PROVISIONED (tools/bench-standard.sh drifts on it, and the provisioning notes create it
// 1777) and never made here: a runner that created a missing /tmp/.dotnet would be racing
// whoever else can write to /tmp for the name, and would then grant `--write` on whatever
// that name resolved to. A bench that has not provisioned it gets no grant and a cs card
// that dies with the mutex error above, which is a drift line somebody reads -- not a
// silent grant on a path a stranger chose.
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
		v, ok := toolchainVersionDir(r.Name, r.Tool)
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
func toolchainVersionDir(prefix, tool string) (string, bool) {
	launcher, err := exec.LookPath(tool)
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

// ThisOS is the operating system whose list the wall is built from: this process's own,
// because the wall contains a card on THIS bench.
func ThisOS() string { return runtime.GOOS }

// sortNames is sort.Strings over a handful of names, kept here so the one list's file has
// no import it needs for nothing else.
func sortNames(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

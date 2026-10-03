package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/nogh"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE CARD'S SHELL NEVER SEES A SECRET (issue #1814).
//
// The harness needs the provider key: `opencode.json` declares the provider as
// `{env:DEEPSEEK_API_KEY}` and the harness reads it from its own environment to make the
// API call. The MODEL does not need it, and until this file the model had it: opencode
// spawns its bash tool with its own environment, so a card whose STEP ran a shell could
// print the key into its tool output, its RESULT.md, the job's harness-output.log and --
// through harvest's openPR, which copies RESULT.md into a PR body -- onto the forge.
// Measured inside the real wall on a bench, 2026-09-19: a card printing
// only `${#DEEPSEEK_API_KEY}` and a name count got back `envlen=35 envnames=1`.
//
// THE SEAM, MEASURED. The same probe reported `shpath=/usr/bin/sh`,
// `bashpath=/usr/bin/bash`, `path1=<the first PATH entry the child was handed>` and
// `ppidcomm=opencode`: the tool's shell is a direct child of the harness, resolved by
// name, and the harness's own config schema calls its `shell` field "Default shell to use
// for terminal and bash tool", resolving it from `$SHELL` when the field is absent. So the
// dispatcher does not need to reach inside the harness: it owns both names the harness
// resolves through. This file writes a `bash` and an `sh` wrapper into <slot>/shim, and
// nativeChildEnv puts that directory FIRST on the child's PATH and pins SHELL at the
// wrapper. Each wrapper unsets every environment name carrying KEY, TOKEN or SECRET --
// keepNativeSecretName, the one predicate the argv log already redacts by -- and then
// execs the real shell. The harness process keeps the key for its API calls; every shell
// under it does not.
//
// WHERE IT LIVES IS THE POINT. <slot>/shim is inside the wall's READ set and outside its
// write set (nativeSandboxArgv: --read <slot>, --write <job>/<data>/<tmp>), so the card
// can execute the wrapper and cannot replace it. A wrapper in the data home or the job
// directory would be the card's to rewrite.
//
// WHAT IT IS NOT. It is not the whole fix. A card that calls `/usr/bin/bash` by its
// absolute path skips the wrapper, and a harness that spawns a shell some third way skips
// it too. The layer that closes those is handing the key to the harness by file descriptor
// instead of by environment, which is a design question in issue #1814 layer 2,
// not this file. This is the cheap layer that closes the channel a card actually has, and
// the harvest's key-shape scan (internal/keyshape) is the backstop behind it.
//
// POSIX ONLY. The wrapper is a /bin/sh script, so it is written on unix benches only. On
// windows no shim is written, nothing is prepended to PATH, and the child's environment is
// exactly what it was; the gap is named in docs/SPEC-SWARM.md.

//
// NOTHING ON PATH TO WRAP IS NOT A REFUSAL. The child's PATH is the one the harness
// resolves its shell through, so a PATH carrying no shell is a child that reaches no shell
// by name either: there is nothing to wrap and nothing to scrub through. (A unit test of
// the argv builder runs with an empty PATH and must still run.) A shell reached by an
// ABSOLUTE path is the acknowledged gap above, and the harvest scan is what stands behind
// it.

// shellShimDirName is the directory under the slot that holds the wrappers.
const shellShimDirName = "shim"

// nativeShellShimNames are the shell names a harness resolves on PATH. Both are wrapped:
// `bash` because it is what the harness prefers, `sh` because it is what a card's own
// command line reaches for.
var nativeShellShimNames = []string{"bash", "sh"}

// nativeShellShimDir is the wrapper directory for one slot: <slot>/shim.
func nativeShellShimDir(slotDir string) string {
	return filepath.Join(slotDir, shellShimDirName)
}

// shellShimBody is the wrapper's whole text bar its last line. It reads the NAMES of the
// environment through awk and unsets each one that carries a secret; no value is read,
// printed or copied, and awk prints names only. A bench with no awk cannot list the names,
// so the wrapper FAILS CLOSED -- exit 127, and it says why -- rather than handing the model
// a shell that still carries the provider key.
const shellShimBody = `#!/bin/sh
# nova-swarm shell shim (nova-tools #1814): the harness keeps the provider key for its
# API calls; the shell it hands the model does not. Every environment NAME carrying KEY,
# TOKEN or SECRET is unset here before the real shell is exec'd. No value is ever read,
# printed or copied: awk prints names, never values.
if ! command -v awk >/dev/null 2>&1; then
	echo 'nova-swarm shell shim: awk is on no PATH entry, so the environment cannot be listed by name; refusing to start a shell that would still carry the provider key' >&2
	exit 127
fi
for __nova_secret_name in $(env 2>/dev/null | awk -F= '/^[A-Za-z_][A-Za-z0-9_]*=/{n=$1;u=toupper(n);if(index(u,"KEY")||index(u,"TOKEN")||index(u,"SECRET"))print n}'); do
	unset "$__nova_secret_name" 2>/dev/null
done
unset __nova_secret_name
`

// shellShimScript is the wrapper for one real shell. It is built by concatenation rather
// than by a formatter: the real shell's path goes into the script EXACTLY as the
// filesystem spells it, and a path an escaper had rewritten would name nothing.
func shellShimScript(real string) string {
	return shellShimBody + "exec '" + real + "' \"$@\"\n"
}

// writeNativeShellShims writes one wrapper per shell name the bench actually has into
// <slot>/shim and returns the directory and the wrapper SHELL should name. An error is a
// refusal for the caller: a native run whose shell still carries the key is the defect
// this closes, not a degraded mode (SPEC-SANDBOX rule 1's shape -- never silently
// degraded).
//
// It returns "", "", nil -- no shim, no refusal -- on windows, and when the child's PATH
// carries no shell to wrap at all.
//
// Each wrapper is written to a unique temporary name and renamed into place, so two runs
// sharing one slot never read a half-written file.
func writeNativeShellShims(slotDir string) (dir, shell string, err error) {
	if runtime.GOOS == "windows" {
		return "", "", nil
	}
	var found []string
	for _, name := range nativeShellShimNames {
		real, lookErr := exec.LookPath(name)
		if lookErr != nil {
			continue
		}
		if abs, absErr := filepath.Abs(real); absErr == nil {
			real = abs
		}
		found = append(found, name, real)
	}
	if len(found) == 0 {
		return "", "", nil
	}
	dir = nativeShellShimDir(slotDir)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", "", fmt.Errorf("the shell shim directory %s could not be made: %w", oneline.Field(dir), mkErr)
	}
	written := map[string]string{}
	for i := 0; i < len(found); i += 2 {
		name, real := found[i], found[i+1]
		if within(dir, real) {
			return "", "", fmt.Errorf("the real %s resolved inside the shim directory %s, which would make the wrapper exec itself",
				oneline.Field(name), oneline.Field(dir))
		}
		if strings.ContainsAny(real, "'\n") {
			return "", "", fmt.Errorf("the path of %s holds a quote or a newline, which no wrapper can spell safely", oneline.Field(name))
		}
		path := filepath.Join(dir, name)
		if writeErr := atomicfile.Write(path, []byte(shellShimScript(real)), 0o755, atomicfile.ExactMode()); writeErr != nil {
			return "", "", fmt.Errorf("the shell shim %s could not be written: %w", oneline.Field(path), writeErr)
		}
		written[name] = path
	}
	// The refusing gh sits beside the shells (nova-tools #3600): a card's shell that
	// reaches for the GitHub CLI by name gets exit 2 and #3594, never a GitHub call.
	if _, ghErr := nogh.Install(dir); ghErr != nil {
		return "", "", ghErr
	}
	shell = written["bash"]
	if shell == "" {
		shell = written["sh"]
	}
	return dir, shell, nil
}

// pathWithDirFirst is one environment with shimDir prepended to its PATH -- the child
// resolves `bash` and `sh` through the wrapper before it reaches the bench's own, and
// `go` and `gofmt` through the bench's GOROOT/bin (nativeChildEnv). An environment
// carrying no PATH gains one naming the directory alone, because a child that resolves no
// shell at all is better than one that resolves an unscrubbed shell.
func pathWithDirFirst(env []string, shimDir string) []string {
	if shimDir == "" {
		return env
	}
	sep := string(os.PathListSeparator)
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if name != "PATH" {
			out = append(out, kv)
			continue
		}
		found = true
		if val == "" {
			out = append(out, "PATH="+shimDir)
			continue
		}
		out = append(out, "PATH="+shimDir+sep+val)
	}
	if !found {
		out = append(out, "PATH="+shimDir)
	}
	return out
}

// THE GO SHIM: A CARD'S BUILD HITS THE MACHINE'S WARM CACHE (nova-tools#5174, cost rule 5).
//
// Every card's GOCACHE is the machine's one shared build cache (nativeChildEnv), but Go puts
// a package's absolute directory into its cache key unless the build is -trimpath
// (the go command's own buildActionID: `fmt.Fprintf(h, "dir %s\n", p.Dir)`), and
// every launch stages its checkout at a path of its own. So the standard library and the
// modules came from the warm cache and the repository's own packages, the gate's, were
// compiled again by every card. Measured on a 16-thread Linux bench, 2026-10-02, `go build ./... && go vet
// ./...` of nova-tools in a fresh slot: 15.6-16.4 s with a cold cache of its own, 6.6 s
// from the shared cache, 1.9-2.0 s from the shared cache with -trimpath (a rebuild in
// place: 1.8 s).
//
// The flag cannot ride the environment alone: the cards tell the model to `export
// GOFLAGS=-mod=readonly ...`, which replaces whatever GOFLAGS the child was handed. So a
// `go` wrapper sits in <slot>/shim beside the shell wrappers, first on the child's PATH, and
// adds -trimpath to GOFLAGS before it execs the bench's go; a GOFLAGS that already names
// -trimpath (either way) is left as it is. It changes no other flag and no other name.
// Written only with the shared caches on, and only on POSIX benches, like the shells'.

// nativeGoShimName is the wrapper's name in <slot>/shim.
const nativeGoShimName = "go"

// goShimScript is the go wrapper for one real go, spelt exactly as the filesystem does.
func goShimScript(real string) string {
	return `#!/bin/sh
# nova-swarm go shim (nova-tools#5174, cost rule 5): every go command builds -trimpath, so
# the machine's shared GOCACHE serves this checkout; without it the checkout's absolute
# path is in each of its packages' cache keys and every card compiles the repository again.
case " ${GOFLAGS-} " in
*" -trimpath"* | *" --trimpath"*) ;;
*) GOFLAGS="${GOFLAGS:+$GOFLAGS }-trimpath"; export GOFLAGS ;;
esac
exec '` + real + `' "$@"
`
}

// writeNativeGoShim writes the go wrapper into dir (<slot>/shim) for the go in goBin. It
// writes nothing on windows, with no shim directory, or with no bench go to wrap.
func writeNativeGoShim(dir, goBin string) error {
	if runtime.GOOS == "windows" || dir == "" || goBin == "" {
		return nil
	}
	real := filepath.Join(goBin, nativeGoShimName)
	if within(dir, real) {
		return fmt.Errorf("the bench's go resolved inside the shim directory %s, which would make the wrapper exec itself", oneline.Field(dir))
	}
	if strings.ContainsAny(real, "'\n") {
		return fmt.Errorf("the path of the bench's go holds a quote or a newline, which no wrapper can spell safely")
	}
	path := filepath.Join(dir, nativeGoShimName)
	if err := atomicfile.Write(path, []byte(goShimScript(real)), 0o755, atomicfile.ExactMode()); err != nil {
		return fmt.Errorf("the go shim %s could not be written: %w", oneline.Field(path), err)
	}
	return nil
}

// THE DARWIN WALL FORBIDS setpriority (its `system-sched` operation; the template is
// `(deny default)` and grants none), so a card's `nice -n 19 <gate>` printed `nice:
// setpriority: Operation not permitted` into every worker's output and ran the gate at the
// priority it started with. Where nativeNicesChild says so, native lowers the child's whole
// group itself, outside the wall (lowerChildPriority), and writes this `nice` beside the
// shell wrappers: it runs the command as nice would, without asking the wall for the
// priority the group already has. Linux's wall leaves setpriority alone and gets neither.

// nativeNicesChild says whether native lowers the child's priority itself: the darwin wall.
func nativeNicesChild(goos string, walled bool) bool {
	return goos == "darwin" && walled
}

// childNice is the priority a walled child runs at where the wall forbids it to lower its
// own: the `nice -n 19` every card's gate line asks for.
const childNice = 19

// niceShimScript is the `nice` for a child already at nice 19: the adjustment (`-n N`,
// `-nN`, `-N`, `--adjustment=N`, `--adjustment N`) and a `--` are read and dropped, and the
// command is exec'd; with no command it prints the group's niceness, as nice does.
const niceShimScript = `#!/bin/sh
# nova-swarm nice shim: the darwin wall forbids setpriority, and native already started
# this card's whole process group at nice 19 outside it, so the command runs as it is.
case "$1" in
-n|--adjustment) shift; shift ;;
-n*|--adjustment=*|-[0-9]*) shift ;;
esac
if [ "$1" = "--" ]; then shift; fi
if [ $# -eq 0 ]; then echo 19; exit 0; fi
exec "$@"
`

// writeNativeNiceShim writes the `nice` into the shim directory dir, which is first on
// the child's PATH and outside its write set.
func writeNativeNiceShim(dir string) error {
	path := filepath.Join(dir, "nice")
	if err := atomicfile.Write(path, []byte(niceShimScript), 0o755, atomicfile.ExactMode()); err != nil {
		return fmt.Errorf("the nice shim %s could not be written: %w", oneline.Field(path), err)
	}
	return nil
}

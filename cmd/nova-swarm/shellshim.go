package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// THE CARD'S SHELL NEVER SEES A SECRET (issue #1814).
//
// The harness needs the provider key: `opencode.json` declares the provider as
// `{env:DEEPSEEK_API_KEY}` and the harness reads it from its own environment to make the
// API call. The MODEL does not need it, and until this file the model had it: opencode
// spawns its bash tool with its own environment, so a card whose STEP ran a shell could
// print the key into its tool output, its RESULT.md, the job's harness-output.log and --
// through harvest's openPR, which copies RESULT.md into a PR body -- onto the forge.
// Measured inside the real wall on space, 2026-09-19, seat swarm-space: a card printing
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
// instead of by environment, which is a design question for Stella (issue #1814 layer 2),
// not this file. This is the cheap layer that closes the channel a card actually has, and
// the harvest's key-shape scan (internal/keyshape) is the backstop behind it.
//
// POSIX ONLY. The wrapper is a /bin/sh script, so it is written on unix benches only. On
// windows no shim is written, nothing is prepended to PATH, and the child's environment is
// exactly what it was; the gap is named in docs/SPEC-SWARM.md.

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

// shellShimScript is the wrapper's whole text. It reads the NAMES of the environment
// through awk and unsets each one that carries a secret; no value is read, printed or
// copied, and awk prints names only. A bench with no awk is refused rather than run with
// the key still in the shell: the shim fails closed, exit 127, and says why.
func shellShimScript(real string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# nova-swarm shell shim (nova-tools #1814): the harness keeps the provider key for its\n")
	b.WriteString("# API calls; the shell it hands the model does not. Every environment NAME carrying KEY,\n")
	b.WriteString("# TOKEN or SECRET is unset here before the real shell is exec'd. No value is ever read,\n")
	b.WriteString("# printed or copied: awk prints names, never values.\n")
	b.WriteString("if ! command -v awk >/dev/null 2>&1; then\n")
	b.WriteString("\techo 'nova-swarm shell shim: awk is on no PATH entry, so the secret names cannot be listed; refusing to start a shell that would still carry the provider key' >&2\n")
	b.WriteString("\texit 127\n")
	b.WriteString("fi\n")
	b.WriteString("for __nova_secret_name in $(env 2>/dev/null | awk -F= '/^[A-Za-z_][A-Za-z0-9_]*=/{n=$1;u=toupper(n);if(index(u,\"KEY\")||index(u,\"TOKEN\")||index(u,\"SECRET\"))print n}'); do\n")
	b.WriteString("\tunset \"$__nova_secret_name\" 2>/dev/null\n")
	b.WriteString("done\n")
	b.WriteString("unset __nova_secret_name\n")
	b.WriteString("exec '" + real + "' \"$@\"\n")
	return b.String()
}

// writeNativeShellShims writes one wrapper per shell name the bench actually has into
// <slot>/shim and returns the directory and the wrapper SHELL should name. It returns a
// reason when the shims cannot be written, and the caller refuses the run: a native run
// whose shell still carries the key is the defect this closes, not a degraded mode
// (SPEC-SANDBOX rule 1's shape -- never silently degraded).
//
// On windows it returns "", "", "" -- no shim, no refusal, no change.
//
// Each wrapper is written to a unique temporary name and renamed into place, so two runs
// sharing one slot never read a half-written file.
func writeNativeShellShims(slotDir string) (dir, shell, reason string) {
	if runtime.GOOS == "windows" {
		return "", "", ""
	}
	dir = nativeShellShimDir(slotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Sprintf("the shell shim directory %s could not be made: %s", dir, err.Error())
	}
	if _, err := exec.LookPath("awk"); err != nil {
		return "", "", fmt.Sprintf("awk is on no PATH entry, so the shell shim cannot list the environment's secret names: %s", err.Error())
	}
	written := map[string]string{}
	for _, name := range nativeShellShimNames {
		real, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if abs, err := filepath.Abs(real); err == nil {
			real = abs
		}
		if within(dir, real) {
			return "", "", fmt.Sprintf("the real %s resolved inside the shim directory %s, which would make the wrapper exec itself", name, dir)
		}
		if strings.ContainsAny(real, "'\n") {
			return "", "", fmt.Sprintf("the path of %s holds a quote or a newline, which no wrapper can spell safely", name)
		}
		path := filepath.Join(dir, name)
		tmp := path + ".tmp" + fmt.Sprint(os.Getpid())
		if err := os.WriteFile(tmp, []byte(shellShimScript(real)), 0o755); err != nil {
			return "", "", fmt.Sprintf("the shell shim %s could not be written: %s", path, err.Error())
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return "", "", fmt.Sprintf("the shell shim %s could not be put in place: %s", path, err.Error())
		}
		written[name] = path
	}
	if len(written) == 0 {
		return "", "", fmt.Sprintf("no shell was found on PATH to wrap (%s), so the card's shell cannot be scrubbed of the provider key", strings.Join(nativeShellShimNames, ", "))
	}
	shell = written["bash"]
	if shell == "" {
		shell = written["sh"]
	}
	return dir, shell, ""
}

// pathWithShimFirst is one environment with shimDir prepended to its PATH -- the child
// resolves `bash` and `sh` through the wrapper before it reaches the bench's own. An
// environment carrying no PATH gains one naming the shim alone, because a child that
// resolves no shell at all is better than one that resolves an unscrubbed shell.
func pathWithShimFirst(env []string, shimDir string) []string {
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

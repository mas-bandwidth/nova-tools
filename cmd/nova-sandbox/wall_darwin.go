//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// The wall on macOS is a sandbox-exec profile filled for this one run and passed inline
// with -p, so no profile file exists to be replaced or left behind. The child execs
// sandbox-exec in place; the parent waits so a killed command can be reported.
const backendName = "sandbox-exec"

// sandboxExec is the system sandbox-exec by its absolute path only: a PATH lookup would
// let any directory earlier on PATH supply a binary that applies no wall.
func sandboxExec() (string, bool) {
	fi, err := os.Stat("/usr/bin/sandbox-exec")
	return "/usr/bin/sandbox-exec", err == nil && fi.Mode().Perm()&0o111 != 0
}

// wallCheck says whether the wall can be built here. It applies a trivial profile, which
// fails when this process is already inside a sandbox that forbids nesting.
func wallCheck() (abi string, netOK bool, note string, err error) {
	bin, ok := sandboxExec()
	if !ok {
		return "-", false, "", fmt.Errorf("sandbox-exec is not at /usr/bin/sandbox-exec")
	}
	if out, err := exec.Command(bin, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		return "-", false, "", fmt.Errorf("sandbox-exec cannot apply a profile here: %v: %s", err, out)
	}
	return "-", true, "the wall is the profile it applies (" + bin + ")", nil
}

// execWall applies the wall and execs the command. It returns only on failure.
func execWall(w *wall, env []string) []refusal {
	bin, text, params, bad := darwinPlan(w)
	if len(bad) > 0 {
		return bad
	}
	argv := []string{"sandbox-exec", "-p", text}
	for _, kv := range params {
		argv = append(argv, "-D", kv)
	}
	argv = append(append(argv, "--"), w.argv...)
	if err := closeInheritedFDs(fdDir()); err != nil {
		return []refusal{refuse("sandbox_failed", "inherited descriptors could not be closed: %v", err)}
	}
	err := syscall.Exec(bin, argv, env)
	return []refusal{refuse("sandbox_failed", "%s could not be started: %v", bin, err)}
}

// darwinPlan is the sandbox-exec binary and the profile execWall applies.
func darwinPlan(w *wall) (bin, text string, params []string, bad []refusal) {
	bin, ok := sandboxExec()
	if !ok {
		return "", "", nil, []refusal{refuse("no_sandbox", "sandbox-exec is not available; this tool does not run a command it cannot contain")}
	}
	text, params, err := darwinProfile(w)
	if err != nil {
		return "", "", nil, []refusal{refuse("sandbox_failed", "the profile could not be generated: %v", err)}
	}
	return bin, text, params, nil
}

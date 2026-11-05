//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// The wall on macOS is a sandbox-exec profile filled for this one run and passed inline
// with -p, so no profile file exists to be replaced or left behind. sandbox-exec is
// exec'd in place and execs the command in place, so no tool process stays behind and
// the command's exit status is the tool's.
const backendName = "sandbox-exec"

func sandboxExec() (string, bool) {
	if p, err := exec.LookPath("sandbox-exec"); err == nil {
		return p, true
	}
	fi, err := os.Stat("/usr/bin/sandbox-exec")
	return "/usr/bin/sandbox-exec", err == nil && fi.Mode().Perm()&0o111 != 0
}

// wallCheck says whether the wall can be built here. It applies a trivial profile, which
// fails when this process is already inside a sandbox that forbids nesting.
func wallCheck() (abi string, netOK bool, note string, err error) {
	bin, ok := sandboxExec()
	if !ok {
		return "-", false, "", fmt.Errorf("sandbox-exec is on no PATH entry and is not at /usr/bin/sandbox-exec")
	}
	if out, err := exec.Command(bin, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		return "-", false, "", fmt.Errorf("sandbox-exec cannot apply a profile here: %v: %s", err, out)
	}
	return "-", true, "sandbox-exec is deprecated by Apple; the wall is the profile it applies (" + bin + ")", nil
}

// execWall applies the wall and execs the command. It returns only on failure.
func execWall(w *wall, env []string) *refusal {
	bin, ok := sandboxExec()
	if !ok {
		r := refuse("no_sandbox", "sandbox-exec is not available; this tool does not run a command it cannot contain")
		return &r
	}
	text, params, err := darwinProfile(w)
	if err != nil {
		r := refuse("sandbox_failed", "the profile could not be generated: %v", err)
		return &r
	}
	argv := []string{"sandbox-exec", "-p", text}
	for _, kv := range params {
		argv = append(argv, "-D", kv)
	}
	argv = append(append(argv, "--"), w.argv...)
	if !w.keepFDs {
		closeInheritedFDs()
	}
	err = syscall.Exec(bin, argv, env)
	r := refuse("sandbox_failed", "%s could not be started: %v", bin, err)
	return &r
}

//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// msvcBudget bounds the two programs benchMSVCEnv runs: vswhere answers in well under a
// second and vcvars64.bat in a few on a weak box. msvcWaitDelay is how long a killed one may
// hold its output pipe before the wait gives up on it.
const (
	msvcBudget    = 60 * time.Second
	msvcWaitDelay = 5 * time.Second
)

// benchMSVCEnv is the MSVC developer environment of this machine (pkg/swarm, msvc.go): vswhere
// at its fixed location names the newest install with the x64 C++ tools, and cmd.exe runs that
// install's vcvars64.bat and prints `set`, read against basePath. A machine with no vswhere has
// no Visual Studio and adds nothing, silently; one where a step fails adds nothing and says
// why, and the launch goes on (a CMake Visual Studio generator still finds MSVC itself).
func benchMSVCEnv(basePath string) (swarm.MSVCEnv, string) {
	pf := os.Getenv("ProgramFiles(x86)")
	if pf == "" {
		pf = `C:\Program Files (x86)`
	}
	vswhere := filepath.Join(pf, "Microsoft Visual Studio", "Installer", "vswhere.exe")
	if _, err := os.Stat(vswhere); err != nil {
		return swarm.MSVCEnv{}, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), msvcBudget)
	defer cancel()
	q := exec.CommandContext(ctx, vswhere, swarm.MSVCVswhereArgs...)
	q.WaitDelay = msvcWaitDelay
	out, err := q.Output()
	if err != nil {
		return swarm.MSVCEnv{}, oneline.Field(vswhere) + ": " + oneline.Err(err)
	}
	install, ok := swarm.MSVCInstall(out)
	if !ok {
		return swarm.MSVCEnv{}, "vswhere names no Visual Studio install with the x64 C++ tools"
	}
	vcvars := swarm.MSVCVcvars(install)
	if _, err := os.Stat(vcvars); err != nil {
		return swarm.MSVCEnv{}, oneline.Field(vcvars) + ": " + oneline.Err(err)
	}
	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.CommandContext(ctx, cmdExe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: swarm.MSVCVcvarsCmdLine(cmdExe, vcvars), HideWindow: true}
	cmd.WaitDelay = msvcWaitDelay
	out, err = cmd.Output()
	if err != nil {
		return swarm.MSVCEnv{}, oneline.Field(vcvars) + ": " + oneline.Err(err)
	}
	env := swarm.MSVCEnvFrom(out, basePath)
	if len(env.PathDirs) == 0 {
		return swarm.MSVCEnv{}, oneline.Field(vcvars) + " added no PATH directory"
	}
	return env, ""
}

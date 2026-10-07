//go:build !windows && (functional || slow)

package update

import (
	"os"
	"os/exec"
	"syscall"
)

// The process-group helpers the functional and slow tiers' child-process tests use on
// unix (join_kill_windows_test.go and escape_windows_test.go are Windows's); built
// only with those tiers' tags, where they have callers.

// spawnEscapedHolder starts a "hold" grandchild in its own process group, so the
// version command's group kill (kill(-pgid)) cannot reach it. It inherits the
// caller's stdout, keeping that pipe's write end open past the caller's death.
func spawnEscapedHolder(d string, readyFile ...string) error {
	args := []string{"-test.run=TestHelperProcess", "--", "helper", "hold", d}
	if len(readyFile) > 0 && readyFile[0] != "" {
		args = append(args, readyFile[0])
	}
	c := exec.Command(os.Args[0], args...)
	c.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
	c.Stdout = os.Stdout
	setGroup(c)
	return c.Start()
}

// The wrapper kills the PROCESS GROUP, not just nova-bus: a Git child of the
// binary we killed would otherwise finish a push nobody is waiting on, and the
// death this test stages would not be the death it claims.
func setGroup(c *exec.Cmd)          { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func assignGroup(c *exec.Cmd) error { return nil }
func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}

//go:build unix

package friend

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// gateProcess keeps the session leader blocked before exec until its durable
// run receipt is written. A daemon crash closes the pipe without a byte, so
// the child exits without starting the harness.
func gateProcess(cmd *exec.Cmd, enabled bool) (func(bool) error, error) {
	if !enabled {
		return func(bool) error { return nil }, nil
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, reader)
	args := append([]string{cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/bin/sh"
	cmd.Args = append([]string{"sh", "-c", "IFS= read -r ready <&" + strconv.Itoa(fd) + " || exit 125; exec \"$@\"", "nova-friend-gate"}, args...)
	return func(permit bool) error {
		_ = reader.Close()
		if !permit {
			return writer.Close()
		}
		_, err := fmt.Fprintln(writer, "go")
		closeErr := writer.Close()
		if err != nil {
			return err
		}
		return closeErr
	}, nil
}

// ownGroup makes cmd a session leader of its own, so a Cancel signals the
// group (every process the harness forked) and the caller's WaitDelay then
// kills it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// ProcessAlive is whether the process pid is alive: a signal 0 the kernel delivers or
// refuses for want of permission (the process is there), never ESRCH.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// killGroup ends every process left in the group led by pid.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) } // ignored: a group already gone is the state wanted

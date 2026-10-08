//go:build unix

package friend

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// gateProcess keeps the group leader blocked before exec until its durable
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
		// ignored: the writer's close below decides whether the gate can open.
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

// ownGroup makes cmd a process-group leader in the daemon's session. The
// durable sidecar can then join its group before the harness gate opens.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

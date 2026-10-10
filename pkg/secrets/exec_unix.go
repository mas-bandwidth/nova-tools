//go:build !windows

package secrets

import (
	"fmt"
	"os/exec"
	"syscall"
)

func setRlimitCoreZero() error {
	var rlimit syscall.Rlimit
	rlimit.Cur = 0
	rlimit.Max = 0
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &rlimit)
}

func replaceProcess(argv []string, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command specified")
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(bin, argv, env)
}

//go:build windows

package secrets

import (
	"fmt"
	"os"
	"os/exec"
)

func setRlimitCoreZero() error {
	return nil
}

func replaceProcess(argv []string, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command specified")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code < 0 {
				code = 1
			}
			os.Exit(code)
		}
		return err
	}
	os.Exit(0)
	return nil
}

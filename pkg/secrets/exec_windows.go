//go:build windows

package secrets

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

func setRlimitCoreZero() error {
	return nil
}

func replaceProcess(argv []string, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command specified")
	}
	// A long-lived child: the command this process stands in for. A cancellable context
	// and no deadline; the context is released when the child has been waited for.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := subproc.Long(ctx, argv[0], argv[1:]...)
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

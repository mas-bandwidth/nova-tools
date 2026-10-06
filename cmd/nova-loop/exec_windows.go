//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

func realExec(argv []string) (bool, error) {
	if len(argv) == 0 {
		return false, errors.New("no command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return false, cmd.Run()
}

//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func realExec(argv []string) (bool, error) {
	if len(argv) == 0 {
		return true, errors.New("no command")
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return true, err
	}
	return true, syscall.Exec(bin, argv, os.Environ())
}

//go:build windows

package main

import (
	"context"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func nativeGroupCommand(ctx context.Context, path string, args ...string) (*exec.Cmd, func() error, func(), error) {
	return subproc.Long(ctx, path, args...), func() error { return nil }, func() {}, nil
}

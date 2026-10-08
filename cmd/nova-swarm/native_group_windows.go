//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
)

func nativeGroupCommand(ctx context.Context, path string, args ...string) (*exec.Cmd, func() error, func(), error) {
	return nil, nil, nil, fmt.Errorf("native process-group anchoring is unavailable on Windows")
}

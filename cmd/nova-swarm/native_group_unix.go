//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// nativeGroupCommand parks the process-group leader on an inherited pipe. The
// harness cannot exec until native has durably recorded the leader's identity.
func nativeGroupCommand(ctx context.Context, path string, args ...string) (*exec.Cmd, func() error, func(), error) {
	rd, wr, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	script := `IFS= read -r gate <&3 || exit 125; exec 3<&-; [ "$gate" = go ] || exit 125; exec "$@"`
	argv := append([]string{"-c", script, "sh", path}, args...)
	cmd := subproc.Long(ctx, "/bin/sh", argv...)
	cmd.ExtraFiles = []*os.File{rd}
	ownChildGroup(cmd)
	release := func() error {
		_ = rd.Close()
		_, writeErr := fmt.Fprintln(wr, "go")
		closeErr := wr.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	abort := func() { _ = rd.Close(); _ = wr.Close() }
	return cmd, release, abort, nil
}

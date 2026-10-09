package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

const landCheckOutputLimit = 4 << 20

// nativeCheck runs SPEC-SPRINT section 7's explicit tree check behind the
// existing native launch gate and verified group cleanup. Its private receipt
// remains available when cleanup cannot prove every same-group descendant exited.
func nativeCheck(ctx context.Context, dir string, env []string, check string) (out string, err error) {
	budget := landCheckBudget
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= budget {
		budget = 0
	}
	bound, cancel := subproc.BoundFor(ctx, landCheckBudget)
	defer cancel()
	receipts, err := os.MkdirTemp("", "nova-check-")
	if err != nil {
		return "", err
	}
	pid := 0
	defer func() {
		if procgroup.GroupRunnable(pid) {
			err = errors.Join(err, fmt.Errorf("native check group remains unverified; receipts kept at %s", receipts))
			return
		}
		err = errors.Join(err, safepath.RemoveUnder(filepath.Dir(receipts), receipts))
	}()
	path := filepath.Join(receipts, "RUN")
	bound = friend.WithProcessStarted(bound, func(started int) error {
		pid = started
		raw, err := json.Marshal(struct {
			PID      int    `json:"pid"`
			Identity string `json:"identity"`
		}{started, friend.ProcessIdentity(started)})
		if err != nil {
			return err
		}
		return atomicfile.WriteFile(path, raw, 0o600)
	})
	bound = friend.WithProcessGroupAnchor(bound, path+".anchor")
	if env != nil {
		bound = friend.WithExecEnvironment(bound, env)
	}
	output := bounded.NewCapture(landCheckOutputLimit+1, cancel)
	bound = friend.WithExecOutputLimit(bound, landCheckOutputLimit)
	bound = friend.WithOutputTail(bound, func(p []byte) {
		_, _ = output.Write(p) // ignored: bounded.Capture.Write cannot fail
	})
	_, exit, err := friend.RealExec(bound, dir, "sh", []string{"-c", check}, "")
	if err == nil && exit != 0 {
		err = fmt.Errorf("exit status %d", exit)
	}
	if err != nil && subproc.Expired(bound) {
		err = &subproc.TimeoutError{What: "check " + check, Budget: budget, Err: err}
	}
	if output.Hit() {
		err = errors.Join(err, fmt.Errorf("check output exceeds %d bytes", landCheckOutputLimit))
	}
	bytes := output.Bytes()
	return string(bytes[:min(len(bytes), landCheckOutputLimit)]), err
}

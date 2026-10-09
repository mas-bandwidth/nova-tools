package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
)

// CodexEnrollmentTransport owns the local SSH wrapper through the same birth-
// bound group cleanup as native setup; receipts contain no output (subscription spec).
func CodexEnrollmentTransport(ctx context.Context, terminal *os.File, args []string, timeout time.Duration) error {
	return codexEnrollmentTransport(ctx, terminal, "/usr/bin/ssh", args, timeout, "")
}

func codexEnrollmentTransport(ctx context.Context, terminal *os.File, program string, args []string, timeout time.Duration, receiptRoot string) error {
	ctx, err := CodexConsentContext(ctx, terminal)
	if err != nil {
		return err
	}
	if timeout <= 0 || timeout > 17*time.Minute {
		return errors.New("enrollment transport wants a bounded deadline up to seventeen minutes; inspect setup -h")
	}
	root := receiptRoot
	if root == "" {
		root, err = os.MkdirTemp("", "nova-codex-enrollment-")
		if err != nil {
			return errors.New("enrollment transport cannot create a private public receipt; retry after fixing temporary storage")
		}
	}
	receiptPath := filepath.Join(root, "RUN")
	anchorPath := receiptPath + ".anchor"
	pid := 0
	birth := ""
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := []string{"HOME=" + os.Getenv("HOME"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		env = append(env, "SSH_AUTH_SOCK="+socket)
	}
	ctx = context.WithValue(ctx, execEnvironmentKey{}, env)
	ctx = WithProcessGroupAnchor(ctx, anchorPath)
	ctx = WithProcessStarted(ctx, func(started int) error {
		pid, birth = started, ProcessIdentity(started)
		if pid <= 0 || birth == "" {
			return errors.New("enrollment transport birth identity unavailable")
		}
		raw, err := json.Marshal(struct {
			PID      int
			Identity string
		}{pid, birth})
		if err != nil {
			return err
		}
		return os.WriteFile(receiptPath, raw, 0600)
	})
	output, exit, runErr := RealExec(ctx, root, program, args, "")
	if pid > 0 && procgroup.GroupRunnable(pid) {
		return fmt.Errorf("enrollment transport group remains live or unverified; preserve public receipt %s and wait for verified exit before retrying", receiptPath)
	}
	// Only proven-quiescent wrapper metadata is removed; neither path holds native output.
	if err := os.RemoveAll(root); err != nil {
		return errors.New("enrollment transport cannot remove its quiescent public receipt; inspect temporary storage before retrying")
	}
	if runErr != nil || exit != 0 || output != "" || ctx.Err() != nil {
		return errors.New("remote enrollment did not complete; inspect the native terminal and target's owned setup receipt, then retry after verified native exit")
	}
	return nil
}

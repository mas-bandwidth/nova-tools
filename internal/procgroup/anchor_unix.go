//go:build !windows

// Package procgroup gives a restarted owner birth-verified authority over a
// process group whose original leader exited while descendants still run.
package procgroup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// StartAnchor starts a TERM-resistant sidecar in pgid behind an inherited
// pipe gate. Its PID and kernel birth are durably recorded before the gate is
// released; a crash before the receipt leaves a sidecar that cannot run.
func StartAnchor(pgid int, receiptPath string) error {
	if pgid <= 0 || receiptPath == "" {
		return fmt.Errorf("invalid process group anchor")
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		return err
	}
	ackRd, ackWr, err := os.Pipe()
	if err != nil {
		_ = rd.Close() // ignored: no child has inherited this pipe
		_ = wr.Close() // ignored: no child has inherited this pipe
		return err
	}
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", `IFS= read -r gate <&3 || exit 125; exec 3<&-; [ "$gate" = go ] || exit 125; trap '' TERM; printf ready >&4; exec 4>&-; while :; do sleep 60; done`)
	cmd.ExtraFiles = []*os.File{rd, ackWr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	if err := cmd.Start(); err != nil {
		_ = rd.Close()    // ignored: no child was started
		_ = wr.Close()    // ignored: no child was started
		_ = ackRd.Close() // ignored: no child was started
		_ = ackWr.Close() // ignored: no child was started
		return err
	}
	_ = ackWr.Close() // ignored: child owns the ready writer now
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	abort := func() {
		_ = rd.Close()         // ignored: abort sends no "go" word
		_ = wr.Close()         // ignored: abort sends no "go" word
		_ = ackRd.Close()      // ignored: abort no longer waits for readiness
		_ = cmd.Process.Kill() // ignored: gated sidecar cleanup
		_ = cmd.Wait()         // ignored: gated sidecar has no result
	}
	if stamp == "-" {
		abort()
		return fmt.Errorf("anchor %d has no kernel birth identity", pid)
	}
	if err := atomicfile.Write(receiptPath, []byte(strconv.Itoa(pid)+" "+stamp+"\n"), 0o600); err != nil {
		abort()
		return err
	}
	if err := rd.Close(); err != nil {
		abort()
		return err
	}
	if _, err := fmt.Fprintln(wr, "go"); err != nil {
		abort()
		return err
	}
	if err := wr.Close(); err != nil {
		abort()
		return err
	}
	// The harness gate is held by the caller until the sidecar has installed
	// its TERM handler. A hung or dead sidecar cannot authorize the harness.
	timer := time.AfterFunc(2*time.Second, func() {
		_ = ackRd.Close() // ignored: closing this pipe refuses a hung sidecar's readiness
	})
	var ready [5]byte
	_, err = io.ReadFull(ackRd, ready[:])
	timer.Stop()
	_ = ackRd.Close() // ignored: readiness was already read or refused
	if err != nil || string(ready[:]) != "ready" {
		abort()
		return fmt.Errorf("group anchor did not become ready: %v", err)
	}
	go func() { _ = cmd.Wait() }() // ignored: group exit proof is checked separately
	return nil
}

func anchorIdentity(receiptPath string) (int, string) {
	b, err := os.ReadFile(receiptPath)
	if err != nil {
		return 0, ""
	}
	f := strings.Fields(string(b))
	if len(f) != 2 || f[1] == "-" {
		return 0, ""
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return 0, ""
	}
	return pid, f[1]
}

// Pinned verifies either the still-live original leader or the recorded
// sidecar still belongs to this exact process group. A bare PGID is never
// enough authority to signal it after a restart.
func Pinned(pgid int, leaderBirth, anchorReceiptPath string) bool {
	if pgid <= 0 {
		return false
	}
	if leaderBirth != "" && leaderBirth != "-" && swarm.StartStamp(pgid) == leaderBirth && processRunnable(pgid) {
		return true
	}
	pid, birth := anchorIdentity(anchorReceiptPath)
	if pid == 0 || swarm.StartStamp(pid) != birth || !processRunnable(pid) {
		return false
	}
	group, err := syscall.Getpgid(pid)
	return err == nil && group == pgid
}

// AnchorPinned specifically verifies the sidecar. Callers may use it to
// distinguish an expired group leader from a surviving group pin.
func AnchorPinned(pgid int, anchorReceiptPath string) bool {
	return Pinned(pgid, "", anchorReceiptPath)
}

// ReapVerified escalates only while a birth-verified process pins the group.
// It returns true only after no runnable member remains; otherwise the owner
// must retain STOP debt. POSIX reserves a live group's PGID, but the pin is
// needed across the liveness-check-to-signal interval.
func ReapVerified(ctx context.Context, pgid int, leaderBirth, anchorReceiptPath string, grace time.Duration) bool {
	if grace <= 0 || !Pinned(pgid, leaderBirth, anchorReceiptPath) {
		return false
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM) // ignored: exit proof below decides success
	if waitGone(ctx, pgid, leaderBirth, anchorReceiptPath, grace, true) {
		return true
	}
	if !Pinned(pgid, leaderBirth, anchorReceiptPath) {
		return false
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL) // ignored: exit proof below decides success
	return waitGone(ctx, pgid, leaderBirth, anchorReceiptPath, grace, false)
}

// KillVerified immediately kills an identity-pinned group and waits for exit.
// A vanished pin refuses the signal, leaving the caller's debt unresolved.
func KillVerified(ctx context.Context, pgid int, leaderBirth, anchorReceiptPath string, timeout time.Duration) bool {
	if timeout <= 0 || !Pinned(pgid, leaderBirth, anchorReceiptPath) {
		return false
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL) // ignored: exit proof below decides success
	return waitGone(ctx, pgid, leaderBirth, anchorReceiptPath, timeout, false)
}

func waitGone(ctx context.Context, pgid int, leaderBirth, anchorReceiptPath string, grace time.Duration, requirePin bool) bool {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if !GroupRunnable(pgid) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return !GroupRunnable(pgid)
		case <-tick.C:
			if requirePin && !Pinned(pgid, leaderBirth, anchorReceiptPath) && GroupRunnable(pgid) {
				return false
			}
		}
	}
}

func processRunnable(pid int) bool {
	if pid <= 0 || processZombie(pid) {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

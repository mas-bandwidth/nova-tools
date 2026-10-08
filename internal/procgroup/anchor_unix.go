//go:build !windows

// Package procgroup gives a restarted owner birth-verified authority over a
// process group whose original leader exited while descendants still run.
package procgroup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// StartAnchor starts a TERM-resistant sidecar in pgid behind an inherited
// pipe gate. Its PID and kernel birth are durably recorded before the gate is
// released; a crash before the receipt leaves a sidecar that cannot run.
func StartAnchor(pgid int, receiptPath string) error {
	if pgid <= 0 || receiptPath == "" {
		return fmt.Errorf("invalid process group anchor")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	fifoName := filepath.Base(receiptPath) + ".fifo-" + hex.EncodeToString(nonce[:])
	fifoPath := filepath.Join(filepath.Dir(receiptPath), fifoName)
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		return err
	}
	var fifoStat syscall.Stat_t
	if err := syscall.Stat(fifoPath, &fifoStat); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(fifoPath) // ignored: a refused launch cannot retain a command channel
		}
	}()
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
	// This process is itself a member of the group it will signal. A command
	// received over the durable FIFO executes kill(0) while the sender still
	// pins the group; a numeric PGID is never signalled from outside it.
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", `IFS= read -r gate <&3 || exit 125; exec 3<&-; [ "$gate" = go ] || exit 125; trap '' TERM; printf ready >&4; exec 4>&-; while :; do IFS= read -r action < "$1" || continue; case "$action" in TERM) kill -TERM 0 ;; KILL) kill -KILL 0 ;; esac; done`, "sh", fifoPath)
	cmd.WaitDelay = subproc.WaitDelay
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
	receipt := fmt.Sprintf("%d %s %s %d %d\n", pid, stamp, fifoName, fifoStat.Dev, fifoStat.Ino)
	if err := atomicfile.Write(receiptPath, []byte(receipt), 0o600); err != nil {
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
	committed = true
	return nil
}

type anchorRecord struct {
	pid   int
	birth string
	fifo  string
	dev   uint64
	ino   uint64
}

func anchorIdentity(receiptPath string) anchorRecord {
	b, err := os.ReadFile(receiptPath)
	if err != nil {
		return anchorRecord{}
	}
	f := strings.Fields(string(b))
	if len(f) != 5 || f[1] == "-" || !strings.HasPrefix(f[2], filepath.Base(receiptPath)+".fifo-") || filepath.Base(f[2]) != f[2] {
		return anchorRecord{}
	}
	nonce := strings.TrimPrefix(f[2], filepath.Base(receiptPath)+".fifo-")
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 16 {
		return anchorRecord{}
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return anchorRecord{}
	}
	dev, err := strconv.ParseUint(f[3], 10, 64)
	if err != nil {
		return anchorRecord{}
	}
	ino, err := strconv.ParseUint(f[4], 10, 64)
	if err != nil {
		return anchorRecord{}
	}
	return anchorRecord{pid: pid, birth: f[1], fifo: filepath.Join(filepath.Dir(receiptPath), f[2]), dev: dev, ino: ino}
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
	record := anchorIdentity(anchorReceiptPath)
	if record.pid == 0 || swarm.StartStamp(record.pid) != record.birth || !processRunnable(record.pid) {
		return false
	}
	group, err := syscall.Getpgid(record.pid)
	return err == nil && group == pgid
}

// AnchorPinned specifically verifies the sidecar. Callers may use it to
// distinguish an expired group leader from a surviving group pin.
func AnchorPinned(pgid int, anchorReceiptPath string) bool {
	return Pinned(pgid, "", anchorReceiptPath)
}

// ReapVerified asks the birth-verified anchor to signal its own group. The
// anchor's live membership protects the PGID during its own kill(0) syscall;
// the owner never signals a possibly recycled numeric PGID. A lost anchor or
// command channel keeps STOP debt until group exit can be proved.
func ReapVerified(ctx context.Context, pgid int, leaderBirth, anchorReceiptPath string, grace time.Duration) bool {
	if grace <= 0 || !AnchorPinned(pgid, anchorReceiptPath) {
		return false
	}
	record := anchorIdentity(anchorReceiptPath)
	if !sendAnchorCommand(ctx, pgid, anchorReceiptPath, "TERM", grace) {
		return false
	}
	if waitGone(ctx, pgid, anchorReceiptPath, grace, true) {
		cleanupQuiescedChannel(record)
		return true
	}
	if !AnchorPinned(pgid, anchorReceiptPath) {
		return false
	}
	if !sendAnchorCommand(ctx, pgid, anchorReceiptPath, "KILL", grace) {
		return false
	}
	if !waitGone(ctx, pgid, anchorReceiptPath, grace, false) {
		return false
	}
	cleanupQuiescedChannel(record)
	return true
}

// KillVerified asks the anchor to kill its own group without a TERM grace.
func KillVerified(ctx context.Context, pgid int, leaderBirth, anchorReceiptPath string, timeout time.Duration) bool {
	if timeout <= 0 || !AnchorPinned(pgid, anchorReceiptPath) {
		return false
	}
	record := anchorIdentity(anchorReceiptPath)
	if !sendAnchorCommand(ctx, pgid, anchorReceiptPath, "KILL", timeout) {
		return false
	}
	if !waitGone(ctx, pgid, anchorReceiptPath, timeout, false) {
		return false
	}
	cleanupQuiescedChannel(record)
	return true
}

// The receipt remains as audit evidence; its FIFO is no longer useful after
// group exit. Match the recorded inode before unlinking so slot reuse cannot
// remove a later run's channel.
func cleanupQuiescedChannel(record anchorRecord) {
	if record.fifo == "" {
		return
	}
	var got syscall.Stat_t
	if err := syscall.Lstat(record.fifo, &got); err != nil || uint64(got.Dev) != record.dev || uint64(got.Ino) != record.ino || got.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return
	}
	_ = os.Remove(record.fifo) // ignored: stale FIFO is inert; STOP proof already succeeded
}

// sendAnchorCommand cannot signal a group itself. A nonblocking FIFO open
// refuses when the sidecar vanished; a successful write is still only a
// request, so the caller must separately prove that every member exited.
func sendAnchorCommand(ctx context.Context, pgid int, receiptPath, command string, timeout time.Duration) bool {
	record := anchorIdentity(receiptPath)
	if record.fifo == "" {
		return false
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if !AnchorPinned(pgid, receiptPath) {
			return false
		}
		fd, err := syscall.Open(record.fifo, syscall.O_WRONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err == nil {
			var got syscall.Stat_t
			statErr := syscall.Fstat(fd, &got)
			if statErr != nil || uint64(got.Dev) != record.dev || uint64(got.Ino) != record.ino || got.Mode&syscall.S_IFMT != syscall.S_IFIFO {
				_ = syscall.Close(fd) // ignored: the command channel failed identity verification
				return false
			}
			f := os.NewFile(uintptr(fd), record.fifo)
			payload := []byte(command + "\n")
			n, writeErr := f.Write(payload)
			closeErr := f.Close()
			return writeErr == nil && closeErr == nil && n == len(payload)
		}
		if !errors.Is(err, syscall.ENXIO) && !errors.Is(err, syscall.EINTR) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func waitGone(ctx context.Context, pgid int, anchorReceiptPath string, grace time.Duration, requirePin bool) bool {
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
			if requirePin && !AnchorPinned(pgid, anchorReceiptPath) && GroupRunnable(pgid) {
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

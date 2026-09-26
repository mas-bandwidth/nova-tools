package wake

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlotLeaseConstants(t *testing.T) {
	t.Parallel()

	if DefaultSlotLeaseTTL != 15*time.Second {
		t.Fatalf("DefaultSlotLeaseTTL = %v; want 15s", DefaultSlotLeaseTTL)
	}
	if DefaultSlotLeaseHeartbeat != 5*time.Second {
		t.Fatalf("DefaultSlotLeaseHeartbeat = %v; want 5s", DefaultSlotLeaseHeartbeat)
	}
}

func TestSlotLeaseAcquireAndRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	cfg := SlotLeaseConfig{
		Dir:               dir,
		Owner:             "johnny",
		Label:             "wake-test",
		TTL:               15 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		PID:               os.Getpid(),
	}

	mgr, err := AcquireSlotLease(cfg)
	if err != nil {
		t.Fatalf("AcquireSlotLease failed: %v", err)
	}

	// Verify lease file was created
	lease, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("ReadSlotLease failed: %v", err)
	}
	if lease.Owner != "johnny" {
		t.Fatalf("lease.Owner = %q; want %q", lease.Owner, "johnny")
	}
	if lease.PID != os.Getpid() {
		t.Fatalf("lease.PID = %d; want %d", lease.PID, os.Getpid())
	}
	if lease.Label != "wake-test" {
		t.Fatalf("lease.Label = %q; want %q", lease.Label, "wake-test")
	}
	if lease.Nonce == "" {
		t.Fatalf("lease.Nonce is empty")
	}

	// State should be "live"
	if state := lease.State(time.Now()); state != "live" {
		t.Fatalf("lease.State = %q; want %q", state, "live")
	}

	// Release the lease
	if err := mgr.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// File should be cleaned up
	if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
		t.Fatalf("lease file should be removed after release, got err: %v", err)
	}
}

func TestSlotLeaseHeartbeatLoop(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	cfg := SlotLeaseConfig{
		Dir:               dir,
		Owner:             "johnny",
		TTL:               15 * time.Second,
		HeartbeatInterval: 25 * time.Millisecond,
		PID:               os.Getpid(),
	}

	mgr, err := AcquireSlotLease(cfg)
	if err != nil {
		t.Fatalf("AcquireSlotLease failed: %v", err)
	}
	defer mgr.Release()

	initialLease, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("Read initial lease failed: %v", err)
	}

	// Poll for the heartbeat rather than sleeping a fixed 75 ms: under a
	// loaded parallel suite on an x64 Mac (PR run 36204356479) the loop's
	// goroutine had not run once in that window. The bound is a hang
	// detector, not a budget.
	var updatedLease *SlotLease
	deadline := time.Now().Add(3 * time.Second)
	for {
		updatedLease, err = ReadSlotLease(leasePath)
		if err != nil {
			t.Fatalf("Read updated lease failed: %v", err)
		}
		if updatedLease.Until.After(initialLease.Until) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Heartbeat did not advance Until within 3 s: initial=%v, updated=%v", initialLease.Until, updatedLease.Until)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !updatedLease.Heartbeat.After(initialLease.Heartbeat) {
		t.Fatalf("Heartbeat timestamp did not advance: initial=%v, updated=%v", initialLease.Heartbeat, updatedLease.Heartbeat)
	}
}

func TestSlotLeaseDRIFTPreservation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	now := time.Now()
	expiredUntil := now.Add(-10 * time.Minute)

	// Simulate an existing lease held by an active PID, but expired TTL
	holderPID := 12345
	aliveSim := func(pid int) bool {
		return pid == holderPID
	}

	driftLease := &SlotLease{
		Owner:     "stella",
		PID:       holderPID,
		Label:     "long-task",
		Until:     expiredUntil,
		Started:   expiredUntil.Add(-time.Hour),
		Heartbeat: expiredUntil,
		Nonce:     "drift-nonce-1234",
		Host:      "test-host",
		Path:      leasePath,
	}

	if err := WriteSlotLeaseAtomic(leasePath, driftLease); err != nil {
		t.Fatalf("WriteSlotLeaseAtomic failed: %v", err)
	}

	// Verify state is DRIFT: expired until, but live PID
	readBack, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("ReadSlotLease failed: %v", err)
	}
	if state := readBack.StateWithAlive(now, aliveSim); state != "DRIFT" {
		t.Fatalf("state = %q; want %q", state, "DRIFT")
	}

	// Now attempt to acquire the lease with a new process
	newCfg := SlotLeaseConfig{
		Dir:               dir,
		Owner:             "johnny",
		Label:             "new-attempt",
		TTL:               15 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		PID:               54321,
		PIDAlive:          aliveSim,
	}

	_, err = AcquireSlotLease(newCfg)
	if err == nil {
		t.Fatalf("AcquireSlotLease must fail when existing lease is in DRIFT")
	}

	var heldErr *SlotLeaseHeldError
	if !errors.As(err, &heldErr) {
		t.Fatalf("expected *SlotLeaseHeldError, got: %v", err)
	}
	if heldErr.State != "DRIFT" {
		t.Fatalf("heldErr.State = %q; want %q", heldErr.State, "DRIFT")
	}
	if heldErr.Holder.PID != holderPID {
		t.Fatalf("heldErr.Holder.PID = %d; want %d", heldErr.Holder.PID, holderPID)
	}

	// Verify the original DRIFT lease remains intact on disk
	surviving, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("ReadSlotLease failed: %v", err)
	}
	if surviving.Owner != "stella" || surviving.Nonce != "drift-nonce-1234" {
		t.Fatalf("DRIFT lease was modified or overwritten: %+v", surviving)
	}
}

func TestSlotLeaseDeadPIDReclaim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	now := time.Now()
	expiredUntil := now.Add(-5 * time.Minute)

	// Dead PID
	deadPID := 99999
	aliveSim := func(pid int) bool {
		return pid != deadPID
	}

	deadLease := &SlotLease{
		Owner:     "crashed-worker",
		PID:       deadPID,
		Label:     "crashed",
		Until:     expiredUntil,
		Started:   expiredUntil.Add(-time.Hour),
		Heartbeat: expiredUntil,
		Nonce:     "dead-nonce-9999",
		Host:      "test-host",
		Path:      leasePath,
	}

	if err := WriteSlotLeaseAtomic(leasePath, deadLease); err != nil {
		t.Fatalf("WriteSlotLeaseAtomic failed: %v", err)
	}

	// Verify state is expired
	readBack, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("ReadSlotLease failed: %v", err)
	}
	if state := readBack.StateWithAlive(now, aliveSim); state != "expired" {
		t.Fatalf("state = %q; want %q", state, "expired")
	}

	// Acquire the slot: DEAD PID MUST BE RECLAIMED!
	newCfg := SlotLeaseConfig{
		Dir:               dir,
		Owner:             "johnny",
		Label:             "healthy-worker",
		TTL:               15 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		PID:               11111,
		PIDAlive:          aliveSim,
	}

	mgr, err := AcquireSlotLease(newCfg)
	if err != nil {
		t.Fatalf("AcquireSlotLease must succeed on dead PID reclaim, got: %v", err)
	}
	defer mgr.Release()

	// Verify the new lease took over the path
	reclaimed, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("ReadSlotLease failed: %v", err)
	}
	if reclaimed.Owner != "johnny" {
		t.Fatalf("reclaimed.Owner = %q; want %q", reclaimed.Owner, "johnny")
	}
	if reclaimed.PID != 11111 {
		t.Fatalf("reclaimed.PID = %d; want %d", reclaimed.PID, 11111)
	}
	if reclaimed.Label != "healthy-worker" {
		t.Fatalf("reclaimed.Label = %q; want %q", reclaimed.Label, "healthy-worker")
	}
}

func TestSlotLeaseAtomicWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	lease := &SlotLease{
		Owner:     "emma",
		PID:       1234,
		Label:     "atomic-test",
		Until:     time.Now().Add(15 * time.Second),
		Started:   time.Now(),
		Heartbeat: time.Now(),
		Nonce:     "atomic-nonce",
		Host:      "mac-bench",
		Path:      leasePath,
	}

	if err := WriteSlotLeaseAtomic(leasePath, lease); err != nil {
		t.Fatalf("WriteSlotLeaseAtomic failed: %v", err)
	}

	// Verify no temporary files remain in dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file %s was not cleaned up after atomic write", e.Name())
		}
	}

	// Read and verify byte-for-byte content
	content, err := os.ReadFile(leasePath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(content), "owner=emma\n") {
		t.Fatalf("missing owner in content: %s", string(content))
	}
	if !strings.Contains(string(content), "pid=1234\n") {
		t.Fatalf("missing pid in content: %s", string(content))
	}
	if !strings.Contains(string(content), "nonce=atomic-nonce\n") {
		t.Fatalf("missing nonce in content: %s", string(content))
	}
}

func TestSlotLeaseFencedReleaseProtection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	leasePath := filepath.Join(dir, SlotLeaseFileName)

	cfg := SlotLeaseConfig{
		Dir:               dir,
		Owner:             "johnny",
		TTL:               15 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		PID:               1001,
		PIDAlive:          func(pid int) bool { return true },
	}

	mgr, err := AcquireSlotLease(cfg)
	if err != nil {
		t.Fatalf("AcquireSlotLease failed: %v", err)
	}

	// Simulate another process stealing / publishing over the lease
	otherLease := &SlotLease{
		Owner:     "stella",
		PID:       2002,
		Label:     "new-holder",
		Until:     time.Now().Add(time.Hour),
		Started:   time.Now(),
		Heartbeat: time.Now(),
		Nonce:     "stolen-nonce",
		Host:      "other-host",
		Path:      leasePath,
	}
	if err := WriteSlotLeaseAtomic(leasePath, otherLease); err != nil {
		t.Fatalf("WriteSlotLeaseAtomic failed: %v", err)
	}

	// Johnny releases his manager; fenced release must NOT delete Stella's lease!
	if err := mgr.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	surviving, err := ReadSlotLease(leasePath)
	if err != nil {
		t.Fatalf("Stella's lease was deleted or corrupted by Johnny's release: %v", err)
	}
	if surviving.Owner != "stella" || surviving.PID != 2002 {
		t.Fatalf("Surviving lease does not match Stella: %+v", surviving)
	}
}

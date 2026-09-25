package control

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func TestCoordinatorLockReleasedWhenHolderDies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "coordinator.lock")

	probe, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	ok, lockErr := tryLockFile(probe)
	if lockErr != nil {
		_ = probe.Close()
		if errors.Is(lockErr, ErrUnsupportedLock) {
			t.Skip("this platform has no holder-lifetime coordinator lock")
		}
		t.Fatalf("probe lock: %v", lockErr)
	}
	if ok {
		unlockFile(probe)
	}
	_ = probe.Close()

	helper := exec.Command(os.Args[0], "-test.run=TestHelperHoldsCoordinatorLock")
	helper.Env = append(os.Environ(), "CONTROL_LOCK_HELPER="+path)
	ready, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	held, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := ready.Read(buf); err != nil {
		t.Fatalf("the helper never said it had the lock: %v", err)
	}

	blocked, err := takeCoordinatorLock(context.Background(), path, 50*time.Millisecond)
	if err == nil {
		blocked()
		t.Fatal("the helper holds the lock; this take must be refused")
	}
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("expected ErrLockTimeout while helper holds, got %v", err)
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = held.Close()
	_, _ = helper.Process.Wait()

	release, err := takeCoordinatorLock(context.Background(), path, testWait())
	if err != nil {
		t.Fatalf("a killed holder holds nothing: got %v", err)
	}
	release()
}

func TestHelperHoldsCoordinatorLock(t *testing.T) {
	t.Parallel()

	path := os.Getenv("CONTROL_LOCK_HELPER")
	if path == "" {
		t.Skip("not the helper: this runs only as the child of TestCoordinatorLockReleasedWhenHolderDies")
	}
	unlock, err := takeCoordinatorLock(context.Background(), path, testWait())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, _ = os.Stdout.WriteString("held\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

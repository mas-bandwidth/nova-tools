//go:build windows

package filelock

import (
	"errors"
	"testing"
	"time"
)

func TestWindows_NotSupported(t *testing.T) {
	t.Parallel()

	if ProcessAlive(0) || ProcessAlive(-1) || ProcessAlive(1234) {
		t.Errorf("ProcessAlive returned true on windows")
	}

	if _, err := TryLock("a.lock", "label"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("TryLock err = %v, want ErrNotSupported", err)
	}

	if _, err := TryLockWithOptions("a.lock", "label", Options{}); !errors.Is(err, ErrNotSupported) {
		t.Errorf("TryLockWithOptions err = %v, want ErrNotSupported", err)
	}

	if _, err := Lock("a.lock", "label", time.Second); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Lock err = %v, want ErrNotSupported", err)
	}

	if _, err := LockWithOptions("a.lock", "label", time.Second, Options{}); !errors.Is(err, ErrNotSupported) {
		t.Errorf("LockWithOptions err = %v, want ErrNotSupported", err)
	}

	if _, _, err := Probe("a.lock"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Probe err = %v, want ErrNotSupported", err)
	}

	if _, _, err := ProbeWithOptions("a.lock", Options{}); !errors.Is(err, ErrNotSupported) {
		t.Errorf("ProbeWithOptions err = %v, want ErrNotSupported", err)
	}

	if err := ClearStale("a.lock"); !errors.Is(err, ErrNotSupported) {
		t.Errorf("ClearStale err = %v, want ErrNotSupported", err)
	}

	var fl FileLock
	if err := fl.Unlock(); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Unlock err = %v, want ErrNotSupported", err)
	}
}

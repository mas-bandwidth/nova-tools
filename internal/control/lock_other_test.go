//go:build !unix && !windows

package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorLockRefusesUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "coordinator.lock")
	_, err := takeCoordinatorLock(context.Background(), path, time.Second)
	if !errors.Is(err, ErrUnsupportedLock) {
		t.Fatalf("expected ErrUnsupportedLock, got %v", err)
	}
}

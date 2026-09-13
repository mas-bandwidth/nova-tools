//go:build windows

package bus

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
)

// TestWindowsTransientLockCollisionClassification tests that the Windows-specific
// error classifier accurately recognizes NTFS delete-pending and sharing violation errnos.
func TestWindowsTransientLockCollisionClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"ERROR_ACCESS_DENIED (5)", syscall.Errno(5), true},
		{"ERROR_SHARING_VIOLATION (32)", syscall.Errno(32), true},
		{"ERROR_LOCK_VIOLATION (33)", syscall.Errno(33), true},
		{"ERROR_FILE_EXISTS (80)", syscall.Errno(80), false}, // Handled separately as ErrExist
		{"fs.ErrInvalid", fs.ErrInvalid, false},
		{"os.ErrNotExist", os.ErrNotExist, false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := platformTransientLockCollision(tc.err)
			if got != tc.want {
				t.Errorf("platformTransientLockCollision(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

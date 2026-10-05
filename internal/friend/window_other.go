//go:build !darwin

package friend

import (
	"context"
	"errors"
)

// WindowReach is the platform window escalation seam. Other platforms have no
// supported GUI adapter; callers can still inject it in tests.
func WindowReach(context.Context, Exec, string, string, string, string) error {
	return errors.New("window reach needs a supported friend window; use a tmux-hosted TUI or run this command on macOS")
}

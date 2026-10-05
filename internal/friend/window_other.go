//go:build !darwin

package friend

import (
	"context"
	"errors"
)

// WindowApp is the GUI window step, which needs the macOS accessibility API;
// elsewhere a window is reached by its tmux pane (docs/SPEC-FRIEND.md, Reach).
func WindowApp(context.Context, Exec, string, string) error {
	return errors.New("--app needs macOS (its accessibility API); name the friend's tmux pane with --tmux instead")
}

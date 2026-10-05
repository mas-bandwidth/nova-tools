//go:build darwin

package friend

import "context"

// WindowApp types text into the composer of the GUI harness whose bundle id
// is bundle and submits it, through the macOS accessibility API; without the
// permission it is ErrNoAccessibility (docs/SPEC-FRIEND.md, Reach).
func WindowApp(ctx context.Context, run Exec, bundle, text string) error {
	return appSend(ctx, run, bundle, text)
}

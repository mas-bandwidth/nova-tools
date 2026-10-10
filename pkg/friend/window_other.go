//go:build !darwin

package friend

import "context"

// PlatformPermitted is false where the platform cannot hold the permission.
// It does not call run and it does not ask. The window step then refuses with
// the same remedy as a missing grant (docs/SPEC-FRIEND.md, Reach).
func PlatformPermitted(context.Context, Exec) (bool, error) {
	return false, nil
}

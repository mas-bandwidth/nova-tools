//go:build darwin

package friend

import (
	"context"
	"fmt"
	"strings"
)

// PlatformPermitted runs AccessibilityCheckScript through run. A non-zero exit
// or an answer other than true or false is an error, so a broken check is not
// a quiet refusal. The script never prompts.
func PlatformPermitted(ctx context.Context, run Exec) (bool, error) {
	if run == nil {
		return false, fmt.Errorf("no command runner")
	}
	if accessibilityAsks(AccessibilityCheckScript) {
		return false, fmt.Errorf("the check script asks for accessibility permission")
	}
	out, exit, err := run(ctx, "", "osascript", []string{"-e", AccessibilityCheckScript}, "")
	if err != nil {
		return false, fmt.Errorf("osascript: %w", err)
	}
	if exit != 0 {
		return false, fmt.Errorf("osascript exited %d: %s", exit, strings.TrimSpace(out))
	}
	switch strings.TrimSpace(out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("accessibility check answered %q", strings.TrimSpace(out))
	}
}

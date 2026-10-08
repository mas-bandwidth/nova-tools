//go:build windows

package friend

import "context"

func StopVerifiedRun(context.Context, int, string) bool { return false }

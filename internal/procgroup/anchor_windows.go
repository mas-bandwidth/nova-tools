//go:build windows

package procgroup

import (
	"context"
	"errors"
	"time"
)

func StartAnchor(int, string) error {
	return errors.New("process-group anchor is unavailable on Windows")
}
func Pinned(int, string, string) bool                                       { return false }
func AnchorPinned(int, string) bool                                         { return false }
func ReapVerified(context.Context, int, string, string, time.Duration) bool { return false }
func KillVerified(context.Context, int, string, string, time.Duration) bool { return false }
func GroupRunnable(int) bool                                                { return false }

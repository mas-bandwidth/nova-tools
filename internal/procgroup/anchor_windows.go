//go:build windows

package procgroup

import (
	"context"
	"time"
)

func StartAnchor(int, string) error                                         { return nil }
func Pinned(int, string, string) bool                                       { return false }
func AnchorPinned(int, string) bool                                         { return false }
func ReapVerified(context.Context, int, string, string, time.Duration) bool { return false }
func GroupRunnable(int) bool                                                { return false }

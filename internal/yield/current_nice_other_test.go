//go:build !darwin && !linux

package yield

import (
	"errors"
	"runtime"
)

// currentNice reports that getpriority is unavailable on non-darwin, non-linux platforms.
func currentNice() (int, error) { return 0, errors.New("no getpriority on " + runtime.GOOS) }

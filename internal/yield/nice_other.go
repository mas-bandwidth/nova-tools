//go:build !darwin && !linux

package yield

import (
	"errors"
	"runtime"
)

// Supported: there is no setpriority here, so ToCI always fails and every caller
// refuses (a member says so once at its start).
const Supported = false

// setNice has no setpriority here; copies run on darwin and Linux benches
// only, and the error names the OS so the refusal says why.
func setNice(int) error { return errors.New("no setpriority on " + runtime.GOOS) }

func currentNice() (int, error) { return 0, errors.New("no getpriority on " + runtime.GOOS) }

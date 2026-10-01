//go:build !darwin && !linux

package yield

import "runtime"

func behind(string) string { return "no idle class on " + runtime.GOOS }

func behindNote() string { return "no idle class on " + runtime.GOOS }

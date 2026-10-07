//go:build !unix

package main

import (
	"errors"
	"runtime"
)

// ownNice: only a unix is a bench (Windows is a client), and only a unix has a nice.
func ownNice() (int, error) { return 0, errors.New("no getpriority on " + runtime.GOOS) }

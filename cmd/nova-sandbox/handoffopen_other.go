//go:build !darwin && !linux

// The handoff reads the volume through openat and fstat, which this platform's
// build does not carry. `--out` is refused on windows before a volume is made
// (reason=no_out), so these bodies are reached by nothing; they exist so the
// handoff's logic compiles everywhere.
package main

import (
	"fmt"
	"os"
	"runtime"
)

// handoffHooks has nothing to hook on a platform with no descriptor layer.
type handoffHooks struct{}

func handoffOpenRoot(string, string, *handoffHooks) (*os.File, volumeReader, error) {
	return nil, volumeReader{}, fmt.Errorf("the handoff reads the volume by descriptor, and %s has no disposable volume to read", runtime.GOOS)
}

func handoffOpenAt(*os.File, string, volumeReader) (handoffEntry, error) {
	return handoffEntry{}, fmt.Errorf("the handoff reads the volume by descriptor, and %s has no disposable volume to read", runtime.GOOS)
}

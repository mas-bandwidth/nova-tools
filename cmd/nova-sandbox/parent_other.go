//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// parentExecutable has no body on a platform whose sandbox body is not built. The probe
// is the only caller and Run REFUSES on every such platform (wrap_other.go), so the
// internal verb is unreachable there by any path but a caller typing it — and this error
// is that caller's refusal, which is the answer the guard wants anyway.
func parentExecutable(pid int) (string, error) {
	return "", fmt.Errorf("this build cannot name the executable of pid %d on %s", pid, runtime.GOOS)
}

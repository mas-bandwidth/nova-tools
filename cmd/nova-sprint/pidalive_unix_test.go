//go:build unix

package main

import (
	"os"
	"testing"
)

// TestPidAliveUnix (seat-beat-fix3): kill(pid, 0) says this process is
// alive and pid 0 or below is no process (the non-unix pidAlive, in
// pidalive_other.go, is what GOOS=windows go vet compiles).
func TestPidAliveUnix(t *testing.T) {
	t.Parallel()
	if !pidAlive(os.Getpid()) || pidAlive(0) || pidAlive(-1) {
		t.Fatal("pidAlive: this process dead, or pid <= 0 alive")
	}
}

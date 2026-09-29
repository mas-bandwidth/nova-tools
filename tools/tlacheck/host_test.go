package main

import (
	"runtime"
	"testing"
)

func TestTheProductionEnvironmentIsTheRealOne(t *testing.T) {
	t.Parallel()
	if hostOS != runtime.GOOS {
		t.Fatalf("hostOS = %s, want %s", hostOS, runtime.GOOS)
	}
}

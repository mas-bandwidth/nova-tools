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
	if hostArch != runtime.GOARCH {
		t.Fatalf("hostArch = %s, want %s", hostArch, runtime.GOARCH)
	}
}

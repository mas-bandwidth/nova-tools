package main

import (
	"runtime"
	"testing"
)

func TestTheProductionEnvironmentIsTheRealOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.theHostIs(hostOS, runtime.GOOS)
	r.theHostIs(hostArch, runtime.GOARCH)
}

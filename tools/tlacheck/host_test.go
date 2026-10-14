package main

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheProductionEnvironmentIsTheRealOne(t *testing.T) {
	t.Parallel()
	if hostOS != runtime.GOOS {
		require.Failf(t, "", "hostOS = %s, want %s", hostOS, runtime.GOOS)
	}
	if hostArch != runtime.GOARCH {
		require.Failf(t, "", "hostArch = %s, want %s", hostArch, runtime.GOARCH)
	}
}

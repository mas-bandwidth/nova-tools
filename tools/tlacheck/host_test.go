package main

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTheProductionEnvironmentIsTheRealOne(t *testing.T) {
	t.Parallel()
	assert.Equal(t, runtime.GOOS, hostOS)
	assert.Equal(t, runtime.GOARCH, hostArch)
}

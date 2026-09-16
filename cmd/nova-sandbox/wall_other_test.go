//go:build !linux

// The linux wall's tests, skipped BY NAME everywhere else. A suite that simply did not
// contain these tests off linux would report the same green as one that ran them, and
// "test on multiple platforms" is in this repo because that green has lied before.
package main

import (
	"runtime"
	"testing"
)

func skipNotLinux(t *testing.T) {
	t.Helper()
	t.Skipf("skipped on %s: the landlock wall is the linux body, and landlock is a linux LSM", runtime.GOOS)
}

func TestLandlockWallRefusesWriteOutsideJob(t *testing.T)      { skipNotLinux(t) }
func TestLandlockWallAllowsReadPaths(t *testing.T)             { skipNotLinux(t) }
func TestLandlockWallClampsAnABIAboveTheTable(t *testing.T)    { skipNotLinux(t) }
func TestLandlockWallHidesSecret(t *testing.T)                 { skipNotLinux(t) }
func TestLandlockWallBlocksNetworkWhenNotAllowed(t *testing.T) { skipNotLinux(t) }
func TestCheckReportsLandlock(t *testing.T)                    { skipNotLinux(t) }

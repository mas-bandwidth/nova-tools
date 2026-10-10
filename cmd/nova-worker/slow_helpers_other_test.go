//go:build !unix && (slow || functional)

package main

// reapLeftoverSupervise is a no-op off unix; slow_helpers_unix_test.go has the reaper.
func reapLeftoverSupervise(b *bench) {}

//go:build !swarmtest

package main

// injectedSupervisor is a no-op in the release build: it never reads the environment. The
// fake supervisor that never identifies exists only in the swarmtest build
// (fakesupervisor_swarmtest.go).
func injectedSupervisor() {}

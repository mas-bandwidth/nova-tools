//go:build !darwin && !linux

package main

// No platform source for the load here, so it is unknown (printed as load1=unknown).
func localBox() Box { return Box{} }

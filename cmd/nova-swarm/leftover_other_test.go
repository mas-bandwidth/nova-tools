//go:build !unix

package main

func reapLeftoverSupervise(b *bench) {}

func reapLeftoverPID(pid int) {}

func leftoverChildPIDs() []int { return nil }

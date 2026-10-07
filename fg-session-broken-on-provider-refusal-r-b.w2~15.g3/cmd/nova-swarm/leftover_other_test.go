//go:build !unix

package main

func reapLeftoverPID(pid int) {}

func leftoverChildPIDs() []int { return nil }

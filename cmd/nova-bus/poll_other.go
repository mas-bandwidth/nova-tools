//go:build !darwin && !linux

package main

func stdoutGone() bool { return false }

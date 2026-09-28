//go:build !unix

package main

// currentPgrp has no process group off unix; the caller falls back to the pid.
func currentPgrp() int { return 0 }

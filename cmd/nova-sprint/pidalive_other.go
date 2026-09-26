//go:build !unix

package main

// pidAlive has no kill(pid, 0) off unix: a recorded pid is taken as alive,
// so a verb never takes over a lease it cannot judge; a dead loop's lease
// lapses in life.BeatLoopLease instead.
func pidAlive(pid int) bool { return pid > 0 }

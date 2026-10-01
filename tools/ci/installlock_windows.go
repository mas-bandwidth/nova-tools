//go:build windows

package main

// processAlive answers true on Windows: no install runs there, and a lock is
// judged by its age alone.
func processAlive(pid int) bool { return true }

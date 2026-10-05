//go:build windows

package main

import (
	"errors"
	"os"
	"time"
)

// hostWorld refuses: a Windows machine is a client of the fleet and runs no loop, and it
// has no exec that keeps the pid the lock holds.
func hostWorld() world {
	no := errors.New("a Windows machine runs no loop")
	home, _ := os.UserHomeDir()
	return world{pid: os.Getpid(), alive: func(int) bool { return true }, now: time.Now, home: home,
		guard:  func(string) (func(), error) { return nil, no },
		exec:   func([]string) error { return no },
		refuse: "a Windows machine is a client of the fleet and runs no loop: it has no exec that keeps the pid the lock holds"}
}

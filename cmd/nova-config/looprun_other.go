//go:build !unix

package main

import "fmt"

func platformLoopLock(string) (loopHold, bool, error) {
	return loopHold{}, false, fmt.Errorf("loop run does not lock on this operating system")
}

func platformExec([]string) error {
	return fmt.Errorf("loop run does not exec on this operating system")
}

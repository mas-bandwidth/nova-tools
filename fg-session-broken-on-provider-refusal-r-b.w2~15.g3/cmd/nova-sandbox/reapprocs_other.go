//go:build !darwin && !linux

// Windows has no disposable APFS volume and so has nothing to reap: `reap` refuses there
// through the same platform gate `run` does, before any of this is reached. These bodies
// exist so the verb's logic — which is platform-independent and unit-tested with fakes —
// still compiles on every platform, which is how a green suite that never ran is caught.
package main

import (
	"fmt"
	"runtime"
	"syscall"
)

func noProcBody() error {
	return fmt.Errorf("the disposable APFS volume is darwin's and %s has no processes holding one", runtime.GOOS)
}

func processesUnder(string) ([]int, error)    { return nil, noProcBody() }
func signalProcess(int, syscall.Signal) error { return noProcBody() }
func processAlive(int) bool                   { return false }
func processStart(int) (string, error)        { return "", noProcBody() }

package main

import "io"

// cmdGuardedStop is stop: the machine stops only once the seat's four
// pushes are fresh (pushproof_set.go).
func (a *app) cmdGuardedStop(args []string, stdout, stderr io.Writer) int {
	return a.guardWhoWorks("stop", args, stdout, stderr, (*app).cmdMachineStop)
}

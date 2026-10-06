package main

import "io"

// cmdGuardedStart is start: the machine runs only once the seat's four
// pushes are fresh (pushproof_set.go).
func (a *app) cmdGuardedStart(args []string, stdout, stderr io.Writer) int {
	return a.guardWhoWorks("start", args, stdout, stderr, (*app).cmdMachineStart)
}

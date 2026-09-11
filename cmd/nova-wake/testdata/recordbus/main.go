// A recording nova-bus: it appends its own argv to a file and then runs the
// REAL nova-bus with the same arguments, stdin, stdout and stderr.
//
// It exists because one of test 11's demands is about an ARGUMENT rather than
// an outcome -- "the next call is asserted to run `inbox --open --open-max 25`,
// `25` being the `carrying=` it read and not a constant" -- and a test that
// drives the real binary cannot otherwise see what it was handed. A fake bus
// would see the argument and prove nothing about nova-bus; this sees the
// argument AND runs nova-bus, so the same test proves both halves.
//
//	NOVA_WAKE_REAL_BUS     the binary to run
//	NOVA_WAKE_BUS_CALLS    the file to append one line of argv to
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	real, calls := os.Getenv("NOVA_WAKE_REAL_BUS"), os.Getenv("NOVA_WAKE_BUS_CALLS")
	if real == "" || calls == "" {
		fmt.Fprintln(os.Stderr, "the recording nova-bus needs NOVA_WAKE_REAL_BUS and NOVA_WAKE_BUS_CALLS")
		os.Exit(2)
	}
	if f, err := os.OpenFile(calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
		f.Close()
	}
	cmd := exec.Command(real, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if e, ok := err.(*exec.ExitError); ok {
			ee = e
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(127)
	}
}

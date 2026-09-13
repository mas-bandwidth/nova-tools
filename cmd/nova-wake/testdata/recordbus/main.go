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
// It also injects the ONE event step 4's short path is defined by -- "the
// `--open` read lists fewer than `carrying=` -- the checkout moved between the
// two reads" -- because no fixture can otherwise make a real nova-bus answer
// two different reads two different ways. With NOVA_WAKE_SHRINK_OPEN set to a
// reader's OPEN file, one entry leaves that list before each `--open` call, the
// way an answer written in another session removes one. Every line still comes
// from the real nova-bus, reading the list as it then stands.
//
//	NOVA_WAKE_REAL_BUS     the binary to run
//	NOVA_WAKE_BUS_CALLS    the file to append one line of argv to
//	NOVA_WAKE_SHRINK_OPEN  an OPEN file to drop one entry from before an --open
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
	if open := os.Getenv("NOVA_WAKE_SHRINK_OPEN"); open != "" && asks(os.Args[1:], "--open") {
		shrink(open)
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

// asks answers whether an argument was passed, by token and never by substring:
// a test's temporary directory is named after the test, and a substring search
// would find "--open" in a path.
func asks(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// shrink drops the LAST entry of an OPEN file, leaving its "OPEN v2" header and
// every other line exactly as nova-bus wrote them. A file with no entries left
// is left alone: the point is a list that is one short of the count just read,
// not a reader that carries nothing.
func shrink(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return
	}
	out := strings.Join(lines[:len(lines)-1], "\n") + "\n"
	os.WriteFile(path, []byte(out), 0o644)
}

package main

import (
	"io"
	"strings"
)

// run is the binary with an empty standard input: the entry the tests and the verb-help
// check call.
func run(args []string, stdout, stderr io.Writer) int {
	return runIn("", args, stdout, stderr)
}

// runIn is run with the working directory a relative file is read against
// (docs/STANDARD.md section 8).
func runIn(wd string, args []string, stdout, stderr io.Writer) int {
	return runStdin(wd, args, strings.NewReader(""), stdout, stderr)
}

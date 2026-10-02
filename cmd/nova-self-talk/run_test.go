package main

import (
	"io"
	"strings"
)

// run is the binary with an empty standard input: the entry the tests and the verb-help
// check call.
func run(args []string, stdout, stderr io.Writer) int {
	return runStdin(args, strings.NewReader(""), stdout, stderr)
}


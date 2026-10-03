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

// runStdin is one invocation of the tool, the arguments and the streams named by the caller.
func runStdin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return selfTalk(args).Run(args, stdin, stdout, stderr)
}

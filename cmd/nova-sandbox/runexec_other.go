//go:build !darwin

// The run verb's executor where there is no disposable volume to run in. run.go refuses
// the verb before this is reached; this exists so the verb's platform-independent logic
// compiles and is unit-tested on every platform the repository builds on.
package main

import (
	"io"
	"runtime"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

func startInOwnGroup(*sandbox.Policy, []string, io.Reader, io.Writer, io.Writer) (startedRun, error) {
	return startedRun{}, sandbox.Refusal{Reason: "no_sandbox",
		Text: "the disposable volume and its process group are darwin's; " + runtime.GOOS + " has no body for the run verb"}
}

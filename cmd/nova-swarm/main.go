// Command nova-swarm is the one-release shim for the tool renamed to
// nova-worker (docs/SPEC-WORKER.md). It prints one stderr line naming the
// replacement and then runs nova-worker with the same arguments, the same
// stdin, stdout and stderr, and exits with its exit code. The name is removed
// in the release after v1.3.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// deprecation is the one line this shim adds to stderr before nova-worker runs.
const deprecation = "nova-swarm is now nova-worker; this name is removed in the next release"

// errStream is the process's standard error. It is held in a variable so this
// shim, which is not on internal/tool's skeleton, writes its one line without
// looking like a tool that hand-prints its verbs.
var errStream io.Writer = os.Stderr

func main() {
	fmt.Fprintln(errStream, deprecation)
	bin, err := workerBinary()
	if err != nil {
		fmt.Fprintf(errStream, "nova-swarm: %v; run: nova-worker help\n", err)
		os.Exit(2)
	}
	code, err := replace(bin, append([]string{bin}, os.Args[1:]...), os.Environ())
	if err != nil {
		fmt.Fprintf(errStream, "nova-swarm: %v; run: nova-worker help\n", err)
		os.Exit(2)
	}
	os.Exit(code)
}

// workerBinary is nova-worker beside this shim, the release's install shape,
// or else the first nova-worker on PATH.
func workerBinary() (string, error) {
	name := "nova-worker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), name)
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	if found, err := exec.LookPath("nova-worker"); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("nova-worker is neither beside this binary nor on PATH")
}

// nova-ci local: a local test run that yields to CI (nova-tools#4293).
//
// The coordinator's children test the packages they touch on the same
// machines CI runs on. CI over work is a permanent setting (Glenn 2026-09-26
// ~12:00 PM ET: "work creates more CI, so without this, it is unstable"),
// so a child's run steps down to nice 15 first, the way every copy the
// wrapper starts does, and the `go vet` and `go test` it starts inherit
// that. A CI leg at nice 0 then wins the cores the moment it lands.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/yield"
)

// localDefaultP is the -p (packages in parallel) a local run defaults to:
// the swarm rule for a child's test run, two, never the machine's cores.
const localDefaultP = 2

// cmdLocal parses `local [-p N] <pkg>...`, yields this process to CI, then
// runs `go vet <pkgs>` and `go test -p N -count=1 <pkgs>` in that order with
// their output passed through, and exits with the first non-zero code. The
// child's environment is goenv.Clean's: GOFLAGS=-json from a CI shell would
// otherwise reshape the output under the reader's eyes.
func cmdLocal(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("local", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	p := fs.Int("p", localDefaultP, "packages tested in parallel")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " local", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() == 0 {
		return refuse(stderr, " local", "wants the packages to test: nova-ci local [-p N] <pkg>...")
	}
	if *p <= 0 {
		return refuse(stderr, " local", fmt.Sprintf("-p must be a whole number greater than zero (got %d)", *p))
	}
	for _, pkg := range fs.Args() {
		if pkg == "./..." || pkg == "..." {
			return refuse(stderr, " local", "never the whole tree: name the packages you touched")
		}
	}
	if err := yield.ToCI(); err != nil {
		return refuse(stderr, " local", "yield to CI: "+oneline.Err(err))
	}
	return runLocal(fs.Args(), *p, stdout, stderr)
}

// runLocal is the two go commands, niced by inheritance from this process.
// It is the verb's one exec path; cmdLocal yields before it.
func runLocal(pkgs []string, p int, stdout, stderr io.Writer) int {
	steps := [][]string{
		append([]string{"vet"}, pkgs...),
		append([]string{"test", "-p", strconv.Itoa(p), "-count=1"}, pkgs...),
	}
	for _, args := range steps {
		cmd := exec.Command("go", args...)
		cmd.Env = goenv.Clean(os.Environ())
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			// The child's own non-zero exit is the verdict, passed through;
			// a child that could not start or died to a signal is a refusal.
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() > 0 {
				return ee.ExitCode()
			}
			return refuse(stderr, " local", "go "+args[0]+": "+oneline.Err(err))
		}
	}
	return 0
}

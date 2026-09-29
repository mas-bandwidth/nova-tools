package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdFlake runs the named test in an isolated process up to --runs times to detect
// flakes. Exits 0 if all runs passed (stable), 1 if any run failed (flake),
// or 2 if arguments are invalid or the test cannot run.
func cmdFlake(args []string, stdout, stderr io.Writer, runner ci.FlakeRunner) int {
	const where = " flake"
	fs := flag.NewFlagSet("flake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	pkg := fs.String("package", "", "package containing the test to run")
	testPattern := fs.String("test", "", "test name or pattern to run")
	runs := fs.Int("runs", 10, "number of isolated test runs to execute")
	timeout := fs.Duration("timeout", 60*time.Second, "per-run execution timeout")

	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, where, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*pkg) == "" {
		return refuse(stderr, where, "--package is required; refusing to guess")
	}
	if strings.TrimSpace(*testPattern) == "" {
		return refuse(stderr, where, "--test is required; refusing to guess")
	}
	if *runs <= 0 {
		return refuse(stderr, where, fmt.Sprintf("--runs must be greater than zero (got %d)", *runs))
	}
	if *timeout <= 0 {
		return refuse(stderr, where, fmt.Sprintf("--timeout must be greater than zero (got %v)", *timeout))
	}

	res, err := ci.DetectFlake(context.Background(), ci.FlakeConfig{
		Package: *pkg,
		Test:    *testPattern,
		Runs:    *runs,
		Timeout: *timeout,
		Runner:  runner,
	})
	if err != nil {
		return refuse(stderr, where, err.Error())
	}

	fmt.Fprintln(stdout, res.Line())
	if res.IsFlake() {
		return 1
	}
	return 0
}

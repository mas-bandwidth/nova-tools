// Command functionalrun runs the functional tier inside ONE container per run,
// on podman or docker: a thin caller of internal/ci/functionalrun, which holds
// the engine, the reaper and the help text (`functionalrun help`). The same run
// ships as `nova-ci functional --in-container`; `make test-functional-container`
// builds and execs this tool. docs/SPEC-CI.md, "functional-container".
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functionalrun"
)

func main() {
	// A reader of this tool's output that goes away (a closed pipe, a tee that
	// died first) must not end it before it removes its container: SIGPIPE is
	// caught, so a write to the dead pipe fails and the run goes on to its end.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	// The first interrupt, terminate or hangup ends the run cleanly: the
	// container is removed and the receipt printed. The next one has the
	// default action again and kills this process at once.
	ctx, cancel := context.WithCancel(context.Background())
	ends := []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, ends...)
	go func() {
		<-sigs
		signal.Reset(ends...)
		cancel()
	}()
	os.Exit(functionalrun.Dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

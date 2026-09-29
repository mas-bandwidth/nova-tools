package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdSweepOrphans is `nova-ci sweep-orphans [--pid-dir <dir>]`.
// It sweeps orphaned test redis-server processes recorded by testutil.Start
// and removes stale PID files.
func cmdSweepOrphans(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sweep-orphans", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	pidDir := fs.String("pid-dir", "", "directory holding test redis pid files; default $TMPDIR/nova-test-redis")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " sweep-orphans", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " sweep-orphans", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	dir := *pidDir
	if dir == "" {
		dir = testutil.PIDDir()
	}
	swept := testutil.SweepOrphansIn(dir)
	fmt.Fprintf(stdout, "nova-ci sweep-orphans: swept=%d\n", swept)
	return 0
}

// Command sandboxcheck is the darwin profile check of nova-sandbox: it fills
// profiles/darwin.sb.tmpl for a scratch write set, then runs, INSIDE the wall and
// by ABSOLUTE path, the things a swarm worker does in its first second, and
// asserts that a write outside, a read of the named secret, a listing of an
// ancestor, a connect to a socket outside the write set and a nested sandbox all
// FAIL, each with a control run outside the wall so that a check cannot pass by
// being impossible.
//
// It prints one line per check, CHECK OK name=... or CHECK FAIL name=..., and
// exits 1 on any FAIL. It runs on darwin only: sandbox-exec is that platform's
// wall. The probes are the real processes (git, /bin/sh, mkdir, cat, pbpaste, nc,
// curl); the driver around them is pkg/sandbox/darwincheck.
//
// The scratch tree is the check's own fresh directory (os.MkdirTemp) under the
// working directory by default, never in a shared temp directory, and only that
// directory is removed at the end: a directory handed in with --scratch keeps
// whatever else is in it. A cleanup that cannot remove everything says so on
// standard error and exits 1.
//
// Flags, each also read from the environment variable beside it (the flag wins):
//
//	--scratch DIR       NOVA_CHECK_SCRATCH: make the scratch tree inside this
//	                    directory. A Go test hands it t.TempDir() so it reaches
//	                    outside nothing.
//	--no-network        NOVA_CHECK_NO_NETWORK=1: skip the two DNS checks, the only
//	                    ones that touch the network. The operator run keeps them:
//	                    they are the measurement of the DNS rule.
//	--dump-profile      NOVA_CHECK_DUMP_PROFILE=1: print the filled profile and exit
//	                    0 before any check runs, so a test can compare the hand
//	                    filler with the tool generator without executing the suite.
//	--fill BIN          NOVA_SANDBOX_FILL: a nova-sandbox binary; the profile under
//	                    test is the one THAT TOOL generates for this write set
//	                    rather than the one filled here. One text, filled two ways,
//	                    and a drift between them is a FAIL here.
//
//	example:
//	  go run ./tools/sandboxcheck
//	  CHECK OK name=cd_absolute
//	  CHECK OK name=mkdir_p_absolute
//	  ...
//	  go build -o /tmp/nova-sandbox ./cmd/nova-sandbox
//	  go run ./tools/sandboxcheck --fill /tmp/nova-sandbox --no-network
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox/darwincheck"
	"github.com/mas-bandwidth/nova-tools/profiles"
)

const usage = `usage: sandboxcheck [--scratch DIR] [--no-network] [--dump-profile] [--fill BIN]

The darwin profile check of nova-sandbox: one CHECK OK / CHECK FAIL line per
check, exit 1 on any FAIL. Darwin only.

  --scratch DIR     scratch tree inside DIR instead of under the working directory
                    (env NOVA_CHECK_SCRATCH)
  --no-network      skip the two DNS checks (env NOVA_CHECK_NO_NETWORK=1)
  --dump-profile    print the filled profile and exit 0 (env NOVA_CHECK_DUMP_PROFILE=1)
  --fill BIN        judge the profile a nova-sandbox binary generates
                    (env NOVA_SANDBOX_FILL)
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, darwincheck.OSSystem{}))
}

// run parses the flags, each defaulting from its environment variable, and runs
// the check.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer, sys darwincheck.System) int {
	fs := flag.NewFlagSet("sandboxcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scratch := fs.String("scratch", getenv("NOVA_CHECK_SCRATCH"), "")
	noNet := fs.Bool("no-network", getenv("NOVA_CHECK_NO_NETWORK") == "1", "")
	dump := fs.Bool("dump-profile", getenv("NOVA_CHECK_DUMP_PROFILE") == "1", "")
	fill := fs.String("fill", getenv("NOVA_SANDBOX_FILL"), "")
	help := fs.Bool("h", false, "")
	fs.BoolVar(help, "help", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "sandboxcheck: %v\n%s", err, usage)
		return 2
	}
	if *help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "sandboxcheck: unexpected argument %q\n%s", fs.Arg(0), usage)
		return 2
	}
	return darwincheck.Run(darwincheck.Options{
		Template:    profiles.DarwinTemplate,
		Scratch:     *scratch,
		NoNetwork:   *noNet,
		DumpProfile: *dump,
		Fill:        *fill,
		Stdout:      stdout,
		Stderr:      stderr,
	}, sys)
}

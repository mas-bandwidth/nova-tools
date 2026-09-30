package main

import (
	"flag"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "runner-share",
		summary: "take this runner's share of the machine's cores (GOMAXPROCS)",
		help: `ci runner-share

Several runners share each machine, and go test defaults GOMAXPROCS to every core it
can see, so concurrent legs each ask for the whole machine and spend the difference
context-switching. A leg takes its FAIR SHARE instead: the cores divided by the runners
on the machine ($NOVA_RUNNERS_PER_MACHINE, default 8, refused when not a positive
number), never below 1 and never above 2. go test -p follows GOMAXPROCS, so this bounds
both the package-level and the in-package parallelism with one number.

Prints "<cores> cores on this machine, <runners> runners per machine
(NOVA_RUNNERS_PER_MACHINE), this leg takes <share> (at most 2)", so a leg's own log
answers what it divided by, and appends GOMAXPROCS=<share> to $GITHUB_ENV.

Exit 0 written, 1 $GITHUB_ENV cannot be written, 2 bad usage.

example:
  NOVA_RUNNERS_PER_MACHINE=4 go run ./tools/ci runner-share
`,
		do: func(e env, args []string) int { return runnerShareVerb(e, args, selRealHost()) },
	})
}

func runnerShareVerb(e env, args []string, h selHost) int {
	const name = "runner-share"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	cores := h.cores()
	runners, share := pkgselect.RunnerShare(cores, e.getenv(pkgselect.RunnersEnv))
	fmt.Fprintf(e.stdout, "%d cores on this machine, %d runners per machine (%s), this leg takes %d (at most %d)\n", cores, runners, pkgselect.RunnersEnv, share, pkgselect.ShareCeiling)
	if err := appendGitHubFile(e.getenv, "GITHUB_ENV", fmt.Sprintf("GOMAXPROCS=%d", share)); err != nil {
		fmt.Fprintf(e.stderr, "runner-share: %v\n", err)
		return 1
	}
	return 0
}

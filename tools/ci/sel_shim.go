package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "unit-tier-shim",
		summary: "put a redis-server that refuses first on PATH for the unit tier",
		help: `ci unit-tier-shim

THE UNIT TIER STARTS NO SERVER (nova-tools#4328; docs/TESTING.md: unit tests mock,
functional tests carry the build tag). Writes $RUNNER_TEMP/unit-tier-bin/redis-server,
a two-line /bin/sh file that prints why on stderr and exits 86, and appends that
directory to $GITHUB_PATH, so a redis-backed test left untagged fails closed under
NOVA_CI=1 instead of starting a real server on a shared runner. The shim stands in for
a binary, which is why it is a file and not a Go program. The functional job installs
the real one.

Prints "redis-server on this leg is <shim> (exit 86)".

Exit 0 written, 1 a file could not be written, 2 bad usage.

example:
  go run ./tools/ci unit-tier-shim
`,
		do: func(e env, args []string) int { return unitTierShimVerb(e, args) },
	})
}

func unitTierShimVerb(e env, args []string) int {
	const name = "unit-tier-shim"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	tmp := e.getenv("RUNNER_TEMP")
	if tmp == "" {
		fmt.Fprintln(e.stderr, "unit-tier-shim: RUNNER_TEMP is not set")
		return 1
	}
	shim, err := pkgselect.WriteUnitShim(tmp)
	if err != nil {
		fmt.Fprintf(e.stderr, "unit-tier-shim: %v\n", err)
		return 1
	}
	dir := filepath.Dir(shim)
	if err := appendGitHubFile(e.getenv, "GITHUB_PATH", dir); err != nil {
		fmt.Fprintf(e.stderr, "unit-tier-shim: %v\n", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "redis-server on this leg is %s (exit %d)\n", shim, pkgselect.UnitShimExit)
	return 0
}

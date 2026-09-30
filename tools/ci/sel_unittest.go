package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "unit-test",
		summary: "run this shard's unit tests through make test",
		help: `ci unit-test --packages "<pkg> <pkg>..."

Runs a unit shard: first it checks that the redis-server first on PATH is the refusing
shim unit-tier-shim wrote ($RUNNER_TEMP/unit-tier-bin/redis-server), so a unit test that
would start a real server fails closed instead; then it runs make test over the shard
(pkgselect.UnitMakeArgs holds the exact arguments). The Makefile's test target tees the
-json stream to $RUNNER_TEMP/test.json and runs the slow-test budget check over it.

Which run it is comes from the environment:
  WHOLE_TREE_SLOWTESTS  slowtests' flags for a run of the whole tree (a push or a manual
                        run); non-empty replaces the Makefile's default budgets
  NIGHTLY_ENFORCE       1 on the nightly reference leg: -count=1 and the budgets enforced

Exit 0 passed, 1 the shim is not first on PATH, else make's own exit status, 2 bad usage.

example:
  go run ./tools/ci unit-test --packages "./cmd/nova-bus ./internal/bus"
`,
		do: func(e env, args []string) int { return unitTestVerb(e, args, selRealHost()) },
	})
}

func unitTestVerb(e env, args []string, h selHost) int {
	const name = "unit-test"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	packages := fs.String("packages", "", "this shard's packages, space-separated")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *packages == "" {
		return selRefuse(e, name, "--packages is required")
	}
	shim := filepath.Join(e.getenv("RUNNER_TEMP"), unitShimDir, "redis-server")
	if found, _ := h.lookPath("redis-server"); found != shim {
		fmt.Fprintf(e.stdout, "unit tier: redis-server on PATH is %s, not the refusing shim\n", found)
		return 1
	}
	argv := pkgselect.UnitMakeArgs(*packages, e.getenv("WHOLE_TREE_SLOWTESTS"), e.getenv("NIGHTLY_ENFORCE") == "1")
	code, err := h.stream(selRoot(e), nil, e.stdout, e.stderr, argv...)
	if err != nil {
		fmt.Fprintf(e.stderr, "unit-test: cannot run make: %v\n", err)
		return 1
	}
	return code
}

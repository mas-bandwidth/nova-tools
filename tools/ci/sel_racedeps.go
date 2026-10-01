package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "race-deps",
		summary: "build the live tree's external dependencies under -race",
		help: `ci race-deps

Builds, under -race, every external package the live tree's tests import (the packages
of other modules: never the standard library, never this module's own), so one saved
build cache serves every shard of the race run. The list is the same on every shard,
whatever the shard holds. Prints "<n> external packages" and then go build's own output.

Exit 0 built, 1 go list or go build failed, 2 bad usage.

example:
  go run ./tools/ci race-deps
`,
		do: func(e env, args []string) int { return raceDepsVerb(e, args, selRealHost()) },
	})
}

func raceDepsVerb(e env, args []string, h selHost) int {
	const name = "race-deps"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	deps, err := pkgselect.RaceDeps(h.run, selRoot(e))
	if err != nil {
		fmt.Fprintf(e.stderr, "race-deps: %v\n", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "%d external packages\n", len(deps))
	if len(deps) == 0 {
		return 0
	}
	argv := append([]string{"go", "build", "-race"}, strings.Fields(strings.Join(deps, "\n"))...)
	code, err := h.stream(selRoot(e), nil, e.stdout, e.stderr, argv...)
	if err != nil {
		fmt.Fprintf(e.stderr, "race-deps: cannot run go build: %v\n", err)
		return 1
	}
	if code != 0 {
		fmt.Fprintf(e.stderr, "race-deps: go build -race exited %d\n", code)
		return 1
	}
	return 0
}

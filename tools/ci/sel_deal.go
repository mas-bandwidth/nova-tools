package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "deal",
		summary: "deal this shard's live packages to HOSTED_PKGS",
		help: `ci deal --shards <n> --shard <i> [--heavy "<pkg> <pkg>..."]

Deals the live packages of the module (go list ./..., less what deprecated/PACKAGES
names) over <n> shards and prints shard <i>'s share as one line of space-separated
packages, and appends HOSTED_PKGS=<that line> to $GITHUB_ENV for the steps after it.

The heavy packages go first, one per shard: --heavy names them by a trailing path
(cmd/nova-bus matches <module>/cmd/nova-bus), the k-th taking shard k. Every other
package then goes round-robin in go list order, continuing after the heavy ones. The
union of the shards is the live tree, each package once.

Exit 0 dealt, 1 go list failed, 2 bad usage.

example:
  go run ./tools/ci deal --shards 6 --shard 1 --heavy "cmd/nova-bus cmd/nova-swarm"
`,
		do: func(e env, args []string) int { return dealVerb(e, args, selRealHost()) },
	})
}

func dealVerb(e env, args []string, h selHost) int {
	const name = "deal"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	shards := fs.Int("shards", 0, "how many shards the tree is dealt over")
	shard := fs.Int("shard", 0, "this shard, 1-based")
	heavy := fs.String("heavy", "", "the heavy packages, space-separated, by trailing path")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *shards < 1 || *shard < 1 || *shard > *shards {
		return selRefuse(e, name, fmt.Sprintf("--shard %d of --shards %d is not a shard (want 1 <= shard <= shards)", *shard, *shards))
	}
	live, err := pkgselect.LiveTree(h.run, selRoot(e))
	if err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	mine, err := pkgselect.Deal(live, strings.Fields(*heavy), *shards, *shard)
	if err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	// One line, each package followed by a space: the line the steps after this
	// one read as $HOSTED_PKGS.
	var line strings.Builder
	for _, p := range mine {
		line.WriteString(p + " ")
	}
	fmt.Fprintln(e.stdout, line.String())
	if err := selAppend(e, "GITHUB_ENV", "HOSTED_PKGS="+line.String()); err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	return 0
}

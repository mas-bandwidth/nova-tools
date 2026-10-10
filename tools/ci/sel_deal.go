package main

import (
	"flag"
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "deal",
		summary: "deal this shard's live packages to HOSTED_PKGS",
		help: `ci deal --shards <n> --shard <i> [--heavy "<pkg> <pkg>..."] [--tags <tag>[,<tag>...]]

Deals the live packages of the module (go list ./..., less what internal/pkgselect/DEPRECATED
names) over <n> shards and prints shard <i>'s share as one line of blank-separated
packages, and appends HOSTED_PKGS=<that line> to $GITHUB_ENV for the steps after it.

The heavy packages go first, one per shard: --heavy names them by a trailing path
(cmd/nova-swarm matches <module>/cmd/nova-swarm), the k-th taking shard k. Every other
package then goes round-robin in go list order, continuing after the heavy ones. The
union of the shards is the live tree, each package once.

--tags narrows the dealt list to the live packages holding a _test.go whose //go:build
expression holds with those tags set and fails without them, the selection a nightly
tagged leg runs (nothing is listed by hand): --tags slow deals the packages a slow
test reaches, --tags functional,slow the packages either tag reaches. The toolchain's
own tags (GOOS, GOARCH, unix) hold in both evaluations. An empty selection deals the
empty line, so its shard prints one line and passes.

Exit 0 dealt, 1 go list failed, 2 bad usage.

example:
  go run ./tools/ci deal --shards 6 --shard 1 --heavy "cmd/nova-swarm internal/ci"
  go run ./tools/ci deal --shards 2 --shard 1 --tags slow
`,
		do: func(e env, args []string) int { return dealVerb(e, args, selRealHost()) },
	})
}

func dealVerb(e env, args []string, h selHost) int {
	const name = "deal"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	shards := fs.Int("shards", 0, "how many shards the tree is dealt over")
	shard := fs.Int("shard", 0, "this shard, 1-based")
	heavy := fs.String("heavy", "", "the heavy packages, blank-separated, by trailing path")
	tags := fs.String("tags", "", "comma-separated build tags; deal only the packages their _test.go files reach")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *shards < 1 || *shard < 1 || *shard > *shards {
		return selRefuse(e, name, fmt.Sprintf("--shard %d of --shards %d is not a shard (want 1 <= shard <= shards)", *shard, *shards))
	}
	root := selRoot(e)
	live, err := pkgselect.LiveTree(h.run, root)
	if err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	if tagList := strings.TrimSpace(*tags); tagList != "" {
		live, err = taggedSelection(root, strings.Split(tagList, ","), live)
		if err != nil {
			fmt.Fprintf(e.stderr, "deal: %v\n", err)
			return 1
		}
	}
	mine, err := pkgselect.Deal(live, strings.Fields(*heavy), *shards, *shard)
	if err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	// One line, each package followed by one blank: the line the steps after this
	// one read as $HOSTED_PKGS.
	var line strings.Builder
	for _, p := range mine {
		line.WriteString(p + " ")
	}
	fmt.Fprintln(e.stdout, line.String())
	if err := appendGitHubFile(e.getenv, "GITHUB_ENV", "HOSTED_PKGS="+line.String()); err != nil {
		fmt.Fprintf(e.stderr, "deal: %v\n", err)
		return 1
	}
	return 0
}

// taggedSelection is the live packages the tags reach: it reads the //go:build
// expression of every _test.go in each live package's directory and asks
// pkgselect.TaggedPackages which packages the tags newly include. The write set
// is the tree's own files, never a list. Reads only happen under --tags, so a
// plain deal touches no file.
func taggedSelection(root string, tags, live []string) ([]string, error) {
	module := pkgselect.ModulePath(root)
	prefix := module + "/"
	var pairs []pkgselect.TaggedPackage
	for _, p := range live {
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(p, prefix)))
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			c, err := firstBuildConstraint(f)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pkgselect.TaggedPackage{Package: p, Constraint: c})
		}
	}
	return pkgselect.TaggedPackages(pairs, tags, hostBaseTags())
}

// firstBuildConstraint returns the //go:build or // +build line that stands
// before path's package clause, or "" when the file carries none: the
// constraint the file is included or dropped by.
func firstBuildConstraint(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if constraint.IsGoBuild(line) || constraint.IsPlusBuild(line) {
			return line, nil
		}
	}
	return "", nil
}

// hostBaseTags are the tags the toolchain sets by itself on this machine: the
// GOOS, the GOARCH, the compiler, and `unix` on a unix system. TaggedPackages
// holds them in both evaluations, so a platform file like `functional && unix`
// is selected for functional here and a `!windows` file is not selected at all.
func hostBaseTags() []string {
	tags := []string{runtime.GOOS, runtime.GOARCH, runtime.Compiler}
	if unixGOOS[runtime.GOOS] {
		tags = append(tags, "unix")
	}
	return tags
}

// unixGOOS is the GOOS set the toolchain tags `unix` (go/build's own list).
var unixGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true,
	"linux": true, "netbsd": true, "openbsd": true, "solaris": true,
}

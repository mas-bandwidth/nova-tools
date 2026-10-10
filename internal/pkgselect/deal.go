package pkgselect

import (
	"fmt"
	"go/build"
	"go/build/constraint"
	"strings"
)

// Deal returns shard's packages (1-based, of shards) from pkgs, the live
// packages in `go list` order (stable: go list sorts).
//
// THE HEAVY PACKAGES FIRST, ONE PER SHARD. heavy names packages by a trailing
// path (`cmd/nova-swarm` matches `<module>/cmd/nova-swarm`); the k-th heavy name
// takes shard k (mod shards), so no two heavy packages share a shard while
// there are shards for them, and every other package goes round-robin in list
// order, continuing after the heavy ones: the first takes shard
// len(heavy) mod shards. A count-only deal kept the heaviest packages together
// and the leg holding them crossed the job cap. If two names match one package
// the later name wins. internal/ci: TestHostedDealSplitsTheHeavyPackages and
// TestCertificationRaceShardsPartitionTheLiveTree.
func Deal(pkgs []string, heavy []string, shards, shard int) ([]string, error) {
	if shards < 1 || shard < 1 || shard > shards {
		return nil, fmt.Errorf("deal: shard %d of %d is not a shard (want 1 <= shard <= shards)", shard, shards)
	}
	var out []string
	r := 0
	for _, p := range pkgs {
		k := 0
		for j, h := range heavy {
			if strings.HasSuffix(p, "/"+h) {
				k = j + 1
			}
		}
		var s int
		if k > 0 {
			s = (k - 1) % shards
		} else {
			s = (len(heavy) + r) % shards
			r++
		}
		if s == shard-1 {
			out = append(out, p)
		}
	}
	return out, nil
}

// TaggedPackage pairs one live package's import path with one //go:build
// expression found on a line of one of its _test.go files. An empty Constraint
// is an untagged test file, which the build includes in every configuration.
type TaggedPackage struct {
	Package    string
	Constraint string
}

// TaggedPackages is the selection behind `ci deal --tags`: the live packages
// that hold a _test.go the build only includes when one of tags is set. A
// package is selected when one of its files' constraints holds with tags and
// the toolchain's own base tags and fails without them; base names what the
// toolchain sets by itself (BaseTags: GOOS, GOARCH, the compiler, cgo when it
// is enabled, `unix` and the GOOS aliases, go1.NN, goexperiment.*), so
// `functional && unix` is selected for functional on a unix host and
// `!functional` is not. An untagged file is true either way and selects
// nothing, and a negated opt-in like `!slow` is true without the tag, so it
// selects nothing either. The result keeps the first-seen order and names each
// package once. A constraint that will not parse is an error, never a silent
// skip that would drop its tests from every leg. The deal itself is Deal, fed
// this list: internal/pkgselect TestDealTags* pins the function and
// nightly-slow.yml deals each leg with `ci deal --tags`.
func TaggedPackages(pairs []TaggedPackage, tags, base []string) ([]string, error) {
	with := tagSet(tags, base)
	without := tagSet(nil, base)
	seen := map[string]bool{}
	var out []string
	for _, p := range pairs {
		if p.Package == "" || p.Constraint == "" || seen[p.Package] {
			continue
		}
		line := p.Constraint
		if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
			line = "//go:build " + line
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("deal: package %s: build constraint %q: %w", p.Package, p.Constraint, err)
		}
		if expr.Eval(with) && !expr.Eval(without) {
			seen[p.Package] = true
			out = append(out, p.Package)
		}
	}
	return out, nil
}

// BaseTags is the tags the toolchain sets by itself for a build context: the
// GOOS, the GOARCH and the compiler; `cgo` when the context has it enabled; the
// portability alias go/build matches from the GOOS (`unix` on a unix GOOS,
// `linux` on android, `solaris` on illumos, `darwin` on ios); the context's
// tool tags (goexperiment.*, arch feature tags like amd64.v1) and release tags
// (go1.NN); and the old `boringcrypto` name for goexperiment.boringcrypto. It
// is what TaggedPackages holds in both of its evaluations and what
// tools/ci/sel_deal.go feeds it from build.Default, so a `slow && cgo`,
// `slow && go1.NN` or `functional && unix` file is selected for the tag on a
// host and toolchain that enable it. Mirrors go/build.Context.matchTag so the
// selection agrees with the compiler about what is already on.
// internal/pkgselect TestDealTagsBaseTags* pins it.
func BaseTags(ctxt build.Context) []string {
	tags := []string{ctxt.GOOS, ctxt.GOARCH, ctxt.Compiler}
	if ctxt.CgoEnabled {
		tags = append(tags, "cgo")
	}
	switch ctxt.GOOS {
	case "android":
		tags = append(tags, "linux")
	case "illumos":
		tags = append(tags, "solaris")
	case "ios":
		tags = append(tags, "darwin")
	}
	if unixGOOS[ctxt.GOOS] {
		tags = append(tags, "unix")
	}
	tags = append(tags, ctxt.ToolTags...)
	for _, t := range ctxt.ToolTags {
		if t == "goexperiment.boringcrypto" {
			tags = append(tags, "boringcrypto")
		}
	}
	tags = append(tags, ctxt.ReleaseTags...)
	return tags
}

// unixGOOS is the GOOS set the toolchain tags `unix` (go/build's own list).
var unixGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true,
	"linux": true, "netbsd": true, "openbsd": true, "solaris": true,
}

// tagSet returns the membership test go/build/constraint's Expr.Eval wants: a
// tag is set when it is one of tags or one of base.
func tagSet(tags, base []string) func(string) bool {
	set := make(map[string]bool, len(tags)+len(base))
	for _, t := range tags {
		set[t] = true
	}
	for _, t := range base {
		set[t] = true
	}
	return func(t string) bool { return set[t] }
}

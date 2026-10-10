package pkgselect

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The CI fan-out: the selected packages dealt onto runner groups. Runner labels
// are carried as single-string fields (os, arch, group), not as a label array:
// a runs-on expression must resolve to a string, so the test job composes its
// label list per leg.
const (
	// LinuxShards and MacShards are the whole-tree shard counts (a push to dev,
	// a manual run, the nightly run). The split is a measurement, not a
	// preference: the two fleets are not symmetric, and the same count on both
	// was the wrong shape once they were not.
	LinuxShards = 8
	MacShards   = 8
	// PullRequestShards is the leg count of each group on a pull request, and
	// MergeGroupMacShards of the macOS group on a merge group, under the
	// two-minute cap: the fan-out cannot be as parallel as the runners a queue
	// would need, so the legs are few and each takes a wave.
	PullRequestShards   = 4
	MergeGroupMacShards = 4
	// PullRequestMacShards is the macOS group's leg count on a pull request: a change that reaches
	// many darwin-sensitive packages (a promotion) dealt four legs of four ran every leg past the
	// two-minute cap (2026-10-04, all four cancelled twice); a small change fills few of the eight.
	PullRequestMacShards = 8
	// FunctionalShards is the leg count of the functional tier. Six: the sprint stream moved
	// slow real-time tests into the tier and four legs ran past the two-minute cap
	// (functional 1/4 and 3/4 were cancelled at it); the cap is permanent, the split is not.
	FunctionalShards = 6
)

// Groups are the labels of the two runner groups the fan-out deals onto. They
// are the workflow's own names (its runs-on labels), so the workflow passes
// them in and no name of a machine or a pool is written here.
type Groups struct{ Linux, Mac string }

// DarwinOnly are the packages with no Linux leg: their sandbox backend is
// macOS-only today (docs/USAGE.md).
var DarwinOnly = []string{"./cmd/nova-sandbox", "./internal/sandbox"}

// LinuxOnly are the packages a pull request never deals to the macOS legs: their unit
// tests cost more than a macOS runner's two cores give in the two-minute cap (cmd/nova-swarm
// about 200 CPU-seconds), so those legs were cancelled at the cap in every pull-request run
// of 2026-10-04. Linux runs them in every pull request and in the merge group. cmd/nova-sprint,
// the other one, left for its own repository, nova-sprint (the split, v1.2.3).
// A push and the nightly run still deal them to macOS.
var LinuxOnly = []string{"./cmd/nova-swarm"}

// HeavyFirst are the measured expensive packages the fan-out deals first, so
// a four-leg PR gives each a separate leg. Ordinary round-robin put all four
// on the first leg; that job crossed its wall even when its test step passed.
var HeavyFirst = []string{"./cmd/nova-swarm", "./internal/ci"}

// DarwinBranches are the target branches whose changes meet the darwin legs:
// the integration branches (the concurrency group's integration list in
// ci.yml, in short form; internal/ci's TestDarwinShardsRunOnlyForIntegrationBranches
// holds the two equal). A change bound for a working branch meets Linux only:
// the Go is the same Go on both OSes, the Linux legs run every selected
// package, and the darwin legs are the slowest and the scarcest.
var DarwinBranches = []string{"main", "dev"}

// DarwinOn reports whether the darwin legs are dealt for a workflow event
// aimed at target: always on schedule and workflow_dispatch; otherwise only
// when target, with any refs/heads/ prefix cut (a merge group's base_ref
// carries it, a pull request's base_ref and a push's ref name do not), is one
// of DarwinBranches.
func DarwinOn(event, target string) bool {
	switch event {
	case "schedule", "workflow_dispatch":
		return true
	}
	target = strings.TrimPrefix(target, "refs/heads/")
	return slices.Contains(DarwinBranches, target)
}

// DropDarwinOnly is pkgs without the packages that have no Linux leg: what a
// run with the darwin legs off selects.
func DropDarwinOnly(pkgs []string) []string {
	kept := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if !slices.Contains(DarwinOnly, p) {
			kept = append(kept, p)
		}
	}
	return kept
}

// Shards is how many legs each runner group deals over.
type Shards struct{ Linux, Mac int }

// OrderHeavyFirst puts the HeavyFirst packages before the rest, each part in
// its own order.
func OrderHeavyFirst(pkgs []string) []string {
	var front, rest []string
	for _, p := range pkgs {
		heavy := false
		for _, h := range HeavyFirst {
			if p == h {
				heavy = true
			}
		}
		if heavy {
			front = append(front, p)
		} else {
			rest = append(rest, p)
		}
	}
	return append(front, rest...)
}

// FunctionalLeg is one entry of the functional tier's matrix.
type FunctionalLeg struct {
	Name     string `json:"name"`
	Packages string `json:"packages"`
}

// Leg is one entry of the unit tier's matrix. The leg name carries its
// platform: two legs sharing one name is two checks GitHub reports under the
// same title, a green and a red that cannot be told apart on the PR.
type Leg struct {
	Name     string `json:"name"`
	Packages string `json:"packages"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Group    string `json:"group"`
}

// NothingLeg is the one leg of a change that touches no Go package: it prints
// "nothing to test for this change" and exits 0.
func NothingLeg(g Groups) Leg {
	return Leg{Name: "nothing", Packages: "", OS: "linux", Arch: "x64", Group: g.Linux}
}

// FunctionalHeavy are the packages whose functional tests run longest on a shared
// runner (measured 28 to 68 s each in the merge-group runs of 2026-10-04, two of
// them landing on one leg ran past the two-minute cap): Functional deals them
// first, in this order, so no leg gets two while another is empty.
var FunctionalHeavy = []string{"./cmd/nova-swarm", "./cmd/nova-bus", "./internal/ci", "./internal/atomicfile", "./internal/ntable", "./internal/swarm"}

// Functional deals the packages into FunctionalShards Linux legs like a pull
// request's unit legs (the darwin-only packages have no Linux leg). The
// functional job reads it on merge_group, schedule and workflow_dispatch only;
// each leg's `make test-functional` runs just the tests behind the functional
// tag. With no package it is one empty leg.
func Functional(pkgs []string, g Groups) []FunctionalLeg {
	pkgs = slices.Clone(pkgs)
	slices.SortStableFunc(pkgs, func(a, b string) int {
		rank := func(p string) int {
			if i := slices.Index(FunctionalHeavy, p); i >= 0 {
				return i
			}
			return len(FunctionalHeavy)
		}
		return cmp.Compare(rank(a), rank(b))
	})
	groups := make([][]string, FunctionalShards)
	f := 0
	for _, p := range pkgs {
		if slices.Contains(DarwinOnly, p) {
			continue
		}
		groups[f%FunctionalShards] = append(groups[f%FunctionalShards], p)
		f++
	}
	var legs []FunctionalLeg
	for i, grp := range groups {
		if len(grp) == 0 {
			continue
		}
		legs = append(legs, FunctionalLeg{Name: fmt.Sprintf("%d/%d %s", i+1, FunctionalShards, g.Linux), Packages: strings.Join(grp, " ")})
	}
	if len(legs) == 0 {
		legs = []FunctionalLeg{{Name: "nothing"}}
	}
	return legs
}

// MarshalLegs is the matrix as compact JSON, in field order.
func MarshalLegs(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the leg types hold only strings
	}
	return string(b)
}

// DarwinSensitive is the set of packages that get a macOS leg on a pull
// request, or All.
//
// A PULL REQUEST'S macOS LEGS ARE FOR THE CODE THAT IS DIFFERENT ON macOS.
// Every PR's waiting jobs needed a macOS runner while the Linux runners sat
// idle, yet most packages compile the same files on darwin as on linux, and
// most import nothing that does not: their macOS leg ran the Go source their
// Linux leg had already run. So on a pull_request a touched package gets a
// macOS leg only when it, or a package it (or its tests) imports from this
// module, compiles a different file set under GOOS=darwin; every other package
// runs on Linux only, the shape the merge_group run has. The whole tree still
// runs on macOS on every push to dev and in certification's `test` job, so
// nothing leaves the macOS evidence; it leaves the PR feedback path. If
// `go list` fails, every touched package keeps its macOS leg.
type DarwinSensitive struct {
	All  bool
	Pkgs map[string]bool
}

// Sorted is the set, sorted, as the line printed for it: each name followed by
// one blank.
func (d DarwinSensitive) Sorted() string {
	var b strings.Builder
	for _, n := range slices.Sorted(maps.Keys(d.Pkgs)) {
		b.WriteString(n + " ")
	}
	return b.String()
}

const darwinFiles = "{{.ImportPath}} {{.GoFiles}} {{.CgoFiles}} {{.TestGoFiles}} {{.XTestGoFiles}}"

// DetectDarwinSensitive reads the file sets of ./cmd/... and ./internal/...
// under GOOS=linux and GOOS=darwin and the test-inclusive imports under
// GOOS=darwin. A `go list` that fails makes every package keep its macOS leg
// (the DarwinSensitive.All shape); the boolean is false then.
func DetectDarwinSensitive(run Runner, root string) (DarwinSensitive, bool, error) {
	modRes, err := run(root, nil, "go", "list", "-m")
	if err != nil {
		return DarwinSensitive{}, false, err
	}
	if modRes.Code != 0 {
		return DarwinSensitive{}, false, fmt.Errorf("go list -m exited %d: %s", modRes.Code, strings.TrimSpace(modRes.Stderr))
	}
	mod := strings.TrimSpace(modRes.Stdout)
	pat := []string{"./cmd/...", "./internal/..."}
	list := func(goos string, args ...string) ([]string, bool) {
		res, err := run(root, []string{"GOOS=" + goos}, append([]string{"go", "list"}, append(args, pat...)...)...)
		if err != nil || res.Code != 0 {
			return nil, false
		}
		return lines(res.Stdout), true
	}
	linux, ok1 := list("linux", "-f", darwinFiles)
	if !ok1 {
		return DarwinSensitive{All: true}, false, nil
	}
	darwin, ok2 := list("darwin", "-f", darwinFiles)
	if !ok2 {
		return DarwinSensitive{All: true}, false, nil
	}
	deps, ok3 := list("darwin", "-test", "-f", "{{.ImportPath}} {{join .Deps \" \"}}")
	if !ok3 {
		return DarwinSensitive{All: true}, false, nil
	}

	// A package differs when its line is in one listing and not the other.
	inLinux, inDarwin := map[string]bool{}, map[string]bool{}
	for _, l := range linux {
		inLinux[l] = true
	}
	for _, l := range darwin {
		inDarwin[l] = true
	}
	differ := map[string]bool{}
	for _, l := range linux {
		if !inDarwin[l] {
			if f := strings.Fields(l); len(f) > 0 {
				differ[f[0]] = true
			}
		}
	}
	for _, l := range darwin {
		if !inLinux[l] {
			if f := strings.Fields(l); len(f) > 0 {
				differ[f[0]] = true
			}
		}
	}

	sens := DarwinSensitive{Pkgs: map[string]bool{}}
	for _, l := range deps {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		p := strings.TrimSuffix(f[0], ".test")
		p = strings.TrimSuffix(p, "_test")
		tokens := append([]string{p}, f[1:]...)
		for _, tok := range tokens {
			if differ[tok] {
				sens.Pkgs["./"+strings.TrimPrefix(p, mod+"/")] = true
				break
			}
		}
	}
	return sens, true, nil
}

// Fanout deals pkgs (already heavy-first) onto the runner groups for event.
//
// On schedule every package runs on the Linux shards, where the unit
// budgets are enforced, and the darwin-only packages have no leg that night
// (the push to dev and certification carry them). On merge_group, and on a
// pull_request for a package that does not need macOS, a package runs on Linux
// only. Everything else, a push to dev and a manual run, runs on both, dealt
// round-robin within each group so every package is covered exactly once per
// OS; the darwin-only packages go to the macOS group only. `sens` is read on a
// pull_request only. With darwin false (DarwinOn said no) every package rides
// the Linux shards and the darwin-only packages have no leg, as on schedule.
func Fanout(event string, pkgs []string, sens DarwinSensitive, g Groups, darwin bool) []Leg {
	sh := Shards{Linux: LinuxShards, Mac: MacShards}
	switch event {
	case "pull_request":
		sh = Shards{Linux: PullRequestShards, Mac: PullRequestMacShards}
	case "merge_group":
		sh = Shards{Linux: LinuxShards, Mac: MergeGroupMacShards}
	}
	linux := make([][]string, sh.Linux)
	mac := make([][]string, sh.Mac)
	s, st := 0, 0
	addLinux := func(p string) { linux[s%sh.Linux] = append(linux[s%sh.Linux], p); s++ }
	for _, p := range pkgs {
		if event == "schedule" || !darwin {
			if slices.Contains(DarwinOnly, p) {
				continue
			}
			addLinux(p)
			continue
		}
		linuxOnly := event == "merge_group" || (event == "pull_request" && (!(sens.All || sens.Pkgs[p]) || slices.Contains(LinuxOnly, p)))
		if linuxOnly && !slices.Contains(DarwinOnly, p) {
			addLinux(p)
			continue
		}
		mac[st%sh.Mac] = append(mac[st%sh.Mac], p)
		st++
		if slices.Contains(DarwinOnly, p) {
			continue
		}
		addLinux(p)
	}
	var legs []Leg
	for i, grp := range linux {
		if len(grp) == 0 {
			continue
		}
		legs = append(legs, Leg{Name: fmt.Sprintf("%d/%d %s", i+1, sh.Linux, g.Linux), Packages: strings.Join(grp, " "), OS: "linux", Arch: "x64", Group: g.Linux})
	}
	for i, grp := range mac {
		if len(grp) == 0 {
			continue
		}
		// The macOS legs run on ARM64, the macOS runners that meet the
		// two-minute cap.
		legs = append(legs, Leg{Name: fmt.Sprintf("%d/%d darwin-arm64", i+1, sh.Mac), Packages: strings.Join(grp, " "), OS: "macOS", Arch: "ARM64", Group: g.Mac})
	}
	return legs
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// Which runner a tree's records are measured with, now that one nova-tla serves every
// repository (runnerTopic says it to the reader). A record's fingerprint holds the
// runner's result files (pkg/tlc: outcome.go, plan.go, run.go, suite.go), so a record is
// current only under the runner that wrote it, and the runner is named by
// tlc.RunnerDigest of those files, never by the repository that built the binary. A
// tree's runner is decided by the tree:
//
//  1. a tree that holds pkg/tlc (nova-tools) is measured with those files:
//     tlc.CheckRunner refuses a binary built from others;
//  2. a tree whose go.mod requires nova-tools (nova-sprint) is measured with the
//     pkg/tlc of the nova-tools its go.mod names: the binary must be built at that
//     module, as `go tool nova-tla` (a go.mod tool line) or `go run
//     <nova-tools>/cmd/nova-tla` from the tree builds it;
//  3. any other tree (a bench copy of tla/ alone, or a repository that pins its runner
//     in its CI workflow, as rocketnet does) is measured with this binary's runner, and
//     every record names it in its fingerprint.

// toolsModule is the module nova-tla and pkg/tlc belong to, read off pkg/tlc's own
// import path so no repository path is written here.
var toolsModule = strings.TrimSuffix(reflect.TypeOf(tlc.Record{}).PkgPath(), "/pkg/tlc")

// version is empty in an ordinary build; a release stamps it with
// -ldflags "-X main.version=<tag>".
var version string

// builtModule is the nova-tools this binary was built from, as path@version (with the
// replacement's when a go.mod replaced it), or "" when the build records none. A
// release names its stamped tag; a build of a nova-tools checkout names the version the
// toolchain stamped from its VCS, which is "(devel)" without one.
func builtModule(stamped string, info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return ""
	}
	if info.Main.Path == toolsModule {
		if stamped != "" {
			return toolsModule + "@" + stamped
		}
		return toolsModule + "@" + info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path != toolsModule {
			continue
		}
		if d.Replace != nil {
			return d.Replace.Path + "@" + d.Replace.Version
		}
		return d.Path + "@" + d.Version
	}
	return ""
}

// treeModule is the nova-tools the go.mod under root names, as builtModule spells it,
// with ok false when root has no go.mod or its go.mod requires no nova-tools.
func treeModule(root string) (string, bool, error) {
	path := filepath.Join(root, "go.mod")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	f, err := modfile.Parse(path, raw, nil)
	if err != nil {
		return "", false, err
	}
	if f.Module != nil && f.Module.Mod.Path == toolsModule {
		return "", false, nil // nova-tools itself: its pkg/tlc is the check (case 1)
	}
	var req string
	for _, r := range f.Require {
		if r.Mod.Path == toolsModule {
			req = r.Mod.Path + "@" + r.Mod.Version
		}
	}
	if req == "" {
		return "", false, nil
	}
	for _, r := range f.Replace {
		if r.Old.Path == toolsModule && (r.Old.Version == "" || toolsModule+"@"+r.Old.Version == req) {
			return r.New.Path + "@" + r.New.Version, true, nil
		}
	}
	return req, true, nil
}

// runnerDigest is this binary's runner, by tlc.RunnerDigest.
func runnerDigest() string {
	files, err := tlc.RunnerFiles()
	if err != nil {
		return "unreadable"
	}
	return tlc.RunnerDigest(files)
}

// checkTreeRunner refuses a binary whose runner is not the tree's (cases 1 and 2
// above), returning the cause; built is builtModule of this binary.
func checkTreeRunner(root, built string) error {
	if err := tlc.CheckRunner(root); err != nil {
		return err
	}
	want, ok, err := treeModule(root)
	if err != nil {
		return fmt.Errorf("the tree's go.mod cannot be read to find the runner it pins: %v", err)
	}
	if !ok || want == built {
		return nil
	}
	have := built
	if have == "" {
		have = "a build that records no nova-tools version"
	}
	return fmt.Errorf("the tree under %s measures its records with the runner of %s (its go.mod), and this nova-tla is %s (runner %s), whose fingerprints may differ; run the tree's own from %s: go tool nova-tla (a go.mod tool line), or go run %s/cmd/nova-tla",
		root, want, have, runnerDigest(), root, toolsModule)
}

// buildModule is builtModule of the running binary.
func buildModule() string {
	info, ok := debug.ReadBuildInfo()
	return builtModule(version, info, ok)
}

// versionExtras are the facts the version line carries after the build identity:
// runner=, the digest of the runner's result files, and module=, the nova-tools
// this binary was built from, when the build records one (runnerTopic says why).
func versionExtras() []string {
	extras := []string{"runner=" + runnerDigest()}
	if m := buildModule(); m != "" && !strings.HasSuffix(m, "@") {
		extras = append(extras, "module="+m)
	}
	return extras
}

// runnerTopic is `nova-tla help runner`. It is built at start, because it names
// the module by toolsModule.
func runnerTopic() string {
	return `nova-tla help runner: which runner a tree's records are measured with.

A record's fingerprint holds the runner's result files (pkg/tlc: outcome.go, plan.go,
run.go, suite.go), so a record is current only under the runner that wrote it. The runner
is named by its digest, never by the repository that built the binary, and the tree
decides which runner is its own:
  1. a tree that holds pkg/tlc (nova-tools): those files; a nova-tla built from others is
     refused (build it from the tree: go build -o /tmp/nova-tla ./cmd/nova-tla);
  2. a tree whose go.mod requires nova-tools (nova-sprint): the nova-tools that go.mod
     names; a nova-tla of another version is refused (run the tree's own, built at the
     pinned version, from the tree: go tool nova-tla <verb> ... with a go.mod tool line,
     or go run ` + toolsModule + `/cmd/nova-tla <verb> ...);
  3. any other tree (a bench copy of tla/ alone, or a repository whose CI pins the runner
     at a nova-tools commit): this binary's runner.
Moving a tree's nova-tools pin to a version with other result files stales every record:
nova-tla groups --stale then names every group.

The version line names the runner: nova-tla <build identity> <goos>/<goarch> <go version>
runner=<12 hex> [module=<path>@<version>]. runner= is the digest of the result files above:
two builds with one runner= give every case one fingerprint, whichever repository built
them. module= is the nova-tools the binary was built from. In a nova-tools tree, run
--bench builds ./cmd/nova-tla for the bench; in a tree that requires nova-tools it builds
` + toolsModule + `/cmd/nova-tla at the version its go.mod names.

first run: nova-tla version
`
}

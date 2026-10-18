package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// Which runner a tree's records are measured with, now that one tlacheck serves every
// repository (helpRunner says it to the reader). A record's fingerprint holds the
// runner's result files (pkg/tlc: outcome.go, plan.go, run.go, suite.go), so a record is
// current only under the runner that wrote it, and the runner is named by
// tlc.RunnerDigest of those files, never by the repository that built the binary. A
// tree's runner is decided by the tree:
//
//  1. a tree that holds pkg/tlc (nova-tools) is measured with those files:
//     tlc.CheckRunner refuses a binary built from others;
//  2. a tree whose go.mod requires nova-tools (nova-sprint) is measured with the
//     pkg/tlc of the nova-tools its go.mod names: the binary must be built at that
//     module, as `go tool tlacheck` (a go.mod tool line) or `go run
//     <nova-tools>/tools/tlacheck` from the tree builds it;
//  3. any other tree (a bench copy of tla/ alone, or a repository that pins its runner
//     in its CI workflow, as rocketnet does) is measured with this binary's runner, and
//     every record names it in its fingerprint.

// toolsModule is the module tlacheck and pkg/tlc belong to, read off pkg/tlc's own
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
	return fmt.Errorf("the tree under %s measures its records with the runner of %s (its go.mod), and this tlacheck is %s (runner %s), whose fingerprints may differ; run the tree's own from %s: go tool tlacheck (a go.mod tool line), or go run %s/tools/tlacheck",
		root, want, have, runnerDigest(), root, toolsModule)
}

// buildModule is builtModule of the running binary.
func buildModule() string {
	info, ok := debug.ReadBuildInfo()
	return builtModule(version, info, ok)
}

func cmdVersion(e env, args []string) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(e.stdout, helpVersion)
		return 0
	}
	if len(args) > 0 {
		return refuse(e, "version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)), tool+" help version")
	}
	extras := []string{"runner=" + runnerDigest()}
	if m := buildModule(); m != "" && !strings.HasSuffix(m, "@") {
		extras = append(extras, "module="+m)
	}
	fmt.Fprintln(e.stdout, buildinfo.Line(tool, version, extras...))
	return 0
}

const helpVersion = `tlacheck version: print which build is running, and its runner.

usage: tlacheck version         (also: tlacheck --version)

One line: tlacheck <build identity> <goos>/<goarch> <go version> runner=<12 hex>
[module=<path>@<version>]. runner= is the digest of the runner's result files
(pkg/tlc: outcome.go, plan.go, run.go, suite.go), the part of the runner every
record's fingerprint holds: two builds with one runner= give every case one
fingerprint, whichever repository built them. module= is the nova-tools the binary
was built from.

output: the one line, on stdout. Exit 0, or 2 given any argument.
first run: tlacheck version
`

// helpRunner is built at start, because it names the module by toolsModule.
var helpRunner = `WHICH RUNNER A TREE'S RECORDS ARE MEASURED WITH. A record's fingerprint holds
the runner's result files, so a record is current only under the runner that wrote it.
The runner is named by its digest (tlacheck version prints runner=), never by the
repository that built the binary, and the tree decides which runner is its own:
  1. a tree that holds pkg/tlc (nova-tools): those files; a tlacheck built from
     others is refused (build it from the tree: go build -o /tmp/tlacheck ./tools/tlacheck);
  2. a tree whose go.mod requires nova-tools (nova-sprint): the nova-tools that go.mod
     names; a tlacheck of another version is refused (run the tree's own, built at the
     pinned version, from the tree: go tool tlacheck <verb> ... with a go.mod tool line,
     or go run ` + toolsModule + `/tools/tlacheck <verb> ...);
  3. any other tree (a bench copy of tla/ alone, or a repository whose CI pins the
     runner at a nova-tools commit): this binary's runner.
Moving a tree's nova-tools pin to a version with other result files stales every record:
tlacheck groups --stale then names every group.
`

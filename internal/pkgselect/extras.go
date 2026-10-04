package pkgselect

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// LiveTree lists every package of the module (`go list ./...`) that is not
// deprecated, as full import paths in `go list` order: the list the hosted deal
// and the race-dependency build read.
func LiveTree(run Runner, root string) ([]string, error) {
	res, err := run(root, nil, "go", "list", "./...")
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("go list ./... exited %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	dep, err := LoadDeprecated(root)
	if err != nil {
		return nil, err
	}
	return dep.Live(lines(res.Stdout)), nil
}

// RaceDeps lists every external package the live tree's tests import (the
// packages of other modules, never the standard library and never this
// module's own), sorted and unique: the packages to build under -race so one
// cache entry serves every shard. It is the same list on every shard,
// whatever the shard holds.
func RaceDeps(run Runner, root string) ([]string, error) {
	live, err := LiveTree(run, root)
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return nil, fmt.Errorf("go list ./... listed no live package, so there are no dependencies to build")
	}
	args := append([]string{"go", "list", "-deps", "-test", "-f", "{{if not .Standard}}{{if not .Module.Main}}{{.ImportPath}}{{end}}{{end}}"}, live...)
	res, err := run(root, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("go list -deps -test exited %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	seen := map[string]bool{}
	var deps []string
	for _, l := range lines(res.Stdout) {
		if strings.TrimSpace(l) == "" || seen[l] {
			continue
		}
		seen[l] = true
		deps = append(deps, l)
	}
	sort.Strings(deps)
	return deps, nil
}

// PerfRun is one live package that holds a perf-tagged test: the tests only the
// perf tag adds, as the regexp `go test -run` takes.
type PerfRun struct {
	Package string
	Run     string
}

var (
	perfBuildLine = regexp.MustCompile(`(?m)^//go:build .*\bperf\b`)
	testNameLine  = regexp.MustCompile(`^(Test|Benchmark|Example|Fuzz)`)
)

// PerfRuns finds the perf-tagged tests. A perf-tagged package with no test
// behind the tag is reported in notes and skipped; the caller refuses a tree
// with no perf-tagged test at all, because then the perf job asserts nothing.
// Only a live package is considered.
func PerfRuns(run Runner, root string) (runs []PerfRun, notes []string, err error) {
	res, err := run(root, nil, "go", "list", "-tags", "perf", "-f", "{{.ImportPath}} {{.Dir}}", "./...")
	if err != nil {
		return nil, nil, err
	}
	if res.Code != 0 {
		return nil, nil, fmt.Errorf("go list -tags perf exited %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	dep, err := LoadDeprecated(root)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range lines(res.Stdout) {
		pkg, dir, _ := strings.Cut(strings.TrimSpace(l), " ")
		if pkg == "" || !dep.LivePackage(pkg) {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		holds := false
		for _, f := range files {
			if b, err := os.ReadFile(f); err == nil && perfBuildLine.Match(b) {
				holds = true
				break
			}
		}
		if !holds {
			continue
		}
		var base, tagged map[string]bool
		for _, argv := range [][]string{
			{"go", "test", "-list", ".", pkg},
			{"go", "test", "-tags", "perf", "-list", ".", pkg},
		} {
			res, err := run(root, nil, argv...)
			if err != nil {
				return nil, nil, err
			}
			if res.Code != 0 {
				return nil, nil, fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), res.Code, strings.TrimSpace(res.Stderr))
			}
			names := map[string]bool{}
			for _, l := range lines(res.Stdout) {
				if testNameLine.MatchString(l) {
					names[l] = true
				}
			}
			if argv[2] == "-tags" {
				tagged = names
			} else {
				base = names
			}
		}
		var extra []string
		for n := range tagged {
			if !base[n] {
				extra = append(extra, n)
			}
		}
		sort.Strings(extra)
		if len(extra) == 0 {
			notes = append(notes, pkg+" carries a perf constraint and no test behind it")
			continue
		}
		notes = append(notes, pkg+": "+strings.Join(extra, " "))
		runs = append(runs, PerfRun{Package: pkg, Run: "^(" + strings.Join(extra, "|") + ")$"})
	}
	return runs, notes, nil
}

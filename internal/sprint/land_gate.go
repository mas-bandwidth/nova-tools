package sprint

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// LandGoBudget bounds one go run in the clone: a build of the module, a vet, a test of
// the tree's own packages, or one update run (a build and two tests of one package).
const LandGoBudget = 15 * time.Minute

// TreeTests are the packages that test the tree itself (its docs and its tests), run by
// the gate when tests are asked; one the clone lacks is not run.
var TreeTests = []string{"internal/docs", "internal/ci"}

// TreeTested says a change to p is one the tree tests read: a Go file, a document, under
// testdata, go.mod, or go.sum.
func TreeTested(p string) bool {
	return strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md") || strings.Contains(p, "testdata/") || p == "go.mod" || p == "go.sum"
}

// PkgArg formats p as a package argument for go test (e.g. ./dir/).
func PkgArg(p string) string {
	p = filepath.Clean(filepath.ToSlash(p))
	if p == "." {
		return "./"
	}
	p = strings.TrimPrefix(p, "./")
	return "./" + strings.TrimSuffix(p, "/") + "/"
}

// HasCI reports whether have holds internal/ci.
func HasCI(have []string) bool {
	return slices.ContainsFunc(have, func(p string) bool {
		p = strings.Trim(filepath.Clean(filepath.ToSlash(p)), "./")
		return p == "internal/ci"
	})
}

// FunctionalChecks are the whole-tree checks run on internal/ci when the clone holds it.
const FunctionalChecks = "^(TestUncheckedErrors|TestStaticcheckFindings|TestDeadCode|TestEveryCommandMeetsTheOnboardingStandard)$"

// GateRuns is the tree gate's runs, in order: the build and the vet of the module, then
// the packages to test when tests is asked, plus the functional whole-tree checks on
// internal/ci when the clone holds it.
func GateRuns(tests bool, have []string) [][]string {
	runs := [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}
	if tests && len(have) > 0 {
		run := []string{"go", "test"}
		for _, p := range have {
			run = append(run, PkgArg(p))
		}
		runs = append(runs, run)
		if HasCI(have) {
			runs = append(runs, []string{"go", "test", "-tags", "functional", "-run", FunctionalChecks, "./internal/ci/"})
		}
	}
	return runs
}

// GateWhy is a red run as a finding, one line: the run, how it ended and its output.
func GateWhy(run []string, err error, out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(run, " ") + ": " + oneline.Err(err) + ": " + oneline.Cap(strings.Join(lines, " | "), 1500)
}

// ReadonlyGoFlags returns GOFLAGS=... with -mod=readonly set, preserving any other
// flags from env's GOFLAGS entries and dropping any existing -mod or -mod=... flag.
func ReadonlyGoFlags(env []string) string {
	var terms []string
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "GOFLAGS="); ok {
			for _, term := range strings.Fields(val) {
				if !strings.HasPrefix(term, "-mod=") && term != "-mod" {
					terms = append(terms, term)
				}
			}
		}
	}
	terms = append(terms, "-mod=readonly")
	return "GOFLAGS=" + strings.Join(terms, " ")
}

// WithEnv is env with each of set (NAME=value) in place of the NAME it held, else added;
// duplicate entries of NAME in env are dropped.
func WithEnv(env []string, set ...string) []string {
	out := slices.Clone(env)
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		prefix := name + "="
		first := slices.IndexFunc(out, func(e string) bool { return strings.HasPrefix(e, prefix) })
		if first >= 0 {
			out[first] = kv
			seen := false
			out = slices.DeleteFunc(out, func(e string) bool {
				if strings.HasPrefix(e, prefix) {
					if !seen {
						seen = true
						return false
					}
					return true
				}
				return false
			})
		} else {
			out = append(out, kv)
		}
	}
	return out
}

// TreePackages are the treeTests the clone at dir holds as Go packages: a directory with
// no .go file in it (the nova-sprint repo's internal/ci holds only data, 2026-10-05) is no
// package, and `go test` of it fails every batch on that base, so it is not run.
func TreePackages(dir string) []string {
	var have []string
	for _, p := range TreeTests {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p), "*.go"))
		if len(matches) > 0 {
			have = append(have, p)
		}
	}
	return have
}

// LandPkgInfo describes a Go package in the clone.
type LandPkgInfo struct {
	RelDir     string
	ImportPath string
	Deps       map[string]bool
}

// GatePackages finds the packages to test for the gate: every package touched by changed
// files plus every package that imports one of them (direct and transitive importers),
// plus the tree tests the clone holds (TreeTests).
// If go list fails, it returns (nil, why) with the error as the gate's finding (never silently falling back).
func GatePackages(ctx context.Context, dir string, changed []string, tests bool, goRun func(ctx context.Context, dir string, run []string, set ...string) (string, error)) ([]string, string) {
	treeHave := TreePackages(dir)
	if !tests {
		return nil, ""
	}
	if len(changed) == 0 {
		return treeHave, ""
	}

	if goRun == nil {
		goRun = DefaultGoRun
	}
	listCmd := []string{"go", "list", "-f", "{{.Dir}}\t{{.ImportPath}}\t{{range .Deps}}{{.}} {{end}}{{range .TestImports}}{{.}} {{end}}{{range .XTestImports}}{{.}} {{end}}", "./..."}
	out, err := goRun(ctx, dir, listCmd)
	if err != nil {
		return nil, GateWhy(listCmd, err, out)
	}

	var pkgs []LandPkgInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		rel, err := filepath.Rel(dir, parts[0])
		if err != nil {
			continue
		}
		relDir := filepath.ToSlash(rel)
		deps := map[string]bool{}
		if len(parts) > 2 {
			for _, d := range strings.Fields(parts[2]) {
				deps[d] = true
			}
		}
		pkgs = append(pkgs, LandPkgInfo{
			RelDir:     relDir,
			ImportPath: parts[1],
			Deps:       deps,
		})
	}

	allTouched := false
	for _, raw := range changed {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		f := filepath.Clean(filepath.ToSlash(raw))
		if f == "go.mod" || f == "go.sum" {
			allTouched = true
			break
		}
	}

	touched := map[string]bool{}
	if allTouched {
		for _, p := range pkgs {
			touched[p.ImportPath] = true
		}
	} else {
		for _, raw := range changed {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			f := filepath.Clean(filepath.ToSlash(raw))
			if p := EnclosingPackage(f, pkgs); p != nil {
				touched[p.ImportPath] = true
			}
		}
	}

	selected := map[string]bool{}
	for _, p := range pkgs {
		if touched[p.ImportPath] {
			selected[p.RelDir] = true
		}
	}
	for {
		added := false
		for _, p := range pkgs {
			if selected[p.RelDir] {
				continue
			}
			for _, other := range pkgs {
				if selected[other.RelDir] && p.Deps[other.ImportPath] {
					selected[p.RelDir] = true
					touched[p.ImportPath] = true
					added = true
					break
				}
			}
		}
		if !added {
			break
		}
	}

	for _, t := range treeHave {
		selected[t] = true
	}

	var outDirs []string
	for d := range selected {
		outDirs = append(outDirs, d)
	}
	slices.Sort(outDirs)
	return outDirs, ""
}

// EnclosingPackage returns the package whose directory is the longest match for f.
func EnclosingPackage(f string, pkgs []LandPkgInfo) *LandPkgInfo {
	fDir := filepath.Dir(f)
	var best *LandPkgInfo
	for i := range pkgs {
		p := &pkgs[i]
		if p.RelDir == "." {
			if fDir == "." && (best == nil || best.RelDir == ".") {
				best = p
			}
		} else {
			if fDir == p.RelDir || strings.HasPrefix(fDir, p.RelDir+"/") {
				if best == nil || len(p.RelDir) > len(best.RelDir) {
					best = p
				}
			}
		}
	}
	return best
}

// TreeGate runs the gate on the clone's tree, the tests too when tests: "" when it
// is green or the clone has no module, else the finding (GateWhy).
// (docs/SPEC-SPRINT.md section 7, the tree gate).
func TreeGate(ctx context.Context, dir string, changed []string, tests bool, goRun func(ctx context.Context, dir string, run []string, set ...string) (string, error)) string {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	if goRun == nil {
		goRun = DefaultGoRun
	}
	have, why := GatePackages(ctx, dir, changed, tests, goRun)
	if why != "" {
		return why
	}
	for _, run := range GateRuns(tests, have) {
		if out, err := goRun(ctx, dir, run); err != nil {
			return GateWhy(run, err, out)
		}
	}
	return ""
}

// DefaultGoRun runs one go command in dir under GOFLAGS=-mod=readonly.
func DefaultGoRun(ctx context.Context, dir string, run []string, set ...string) (string, error) {
	b := subproc.Prepare(ctx, LandGoBudget, run[0], run[1:]...)
	defer b.Cancel()
	env := os.Environ()
	b.Cmd.Dir = dir
	b.Cmd.Env = WithEnv(env, append([]string{ReadonlyGoFlags(env)}, set...)...)
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}

package sprint

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The lander's tree gate, the part that decides which go runs a merged tip must
// pass (docs/SPEC-SPRINT.md section 7, the tree gate). The module always builds
// and vets. A merged head also tests every package under a directory the merge
// changes and every package that imports one of those directly, plus the packages
// that test the tree itself, plus the four whole-tree functional checks when the
// clone holds internal/ci as a Go package. A file in the module root selects the
// root package only, when the root is a package, and not every nested package:
// a root edit must not become a whole-module test. The runs are bounded by the
// lander's per-run budget; this package names them, it does not start them.

const (
	// FunctionalTreeRun is the four whole-tree checks. They read the tree; none
	// of them opens a store.
	FunctionalTreeRun = `^(TestUncheckedErrors|TestStaticcheckFindings|TestDeadCode|TestEveryCommandMeetsTheOnboardingStandard)$`
	// FunctionalTreeTimeout is that run's own deadline, inside the lander's
	// per-run budget so the run reports a timeout instead of being killed.
	FunctionalTreeTimeout = "14m"
)

// goListFormat is one package per line: import path, absolute dir, and the
// import paths it builds and tests, tab-separated. Commas join the import
// paths; an empty list is an empty field.
const goListFormat = `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{join .Imports ","}}{{"\t"}}{{join .TestImports ","}}{{"\t"}}{{join .XTestImports ","}}`

// GoListArgv is the list the gate reads. It is the module's packages, not
// `go list -deps` of the standard library; the reverse edges are taken from
// each package's import lists.
func GoListArgv() []string {
	return []string{"go", "list", "-f", goListFormat, "./..."}
}

// ModulePackage is one package of the module the gate is reading.
// Dir is relative to the module root, slash-separated, "." for the root.
// Imports are the import paths of the packages it builds and tests.
type ModulePackage struct {
	Import  string
	Dir     string
	Imports []string
}

// ReadGoList reads GoListArgv's stdout. root is the module root. A package
// whose directory is outside root is dropped. A line that is not five fields
// is an error: a list the gate cannot read is not an empty module.
func ReadGoList(out, root string) ([]ModulePackage, error) {
	root = canonPath(root)
	var pkgs []ModulePackage
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			return nil, errGoListLine(line)
		}
		dir := f[1]
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		rel, err := filepath.Rel(root, canonPath(dir))
		if err != nil {
			return nil, err
		}
		rel = path.Clean(filepath.ToSlash(rel))
		if rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		var imports []string
		for _, field := range f[2:] {
			for _, imp := range strings.Split(field, ",") {
				if imp != "" {
					imports = append(imports, imp)
				}
			}
		}
		pkgs = append(pkgs, ModulePackage{Import: f[0], Dir: rel, Imports: imports})
	}
	return pkgs, nil
}

func errGoListLine(line string) error {
	return &goListLineError{line: line}
}

type goListLineError struct{ line string }

func (e *goListLineError) Error() string {
	return "go list line is not five fields: " + e.line
}

// TreeGatePkgs is who `go test` runs for a merge that changed these files.
// have are the tree packages the clone holds, in the order they are named,
// and they lead the result. Then every package under a changed file's
// directory (the root selects only the root package), the nearest package
// that contains a file whose directory is not itself a package, and every
// package that imports one of those directly. The rest is sorted.
func TreeGatePkgs(changed []string, pkgs []ModulePackage, have []string) []string {
	byDir := map[string]ModulePackage{}
	for _, p := range pkgs {
		if p.Dir == "" {
			continue
		}
		if _, ok := byDir[p.Dir]; !ok {
			byDir[p.Dir] = p
		}
	}
	known := map[string]bool{}
	for d := range byDir {
		known[d] = true
	}
	touched := map[string]bool{}
	for _, file := range changed {
		file = path.Clean(filepath.ToSlash(file))
		if file == "." || file == "" {
			continue
		}
		dir := path.Dir(file)
		for _, d := range underDir(dir, known) {
			touched[d] = true
		}
		if d, ok := nearestPkg(dir, known); ok {
			touched[d] = true
		}
	}
	want := map[string]bool{}
	for d := range touched {
		if p, ok := byDir[d]; ok && p.Import != "" {
			want[p.Import] = true
		}
	}
	extra := map[string]bool{}
	for _, p := range pkgs {
		if p.Dir == "" || touched[p.Dir] {
			continue
		}
		for _, imp := range p.Imports {
			if want[imp] {
				extra[p.Dir] = true
				break
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, h := range have {
		add(h)
	}
	var rest []string
	for d := range touched {
		rest = append(rest, d)
	}
	for d := range extra {
		rest = append(rest, d)
	}
	slices.Sort(rest)
	for _, d := range rest {
		add(d)
	}
	return out
}

// underDir is the packages at dir or nested under it. The module root selects
// only itself: a file there must not pull in every nested package.
func underDir(dir string, known map[string]bool) []string {
	if dir == "." {
		if known["."] {
			return []string{"."}
		}
		return nil
	}
	var out []string
	for d := range known {
		if d == dir || strings.HasPrefix(d, dir+"/") {
			out = append(out, d)
		}
	}
	return out
}

func nearestPkg(dir string, known map[string]bool) (string, bool) {
	for d := dir; ; d = path.Dir(d) {
		if known[d] {
			return d, true
		}
		if d == "." {
			return "", false
		}
	}
}

// TreeGateArgv is the gate's commands, in order: build, vet, one `go test` of
// pkgs when there are any, then the four functional checks when functional.
func TreeGateArgv(pkgs []string, functional bool) [][]string {
	runs := [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}
	if args := testArgs(pkgs); len(args) > 0 {
		runs = append(runs, append([]string{"go", "test"}, args...))
	}
	if functional {
		runs = append(runs, []string{"go", "test", "-tags", "functional", "-run", FunctionalTreeRun, "-timeout", FunctionalTreeTimeout, "./internal/ci/"})
	}
	return runs
}

func testArgs(pkgs []string) []string {
	var args []string
	for _, p := range pkgs {
		switch {
		case p == ".":
			args = append(args, ".")
		case p == "" || strings.HasPrefix(p, "-") || strings.Contains(p, "..") || strings.ContainsAny(p, " \t\\"):
			continue
		default:
			args = append(args, "./"+path.Clean(p)+"/")
		}
	}
	return args
}

// GateFinding is one red run as the gate says it: the command, how it ended,
// and its output on one line.
func GateFinding(run []string, err error, out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(run, " ") + ": " + oneline.Err(err) + ": " + oneline.Cap(strings.Join(lines, " | "), 1500)
}

// FirstGateFinding runs the gate's commands in order and returns the finding
// of the first red one. "" when every run is green. The caller bisects on a
// non-empty finding: the head comes off the batch branch.
func FirstGateFinding(runs [][]string, run func([]string) (string, error)) string {
	for _, argv := range runs {
		out, err := run(slices.Clone(argv))
		if err != nil {
			return GateFinding(argv, err, out)
		}
	}
	return ""
}

func canonPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

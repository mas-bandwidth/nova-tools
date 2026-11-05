package boundary

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/mas-bandwidth/nova-tools"

// allowedTop is every entry that may exist at the top level of the repository.
// Anything else fails the test; see docs/BOUNDARY.md before extending it.
var allowedTop = map[string]bool{
	// tools and support directories
	"bus": true, "config": true, "sandbox": true, "secrets": true, "reserve": true, "cairn": true,
	"fleet": true, "tla": true, "docs": true, "boundary": true, "contract": true, ".github": true,
	// version control
	".git": true,
	// root files: exactly these, nothing else (nothing in root unless a user cares about it)
	"README.md": true, "LICENSE": true, "go.mod": true, "go.sum": true,
	".gitignore": true, ".gitattributes": true,
}

// libraryDirs are named in the failure message when present.
var libraryDirs = map[string]bool{"pkg": true, "internal": true, "cmd": true}

// mainTools are the tools that are one package main and import nothing from
// this module.
var mainTools = []string{"bus", "config", "sandbox", "secrets", "reserve", "cairn"}

// allowedTools is the exact set of tool directives permitted in go.mod.
var allowedTools = []string{
	"github.com/kisielk/errcheck",
	"golang.org/x/exp/cmd/txtar",
	"golang.org/x/tools/cmd/deadcode",
	"honnef.co/go/tools/cmd/staticcheck",
}

// allowedRequires is the exact set of direct dependencies in go.mod.
var allowedRequires = []string{
	"github.com/jackc/pgx/v5",
	"github.com/redis/go-redis/v9",
	"github.com/stretchr/testify",
	"golang.org/x/sys",
	"gopkg.in/yaml.v3",
}

func root(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestTopLevelShape(t *testing.T) {
	base := root(t)
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if fi, err := os.Lstat(filepath.Join(base, name)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s: top-level entries must not be symlinks; see docs/BOUNDARY.md", name)
			continue
		}
		if allowedTop[name] {
			continue
		}
		if libraryDirs[name] {
			t.Errorf("%s: nova-tools has no library layer: each tool is one top-level package main; see docs/BOUNDARY.md", name)
			continue
		}
		if strings.HasSuffix(name, ".md") {
			t.Errorf("%s: root holds README.md only; docs go in docs/", name)
			continue
		}
		t.Errorf("%s: not an allowed top-level entry; see docs/BOUNDARY.md", name)
	}
}

func TestNoSymlinks(t *testing.T) {
	base := root(t)
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" || e.Name() == "docs" {
			continue
		}
		filepath.WalkDir(filepath.Join(base, e.Name()), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				t.Errorf("%s: %v", p, err)
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				rel, _ := filepath.Rel(base, p)
				t.Errorf("%s is a symlink: no symlinks anywhere in the repository; see docs/BOUNDARY.md", rel)
			}
			return nil
		})
	}
}

// goFiles returns every .go file under dir (symlinks are not followed).
// docs/ is never looked inside: it may hold assets and anything else.
func goFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == "docs" {
			return fs.SkipDir
		}
		if err == nil && d.Type().IsRegular() && strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// imports parses a file's package name and import paths.
func imports(t *testing.T, f string) (string, []string) {
	t.Helper()
	af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
	if err != nil {
		t.Errorf("%s: %v", f, err)
		return "", nil
	}
	var out []string
	for _, imp := range af.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		out = append(out, p)
	}
	return af.Name.Name, out
}

func isOwn(p string) bool { return p == modulePath || strings.HasPrefix(p, modulePath+"/") }

func TestToolsAreIndependentMains(t *testing.T) {
	base := root(t)
	for _, tool := range mainTools {
		files := goFiles(filepath.Join(base, tool))
		if len(files) == 0 {
			t.Errorf("%s: no Go files (is it a symlink or missing?)", tool)
		}
		for _, f := range files {
			rel, _ := filepath.Rel(base, f)
			pkg, imps := imports(t, f)
			if pkg != "main" {
				t.Errorf("%s: package %s, want main", rel, pkg)
			}
			for _, p := range imps {
				if isOwn(p) {
					t.Errorf("%s imports %s: tools do not import each other or any shared package; see docs/BOUNDARY.md", rel, p)
				}
			}
		}
	}
}

// fleet holds Ansible plays; its Go files are doc.go and tests only.
func TestFleetHasNoCode(t *testing.T) {
	base := root(t)
	files := goFiles(filepath.Join(base, "fleet"))
	if len(files) == 0 {
		t.Error("fleet: no Go files (is it a symlink or missing?)")
	}
	for _, f := range files {
		rel, _ := filepath.Rel(base, f)
		pkg, imps := imports(t, f)
		if pkg == "main" {
			t.Errorf("%s: fleet holds plays, not a program: no package main", rel)
		}
		for _, p := range imps {
			if isOwn(p) {
				t.Errorf("%s imports %s: fleet imports nothing from this module", rel, p)
			}
		}
	}
}

// parseGoMod returns the require entries (module, marked indirect), replace
// directives and tool entries of a go.mod.
func parseGoMod(gomod string) (reqs map[string]bool, replaces, tools []string) {
	reqs = map[string]bool{}
	block := ""
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
		} else if i := strings.Index(line, " ("); line != "" && i > 0 && strings.HasSuffix(line, "(") {
			block = line[:i]
			continue
		}
		verb, spec := block, line
		if block == "" {
			f := strings.SplitN(line, " ", 2)
			if len(f) < 2 {
				continue
			}
			verb, spec = f[0], f[1]
		}
		if spec == "" || strings.HasPrefix(spec, "//") {
			continue
		}
		f := strings.Fields(spec)
		switch verb {
		case "require":
			if len(f) >= 2 {
				reqs[f[0]] = strings.Contains(spec, "// indirect")
			}
		case "replace":
			replaces = append(replaces, spec)
		case "tool":
			tools = append(tools, f[0])
		}
	}
	return
}

func TestDirectDependencies(t *testing.T) {
	base := root(t)
	data, err := os.ReadFile(filepath.Join(base, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	reqs, replaces, tools := parseGoMod(string(data))
	for _, r := range replaces {
		t.Errorf("go.mod has a replace directive (%s): replace is not allowed; see docs/BOUNDARY.md", r)
	}
	okTool := map[string]bool{}
	for _, m := range allowedTools {
		okTool[m] = true
	}
	for _, m := range tools {
		if !okTool[m] {
			t.Errorf("go.mod tool %s is not in allowedTools in boundary_test.go: add it in the same PR and say why in docs/BOUNDARY.md", m)
		}
	}
	// A module imported by any .go file is direct whatever its comment says.
	imported := map[string]bool{}
	for _, f := range goFiles(base) {
		_, imps := imports(t, f)
		for _, p := range imps {
			imported[p] = true
		}
	}
	direct := map[string]bool{}
	for m, indirect := range reqs {
		direct[m] = !indirect
		for p := range imported {
			if p == m || strings.HasPrefix(p, m+"/") {
				direct[m] = true
			}
		}
	}
	want := map[string]bool{}
	for _, m := range allowedRequires {
		want[m] = true
	}
	var mods []string
	for m := range direct {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		if !direct[m] {
			continue
		}
		if !want[m] {
			t.Errorf("new direct dependency %s: add it to the list in boundary_test.go in the same PR and say why in docs/BOUNDARY.md", m)
		}
		delete(want, m)
	}
	var gone []string
	for m := range want {
		gone = append(gone, m)
	}
	sort.Strings(gone)
	for _, m := range gone {
		t.Errorf("%s is listed in boundary_test.go but is no longer a direct requirement: remove it from the list", m)
	}
}

var budgetLine = regexp.MustCompile(`^([A-Za-z0-9_-]+) +([0-9]+)$`)

func TestBudgets(t *testing.T) {
	base := root(t)
	data, err := os.ReadFile(filepath.Join(base, "boundary", "BUDGET"))
	if err != nil {
		t.Fatal(err)
	}
	budgets := map[string]int{}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for _, line := range lines {
		m := budgetLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("BUDGET: want exactly \"name  max-lines\" per line (no comments, no blank lines), got %q", line)
		}
		if _, dup := budgets[m[1]]; dup {
			t.Fatalf("BUDGET: duplicate name %s", m[1])
		}
		budgets[m[1]], _ = strconv.Atoi(m[2])
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == ".git" || name == "docs" || len(goFiles(filepath.Join(base, name))) == 0 {
			continue
		}
		if _, ok := budgets[name]; !ok {
			t.Errorf("%s has Go code but no line in BUDGET: add one in the same PR", name)
		}
	}
	for tool, b := range budgets {
		n, err := countLines(filepath.Join(base, tool))
		if err != nil {
			t.Errorf("BUDGET names %s: %v", tool, err)
			continue
		}
		if n > b {
			t.Errorf("%s is %d non-test .go lines, budget %d: a tool over budget means the spec is not minimal; raise the budget only in a PR that says why", tool, n, b)
		}
	}
}

// countLines counts the lines of the non-test .go files under dir.
func countLines(dir string) (int, error) {
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return 0, os.ErrNotExist
	}
	total := 0
	for _, p := range goFiles(dir) {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		total += bytes.Count(b, []byte("\n"))
	}
	return total, nil
}

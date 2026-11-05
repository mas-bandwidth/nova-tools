package boundary

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
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
	"bus": true, "config": true, "sandbox": true, "secrets": true, "slot": true,
	"fleet": true, "tla": true, "docs": true, "boundary": true, ".github": true,
	// version control
	".git": true,
	// root files
	"README.md": true, "CHANGELOG.md": true, "ROADMAP.md": true, "TESTING.md": true,
	"LICENSE": true, "go.mod": true, "go.sum": true, ".gitignore": true,
	".gitattributes": true, "BUDGET": true,
}

// libraryDirs are named in the failure message when present.
var libraryDirs = map[string]bool{"pkg": true, "internal": true, "cmd": true}

// mainTools are the tools that are one package main and import nothing from
// this module.
var mainTools = []string{"bus", "config", "sandbox", "secrets", "slot"}

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
	entries, err := os.ReadDir(root(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if allowedTop[name] {
			continue
		}
		if libraryDirs[name] {
			t.Errorf("%s: nova-tools has no library layer: each tool is one top-level package main; see docs/BOUNDARY.md", name)
			continue
		}
		t.Errorf("%s: not an allowed top-level entry; see docs/BOUNDARY.md", name)
	}
}

func TestToolsAreIndependentMains(t *testing.T) {
	base := root(t)
	for _, tool := range mainTools {
		dir := filepath.Join(base, tool)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Errorf("%s: no Go files", tool)
		}
		fset := token.NewFileSet()
		for _, f := range files {
			af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			if af.Name.Name != "main" {
				t.Errorf("%s: package %s, want main", f, af.Name.Name)
			}
			for _, imp := range af.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
					t.Errorf("%s imports %s: tools do not import each other or any shared package; see docs/BOUNDARY.md", filepath.Join(tool, filepath.Base(f)), p)
				}
			}
		}
	}
}

func TestDirectDependencies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	got := directRequires(string(data))
	want := map[string]bool{}
	for _, m := range allowedRequires {
		want[m] = true
	}
	for _, m := range got {
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

// directRequires returns the modules in require directives not marked indirect.
func directRequires(gomod string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		}
		var spec string
		if inBlock {
			spec = line
		} else if strings.HasPrefix(line, "require ") {
			spec = strings.TrimPrefix(line, "require ")
		} else {
			continue
		}
		if spec == "" || strings.HasPrefix(spec, "//") || strings.Contains(spec, "// indirect") {
			continue
		}
		if f := strings.Fields(spec); len(f) >= 2 {
			out = append(out, f[0])
		}
	}
	return out
}

func TestBudgets(t *testing.T) {
	base := root(t)
	data, err := os.ReadFile(filepath.Join(base, "BUDGET"))
	if err != nil {
		t.Fatal(err)
	}
	budgets := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("BUDGET: want \"name  max-lines\", got %q", line)
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatalf("BUDGET: %q: %v", line, err)
		}
		budgets[f[0]] = n
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !hasNonTestGo(filepath.Join(base, name)) || name == "boundary" {
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
			t.Errorf("%s is %d lines, budget %d: a tool over budget means the spec is not minimal; raise the budget only in a PR that says why", tool, n, b)
		}
	}
}

func hasNonTestGo(dir string) bool {
	n, err := countLines(dir)
	return err == nil && n > 0
}

// countLines counts the lines of the non-test .go files under dir.
func countLines(dir string) (int, error) {
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	total := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += bytes.Count(b, []byte("\n"))
		return nil
	})
	return total, err
}

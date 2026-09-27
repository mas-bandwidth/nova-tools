package ci

import (
	"path"
	"strconv"
	"strings"
	"testing"
)

// A bus adopter must not compile Redis, SQLite or sprint policy merely to
// classify an optional public inbox. Walk every platform's production imports
// transitively: a new intermediate package must not hide the dependency.
func TestBusHasNoStorageOrSprintDependencies(t *testing.T) {
	t.Parallel()
	const module = "github.com/mas-bandwidth/nova-tools/"
	imports := map[string][]string{}
	for _, f := range repoTree(t).GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		if f.ParseErr != nil {
			t.Fatal(f.ParseErr)
		}
		pkg := module + path.Dir(f.Rel)
		if _, ok := imports[pkg]; !ok {
			imports[pkg] = nil
		}
		for _, spec := range f.AST.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			imports[pkg] = append(imports[pkg], name)
		}
	}
	for _, root := range []string{"cmd/nova-bus", "internal/jevclient"} {
		if _, ok := imports[module+root]; !ok {
			t.Fatalf("missing dependency root %s", root)
		}
		visited := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if visited[pkg] {
				return
			}
			visited[pkg] = true
			for _, dep := range imports[pkg] {
				if strings.HasPrefix(dep, module) {
					if root == "internal/jevclient" || dep == module+"internal/decide" || dep == module+"internal/events" || strings.HasPrefix(dep, module+"internal/nsprint/") {
						t.Errorf("%s reaches %s through %s; keep provider transport separate from policy and storage", root, dep, pkg)
					}
					if _, ok := imports[dep]; !ok {
						t.Errorf("cannot inspect local dependency %s", dep)
						continue
					}
					walk(dep)
				} else if first, _, _ := strings.Cut(dep, "/"); strings.Contains(first, ".") {
					t.Errorf("%s reaches third-party dependency %s through %s; the bus and provider transport use the standard library", root, dep, pkg)
				}
			}
		}
		walk(module + root)
	}
}

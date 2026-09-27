package ci

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// All production platform variants are included. Readers may share the wire
// identity, but must not compile the webhook decoder through any intermediary.
func TestGitHubEventReadersDoNotImportIngestion(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := ""
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			module = strings.Trim(fields[1], "\"") + "/"
			break
		}
	}
	if module == "" {
		t.Fatal("go.mod has no module directive")
	}
	wire := module + "internal/ghevent/wire"
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
			dep, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			imports[pkg] = append(imports[pkg], dep)
		}
	}
	for _, root := range []string{"internal/gh", "internal/wake", "cmd/nova-wake", "internal/ghevent/wire"} {
		if _, ok := imports[module+root]; !ok {
			t.Fatalf("missing root %s", root)
		}
		seen := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if seen[pkg] {
				return
			}
			seen[pkg] = true
			for _, dep := range imports[pkg] {
				if pkg == wire {
					t.Errorf("wire identity imports %s; keep the contract import-free", dep)
				}
				if dep == module+"internal/ghevent" {
					t.Errorf("%s reaches webhook ingestion through %s; depend on the wire contract", root, pkg)
				}
				if strings.HasPrefix(dep, module) {
					if _, ok := imports[dep]; !ok {
						t.Errorf("cannot inspect %s", dep)
						continue
					}
					walk(dep)
				}
			}
		}
		walk(module + root)
	}
}

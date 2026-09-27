package ci

import (
	"path"
	"strconv"
	"strings"
	"testing"
)

// Webhook decoding must not compile merge policy to filter comment text. The
// shared filter stays standard-library-only. Walk all platform variants and
// transitive edges, so another intermediary cannot reintroduce the dependency.
func TestWebhookTextDoesNotPullInMergePolicy(t *testing.T) {
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
			dep, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			imports[pkg] = append(imports[pkg], dep)
		}
	}
	for _, root := range []string{"internal/ghevent", "internal/textbody"} {
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
				if strings.HasPrefix(dep, module) {
					if root == "internal/textbody" || dep == module+"internal/merge" {
						t.Errorf("%s reaches %s through %s; use general text filtering without merge policy", root, dep, pkg)
					}
					if _, ok := imports[dep]; !ok {
						t.Errorf("cannot inspect %s", dep)
						continue
					}
					walk(dep)
				} else if first, _, _ := strings.Cut(dep, "/"); root == "internal/textbody" && strings.Contains(first, ".") {
					t.Errorf("textbody reaches third-party %s", dep)
				}
			}
		}
		walk(module + root)
	}
}

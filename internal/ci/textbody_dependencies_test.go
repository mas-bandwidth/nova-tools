package ci

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Webhook decoding must not compile merge policy to filter comment text. The
// shared filter stays standard-library-only. Walk all platform variants and
// transitive edges, so another intermediary cannot reintroduce the dependency.
func TestWebhookTextDoesNotPullInMergePolicy(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	require.NoError(t, err)
	module := ""
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			module = strings.Trim(fields[1], "\"") + "/"
			break
		}
	}
	require.NotEmpty(t, module, "go.mod has no module directive")
	imports := map[string][]string{}
	for _, f := range repoTree(t).GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr)
		pkg := module + path.Dir(f.Rel)
		if _, ok := imports[pkg]; !ok {
			imports[pkg] = nil
		}
		for _, spec := range f.AST.Imports {
			dep, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			imports[pkg] = append(imports[pkg], dep)
		}
	}
	for _, root := range []string{"internal/ghevent", "internal/textbody"} {
		_, ok := imports[module+root]
		require.Truef(t, ok, "missing root %s", root)
		seen := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if seen[pkg] {
				return
			}
			seen[pkg] = true
			for _, dep := range imports[pkg] {
				if strings.HasPrefix(dep, module) {
					assert.Falsef(t, root == "internal/textbody" || dep == module+"internal/merge", "%s reaches %s through %s; use general text filtering without merge policy", root, dep, pkg)
					_, ok := imports[dep]
					if !assert.Truef(t, ok, "cannot inspect %s", dep) {
						continue
					}
					walk(dep)
				} else {
					first, _, _ := strings.Cut(dep, "/")
					assert.Falsef(t, root == "internal/textbody" && strings.Contains(first, "."), "textbody reaches third-party %s", dep)
				}
			}
		}
		walk(module + root)
	}
}

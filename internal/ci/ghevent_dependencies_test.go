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

// All production platform variants are included. Readers may share the wire
// identity, but must not compile the webhook decoder through any intermediary.
func TestGitHubEventReadersDoNotImportIngestion(t *testing.T) {
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
	wire := module + "internal/ghevent/wire"
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
	for _, root := range []string{"internal/ghevent/wire"} {
		_, ok := imports[module+root]
		require.True(t, ok, "missing root %s", root)
		seen := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if seen[pkg] {
				return
			}
			seen[pkg] = true
			for _, dep := range imports[pkg] {
				assert.NotEqual(t, wire, pkg, "wire identity imports %s; keep the contract import-free", dep)
				assert.NotEqual(t, module+"internal/ghevent", dep, "%s reaches webhook ingestion through %s; depend on the wire contract", root, pkg)
				if strings.HasPrefix(dep, module) {
					_, ok := imports[dep]
					if !assert.True(t, ok, "cannot inspect %s", dep) {
						continue
					}
					walk(dep)
				}
			}
		}
		walk(module + root)
	}
}

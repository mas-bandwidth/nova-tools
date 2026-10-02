package ci

import (
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bus is messages over Git. Only its bus, build-info and one-line modules
// are needed, the verb-help seam every living tool parses its flags through
// (internal/nsprint/verbflag: standard library only, no decision and no
// storage; it prints a verb's help on -h), and the three general process and file
// doors: internal/gitrun (the one git runner) and internal/subproc (the deadline
// and WaitDelay every child gets), both standard library only, and
// internal/atomicfile (write-then-rename with fsync; it imports only
// internal/oneline). Every other import is standard library. Walk all platforms
// transitively so an intermediate package cannot hide an added dependency.
// busAllowed is the module-local packages cmd/nova-bus may reach.
var busAllowed = map[string]bool{
	"github.com/mas-bandwidth/nova-tools/internal/bus":              true,
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo":        true,
	"github.com/mas-bandwidth/nova-tools/internal/oneline":          true,
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag": true,
	"github.com/mas-bandwidth/nova-tools/internal/atomicfile":       true,
	"github.com/mas-bandwidth/nova-tools/internal/gitrun":           true,
	"github.com/mas-bandwidth/nova-tools/internal/subproc":          true,
}

func TestBusHasOnlyGeneralModulesAndStandardLibrary(t *testing.T) {
	t.Parallel()
	const module = "github.com/mas-bandwidth/nova-tools/"
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
			name, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			imports[pkg] = append(imports[pkg], name)
		}
	}
	for _, root := range []string{"cmd/nova-bus"} {
		require.Contains(t, imports, module+root, "missing dependency root %s", root)
		visited := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if visited[pkg] {
				return
			}
			visited[pkg] = true
			for _, dep := range imports[pkg] {
				if strings.HasPrefix(dep, module) {
					assert.True(t, busAllowed[dep], "%s reaches %s through %s; the bus carries messages over Git without decision or storage dependencies", root, dep, pkg)
					if !assert.Contains(t, imports, dep, "cannot inspect local dependency %s", dep) {
						continue
					}
					walk(dep)
				} else {
					first, _, _ := strings.Cut(dep, "/")
					assert.False(t, strings.Contains(first, "."), "%s reaches third-party dependency %s through %s; the bus uses the standard library", root, dep, pkg)
				}
			}
		}
		walk(module + root)
	}
}

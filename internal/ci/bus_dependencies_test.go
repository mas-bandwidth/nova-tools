package ci

import (
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nova-bus keeps Git as its default transport and offers Redis only through
// send --redis. The binary therefore reaches both narrow transport adapters:
// internal/gitrun/subproc for Git and internal/friendbus/redisconn for explicit
// Redis sends. The Redis client is allowed only underneath those adapters; all
// other dependencies remain standard-library-only. Walk all platforms
// transitively so an intermediate package cannot hide an added dependency.
// busAllowed is the module-local packages cmd/nova-bus may reach.
var busAllowed = map[string]bool{
	"github.com/mas-bandwidth/nova-tools/internal/bus":               true,
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo":         true,
	"github.com/mas-bandwidth/nova-tools/internal/friendbus":         true,
	"github.com/mas-bandwidth/nova-tools/internal/oneline":           true,
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth": true,
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag":  true,
	"github.com/mas-bandwidth/nova-tools/internal/atomicfile":        true,
	"github.com/mas-bandwidth/nova-tools/internal/gitrun":            true,
	"github.com/mas-bandwidth/nova-tools/internal/redisconn":         true,
	"github.com/mas-bandwidth/nova-tools/internal/subproc":           true,
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
					redisClient := strings.HasPrefix(dep, "github.com/redis/go-redis/v9")
					redisAdapter := pkg == module+"internal/friendbus" || pkg == module+"internal/redisconn"
					if redisClient {
						assert.True(t, redisAdapter, "%s reaches Redis client %s outside the explicit transport adapters through %s", root, dep, pkg)
						continue
					}
					first, _, _ := strings.Cut(dep, "/")
					assert.False(t, strings.Contains(first, "."), "%s reaches third-party dependency %s through %s; only friendbus and redisconn may use the Redis client", root, dep, pkg)
				}
			}
		}
		walk(module + root)
	}
}

package ci

import (
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: ONE WRITER. A WORKER IS A CLIENT, AND DOES NOT OPEN THE STORE.
//
// Glenn 2026-10-01: "never write a complicated distributed system when a simple
// client/server will work just fine." / "simple client/server always wins."
//
// nova-sprint's workers each read the sprint's tables across the network, planned on
// what they read, and wrote back behind one fence. From a machine 108 ms from the store
// a write lost that fence for about 50 s and gave up; a finished card took a median
// 391 s to be reported. A lock, a reservation and a queue with four recovery rules each
// added states. One server beside the store that runs the workers' verbs for them
// (nova-sprint run --listen; docs/SPEC-SPRINT.md, The server) removed the problem.
//
// So the worker's side of the tree holds no way to reach the store: the packages a
// worker runs (onewriterWorkerRoots) import, directly or through any package of this
// module, none of the packages that open it (onewriterStore). A worker asks the server.

const onewriterModule = "github.com/mas-bandwidth/nova-tools/"

// onewriterWorkerRoots are the packages a worker's machine runs for a sprint.
var onewriterWorkerRoots = []string{"cmd/nova-swarm", "internal/member", "internal/sprintwire"}

// onewriterStore are the import path prefixes that open a sprint's store.
var onewriterStore = []string{
	onewriterModule + "internal/sprint/store",
	onewriterModule + "internal/redisconn",
	onewriterModule + "internal/ntable",
	onewriterModule + "internal/nsprint/store",
	"github.com/redis/",
}

// onewriterImports is each package directory's imports, from its non-test files.
func onewriterImports(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, f := range repoTree(t).GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f.Rel, f.Src, parser.ImportsOnly)
		require.NoError(t, err, f.Rel)
		dir := path.Dir(f.Rel)
		for _, im := range parsed.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			require.NoError(t, err, f.Rel)
			out[dir] = append(out[dir], p)
		}
	}
	return out
}

// onewriterReach is the first chain of imports from the package to one that opens the
// store, as the packages' names in order; nil when there is none.
func onewriterReach(imports map[string][]string, dir string, seen map[string]bool) []string {
	if seen[dir] {
		return nil
	}
	seen[dir] = true
	for _, p := range imports[dir] {
		for _, s := range onewriterStore {
			if p == strings.TrimSuffix(s, "/") || strings.HasPrefix(p, s) {
				return []string{dir, p}
			}
		}
	}
	for _, p := range imports[dir] {
		if next, ok := strings.CutPrefix(p, onewriterModule); ok {
			if chain := onewriterReach(imports, next, seen); chain != nil {
				return append([]string{dir}, chain...)
			}
		}
	}
	return nil
}

func TestAWorkerDoesNotOpenTheStore(t *testing.T) {
	t.Parallel()
	imports := onewriterImports(t)
	for _, root := range onewriterWorkerRoots {
		require.NotEmpty(t, imports[root], "%s has no imports: this test is looking in the wrong place and would pass by checking nothing", root)
		chain := onewriterReach(imports, root, map[string]bool{})
		assert.Empty(t, chain, "%s reaches the store: %s; remedy=\"a worker is a client: ask the sprint's server (internal/sprintwire) and never open the store from a worker's machine (docs/SPEC-CI.md, onewriter)\"", root, strings.Join(chain, " -> "))
	}
}

// The walk finds a chain when there is one: through a package of the module, and
// directly; and none when the imports stop short of the store.
func TestOneWriterFindsAChainToTheStore(t *testing.T) {
	t.Parallel()
	imports := map[string][]string{
		"cmd/worker":      {"fmt", onewriterModule + "internal/helper"},
		"internal/helper": {onewriterModule + "internal/deeper"},
		"internal/deeper": {"github.com/redis/go-redis/v9"},
		"cmd/direct":      {onewriterModule + "internal/sprint/store"},
		"cmd/clean":       {"net/http", onewriterModule + "internal/loop"},
		"internal/loop":   {onewriterModule + "internal/loop2"},
		"internal/loop2":  {onewriterModule + "internal/loop"},
	}
	assert.Equal(t, []string{"cmd/worker", "internal/helper", "internal/deeper", "github.com/redis/go-redis/v9"}, onewriterReach(imports, "cmd/worker", map[string]bool{}))
	assert.Equal(t, []string{"cmd/direct", onewriterModule + "internal/sprint/store"}, onewriterReach(imports, "cmd/direct", map[string]bool{}))
	assert.Nil(t, onewriterReach(imports, "cmd/clean", map[string]bool{}), "a cycle of clean packages ends")
	roots := append([]string(nil), onewriterWorkerRoots...)
	sort.Strings(roots)
	assert.Equal(t, []string{"cmd/nova-swarm", "internal/member", "internal/sprintwire"}, roots)
}

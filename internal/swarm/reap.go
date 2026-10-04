package swarm

import (
	"os"
	"path/filepath"
	"strings"
)

// SHARED PER-BENCH CACHES (issue #1048). A native job downloads the Go toolchain and every
// module into its own sandboxed data home -- up to 5 GB per slot -- and 120 cards filled
// hulk and vision to 100%. The toolchain and the module cache are the same for every job
// under one swarm root, so they live once under <root>/cache and every job's child is
// pointed at them (GOMODCACHE, GOCACHE, NPM_CONFIG_CACHE). The root is a permitted write
// root beside the job directory (docs/SPEC-SANDBOX.md), never the job's own data home.
const (
	// CacheDirName is the one shared cache directory under a swarm root.
	CacheDirName = "cache"
)

// CacheRoot is the shared cache directory under a swarm root.
func CacheRoot(root string) string { return filepath.Join(root, CacheDirName) }

// GoModCacheDir is the shared module cache (GOMODCACHE) under a swarm root: the one name
// native hands the child, so the directory made here is the one the child uses.
func GoModCacheDir(root string) string { return filepath.Join(root, CacheDirName, "go-mod") }

// GoBuildCacheDir is the shared build cache (GOCACHE) under a swarm root: the one the child
// is handed, JOB.md names and the member's lazy cleaner holds under gocache.Limit.
func GoBuildCacheDir(root string) string { return filepath.Join(root, CacheDirName, "go-build") }

// NPMCacheDir is the shared npm cache (NPM_CONFIG_CACHE) under a swarm root.
func NPMCacheDir(root string) string { return filepath.Join(root, CacheDirName, "npm") }

// EnsureCacheDirs makes the three shared cache directories, so the child's first cache write
// lands in a directory that exists inside the write set.
func EnsureCacheDirs(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	for _, dir := range []string{GoModCacheDir(root), GoBuildCacheDir(root), NPMCacheDir(root), LispCacheDir(root)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

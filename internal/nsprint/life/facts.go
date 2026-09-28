package life

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Facts are what a bench measures about itself on every beat (#3646), so
// `nova-sprint preflight --fleet` reads them from Redis instead of a
// coordinator ssh-ing round the fleet in LLM context. Each is a plain string
// on the beat hash; an empty one is a fact the bench could not measure, and
// preflight reads it as MISSING, never as fine.
type Facts struct {
	Harness string // the harness-v<version> dirs under the root, versions comma-joined
	Mirrors string // the <repo>.git mirrors under <root>/mirror holding objects, comma-joined
	DiskGiB string // whole GiB free to the bench user on the root's filesystem
}

// DefaultRoot is the bench root under the bench user's home.
const DefaultRoot = "nova-bench"

// MeasureBench reads the facts under root: two directory listings, one stat
// per mirror and one statfs, cheap enough for the one-second beat.
func MeasureBench(root string) Facts {
	var f Facts
	if entries, err := os.ReadDir(root); err == nil {
		var versions []string
		for _, e := range entries {
			if v, ok := strings.CutPrefix(e.Name(), "harness-v"); ok && v != "" && e.IsDir() {
				versions = append(versions, v)
			}
		}
		sort.Strings(versions)
		f.Harness = strings.Join(versions, ",")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "mirror")); err == nil {
		var repos []string
		for _, e := range entries {
			repo, ok := strings.CutSuffix(e.Name(), ".git")
			if !ok || repo == "" {
				continue
			}
			if st, err := os.Stat(filepath.Join(root, "mirror", e.Name(), "objects")); err == nil && st.IsDir() {
				repos = append(repos, repo)
			}
		}
		sort.Strings(repos)
		f.Mirrors = strings.Join(repos, ",")
	}
	if free, ok := freeBytes(root); ok {
		f.DiskGiB = strconv.FormatUint(free>>30, 10)
	}
	return f
}

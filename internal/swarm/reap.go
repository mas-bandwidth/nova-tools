package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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
	// DefaultReapOlder is --older's default: a slot whose newest harness log is older than
	// an hour, with no RESULT.md, is a slot no live job is writing.
	DefaultReapOlder = time.Hour
)

// CacheRoot is the shared cache directory under a swarm root.
func CacheRoot(root string) string { return filepath.Join(root, CacheDirName) }

// GoModCacheDir is the shared module cache (GOMODCACHE) under a swarm root.
func GoModCacheDir(root string) string { return filepath.Join(root, CacheDirName, "gomod") }

// GoBuildCacheDir is the shared build cache (GOCACHE) under a swarm root.
func GoBuildCacheDir(root string) string { return filepath.Join(root, CacheDirName, "gobuild") }

// NPMCacheDir is the shared npm cache (NPM_CONFIG_CACHE) under a swarm root.
func NPMCacheDir(root string) string { return filepath.Join(root, CacheDirName, "npm") }

// CacheEnv is the three cache variables the harness child carries: one cache root for every
// job under one swarm root, so N workers do not each download the same toolchain and modules.
func CacheEnv(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	return []string{
		"GOMODCACHE=" + GoModCacheDir(root),
		"GOCACHE=" + GoBuildCacheDir(root),
		"NPM_CONFIG_CACHE=" + NPMCacheDir(root),
	}
}

// EnsureCacheDirs makes the three shared cache directories, so the child's first cache write
// lands in a directory that exists inside the write set.
func EnsureCacheDirs(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	for _, dir := range []string{GoModCacheDir(root), GoBuildCacheDir(root), NPMCacheDir(root)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ReapInput is one reap pass. Root is the swarm root whose slots are reaped; Older is the
// age a harness log must pass, with no RESULT.md, for the slot to count as finished; DryRun
// counts without removing; KeepData is the worker description's keep_data, which leaves the
// slot's data/ where it is. Now is injectable for tests.
type ReapInput struct {
	Root     string
	Older    time.Duration
	DryRun   bool
	KeepData bool
	Now      func() time.Time
}

// ReapAtTaskEnd is the call `run` makes when a task ends: a worker description that sets
// keep_data=true leaves every slot's data alone, and one that does not frees the finished
// slots' data/, tmp/ and jobs/*/scratch under the root.
func ReapAtTaskEnd(root string, w Worker, now func() time.Time) (int, int64) {
	if w.KeepData {
		return 0, 0
	}
	slots, freed, err := ReapSlots(ReapInput{Root: root, Older: DefaultReapOlder, Now: now})
	if err != nil {
		return 0, 0
	}
	return slots, freed
}

// ReapSlots removes, for every finished slot under the root, the slot's data/, tmp/ and
// jobs/*/scratch, keeping RESULT.md, usage.tsv and the logs. It returns the number of slots
// reaped and the bytes freed. A slot is finished when a job under it published a RESULT.md,
// or its newest harness log is older than Older. A live slot -- no RESULT.md and a log that
// has moved recently -- is untouched.
func ReapSlots(in ReapInput) (int, int64, error) {
	if in.KeepData {
		return 0, 0, nil
	}
	root := strings.TrimSpace(in.Root)
	if root == "" {
		return 0, 0, fmt.Errorf("reap wants the swarm root whose finished slots are reaped")
	}
	older := in.Older
	if older <= 0 {
		older = DefaultReapOlder
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, err
	}
	cutoff := now().Add(-older)
	slots, freed := 0, int64(0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slotPath := filepath.Join(root, e.Name())
		if !looksLikeSlot(slotPath) {
			continue
		}
		finished, err := slotFinished(slotPath, cutoff)
		if err != nil {
			return slots, freed, err
		}
		if !finished {
			continue
		}
		slots++
		targets := []string{
			filepath.Join(slotPath, "data"),
			filepath.Join(slotPath, "tmp"),
		}
		if scratch, err := filepath.Glob(filepath.Join(slotPath, "jobs", "*", "scratch")); err == nil {
			targets = append(targets, scratch...)
		}
		for _, target := range targets {
			freed += treeBytes(target)
			if in.DryRun {
				continue
			}
			if err := safepath.RemoveUnder(slotPath, target); err != nil {
				return slots, freed, err
			}
		}
	}
	return slots, freed, nil
}

// looksLikeSlot reports whether a direct child of the root is a job slot rather than one of
// the root's own structural directories: a slot is a numeric directory, or any directory
// that holds a jobs/ subtree.
func looksLikeSlot(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "jobs")); err == nil {
		return true
	}
	name := filepath.Base(path)
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// slotFinished reports whether no live job is writing a slot: a published RESULT.md, or a
// newest harness log older than the cutoff. A slot with neither is left alone, because the
// answer is not known.
func slotFinished(slotPath string, cutoff time.Time) (bool, error) {
	hasResult, err := hasResultBelow(filepath.Join(slotPath, "jobs"))
	if err != nil {
		return false, err
	}
	if hasResult {
		return true, nil
	}
	newest, found, err := newestHarnessLog(slotPath)
	if err != nil {
		return false, err
	}
	return found && newest.Before(cutoff), nil
}

// hasResultBelow reports whether any job directly under jobs/ published a RESULT.md.
func hasResultBelow(jobsDir string) (bool, error) {
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(jobsDir, e.Name(), "RESULT.md")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// newestHarnessLog is the newest mtime among a slot's own logs -- native.log at the slot,
// and harness.log, harness-output.log and native.log under each job.
func newestHarnessLog(slotPath string) (time.Time, bool, error) {
	candidates := []string{filepath.Join(slotPath, "native.log")}
	for _, name := range []string{"harness.log", "harness-output.log", "native.log"} {
		if glob, err := filepath.Glob(filepath.Join(slotPath, "jobs", "*", name)); err == nil {
			candidates = append(candidates, glob...)
		}
	}
	var newest time.Time
	found := false
	for _, path := range candidates {
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return time.Time{}, false, err
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		if !found || fi.ModTime().After(newest) {
			newest, found = fi.ModTime(), true
		}
	}
	return newest, found, nil
}

// treeBytes is the total size of a file or directory tree: what a reap would free. It reuses
// finalize.go's treeBytes, which walks a tree and ignores what it cannot read.

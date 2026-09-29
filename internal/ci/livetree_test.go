package ci

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// liveTree is deprecated/PACKAGES read the way .github/scripts/live-packages.sh
// reads it, for the class tests that walk source rather than a package list: a
// class rule over deprecated code is a test of deprecated code, which is never
// run (Glenn 2026-09-27), so a rule walks the packages CI selects and no
// others. A path names that package and everything under it; `keep <path>`
// names one package under such a path that stays live. The script is the
// selection's own reading; TestDeprecatedPackagesAreNeverSelected holds the
// two to the same answer.
type liveTree struct {
	root       string
	drop       []string
	keep       map[string]bool
	hasGoCache map[string]bool
	mu         sync.Mutex
}

var (
	sharedLiveTreeOnce sync.Once
	sharedLiveTree     *liveTree
)

func loadLiveTree(t *testing.T, root string) *liveTree {
	t.Helper()
	if root == repoRoot(t) {
		sharedLiveTreeOnce.Do(func() {
			sharedLiveTree = parseLiveTree(t, root)
		})
		return sharedLiveTree
	}
	return parseLiveTree(t, root)
}

func parseLiveTree(t *testing.T, root string) *liveTree {
	t.Helper()
	lt := &liveTree{root: root, keep: map[string]bool{}, hasGoCache: map[string]bool{}}
	for _, line := range strings.Split(readFile(t, filepath.Join(root, "deprecated", "PACKAGES")), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "keep ") || strings.HasPrefix(line, "keep\t"):
			lt.keep[strings.TrimSpace(line[len("keep"):])] = true
		default:
			lt.drop = append(lt.drop, line)
		}
	}
	return lt
}

// Package reports whether the package at dir (slash-separated, from the
// repository root) is live: CI selects it.
func (lt *liveTree) Package(dir string) bool {
	dir = strings.TrimPrefix(filepath.ToSlash(dir), "./")
	if lt.keep[dir] {
		return true
	}
	for _, d := range lt.drop {
		if dir == d || strings.HasPrefix(dir, d+"/") {
			return false
		}
	}
	return true
}

// File reports whether the source file rel belongs to a live package. A file
// in a directory with no Go of its own (a Lua library under lua/, embedded by
// the package above it) belongs to the nearest directory above it that holds a
// non-test .go file.
func (lt *liveTree) File(rel string) bool {
	dir := filepath.ToSlash(filepath.Dir(rel))
	for dir != "." && dir != "" && !lt.hasGo(dir) {
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
	return lt.Package(dir)
}

func (lt *liveTree) hasGo(dir string) bool {
	lt.mu.Lock()
	if v, ok := lt.hasGoCache[dir]; ok {
		lt.mu.Unlock()
		return v
	}
	lt.mu.Unlock()

	var has bool
	entries, err := os.ReadDir(filepath.Join(lt.root, filepath.FromSlash(dir)))
	if err == nil {
		for _, e := range entries {
			if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				has = true
				break
			}
		}
	}

	lt.mu.Lock()
	lt.hasGoCache[dir] = has
	lt.mu.Unlock()
	return has
}

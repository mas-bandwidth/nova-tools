package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// liveTree is pkgselect.DeprecatedFile read the way pkgselect.Deprecated reads it,
// for the class tests that walk source rather than a package list: a class
// rule over deprecated code is a test of deprecated code, which is never run
// (Glenn 2026-09-27), so a rule walks the packages CI selects and no others. A
// path names that package and everything under it; `keep <path>` names one
// package under such a path that stays live. The package is the selection's
// own reading; TestDeprecatedPackagesAreNeverSelected holds the two to the
// same answer.
type liveTree struct {
	drop []string
	keep map[string]bool
}

func loadLiveTree(t *testing.T, root string) *liveTree {
	t.Helper()
	lt := &liveTree{keep: map[string]bool{}}
	for _, line := range strings.Split(readFile(t, filepath.Join(root, filepath.FromSlash(pkgselect.DeprecatedFile))), "\n") {
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

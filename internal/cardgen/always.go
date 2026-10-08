package cardgen

import (
	"path"
	"slices"
	"strings"
)

// AlwaysInPathsRule is the one sentence the brief generator, the lander and the readers
// hold a card's scope to (docs/SPEC-SPRINT.md section 7, the lander's checks;
// docs/SPEC-CARD-CONTRACT.md): the files a change must touch to keep the tree green are
// inside every card's PATHS. The owner, 2026-10-06: "Can we stop this whole 'test
// outside of paths' thing. It's wasteful."
const AlwaysInPathsRule = "Files a change must touch to keep the tree green are always inside PATHS, whatever the brief names: every *_test.go, every file under a testdata/ directory, tla/RUNS.tsv and tla/CASES.tsv, internal/docs/catalog.go, and every AGENTS.md map; any other file outside PATHS is still out of scope."

// alwaysInPathsFiles are the single files AlwaysInPathsRule names.
var alwaysInPathsFiles = []string{"tla/RUNS.tsv", "tla/CASES.tsv", "internal/docs/catalog.go"}

// AlwaysInPaths says p is a file AlwaysInPathsRule puts inside every card's PATHS: a Go
// test file, a file under a testdata directory at any depth, one of the TLA+ ledgers,
// the docs catalog, or an AGENTS.md map. p is a path relative to the repository root.
func AlwaysInPaths(p string) bool {
	p = strings.TrimPrefix(path.Clean(p), "./")
	switch {
	case p == "." || p == "" || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/"):
		return false
	case strings.HasSuffix(p, "_test.go"), path.Base(p) == "AGENTS.md", slices.Contains(alwaysInPathsFiles, p):
		return true
	}
	return slices.Contains(strings.Split(path.Dir(p), "/"), "testdata")
}

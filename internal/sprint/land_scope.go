package sprint

import (
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
)

// LandScope is E12 as the lander reads one merge's diff (docs/SPEC-SPRINT.md section 7,
// the lander's checks): the files the diff changes outside the card's PATHS
// (diffcheck.Outside), less those inside every card's PATHS by rule (OutsideByRule),
// split into the scope amendments of the same change and the files refused
// (ScopeAmended). trackedBefore is diffcheck.Outside's.
func LandScope(paths []string, diff string, trackedBefore []string) (amended, refused []string) {
	files := diffcheck.Parse(diff)
	var changed []string
	blockedRename := map[string]bool{}
	for _, f := range files {
		changed = append(changed, f.New)
		if f.Old != f.New && !cardgen.AlwaysInPaths(f.Old) && cardgen.AlwaysInPaths(f.New) {
			blockedRename[f.New] = true
		}
	}
	var ordinary, renamed []string
	for _, p := range OutsideByRule(files, diffcheck.Outside(paths, diff, trackedBefore)) {
		if blockedRename[p] {
			renamed = append(renamed, p)
		} else {
			ordinary = append(ordinary, p)
		}
	}
	// SPEC-SPRINT section 7: a rename cannot borrow another file's test amendment
	// to move source that the card did not own into an always-allowed category.
	amended, refused = ScopeAmended(changed, ordinary)
	return amended, append(refused, renamed...)
}

// OutsideByRule is outside, files a diff changes outside a card's PATHS, without those
// cardgen.AlwaysInPathsRule puts inside every card's PATHS: a test, a testdata file, a
// TLA+ ledger, the docs catalog or an AGENTS.md map (cardgen.AlwaysInPaths). files is
// the diff's (diffcheck.Parse); a rename is inside by rule only when both its sides are,
// so a source file moved to a test's name is still refused.
func OutsideByRule(files []diffcheck.File, outside []string) []string {
	var out []string
	for _, p := range outside {
		byRule := slices.ContainsFunc(files, func(f diffcheck.File) bool {
			return f.New == p && cardgen.AlwaysInPaths(f.Old) && cardgen.AlwaysInPaths(f.New)
		})
		if !byRule {
			out = append(out, p)
		}
	}
	return out
}

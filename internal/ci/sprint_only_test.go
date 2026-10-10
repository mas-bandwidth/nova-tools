package ci

import "strings"

// sprintOnlyPaths are nova-sprint's paths in this tree: the opinionated system that is
// leaving for a repository of its own (the split, v1.2.3), which imports
// this module's building blocks and is imported by none of them. A rule that holds this
// tree's own code leaves them out; nova-sprint's tree holds them by its own copy.
var sprintOnlyPaths = []string{
	"cmd/nova-sprint", "cmd/nova-card", "cmd/nova-work",
	"internal/sprint", "internal/sprintdash", "internal/card", "internal/cardgen",
	"internal/workfile", "internal/workgh", "internal/worklang",
	"tools/sprintsize",
}

// sprintOnly is whether the repo-relative path rel is in one of sprintOnlyPaths.
func sprintOnly(rel string) bool {
	for _, p := range sprintOnlyPaths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

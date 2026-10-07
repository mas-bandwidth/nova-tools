// Package fleet holds the child rules files this repository's members inject into every
// card at stage time (nova-tools#5174 rule 6, rules by reference): fleet/child-rules.txt,
// and fleet/child-rules.<repo>.txt for a repository with rules of its own. go:embed cannot
// reach outside its own directory, so the embed lives beside the files; the member holds
// the rules of the build it runs, and the logic that reads them is internal/swarm's
// (heldrules.go). Nothing else belongs here.
package fleet

import "embed"

// Rules is every fleet/child-rules*.txt of this build, by base name.
//
//go:embed child-rules*.txt
var Rules embed.FS

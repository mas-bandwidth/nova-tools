package main

import "github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"

// fold is `nova-sprint fold <S>` (nova-tools #2618): the closed sprint's
// outcomes, priced per route, written back to nova-work as one commit.
func init() {
	register(Verb{Name: "fold", Summary: fold.VerbSummary, Run: fold.Main})
}

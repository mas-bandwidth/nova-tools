// Package pr reads the reap fields of one #3139 unit record, s:<S>:u:<unit>
// (nova-tools#3091 rev 5). The name is kept because #3156 imports pr.Inputs;
// the package reads a unit hash, never a retired PR key.
//
// Seven fields feed #3156's reap rules. land.lua writes every one of them,
// each from exactly one function:
//
//	head          ns_unit_head (#3139; this package only reads it)
//	paths         ns_unit_head, write-once, canonical JSON from CanonPaths
//	card_type     ns_unit_head, write-once, from the card hash (TYPE: line)
//	cut_at        ns_unit_head, write-once, from the card hash (Redis TIME at ns_card_push)
//	last_read_at  present-empty at create; ns_read on a counted read, monotonic
//	approve_head  present-empty at create; ns_read on a counted APPROVE
//	merged_at     present-empty at create; ns_land, once, with state=landed
//
// A present-empty last_read_at, approve_head or merged_at is a valid "not
// yet". An absent field is MISSING: the unit is never reap-eligible.
package pr

import (
	"github.com/mas-bandwidth/nova-tools/internal/canonpath"
)

// ErrRefused is wrapped by every CanonPaths refusal.
var ErrRefused = canonpath.ErrRefused

// ReapFields are the seven unit fields #3156's rules read, in the order
// Inputs checks them.
var ReapFields = []string{"head", "paths", "card_type", "cut_at", "last_read_at", "approve_head", "merged_at"}

// CanonPaths canonicalizes a card's PATHS line via canonpath.CanonPaths.
func CanonPaths(line string) ([]string, error) {
	return canonpath.CanonPaths(line)
}

// EncodePaths is the stored form of a canonical path list via canonpath.EncodePaths:
// compact JSON, e.g. ["a b/c","internal/nsprint/pr"].
func EncodePaths(paths []string) string {
	return canonpath.EncodePaths(paths)
}

// CanonJSON is CanonPaths followed by EncodePaths via canonpath.CanonJSON:
// the value ns_unit_head stores.
func CanonJSON(line string) (string, error) {
	return canonpath.CanonJSON(line)
}

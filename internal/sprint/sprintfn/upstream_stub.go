package sprintfn

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// IT05's shared types (internal/sprint, plan_types.go) have merged: the
// write path takes them as they are, by alias, as errata 2 (item 11) says.
//
// Body, Op and Meta stand in for the step package's of 8.0 (IT04). The step
// builder landed as another package, of another shape (internal/sprint/
// stepbuild, with its own Entry), so the request body 8.0 fixes has no home
// yet; these are 8.0's block as written, and they move when IT04's owner
// names the home. This file is deleted then.

// Intent is an effect a rule cannot plan from its read, decided in Lua from
// the real before-state by the derive phase (1.3.3).
type Intent = sprint.Intent

// XGuard is a guard on a sprint key that X checks at apply (1.3.5, 2.3).
type XGuard = sprint.XGuard

// NoteReq is a request to J for a note (1.3.4), turned into notes in the pre
// stage, one per cause.
type NoteReq = sprint.NoteReq

// Quarantined is a card a lower layer refused, whose quarantine rides the
// next step the tick sends (1.3.5).
type Quarantined = sprint.Quarantined

// Body is what the step builder hands the write path (8.0, package step):
// the Layer 1 entries a plan made, and everything the sprint's phases act on
// in the same call.
type Body struct {
	Entries    []tset.Entry
	Intents    []Intent
	Guards     []XGuard
	Notes      []NoteReq
	Quarantine []Quarantined
	Done       []string // agenda keys removed; empty on every request of a cut plan
	Requeue    []string // agenda keys requeued, with their orders kept
	Op         *Op      // part identity, intent, caller result
}

// Op is a step's identity at Layer 1 (L1 5): the op, its intent and the
// caller result the receipt keeps (8.0, package step).
type Op struct{ ID, Intent, Result string }

// Meta is who sends a step and why (8.0, package step): the verb or rule, the
// actor X checks for a coordinator's verb (NOTCOORD), and for a tick step the
// lease generation X checks (STALEGEN).
type Meta struct {
	Verb, Actor, Rule string
	Tick              bool
	Gen               uint64
}

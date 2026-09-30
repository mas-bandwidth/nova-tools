package sprintfn

import "github.com/mas-bandwidth/nova-tools/internal/tset"

// The shapes in this file stand in for types two other items own, until they
// merge (upper design version 2.1, 8.0: "an item that needs another item's
// code builds against these signatures and a stub until that item merges").
//
//   - Intent, XGuard, NoteReq and Quarantined are IT05's (internal/sprint,
//     plan_types.go), copied field for field from that item's pull request.
//     When it merges, each becomes an alias (type Intent = sprint.Intent) and
//     nothing that uses it changes.
//   - Body, Op and Meta are the step package's of 8.0 (IT04). The step
//     builder's pull request builds another package, of another shape (the
//     stepbuild package, with its own Entry), so the request body 8.0 fixes
//     has no home yet; these are 8.0's block as written, and they move when
//     IT04's owner names the home.
//
// This file is deleted when both have merged.

// Intent is an effect a rule cannot plan from its read, decided in Lua from
// the real before-state by the derive phase (1.3.3). IT05's shape.
type Intent struct {
	Kind    string   // waitfor, needmet, needgone or waive
	Card    string   // the waiter
	Need    string   // needmet, needgone
	Needs   []string // waitfor, waive
	Waiters []string // needmet, needgone
}

// XGuard is a guard on a sprint key that X checks at apply (1.3.5, 2.3).
// IT05's shape.
type XGuard struct {
	Kind   string // memberup, beatstale, due, hold, clock, coordinator or stranger
	Member string
	Key    string
	Score  int64
}

// NoteReq is a request to J for a note (1.3.4), turned into notes in the pre
// stage, one per cause. IT05's shape.
type NoteReq struct {
	Op        string // open, close, update, hold, unhold, know or request
	Type      string
	Cause     string
	Subjects  []string
	Text      string
	Decisions []string
	Until     int64 // a hold's R, or the review time
}

// Quarantined is a card a lower layer refused, whose quarantine rides the
// next step the tick sends (1.3.5). IT05's shape.
type Quarantined struct {
	ID, Stream, Code, Rule string
	Cells                  []string // from the refusal's detail
}

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

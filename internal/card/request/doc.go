// Package request is the typed input and receipt layer of the card manager: pure
// data with validation and one canonical encoding. It owns no store, opens no
// network connection and applies no lifecycle policy; the manager above it
// decides what a valid request means, and this package decides only whether a
// request is well formed and what its bytes are.
//
// # The contract
//
// Every operation takes an array (or an explicit complete scope) and is one
// request. A single card is an array of one. A request is a Request: the
// envelope (schema version, operation, table, expected epoch, observed table
// revision, operation ID, actor) plus exactly one payload for its operation:
// Admissions, Events, Evidence, Replacements or a Scope.
//
// Parse reads a request document strictly: an unknown field, a duplicate JSON
// key at any depth, trailing data after the document, a float, invalid UTF-8
// or a control character each refuse. Parse and Validate check the whole
// request before anything else and report every refusal found (at most
// MaxRefusals, the rest counted), never a first-only prefix, and a request
// with any refusal is refused whole: nothing is partly accepted.
//
// A card's destination state is never a field of an event. Destination derives
// it from the event type and the source state the event declares, and an event
// whose type and source state have no listed transition refuses. One request
// holds at most one event per card, and no event whose source state is
// reachable only through another event of the same request.
//
// Canonical gives a request one deterministic byte form (sorted keys, no
// floats, no HTML escaping, no insignificant whitespace, empty optional fields
// omitted); Hash is its SHA-256. A request has one Identity, its table, epoch
// and operation ID, and SameRequest decides whether two requests are the same
// by comparing canonical bytes, never caller-supplied digests.
//
// Receipt and Rejection are the result types the manager fills: the batch
// receipt of a change or no-op with every changed card's before and after, and
// the refusal of a batch or a card, whose Changed is no or unknown and is
// unknown for a transport failure. Both have a canonical encoding and a
// one-line rendering.
//
// The definition file format and its admission records belong to the card
// layer's definition package; ID and Digest here validate the same identities
// locally so that this package imports nothing of the card layer.
package request

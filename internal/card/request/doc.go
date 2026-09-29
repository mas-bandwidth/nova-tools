// Package request is the typed input and receipt layer of the card manager: pure
// data with validation and one canonical encoding. It owns no store, opens no
// network connection and applies no lifecycle policy; the manager above it
// decides what a valid request means, and this package decides only whether a
// request is well formed and what its bytes are. The identities, the refusal
// shape, the canonical encoder and the bounds are internal/card's, shared with the
// definition package.
//
// # The contract
//
// Every operation takes an array (or an explicit complete scope) and is one
// request. A single card is an array of one. A request is a Request document: the
// envelope (schema version, operation, table, expected epoch, observed table
// revision, operation ID, actor) plus exactly one payload for its operation:
// Admissions, Inputs (lifecycle inputs), Evidence, Replacements or a Scope. An
// inspect and a check are reads and carry no epoch, revision, operation ID or
// actor. The operation ID is optional: a request that gives none is known by an
// ID derived from its hash.
//
// Parse reads a request document strictly: an unknown key, a duplicate JSON key at
// any depth, trailing data after the document, a float, invalid UTF-8 or a control
// character each refuse. Parse and Validate check the whole request and report
// every refusal found (at most MaxRefusals, the rest counted), never a first-only
// prefix, and a request with any refusal is refused whole: nothing is partly
// accepted. What they return is a Valid, which carries the canonical bytes and the
// hash, cannot be built any other way, and satisfies Checked for every operation.
//
// # Lifecycle inputs, evidence and forced moves
//
// A lifecycle input is a request that may move a card; a notification is what the
// coordinator is told. A card's destination state is never a field of an input:
// Destination derives it from the input type and the source state the input
// declares, and an input whose type and source state have no listed transition
// refuses. One request holds at most one lifecycle input per card, and no input
// whose source state is reachable only through another input of the same request.
// A CI result is not a lifecycle input; it is evidence, a Record bound to the
// card's definition digest and a code head. One observation moves a card in the
// batch that records it, and it is data here: ForcedMove.
//
// Every lifecycle input type, forced move, evidence observation and selection
// outcome is classified as mechanical forward progress or a point where judgment
// may be required (ClassifyInput and its siblings); a test fails when one is added
// without a classification. The receipt carries, per changed card, the
// notifications the manager derives and the cycle counters; nothing here produces
// them.
//
// # Bytes
//
// Canonical gives a request one deterministic byte form (card.Encode: sorted keys,
// no floats, no HTML escaping, one escape rule for control characters, empty
// optional fields left out, order-free arrays sorted); Hash is its SHA-256 and
// HashWithoutOperationID the same with the operation ID left out. A request has
// one Identity, its table, epoch and operation ID, and SameRequest decides whether
// an incoming validated request is the recorded one by comparing canonical bytes,
// after checking that the recorded bytes are their own canonical form.
//
// Receipt, Rejection and InspectResult are the result types the manager fills.
// ValidateReceipt refuses a receipt no operation could produce. A Rejection's
// answer to "did anything change" is derived from its cause.
package request

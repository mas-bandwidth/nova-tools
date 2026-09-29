// Package card is the leaf of the card layer: the identities, the refusal shape,
// the canonical encoding and the bounds that the definition package and the
// request package share. It imports neither of them, and nothing else of the
// card layer; both import it.
//
// One ID (ASCII letters, digits, underscore and hyphen, at most 64 bytes, never
// the reserved words "-" and "none"), one Digest (64 lower-case hexadecimal
// characters), one object-id rule (a git object id or a head: 40 or 64
// lower-case hexadecimal characters), one Repository identity with its origin
// normaliser, and one repository-relative path grammar are defined here, once.
//
// One Refusal shape (operation, index, file, line, card, field, cause, found,
// limit, next) with one closed vocabulary of Cause names, one rendering
// ("refused <operation> ..."), and a Collector that keeps at most MaxRefusals of
// them and counts the rest. Text that came from the caller (a key, a value, a
// file name) is quoted and bounded when it is rendered, so no caller can forge a
// second refusal line with a newline or flood one with a long key.
//
// One canonical encoder (Encode) turns a tree of objects, arrays and strings
// into bytes: keys sorted, no floats, no HTML escaping, one escape rule for
// control characters, order-free arrays (Set) sorted by the encoder, and an empty
// optional field written as absent. Both packages hash the bytes it returns.
//
// The batch limits of the card layer, and the arithmetic that shows any request
// valid in the layer fits one table manifest, are the constants of limits.go.
package card

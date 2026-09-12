/*
Package records is the core of the retained token record format: the canonical envelope,
exact numeric strings with presence kept, and the structural validation the publisher
boundary performs.

The contract is docs/PROPOSAL-TOKENS-FORMAT.md and docs/PROPOSAL-TOKENS-RECORDS.md at
71ea08b (Stella's #124). Three sentences carry most of this package:

  - An object envelope is exactly {"id":"sha256:<64 lowercase hex>","body":{...}}, and the
    ID is SHA-256 of the body's RFC 8785 canonical JSON with no trailing newline. The
    envelope ID is excluded from its own digest because only the body is hashed.
  - Usage values are JSON strings. A float64 round trip is never performed on them, so an
    integer above 2^53 and a decimal lexeme survive byte-for-byte. Presence is present,
    absent or unavailable; absent and unavailable require value:null and a bounded reason
    code; a present zero survives as a present zero.
  - Validation here is STRUCTURAL. A record that fails is refused with the field named and
    is never repaired: no key is dropped, no value normalised, no missing field defaulted.
    A refusal is a question for the table, not a thing this package fixes.

What this package deliberately is not: it has no adapters, no batch or path joins, and no
publication. Those are separate owners (see the PR that introduced this package).
*/
package records

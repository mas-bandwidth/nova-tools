// Package definition is the card layer's definitions: what a card file is, how a
// set of card files is read, checked and pinned to committed Git blobs, and the
// canonical record an admission would carry. It holds no store, no network and no
// verbs; nothing in it executes or interprets a card's prose.
//
// # Contract
//
// A card is plain data: a UTF-8 text file committed in Git, made of a contract
// line, an uninterrupted header block of KEY: value lines, and a prose brief.
// Every function in this package takes and returns arrays. A single card is an
// array of one; there is no per-card entry point.
//
// Every function that can refuse returns its refusals as values (Refusal). An
// array that draws any refusal is refused whole: Parse, Validate, Pin and
// Admissions return no definitions, no pins and no records beside a refusal, so
// a caller cannot admit a prefix by accident.
//
// # The file
//
// Line 1 is the contract line, `RESULT: <id> sha=<hex>` in the shape the swarm
// cards carry (`RESULT <id> sha=<hex>` is read too); the sha= token is optional
// and any text after it is kept as the contract note. The header starts on line 2
// and ends at the first nonblank line that is not a KEY: value line; blank lines
// inside the header are allowed. The brief is every byte from that line to the
// end of the file. A known key at column zero below the header, outside a fenced
// block, is stranded and refused; quoted and fenced text declares no fields.
//
// The profile's keys are SCHEMA (v2), ID, ENTRY (optional), TITLE, KIND, PATHS,
// DEPENDS-ON, TIER, TEST, DONE-WHEN, DOORS and PROBES. A key repeated, a key not
// in the profile, a case or spacing variant of a known key, and a required key
// missing are refused by line and key. SCHEMA v3 is refused by name.
//
// The file is refused when it is not valid UTF-8, starts with a byte-order mark,
// holds a carriage return (CRLF and bare CR alike) or a NUL byte, or has no brief.
//
// # Values
//
// KIND is a name in internal/hygiene/kinds.txt and carries a completion class
// (pr or non-pr) from the versioned completion policy embedded beside the code;
// a kind the policy does not classify is refused. PATHS, TEST and TIER use the
// shared grammars of internal/hygiene and internal/cardhdr. IDs are nonempty ASCII
// letters, digits, underscore and hyphen; IDs and ENTRY exclude commas and control
// characters. The ID header agrees with the contract line.
//
// # The array
//
// Validate refuses a repeated ID across the array (naming both files), a card
// that depends on itself, and a dependency cycle inside the array. A dependency
// on an ID that is not in the array is not an error; the result lists it as
// external so the caller can guard it against what is already admitted.
//
// # Pinning
//
// Pin reads committed blobs, never working files, for an array of repository-
// relative paths at one full commit, in a fixed number of git invocations that
// does not grow with the array, each under a deadline. It reports the repository
// identity, commit, path, Git object id and the SHA-256 of the bytes. It runs no
// fetch, no publication and no network call.
//
// # Admission records
//
// Admissions joins definitions to their pins and produces one record per card
// with a canonical encoding (sorted keys, strings and string arrays only, no HTML
// escaping) and a stable SHA-256 digest. The record holds identities, digests and
// data fields; the brief, DONE-WHEN and PROBES appear only through their digests.
package definition

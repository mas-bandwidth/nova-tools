// Package definition is the card layer's definitions: what a card file is, how a
// set of card files is read, checked and pinned to committed Git blobs, and the
// canonical record an admission carries. It holds no store, no network and no
// verbs; nothing in it executes or interprets a card's prose. The docs are
// docs/SPEC-CARD.md. The identities, the refusal shape, the canonical encoder and
// the bounds are internal/card's, shared with the request package.
//
// # The one entry point
//
// Admissions takes a repository root, a full commit and paths, and returns one
// admission record per card: it pins the committed blobs, parses the pinned bytes
// itself and validates the array. It never takes a definition or a digest from its
// caller. parse, validate and pin are the package's own steps and are not
// exported. Every function takes and returns arrays; a single card is an array of
// one. A refused array returns no records and every refusal of the stage that
// refused (at most 64, the rest counted): a caller cannot admit a prefix.
//
// # The file
//
// A card is a UTF-8 text file: a contract line (`RESULT: <id>`, optionally
// ` sha=<40 or 64 lower-case hex>` and a note), an uninterrupted header of upper
// case `KEY: value` lines (a key outside the profile, and a variant of one that
// folds to a known key, refuse; any other first nonblank line ends the header), and
// a prose brief. The profile's keys are SCHEMA (v2), ID, ENTRY (optional), TITLE,
// KIND, PATHS, DEPENDS-ON, TIER, TEST, DONE-WHEN, DOORS and PROBES. `-` is the
// DEPENDS-ON none and `none` the PATHS, DOORS and PROBES one; neither is a card ID.
// KIND carries a completion class (pr or non-pr) from the versioned completion
// policy; a kind the policy does not classify is refused.
//
// # Pinning
//
// The pin reads committed blobs, never working files, in at most five git
// invocations whatever the array's size, each under a deadline, with lazy fetching
// off and every transport refused: in a partial clone a blob that was not fetched
// is the refusal missing-object, and stays missing. The directory must be the root
// of a repository. A commit that exists is accepted whether or not a ref reaches
// it. A refusal about an origin names the rule it broke and never quotes the URL.
//
// # Admission records
//
// A record holds identities, digests and the data fields of the header; the brief,
// DONE-WHEN and PROBES appear only through the definition digest. It is encoded by
// the card layer's one encoder and is at most card.MaxAdmissionRecordBytes.
package definition

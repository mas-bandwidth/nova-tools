package request

// SchemaVersion is the one request schema version.
const SchemaVersion = 1

// Count bounds. Each names the limit a refusal cites; exceeding one refuses
// the whole request and never splits it into several.
const (
	// MaxChangedEntries bounds an array whose entries change cards: admissions,
	// events, evidence entries. It is the table batch's bound on entries with
	// changes.
	MaxChangedEntries = 128
	// MaxReplacementPairs bounds a replacement array: each pair is two table
	// members (the old one terminated, the new one created), so 64 pairs fit
	// the 128 changed entries of one table batch.
	MaxReplacementPairs = MaxChangedEntries / 2
	// MaxGuardOnlyEntries bounds a scope's ID array and a selection's declared
	// bound: cards read or guarded without a change.
	MaxGuardOnlyEntries = 1024
	// MaxEvidenceRecordsPerCard bounds the records one evidence entry carries.
	MaxEvidenceRecordsPerCard = 8
	// MaxRefusals bounds the refusals one validation reports; further ones are
	// counted in Refusals.Omitted.
	MaxRefusals = 64
	// MaxDepth bounds JSON nesting.
	MaxDepth = 8
)

// Byte bounds.
const (
	// MaxCanonicalBytes bounds the canonical encoded request.
	MaxCanonicalBytes = 1 << 20
	// MaxInputBytes bounds the document Parse reads, before parsing: room for
	// indentation around a request whose canonical form fits MaxCanonicalBytes.
	MaxInputBytes = 4 << 20
	// MaxIDBytes bounds a card ID and an evidence ID.
	MaxIDBytes = 64
	// MaxNameBytes bounds a table name and a row name.
	MaxNameBytes = 64
	// MaxIdentityBytes bounds an operation ID, an actor and an issuer.
	MaxIdentityBytes = 128
	// MaxRefBytes bounds an opaque reference: a repository identity, a source
	// artifact identity, a landing identity.
	MaxRefBytes = 256
	// MaxPathBytes bounds a repository-relative path.
	MaxPathBytes = 512
	// MaxReasonBytes bounds a one-line reason.
	MaxReasonBytes = 512
	// MaxCounterDigits bounds a decimal counter (epoch, table revision, card
	// revision): the digits of the largest uint64.
	MaxCounterDigits = 20
	// MaxFoundBytes bounds the text a refusal quotes as what it found.
	MaxFoundBytes = 48
	// MaxReceiptCards bounds each card list of a receipt: the changed cards and
	// each declared selection outcome list.
	MaxReceiptCards = MaxGuardOnlyEntries
)

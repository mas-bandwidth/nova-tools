package request

import "github.com/mas-bandwidth/nova-tools/internal/card"

// SchemaVersion is the one request schema version.
const SchemaVersion = 1

// The bounds of a request are the card layer's, defined once in internal/card
// with the arithmetic that shows any request valid here fits one table manifest.
// They are named here so a refusal and a caller cite the package they use.
const (
	MaxChangedEntries         = card.MaxChangedEntries
	MaxReplacementPairs       = card.MaxReplacementPairs
	MaxGuardOnlyEntries       = card.MaxGuardOnlyEntries
	MaxScopeCards             = card.MaxScopeCards
	MaxResolveCards           = card.MaxResolveCards
	MaxScopeRows              = card.MaxScopeRows
	MaxDependsOn              = card.MaxDependsOn
	MaxOutsideDependencies    = card.MaxOutsideDependencies
	MaxEvidenceRecordsPerCard = card.MaxEvidenceRecordsPerCard
	MaxEvidenceRecords        = card.MaxEvidenceRecords
	MaxRefusals               = card.MaxRefusals
	MaxDepth                  = card.MaxDepth

	MaxCanonicalBytes = card.MaxCanonicalBytes
	MaxInputBytes     = card.MaxInputBytes
	MaxIDBytes        = card.MaxIDBytes
	MaxNameBytes      = card.MaxNameBytes
	MaxIdentityBytes  = card.MaxIdentityBytes
	MaxRefBytes       = card.MaxRefBytes
	MaxReasonBytes    = card.MaxReasonBytes
	MaxTitleBytes     = card.MaxTitleBytes
	MaxEntryBytes     = card.MaxEntryBytes
	MaxCounterDigits  = card.MaxCounterDigits
	MaxReceiptCards   = card.MaxReceiptCards
	MaxDetailBytes    = card.MaxDetailBytes
	MaxNoteBytes      = card.MaxNoteBytes
	MaxFoundBytes     = card.MaxFoundBytes
)

// MaxVerifierBytes bounds a verifier name in an evidence record.
const MaxVerifierBytes = 32

// MaxHeadBytes bounds a code head: a git object id, or a tagged digest.
const MaxHeadBytes = 71

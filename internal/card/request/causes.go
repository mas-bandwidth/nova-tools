package request

import "github.com/mas-bandwidth/nova-tools/internal/card"

// Cause is the named reason a request or a batch refuses: the card layer's one
// closed vocabulary.
type Cause = card.Cause

// The causes, re-exported from internal/card so a caller of this package names
// them here.
const (
	CauseSyntax             = card.CauseSyntax
	CauseTrailingData       = card.CauseTrailingData
	CauseDuplicateKey       = card.CauseDuplicateKey
	CauseUnknownKey         = card.CauseUnknownKey
	CauseNotApplicable      = card.CauseNotApplicable
	CauseWrongType          = card.CauseWrongType
	CauseTooDeep            = card.CauseTooDeep
	CauseRequired           = card.CauseRequired
	CauseInvalidValue       = card.CauseInvalidValue
	CauseReservedWord       = card.CauseReservedWord
	CauseInvalidUTF8        = card.CauseInvalidUTF8
	CauseControlChar        = card.CauseControlChar
	CauseTooLong            = card.CauseTooLong
	CauseTooMany            = card.CauseTooMany
	CauseTooLarge           = card.CauseTooLarge
	CauseEmptyArray         = card.CauseEmptyArray
	CauseRepeatedID         = card.CauseRepeatedID
	CauseConflictingInputs  = card.CauseConflictingInputs
	CauseInputChain         = card.CauseInputChain
	CauseConflictingRecords = card.CauseConflictingRecords
	CauseNoTransition       = card.CauseNoTransition
	CauseNotEligible        = card.CauseNotEligible
	CauseInvalidRepository  = card.CauseInvalidRepository
	CauseInvalidCommit      = card.CauseInvalidCommit
	CauseInvalidPath        = card.CauseInvalidPath
	CausePathEscapes        = card.CausePathEscapes
	CauseEmptyFile          = card.CauseEmptyFile
	CauseDuplicateFile      = card.CauseDuplicateFile
	CauseBOM                = card.CauseBOM
	CauseCarriageReturn     = card.CauseCarriageReturn
	CauseContractLine       = card.CauseContractLine
	CauseContractSHA        = card.CauseContractSHA
	CauseAmbiguousSpelling  = card.CauseAmbiguousSpelling
	CauseUnsupportedSchema  = card.CauseUnsupportedSchema
	CauseStranded           = card.CauseStranded
	CauseIDMismatch         = card.CauseIDMismatch
	CauseEmptyValue         = card.CauseEmptyValue
	CauseInvalidID          = card.CauseInvalidID
	CauseInvalidEntry       = card.CauseInvalidEntry
	CauseInvalidKind        = card.CauseInvalidKind
	CauseUnclassifiedKind   = card.CauseUnclassifiedKind
	CauseInvalidPaths       = card.CauseInvalidPaths
	CauseInvalidDependsOn   = card.CauseInvalidDependsOn
	CauseInvalidTier        = card.CauseInvalidTier
	CauseInvalidTest        = card.CauseInvalidTest
	CauseNoBrief            = card.CauseNoBrief
	CauseSelfDependent      = card.CauseSelfDependent
	CauseCycle              = card.CauseCycle
	CauseTooManyExternal    = card.CauseTooManyExternal
	CauseDuplicatePath      = card.CauseDuplicatePath
	CauseNotRepository      = card.CauseNotRepository
	CauseGitUnavailable     = card.CauseGitUnavailable
	CauseGitFailed          = card.CauseGitFailed
	CauseTimeout            = card.CauseTimeout
	CauseUnknownCommit      = card.CauseUnknownCommit
	CauseNotCommit          = card.CauseNotCommit
	CauseMissingPath        = card.CauseMissingPath
	CauseMissingObject      = card.CauseMissingObject
	CauseSymlink            = card.CauseSymlink
	CauseNotBlob            = card.CauseNotBlob
	CauseIdentityMissing    = card.CauseIdentityMissing
	CauseInvalidRequest     = card.CauseInvalidRequest
	CauseStaleEpoch         = card.CauseStaleEpoch
	CauseStaleTableRev      = card.CauseStaleTableRev
	CauseStaleCardRev       = card.CauseStaleCardRev
	CausePlaceMismatch      = card.CausePlaceMismatch
	CauseDigestMismatch     = card.CauseDigestMismatch
	CauseOperationConflict  = card.CauseOperationConflict
	CauseUnknownRowCol      = card.CauseUnknownRowCol
	CauseGuardFailed        = card.CauseGuardFailed
	CauseOverLimit          = card.CauseOverLimit
	CauseTransport          = card.CauseTransport
	CauseStoreError         = card.CauseStoreError
)

package typedrec

// TableRefusal identifies the classified refusal tag of a table Lua reply.
type TableRefusal int

const (
	TableRefusalUnknown TableRefusal = iota
	TableRefusalStale
	TableRefusalMemberEpoch
	TableRefusalMemberExists
	TableRefusalPlaced
	TableRefusalDrift
	TableRefusalNoTable
	TableRefusalExists
	TableRefusalNoRow
	TableRefusalNoCol
	TableRefusalText
	TableRefusalNotMember
	TableRefusalOccupiedValue
	TableRefusalOccupied
	TableRefusalOccupiedCells
	TableRefusalOwnedAlias
	TableRefusalNoMember
	TableRefusalNoView
	TableRefusalViewTable
	TableRefusalSummary
	TableRefusalSelf
	TableRefusalWhere
	TableRefusalColExists
	TableRefusalDepends
	TableRefusalLastCol
	TableRefusalSorted
	TableRefusalSortKey
	TableRefusalSortKeep
	TableRefusalNotText
	TableRefusalBound
	TableRefusalEpoch
	TableRefusalCondition
	TableRefusalArgs
	TableRefusalRevision
	TableRefusalMemberRevision
	TableRefusalFieldGuard
	TableRefusalOpConflict
	TableRefusalLimit
	TableRefusalReservedField
	TableRefusalTwice
	TableRefusalScore
	TableRefusalOverflow
	TableRefusalMutation
	TableRefusalManifest
	TableRefusalStreamFull
	TableRefusalSchema
	TableRefusalOperation
	TableRefusalMember
	TableRefusalWrongType
	TableRefusalStreamType
	TableRefusalEpochAhead
	TableRefusalPlaceGuard
	TableRefusalFormula
	TableRefusalPropGuard
	TableRefusalOrphan
)

// ParseTableRefusal parses a raw table Lua reply reason token into a typed TableRefusal.
func ParseTableRefusal(reason string) TableRefusal {
	switch reason {
	case "STALE":
		return TableRefusalStale
	case "MEMBEREPOCH":
		return TableRefusalMemberEpoch
	case "MEMBEREXISTS":
		return TableRefusalMemberExists
	case "PLACED":
		return TableRefusalPlaced
	case "DRIFT":
		return TableRefusalDrift
	case "NOTABLE":
		return TableRefusalNoTable
	case "EXISTS":
		return TableRefusalExists
	case "NOROW":
		return TableRefusalNoRow
	case "NOCOL":
		return TableRefusalNoCol
	case "TEXT":
		return TableRefusalText
	case "NOTMEMBER":
		return TableRefusalNotMember
	case "OCCUPIEDVALUE":
		return TableRefusalOccupiedValue
	case "OCCUPIED":
		return TableRefusalOccupied
	case "OCCUPIEDCELLS":
		return TableRefusalOccupiedCells
	case "OWNEDALIAS":
		return TableRefusalOwnedAlias
	case "NOMEMBER":
		return TableRefusalNoMember
	case "NOVIEW":
		return TableRefusalNoView
	case "VIEWTABLE":
		return TableRefusalViewTable
	case "SUMMARY":
		return TableRefusalSummary
	case "SELF":
		return TableRefusalSelf
	case "WHERE":
		return TableRefusalWhere
	case "COLEXISTS":
		return TableRefusalColExists
	case "DEPENDS":
		return TableRefusalDepends
	case "LASTCOL":
		return TableRefusalLastCol
	case "SORTED":
		return TableRefusalSorted
	case "SORTKEY":
		return TableRefusalSortKey
	case "SORTKEEP":
		return TableRefusalSortKeep
	case "NOTTEXT":
		return TableRefusalNotText
	case "BOUND":
		return TableRefusalBound
	case "EPOCH":
		return TableRefusalEpoch
	case "CONDITION":
		return TableRefusalCondition
	case "ARGS":
		return TableRefusalArgs
	case "REVISION":
		return TableRefusalRevision
	case "MEMBERREVISION":
		return TableRefusalMemberRevision
	case "FIELDGUARD":
		return TableRefusalFieldGuard
	case "OPCONFLICT":
		return TableRefusalOpConflict
	case "LIMIT":
		return TableRefusalLimit
	case "RESERVEDFIELD":
		return TableRefusalReservedField
	case "TWICE":
		return TableRefusalTwice
	case "SCORE":
		return TableRefusalScore
	case "OVERFLOW":
		return TableRefusalOverflow
	case "MUTATION":
		return TableRefusalMutation
	case "MANIFEST":
		return TableRefusalManifest
	case "STREAMFULL":
		return TableRefusalStreamFull
	case "SCHEMA":
		return TableRefusalSchema
	case "OPERATION":
		return TableRefusalOperation
	case "MEMBER":
		return TableRefusalMember
	case "WRONGTYPE":
		return TableRefusalWrongType
	case "STREAMTYPE":
		return TableRefusalStreamType
	case "EPOCHAHEAD":
		return TableRefusalEpochAhead
	case "PLACEGUARD":
		return TableRefusalPlaceGuard
	case "FORMULA":
		return TableRefusalFormula
	case "PROPGUARD":
		return TableRefusalPropGuard
	case "ORPHAN":
		return TableRefusalOrphan
	default:
		return TableRefusalUnknown
	}
}

package typedrec_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func TestParseTableRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want typedrec.TableRefusal
	}{
		{"NOCOL", typedrec.TableRefusalNoCol},
		{"TEXT", typedrec.TableRefusalText},
		{"NOROW", typedrec.TableRefusalNoRow},
		{"NOTMEMBER", typedrec.TableRefusalNotMember},
		{"EPOCH", typedrec.TableRefusalEpoch},
		{"REVISION", typedrec.TableRefusalRevision},
		{"MEMBERREVISION", typedrec.TableRefusalMemberRevision},
		{"CONDITION", typedrec.TableRefusalCondition},
		{"FIELDGUARD", typedrec.TableRefusalFieldGuard},
		{"SELF", typedrec.TableRefusalSelf},
		{"WHERE", typedrec.TableRefusalWhere},
		{"COLEXISTS", typedrec.TableRefusalColExists},
		{"DEPENDS", typedrec.TableRefusalDepends},
		{"FORMULA", typedrec.TableRefusalFormula},
		{"LASTCOL", typedrec.TableRefusalLastCol},
		{"SORTED", typedrec.TableRefusalSorted},
		{"SORTKEY", typedrec.TableRefusalSortKey},
		{"SORTKEEP", typedrec.TableRefusalSortKeep},
		{"NOTTEXT", typedrec.TableRefusalNotText},
		{"BOUND", typedrec.TableRefusalBound},
		{"OCCUPIED", typedrec.TableRefusalOccupied},
		{"OCCUPIEDCELLS", typedrec.TableRefusalOccupiedCells},
		{"OWNEDALIAS", typedrec.TableRefusalOwnedAlias},
		{"NOMEMBER", typedrec.TableRefusalNoMember},
		{"NOVIEW", typedrec.TableRefusalNoView},
		{"VIEWTABLE", typedrec.TableRefusalViewTable},
		{"SUMMARY", typedrec.TableRefusalSummary},
		{"ARGS", typedrec.TableRefusalArgs},
		{"LIMIT", typedrec.TableRefusalLimit},
		{"RESERVEDFIELD", typedrec.TableRefusalReservedField},
		{"TWICE", typedrec.TableRefusalTwice},
		{"SCORE", typedrec.TableRefusalScore},
		{"OVERFLOW", typedrec.TableRefusalOverflow},
		{"MUTATION", typedrec.TableRefusalMutation},
		{"MANIFEST", typedrec.TableRefusalManifest},
		{"STREAMFULL", typedrec.TableRefusalStreamFull},
		{"SCHEMA", typedrec.TableRefusalSchema},
		{"OPERATION", typedrec.TableRefusalOperation},
		{"MEMBER", typedrec.TableRefusalMember},
		{"OPCONFLICT", typedrec.TableRefusalOpConflict},
		{"WRONGTYPE", typedrec.TableRefusalWrongType},
		{"STREAMTYPE", typedrec.TableRefusalStreamType},
		{"EPOCHAHEAD", typedrec.TableRefusalEpochAhead},
		{"PLACEGUARD", typedrec.TableRefusalPlaceGuard},
		{"PROPGUARD", typedrec.TableRefusalPropGuard},
		{"ORPHAN", typedrec.TableRefusalOrphan},
		{"RESIDUE", typedrec.TableRefusalResidue},
		{"UNRECOGNIZED", typedrec.TableRefusalUnknown},
	}
	for _, tc := range cases {
		if got := typedrec.ParseTableRefusal(tc.in); got != tc.want {
			t.Errorf("ParseTableRefusal(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

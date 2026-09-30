package stepbuild

import "fmt"

// The bounds of one Layer 1 write step. Every constant copies one row of
// section 6 of the Layer 1 table contract (tset/1, contract revision 3), and
// the section stands beside it: a change to the contract is a changed row
// here and nowhere else. MiB is 1,048,576 bytes.
//
// These are the bounds the cut works to. A step is cut down to them; a value
// that no cut can bring under one (a name, a field value, a member's own
// fields) is refused whole, before any step is returned.
const (
	// The step-level bounds: the cut splits work to stay under these.

	LimitRequestBytes      = 4 << 20 // section 6: encoded write request, before JSON decoding
	LimitEntries           = 256     // section 6: entries per step
	LimitTables            = 4       // section 6: tables per step (section 3: tables are limited to four)
	LimitCandidates        = 2000    // section 6: member mutation candidates per write, no-ops included
	LimitGuardOnly         = 4000    // section 6: guard-only members per write, additional to the candidates
	LimitEntryIDs          = 2000    // section 6: IDs in an entry
	LimitRowPairs          = 100     // section 6: normal row add/delete names, (table,row) pairs; counted before dedup, the stricter reading
	LimitNotes             = 100     // section 6: notes per step
	LimitAboutIDs          = 4000    // section 6: aligned about IDs per step, before dedup
	LimitFieldObservations = 768000  // section 6: field-value observations per write (128 x 6,000)
	LimitLineBytes         = 1 << 20 // section 6: one generated log line
	LimitLineIDs           = 2000    // section 6: IDs in one log line
	LimitPlannedArgvBytes  = 8 << 20 // section 6: summed encoded argv bytes of the planned commands (counted as an upper bound with a margin, layout.go)

	// LimitReceiptBytes is the stored done receipt's cap (section 6 and
	// section 5). A step with an op reserves it whole in its planned argv
	// bytes: the strictest reading, since the receipt's size is not known
	// before the step runs.
	LimitReceiptBytes = 32 << 10

	// The item-level bounds: no cut brings a value under these, so a value
	// over one refuses the whole build.

	LimitNameBytes       = 256      // section 6: an ID, row, column, table, op or application field name
	LimitFieldValueBytes = 64 << 10 // section 6: one application field value
	LimitIntentBytes     = 64 << 10 // section 6: the intent string
	LimitResultBytes     = 4 << 10  // section 6: the caller result
	LimitFieldsPerMember = 128      // section 6: set/each effective fields of one member
	LimitUnsetNames      = 128      // section 6: unset names of one entry, before dedup
	LimitBeforeFields    = 128      // section 6: before_fields names of one entry, before dedup

	// LimitNesting is section 6's JSON nesting depth. The encoding this
	// package writes never nests deeper than five (a note's meta object), so
	// the bound is stated and tested, never reached.
	LimitNesting = 16

	// DefaultMemberPrefixBytes is the longest member prefix a build assumes
	// when Config.MemberPrefixBytes is zero: a record's key is its table's
	// member prefix and the stored ID (section 1.2), and every command that
	// writes a record carries that key. The prefixes are a definition's, not
	// the contract's, and the contract sets no bound on one; the default is
	// the longest that Layer 1 accepts for a definition (the tset-l1 branch,
	// table_set.lua:443 refuses a longer prefix with CONFIG), so that a caller
	// who names none is safe. A caller that knows the longest of its own
	// passes it, and the cut is finer for it.
	DefaultMemberPrefixBytes = 512
)

// Bounds is the step-level bounds one build cuts to. The zero Bounds is
// Contract. A caller with a measured chunk smaller than the contract's
// admission ceiling (section 6: the 2,000 is a ceiling, not the production
// chunk) sets any field lower; a field over the contract's number, or below
// one, refuses the build.
type Bounds struct {
	RequestBytes      int // encoded bytes of one step's request
	Entries           int // wire entries in one step, attached guards included
	Tables            int // distinct tables in one step
	Candidates        int // members changed by create, move or remove entries
	GuardOnly         int // members named by guard entries
	EntryIDs          int // members in one wire entry
	RowPairs          int // row names of the rows entries of one step, before dedup
	Notes             int // notes in one step
	AboutIDs          int // about IDs of the member entries and notes of one step
	FieldObservations int // field-value observations of one step
	LineBytes         int // bytes of one generated log line
	LineIDs           int // members in one generated log line
	PlannedArgvBytes  int // planned argv bytes of one step
}

// Contract is the contract's own bounds: every field at its section 6 number.
func Contract() Bounds {
	return Bounds{
		RequestBytes:      LimitRequestBytes,
		Entries:           LimitEntries,
		Tables:            LimitTables,
		Candidates:        LimitCandidates,
		GuardOnly:         LimitGuardOnly,
		EntryIDs:          LimitEntryIDs,
		RowPairs:          LimitRowPairs,
		Notes:             LimitNotes,
		AboutIDs:          LimitAboutIDs,
		FieldObservations: LimitFieldObservations,
		LineBytes:         LimitLineBytes,
		LineIDs:           LimitLineIDs,
		PlannedArgvBytes:  LimitPlannedArgvBytes,
	}
}

// resolve is the zero Bounds as the contract's, and any other Bounds as it is
// when every field is inside [1, contract], else a refusal.
func (b Bounds) resolve() (Bounds, error) {
	if b == (Bounds{}) {
		return Contract(), nil
	}
	c := Contract()
	for _, f := range []struct {
		name      string
		got, want int
	}{
		{"RequestBytes", b.RequestBytes, c.RequestBytes},
		{"Entries", b.Entries, c.Entries},
		{"Tables", b.Tables, c.Tables},
		{"Candidates", b.Candidates, c.Candidates},
		{"GuardOnly", b.GuardOnly, c.GuardOnly},
		{"EntryIDs", b.EntryIDs, c.EntryIDs},
		{"RowPairs", b.RowPairs, c.RowPairs},
		{"Notes", b.Notes, c.Notes},
		{"AboutIDs", b.AboutIDs, c.AboutIDs},
		{"FieldObservations", b.FieldObservations, c.FieldObservations},
		{"LineBytes", b.LineBytes, c.LineBytes},
		{"LineIDs", b.LineIDs, c.LineIDs},
		{"PlannedArgvBytes", b.PlannedArgvBytes, c.PlannedArgvBytes},
	} {
		if f.got < 1 || f.got > f.want {
			return Bounds{}, &InputError{Entry: -1, Field: "bounds", Reason: fmt.Sprintf("%s is %d: a bound is from 1 to the contract's %d", f.name, f.got, f.want)}
		}
	}
	return b, nil
}

// bound names one bound of the contract for a refusal: what it measures and
// the section that sets it.
type bound struct{ name, section string }

var (
	boundRequest     = bound{"encoded request bytes", "section 6"}
	boundEntries     = bound{"entries per step", "section 6"}
	boundTables      = bound{"tables per step", "section 6"}
	boundCandidates  = bound{"member mutation candidates per step", "section 6"}
	boundGuardOnly   = bound{"guard-only members per step", "section 6"}
	boundEntryIDs    = bound{"IDs in an entry", "section 6"}
	boundRowPairs    = bound{"row add/delete names per step", "section 6"}
	boundNotes       = bound{"notes per step", "section 6"}
	boundAbout       = bound{"about IDs per step", "section 6"}
	boundObserved    = bound{"field-value observations per step", "section 6"}
	boundLineBytes   = bound{"generated log line bytes", "section 6"}
	boundLineIDs     = bound{"IDs in one log line", "section 6"}
	boundArgv        = bound{"planned argv bytes (an upper bound with a margin of 25 percent)", "section 6"}
	boundName        = bound{"name bytes", "section 6"}
	boundFieldValue  = bound{"field value bytes", "section 6"}
	boundIntent      = bound{"intent bytes", "section 6"}
	boundResult      = bound{"caller result bytes", "section 6"}
	boundFields      = bound{"effective set fields per member", "section 6"}
	boundUnset       = bound{"unset names per entry", "section 6"}
	boundBeforeNames = bound{"before_fields names per entry", "section 6"}
)

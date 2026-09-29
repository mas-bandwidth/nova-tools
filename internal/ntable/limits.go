package ntable

import "fmt"

// The bounds of a batch manifest and of a read set. table.lua holds the same
// numbers (T.limits) and docs/SPEC-NOVA-TABLE.md states them; a test compares
// the three. A refusal names the bound and the count found, never the input.
const (
	LimitManifestBytes    = 1 << 20 // canonical encoded request
	LimitChangedEntries   = 128     // entries with changes
	LimitGuardEntries     = 1024    // guard-only entries
	LimitMemberIDBytes    = 256
	LimitFieldValueBytes  = 64 << 10
	LimitSetFields        = 128  // set fields per member
	LimitUnsetFields      = 1000 // unset fields per member
	LimitFieldGuards      = 1000 // guards per member
	LimitOneOfOptions     = 1000 // options in one guard
	LimitReadSetMembers   = 1024
	LimitColumns          = 1000   // columns per table
	LimitRows             = 100000 // rows per table
	limitNameManifest     = "manifest bytes"
	limitNameChanged      = "entries with changes"
	limitNameGuardEntries = "guard-only entries"
	limitNameMemberID     = "member id bytes"
	limitNameFieldValue   = "field value bytes"
	limitNameSet          = "set fields per member"
	limitNameUnset        = "unset fields per member"
	limitNameGuards       = "guards per member"
	limitNameOneOf        = "one_of options"
	limitNameReadSet      = "read set members"
	limitNameColumns      = "columns per table"
	limitNameRows         = "rows per table"
)

// LimitError is a named LIMIT refusal: the bound and the count found, and the
// member at fault when one is. It wraps ErrLimit.
type LimitError struct {
	Name            string
	Bound, Observed int
	Member          string
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: %s: bound %d, observed %d", ErrLimit, e.Name, e.Bound, e.Observed)
}

func (e *LimitError) Unwrap() error { return ErrLimit }

func over(name string, bound, observed int, member string) error {
	if observed <= bound {
		return nil
	}
	return &LimitError{Name: name, Bound: bound, Observed: observed, Member: member}
}

// entryHasChanges says what the server says: a create, a move, a remove, a
// nonempty set or a nonempty unset.
func entryHasChanges(m BatchMemberEntry) bool {
	return m.Create != nil || m.Move != nil || m.Remove || len(m.Set) > 0 || len(m.Unset) > 0
}

// CheckBatchBounds applies every bound to a decoded manifest, in the order the
// server applies them: per entry (id, set, unset, guards, one_of), then the
// entry counts.
func CheckBatchBounds(m *BatchManifest) error {
	changed, guards := 0, 0
	for _, e := range m.Members {
		if err := over(limitNameMemberID, LimitMemberIDBytes, len(e.ID), ""); err != nil {
			return err
		}
		if err := over(limitNameSet, LimitSetFields, len(e.Set), e.ID); err != nil {
			return err
		}
		for _, v := range e.Set {
			if err := over(limitNameFieldValue, LimitFieldValueBytes, len(v), e.ID); err != nil {
				return err
			}
		}
		if err := over(limitNameUnset, LimitUnsetFields, len(e.Unset), e.ID); err != nil {
			return err
		}
		if e.Expect != nil {
			for _, g := range e.Expect.Fields {
				if err := over(limitNameOneOf, LimitOneOfOptions, len(g.OneOf), e.ID); err != nil {
					return err
				}
			}
			if err := over(limitNameGuards, LimitFieldGuards, len(e.Expect.Fields), e.ID); err != nil {
				return err
			}
		}
		if entryHasChanges(e) {
			changed++
		} else {
			guards++
		}
	}
	if err := over(limitNameChanged, LimitChangedEntries, changed, ""); err != nil {
		return err
	}
	return over(limitNameGuardEntries, LimitGuardEntries, guards, "")
}

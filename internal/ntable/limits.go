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
	LimitColumns          = 1000               // columns per table
	LimitRows             = 100000             // rows per table
	LimitReceiptBytes     = LimitManifestBytes // the encoded batch delta of one receipt
	LimitBatchValueBytes  = 16 << 20           // bytes of the field values a batch's entries name, before and after
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
	limitNameReceipt      = "receipt bytes"
	limitNameBatchValues  = "value bytes per batch"
)

// ReceiptValueBytes is the longest field value a receipt, the change event and
// the operation record hold in full; a longer one is recorded as its length and
// SHA-1 (FieldChange.BeforeBytes, BeforeSHA1). table.lua holds the same number
// (T.receipt_value_bytes); a test compares them.
const ReceiptValueBytes = 64

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

// Advice says how to get under the bound: what to narrow. A request that is
// narrowed is a different transaction, with its own operation id.
func (e *LimitError) Advice() string {
	switch e.Name {
	case limitNameChanged, limitNameGuardEntries:
		return fmt.Sprintf("send at most %d in one manifest; split the entries across manifests, each its own transaction with its own operation id", e.Bound)
	case limitNameManifest:
		return fmt.Sprintf("send at most %d bytes; fewer entries or shorter values, split across manifests with their own operation ids", e.Bound)
	case limitNameMemberID:
		return fmt.Sprintf("use a member id of at most %d bytes", e.Bound)
	case limitNameFieldValue:
		return fmt.Sprintf("keep a field value under %d bytes: store the value elsewhere and keep its identity in the field", e.Bound)
	case limitNameSet, limitNameUnset:
		return fmt.Sprintf("change at most %d fields of a member in one manifest; change the rest in a later manifest", e.Bound)
	case limitNameGuards:
		return fmt.Sprintf("guard at most %d fields of a member in one manifest", e.Bound)
	case limitNameOneOf:
		return fmt.Sprintf("list at most %d options in a one_of guard", e.Bound)
	case limitNameReadSet:
		return fmt.Sprintf("read at most %d members in one call; split the read", e.Bound)
	case limitNameColumns:
		return fmt.Sprintf("a table holds at most %d columns; remove one first or use another table", e.Bound)
	case limitNameRows:
		return fmt.Sprintf("a table holds at most %d rows; delete a row first or use another table", e.Bound)
	case limitNameBatchValues:
		return fmt.Sprintf("a batch touches at most %d bytes of field values (every before-value and after-value of the fields its entries name); change fewer members or fields in one manifest, split across manifests with their own operation ids", e.Bound)
	case limitNameReceipt:
		return fmt.Sprintf("the receipt of one batch is at most %d bytes and records every changed field; change fewer members or fewer fields in one manifest, split across manifests with their own operation ids", e.Bound)
	}
	return "narrow the request"
}

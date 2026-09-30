package ntable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

const (
	FnClear          = "ns_table_clear"
	FnCreate         = "ns_table_create"
	FnSet            = "ns_table_set"
	FnRowSet         = "ns_table_row_set"
	FnRowsAdd        = "ns_table_rows_add"
	FnRowsHide       = "ns_table_rows_hide"
	FnDrop           = "ns_table_drop"
	FnDropDefinition = "ns_table_drop_definition"
	FnMemberCreate   = "ns_table_member_create"
	FnMemberFind     = "ns_table_member_find"
	FnRowAdd         = "ns_table_row_add"
	FnRowDel         = "ns_table_row_del"
	FnCellAdd        = "ns_table_cell_add"
	FnCellRemove     = "ns_table_cell_remove"
	FnCellMove       = "ns_table_cell_move"
	FnBind           = "ns_table_bind"
	FnRead           = "ns_table_read"
	FnCheck          = "ns_table_check"
	FnList           = "ns_table_list"
	FnMembers        = "ns_table_members"
	FnApply          = "ns_table_apply"
	FnReadSet        = "ns_table_read_set"
)

var (
	ErrExists            = errors.New("exists with another definition")
	ErrOccupied          = errors.New("shape would delete or hide placed members")
	ErrOwnedAlias        = errors.New("binding target is table-owned storage")
	ErrStale             = errors.New("requested epoch is stale, not the active epoch")
	ErrEpochAhead        = errors.New("requested epoch is ahead of the active epoch")
	ErrMemberEpoch       = errors.New("member belongs to another epoch")
	ErrPlaced            = errors.New("member already has a place in this table")
	ErrDrift             = errors.New("member record and owned set disagree")
	ErrMemberExists      = errors.New("member identity already exists")
	ErrRevisionMismatch  = errors.New("table revision mismatch")
	ErrMemberRevision    = errors.New("member revision mismatch")
	ErrFieldGuard        = errors.New("failed field guard")
	ErrPlaceGuard        = errors.New("failed place guard")
	ErrPropGuard         = errors.New("failed property guard")
	ErrOpConflict        = errors.New("operation ID conflict")
	ErrLimit             = errors.New("limit exceeded")
	ErrReservedField     = errors.New("reserved field write")
	ErrDuplicateMember   = errors.New("duplicate manifest member")
	ErrInvalidScore      = errors.New("invalid score")
	ErrCounterOverflow   = errors.New("counter overflow")
	ErrMutation          = errors.New("incompatible mutation")
	ErrWrongType         = errors.New("WRONGTYPE")
	ErrMalformedManifest = errors.New("malformed manifest")
	// ErrUnknownOutcome is a transport failure: the store did not answer, so the
	// batch may or may not have been applied (changed=unknown). Send the same
	// manifest again with the same operation id.
	ErrUnknownOutcome = errors.New("the store did not confirm the batch")
)

// Refusal is the store's no, or a rule's, with its code and its sentence: the
// operation, what was expected against what was found, whether anything changed,
// and the next command. A refusal is not a transport failure (ErrUnknownOutcome)
// and not a manifest that cannot be read (ErrMalformedManifest); a caller tells
// them apart with errors.Is, or IsRefusal.
type Refusal struct {
	Code     string // the store's refusal code: NOTMEMBER, LIMIT, MEMBERREVISION, ...
	Location string // the operation: table "demo" batch "op-1" member "a"
	Sentence string // what was expected against what was found
	Next     string // a command that runs when pasted
	Guarded  bool   // a batch or a read set: it wrote nothing, and says so
	cause    error
}

func (r *Refusal) Error() string {
	s := r.Location + ": " + r.Sentence
	if r.Guarded {
		s += "; code=" + r.Code + "; changed=no"
	}
	return s + "; run: " + r.Next
}

func (r *Refusal) Unwrap() error { return r.cause }

// IsRefusal says err is a refusal: the store or a rule said no and nothing changed.
func IsRefusal(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}

// prose is an error with a sentence of its own that still answers errors.Is for
// its sentinel: the store's code is a field of the refusal, not part of a sentence.
type prose struct {
	text string
	is   error
}

func (p *prose) Error() string { return p.text }
func (p *prose) Unwrap() error { return p.is }
func say(is error, format string, a ...any) error {
	return &prose{fmt.Sprintf(format, a...), is}
}

// BoundError names the other writer. The table may read the binding but
// cannot acquire write ownership merely by displaying it.
type BoundError struct{ Table, Row, Col, Key, Owner string }

func (e *BoundError) Error() string {
	s := fmt.Sprintf("%s.%s.%s is bound to %s, owned elsewhere", e.Table, e.Row, e.Col, e.Key)
	if e.Owner != "" {
		s += "; run: " + e.Owner
	}
	return s
}

// WriteOptions binds a mutation to the epoch its caller observed. Omitting
// options means epoch zero; a stale call is never retried into a new epoch.
// Actor, Fence and Idem accompany the change event; authorization and the
// coordinator lease are separate from this table primitive.
type WriteOptions struct {
	Epoch uint64
	Actor string
	Fence string
	Idem  string
	// Receipt receives the committed event without another store exchange.
	Receipt *Receipt
}

// Receipt identifies the durable table change. Idem is recorded as caller
// metadata; this primitive does not deduplicate attempts.
type Receipt struct {
	ID                   string
	Epoch, Before, After uint64
	Outcome              string
	BatchDelta           *BatchDelta
	// Replay is true when ApplyBatch returned the receipt recorded for an
	// operation it had already applied, and wrote nothing.
	Replay bool
}

type operation struct {
	table, row, col, member string
	opID                    string
	view                    bool
	batch                   bool
	readSet                 bool
}

// guarded reports an operation whose refusal writes nothing by contract: a
// batch, and the read that prepares one.
func (o operation) guarded() bool { return o.batch || o.readSet }

func (o operation) location() string {
	kind := "table"
	if o.view {
		if o.table == "" {
			return "views"
		}
		kind = "view"
	}
	s := fmt.Sprintf("%s %q", kind, o.table)
	if o.batch {
		if o.opID != "" {
			s += fmt.Sprintf(" batch %q", o.opID)
		} else {
			s += " batch"
		}
	}
	if o.readSet {
		s += " read set"
	}
	if o.row != "" {
		s += fmt.Sprintf(" row %q", o.row)
	}
	if o.col != "" {
		s += fmt.Sprintf(" column %q", o.col)
	}
	if o.member != "" {
		s += fmt.Sprintf(" member %q", o.member)
	}
	return s
}

// shellWord makes the suggested command safe to paste even for spaced
// rows or members containing shell metacharacters.
func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func removeMembersCommand(table, row, col string, members []any) string {
	args := []string{table, row, col}
	for _, member := range members {
		args = append(args, fmt.Sprint(member))
	}
	endFlags := false
	for i, arg := range args {
		endFlags = endFlags || strings.HasPrefix(arg, "-")
		args[i] = shellWord(arg)
	}
	if endFlags {
		args = append([]string{"--"}, args...)
	}
	return "nova-table cell remove " + strings.Join(args, " ")
}

// runUnlessNamed is "; run: <remedy>", or nothing when err names its own next
// step already (a "; run:" of its own, as nova-table's refusal of an FCALL the
// store's library lacks does), so a line carries one remedy.
func runUnlessNamed(err error, remedy string) string {
	if strings.Contains(err.Error(), "; run: ") {
		return ""
	}
	return "; run: " + remedy
}

// memberReadCommand names the verb that shows a member's place, score,
// revision and fields now.
func memberReadCommand(table, member string, flags ...string) string {
	args := []string{table, member}
	endFlags := false
	for i, arg := range args {
		endFlags = endFlags || strings.HasPrefix(arg, "-")
		args[i] = shellWord(arg)
	}
	if endFlags {
		args = append([]string{"--"}, args...)
	}
	return "nova-table member read " + strings.Join(append(flags, args...), " ")
}

// words joins the detail elements of a refusal reply.
func words(detail []any) string {
	parts := make([]string, len(detail))
	for i, v := range detail {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, " ")
}

func (o operation) remedy() string {
	if o.view {
		if o.table == "" {
			return "nova-table help view"
		}
		return "nova-table view show " + shellWord(o.table)
	}
	return "nova-table show " + shellWord(o.table)
}
func (o operation) refused(reply []any) error {
	if len(reply) == 0 {
		return fmt.Errorf("%s: empty function reply; run: %s", o.location(), o.remedy())
	}
	if fmt.Sprint(reply[0]) != "REFUSED" {
		return nil
	}
	if len(reply) < 2 {
		return fmt.Errorf("%s: malformed refusal; run: %s", o.location(), o.remedy())
	}
	reason := fmt.Sprint(reply[1])
	var cause error
	remedy := o.remedy()
	refusal := typedrec.ParseTableRefusal(reason)
	switch refusal {
	case typedrec.TableRefusalStale:
		cause = fmt.Errorf("%w: requested %v, active %v", ErrStale, reply[2], reply[3])
	case typedrec.TableRefusalEpochAhead:
		cause = fmt.Errorf("%w: requested epoch %v, active epoch %v (show prints the active epoch)", ErrEpochAhead, reply[2], reply[3])
	case typedrec.TableRefusalMemberEpoch:
		if o.guarded() && len(reply) >= 5 {
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: the member is of epoch %v, the active epoch is %v", ErrMemberEpoch, reply[3], reply[4])
			// A read at the active epoch repeats this refusal; the member is read at its own.
			if theirs, err1 := strconv.ParseUint(fmt.Sprint(reply[3]), 10, 64); err1 == nil {
				if active, err2 := strconv.ParseUint(fmt.Sprint(reply[4]), 10, 64); err2 == nil && theirs < active {
					remedy = memberReadCommand(o.table, o.member, "--at-epoch", fmt.Sprint(reply[3]))
				}
			}
		} else {
			cause = fmt.Errorf("%w: %v", ErrMemberEpoch, reply[2:])
		}
	case typedrec.TableRefusalMemberExists:
		if o.guarded() && len(reply) >= 4 {
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: expected absent, observed %v", ErrMemberExists, reply[3])
			remedy = memberReadCommand(o.table, o.member)
		} else {
			cause = ErrMemberExists
		}
	case typedrec.TableRefusalPlaced:
		cause = fmt.Errorf("%w: %v", ErrPlaced, reply[2:])
	case typedrec.TableRefusalDrift:
		switch {
		case o.guarded() && len(reply) == 6 && fmt.Sprint(reply[5]) == "set-only":
			o.member = fmt.Sprint(reply[4])
			cause = say(ErrDrift, "record and owned set disagree: the owned set at row %q column %q holds member %q, and no record places it there; list the cell, and run check for every such disagreement, then remove the stray entry or restore the member's record (nova-table has no repair verb)", reply[2], reply[3], reply[4])
			remedy = "nova-table cell members " + shellWord(o.table) + " " + shellWord(fmt.Sprint(reply[2])) + " " + shellWord(fmt.Sprint(reply[3]))
		case o.guarded() && len(reply) == 5:
			o.member = fmt.Sprint(reply[4])
			cause = say(ErrDrift, "record and owned set disagree: the record places member %q at row %q column %q, and the owned set there does not hold it; list the cell, and run check for every such disagreement", reply[4], reply[2], reply[3])
			remedy = "nova-table cell members " + shellWord(o.table) + " " + shellWord(fmt.Sprint(reply[2])) + " " + shellWord(fmt.Sprint(reply[3]))
		case o.guarded() && len(reply) == 4:
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: the record says place %q, which is not a usable owned cell", ErrDrift, reply[3])
			remedy = "nova-table check " + shellWord(o.table)
		default:
			cause = fmt.Errorf("%w: %v", ErrDrift, reply[2:])
		}
	case typedrec.TableRefusalNoTable:
		cause = ErrNoTable
		remedy = "nova-table create " + shellWord(o.table) + " --columns <columns>"
		if o.guarded() {
			remedy = "nova-table list"
		}
	case typedrec.TableRefusalExists:
		cause = ErrExists
		remedy = "nova-table set " + shellWord(o.table) + " --columns <columns>"
	case typedrec.TableRefusalNoRow:
		cause = errors.New("no such row")
	case typedrec.TableRefusalNoCol:
		cause = errors.New("no such column")
	case typedrec.TableRefusalText:
		cause = errors.New("text column holds no ordered set; choose a body column")
	case typedrec.TableRefusalNotMember:
		if o.guarded() && len(reply) >= 5 {
			o.member = fmt.Sprint(reply[2])
			cause = say(ErrNotMember, "not a member: expected %v, observed %v", reply[4], reply[3])
			remedy = memberReadCommand(o.table, o.member)
		} else {
			cause = ErrNotMember
			remedy = "nova-table cell members " + shellWord(o.table) + " " + shellWord(o.row) + " " + shellWord(o.col)
		}
	case typedrec.TableRefusalOccupiedValue:
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		cause = fmt.Errorf("%w: text cell is nonempty; clear it with row set first", ErrOccupied)
	case typedrec.TableRefusalOccupied:
		if len(reply) != 5 {
			return fmt.Errorf("%s: malformed occupied-cell refusal", o.location())
		}
		members, ok := reply[4].([]any)
		if !ok || len(members) == 0 {
			return fmt.Errorf("%s: malformed occupied members", o.location())
		}
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		names := make([]string, 0, len(members))
		for _, member := range members {
			names = append(names, fmt.Sprintf("%q", member))
		}
		cause = fmt.Errorf("%w: %s; move each member to a retained owned cell or remove it first", ErrOccupied, strings.Join(names, ", "))
		remedy = removeMembersCommand(o.table, o.row, o.col, members)
	case typedrec.TableRefusalOccupiedCells:
		if len(reply) != 3 {
			return fmt.Errorf("%s: malformed occupied-cells refusal", o.location())
		}
		cells, ok := reply[2].([]any)
		if !ok || len(cells) == 0 {
			return fmt.Errorf("%s: malformed occupied cells", o.location())
		}
		var blockers, commands []string
		for _, value := range cells {
			cell, ok := value.([]any)
			if !ok || len(cell) != 3 {
				return fmt.Errorf("%s: malformed occupied cell", o.location())
			}
			members, ok := cell[2].([]any)
			if !ok || len(members) == 0 {
				return fmt.Errorf("%s: malformed occupied members", o.location())
			}
			row, col := fmt.Sprint(cell[0]), fmt.Sprint(cell[1])
			blockers = append(blockers, fmt.Sprintf("row %q column %q members %q", row, col, members))
			commands = append(commands, removeMembersCommand(o.table, row, col, members))
		}
		cause = fmt.Errorf("%w: %s; move the members or remove them first", ErrOccupied, strings.Join(blockers, "; "))
		remedy = strings.Join(commands, "; ")
	case typedrec.TableRefusalOwnedAlias:
		if len(reply) != 5 {
			return fmt.Errorf("%s: malformed owned-alias refusal", o.location())
		}
		o.row, o.col = fmt.Sprint(reply[2]), fmt.Sprint(reply[3])
		cause = fmt.Errorf("%w: %q; choose a set owned outside nova-table", ErrOwnedAlias, reply[4])
	case typedrec.TableRefusalNoMember:
		cause = errors.New("wants at least one member")
	case typedrec.TableRefusalNoView:
		cause = ErrNoView
		remedy = "nova-table view set " + shellWord(o.table) + " --tables <a,b,...>"
	case typedrec.TableRefusalViewTable:
		if len(reply) != 3 {
			return fmt.Errorf("%s: malformed view-table refusal", o.location())
		}
		cause = fmt.Errorf("referenced table %q does not exist", reply[2])
		remedy = "nova-table create " + shellWord(fmt.Sprint(reply[2])) + " --columns <columns>"
	case typedrec.TableRefusalSummary:
		if len(reply) != 4 {
			return fmt.Errorf("%s: malformed summary refusal", o.location())
		}
		cause = fmt.Errorf("summary wants a count column in table %q; %q is not one", reply[2], reply[3])
		remedy = "nova-table show " + shellWord(fmt.Sprint(reply[2]))
	case typedrec.TableRefusalSelf:
		cause = fmt.Errorf("%v cannot go before or after itself", reply[2])
	case typedrec.TableRefusalWhere:
		cause = errors.New("a place is --first, --last, --before <x> or --after <x>")
	case typedrec.TableRefusalColExists:
		o.col = fmt.Sprint(reply[2])
		cause = errors.New("the column is already there")
		remedy = "nova-table col move " + shellWord(o.table) + " " + shellWord(o.col) + " --last"
	case typedrec.TableRefusalDepends:
		o.col = fmt.Sprint(reply[2])
		cause = fmt.Errorf("column %q is a formula that reads it; remove that column first", reply[3])
		remedy = "nova-table col del " + shellWord(o.table) + " " + shellWord(fmt.Sprint(reply[3]))
	case typedrec.TableRefusalFormula:
		if len(reply) != 5 {
			return fmt.Errorf("%s: malformed formula refusal", o.location())
		}
		o.col = fmt.Sprint(reply[2])
		arg, found := fmt.Sprint(reply[3]), fmt.Sprint(reply[4])
		if found == "missing" {
			cause = fmt.Errorf("it reads column %q, which the table does not have; add %q as a count column first", arg, arg)
			remedy = "nova-table col add " + shellWord(o.table) + " " + shellWord(arg)
		} else {
			cause = fmt.Errorf("it reads column %q, a %s column; a formula reads count columns only: name a count column", arg, found)
			remedy = "nova-table show " + shellWord(o.table)
		}
	case typedrec.TableRefusalLastCol:
		o.col = fmt.Sprint(reply[2])
		cause = errors.New("a table keeps at least one column")
		remedy = "nova-table drop " + shellWord(o.table)
	case typedrec.TableRefusalSorted:
		cause = fmt.Errorf("the rows are kept sorted by %v, so no row is placed by hand; end the standing sort first", reply[2])
		remedy = "nova-table row sort " + shellWord(o.table) + " --manual"
	case typedrec.TableRefusalSortKey:
		cause = fmt.Errorf("rows sort by name, label, a count column or a text column, not %v", reply[2])
	case typedrec.TableRefusalSortKeep:
		cause = fmt.Errorf("a standing sort is by name or label; by %v the rows are sorted once, without --keep", reply[2])
		remedy = "nova-table row sort " + shellWord(o.table) + " --by " + shellWord(fmt.Sprint(reply[2]))
	case typedrec.TableRefusalNotText:
		cause = errors.New("not a text column; row set writes text columns only")
	case typedrec.TableRefusalBound:
		if len(reply) < 6 {
			return fmt.Errorf("%s: malformed bound-cell refusal", o.location())
		}
		boundErr := &BoundError{Table: o.table, Row: fmt.Sprint(reply[2]), Col: fmt.Sprint(reply[3]), Key: fmt.Sprint(reply[4]), Owner: fmt.Sprint(reply[5])}
		if o.guarded() {
			if len(reply) >= 7 {
				o.member = fmt.Sprint(reply[6])
			}
			// The owner is store data; it is named, never offered as a command.
			return &Refusal{Code: reason, Location: o.location(), Guarded: true, Next: o.remedy(), cause: boundErr,
				Sentence: fmt.Sprintf("%s.%s.%s is bound to %s, owned by %q", boundErr.Table, boundErr.Row, boundErr.Col, boundErr.Key, boundErr.Owner)}
		}
		return fmt.Errorf("%s: %w", o.location(), boundErr)
	case typedrec.TableRefusalRevision:
		if len(reply) >= 4 {
			cause = fmt.Errorf("%w: expected %v, observed %v", ErrRevisionMismatch, reply[2], reply[3])
		} else {
			cause = fmt.Errorf("%w: the table revision %v is at its maximum and cannot advance", ErrCounterOverflow, reply[2])
		}
	case typedrec.TableRefusalMemberRevision:
		if len(reply) >= 5 {
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: expected %v, observed %v", ErrMemberRevision, reply[3], reply[4])
			remedy = memberReadCommand(o.table, o.member)
		} else {
			cause = fmt.Errorf("%w: %v", ErrMemberRevision, reply[2:])
		}
	case typedrec.TableRefusalFieldGuard:
		if len(reply) >= 3 {
			o.member = fmt.Sprint(reply[2])
		}
		if len(reply) >= 7 && fmt.Sprint(reply[4]) == "equals" {
			cause = fmt.Errorf("%w: member %q field %q: expected equals %q, observed %q", ErrFieldGuard, reply[2], reply[3], reply[5], reply[6])
		} else if len(reply) >= 6 && fmt.Sprint(reply[4]) == "absent" {
			cause = fmt.Errorf("%w: member %q field %q: expected absent, observed %q", ErrFieldGuard, reply[2], reply[3], reply[5])
		} else if len(reply) >= 7 && fmt.Sprint(reply[4]) == "one_of" {
			cause = fmt.Errorf("%w: member %q field %q: expected one_of %s, observed %q", ErrFieldGuard, reply[2], reply[3], reply[5], reply[6])
		} else {
			cause = fmt.Errorf("%w: %s", ErrFieldGuard, words(reply[2:]))
		}
		if o.member != "" {
			remedy = memberReadCommand(o.table, o.member)
		}
	case typedrec.TableRefusalPropGuard:
		// a table property's expectation (the manifest's prop_expect or
		// prop_absent) did not hold: L1 contract amendment, table properties
		if len(reply) >= 4 {
			cause = fmt.Errorf("%w: table %q property %q", ErrPropGuard, reply[2], reply[3])
		} else {
			cause = fmt.Errorf("%w: %s", ErrPropGuard, words(reply[2:]))
		}
	case typedrec.TableRefusalPlaceGuard:
		if len(reply) >= 5 {
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: member %q: expected place %v, observed %v", ErrPlaceGuard, reply[2], reply[3], reply[4])
			remedy = memberReadCommand(o.table, o.member)
		} else {
			cause = fmt.Errorf("%w: %s", ErrPlaceGuard, words(reply[2:]))
		}
	case typedrec.TableRefusalArgs:
		if o.guarded() {
			if len(reply) >= 4 {
				o.member = fmt.Sprint(reply[3])
			}
			cause = fmt.Errorf("invalid argument: %v", reply[2])
		} else {
			cause = fmt.Errorf("%s %v", reason, reply[2:])
		}
	case typedrec.TableRefusalOpConflict:
		cause = say(ErrOpConflict, "operation %q already holds a different request; use a new operation id for this request, or send the original manifest byte for byte to replay it", words(reply[2:]))
	case typedrec.TableRefusalLimit:
		if len(reply) >= 5 {
			limit := &LimitError{Name: fmt.Sprint(reply[2])}
			limit.Bound, _ = strconv.Atoi(fmt.Sprint(reply[3]))
			limit.Observed, _ = strconv.Atoi(fmt.Sprint(reply[4]))
			if len(reply) >= 6 && fmt.Sprint(reply[5]) != "" {
				limit.Member = fmt.Sprint(reply[5])
				o.member = limit.Member
			}
			limit.AtLeast = len(reply) >= 7 && fmt.Sprint(reply[6]) == "at least"
			cause = say(limit, "%s; %s", limit.Error(), limit.Advice())
		} else {
			cause = fmt.Errorf("%w: %s", ErrLimit, words(reply[2:]))
		}
	case typedrec.TableRefusalReservedField:
		if len(reply) >= 4 {
			o.member = fmt.Sprint(reply[2])
			cause = fmt.Errorf("%w: member %q field %q", ErrReservedField, reply[2], reply[3])
		} else {
			cause = fmt.Errorf("%w: %v", ErrReservedField, reply[2:])
		}
	case typedrec.TableRefusalTwice:
		if len(reply) >= 3 {
			o.member = fmt.Sprint(reply[2])
		}
		if o.guarded() {
			cause = say(ErrDuplicateMember, "duplicate manifest member: member %q appears more than once", words(reply[2:]))
		} else {
			cause = fmt.Errorf("%w (TWICE): member %s appears more than once", ErrDuplicateMember, words(reply[2:]))
		}
	case typedrec.TableRefusalScore:
		if len(reply) >= 3 {
			o.member = fmt.Sprint(reply[2])
		}
		if len(reply) >= 4 {
			cause = fmt.Errorf("%w: expected a finite JSON number, observed %v", ErrInvalidScore, reply[3])
		} else {
			cause = fmt.Errorf("%w: %s", ErrInvalidScore, words(reply[2:]))
		}
	case typedrec.TableRefusalOverflow:
		if len(reply) >= 3 {
			o.member = fmt.Sprint(reply[2])
		}
		cause = fmt.Errorf("%w: a revision is at its maximum, 18446744073709551615, and cannot advance", ErrCounterOverflow)
	case typedrec.TableRefusalMutation:
		if len(reply) >= 3 {
			o.member = fmt.Sprint(reply[2])
		}
		cause = fmt.Errorf("%w: %s", ErrMutation, words(reply[3:]))
	case typedrec.TableRefusalManifest:
		cause = fmt.Errorf("%w: %s", ErrMalformedManifest, words(reply[2:]))
	case typedrec.TableRefusalStreamFull:
		cause = fmt.Errorf("%w: stream %v is full", ErrCounterOverflow, reply[2:])
	case typedrec.TableRefusalSchema:
		cause = fmt.Errorf("unsupported schema %s, expected 1", words(reply[2:]))
	case typedrec.TableRefusalOperation:
		cause = fmt.Errorf("operation: %s", words(reply[2:]))
	case typedrec.TableRefusalMember:
		cause = fmt.Errorf("member: %s", words(reply[2:]))
	case typedrec.TableRefusalWrongType:
		if len(reply) >= 6 {
			o.member = fmt.Sprint(reply[5])
		}
		switch {
		case len(reply) >= 5 && o.guarded():
			cause = say(ErrWrongType, "wrong type: key %v is %v, expected %v", reply[2], reply[3], reply[4])
		case len(reply) >= 5:
			cause = fmt.Errorf("%w: key %v is %v, expected %v", ErrWrongType, reply[2], reply[3], reply[4])
		default:
			cause = fmt.Errorf("%w: %v", ErrWrongType, reply[2:])
		}
	case typedrec.TableRefusalStreamType:
		cause = fmt.Errorf("%w: stream %v is not a stream", ErrWrongType, reply[2:])
	default:
		cause = fmt.Errorf("%s %v", reason, reply[2:])
	}
	// The server can identify a different column (e.g. the move destination).
	if (refusal == typedrec.TableRefusalNoCol || refusal == typedrec.TableRefusalText) && len(reply) >= 4 {
		o.row = fmt.Sprint(reply[2])
		o.col = fmt.Sprint(reply[3])
	}
	// A batch entry's refusal about its destination carries the entry's id last.
	if o.guarded() {
		switch {
		case refusal == typedrec.TableRefusalNoRow && len(reply) >= 4:
			o.member = fmt.Sprint(reply[3])
		case (refusal == typedrec.TableRefusalNoCol || refusal == typedrec.TableRefusalText) && len(reply) >= 5:
			o.member = fmt.Sprint(reply[4])
		}
	}
	if refusal == typedrec.TableRefusalNoRow && len(reply) >= 3 {
		o.row = fmt.Sprint(reply[2])
		if !o.guarded() {
			// a batch or a read set never prepares a write: it shows the table, whose rows it lists
			remedy = "nova-table row add " + shellWord(o.table) + " " + shellWord(o.row)
		}
	}
	if o.readSet && refusal == typedrec.TableRefusalNoRow {
		remedy = o.remedy() // a read prepares nothing to write; show the table
	}
	return &Refusal{Code: reason, Location: o.location(), Sentence: cause.Error(), Next: remedy, Guarded: o.guarded(), cause: cause}
}

// CheckedBeforeSending is what a refusal made before anything is sent says of
// itself; the library and the command both say it. The store looks an operation up before it judges the request, so a
// manifest that the current rules refuse can still have been applied earlier, under
// looser rules: this refusal is about this call only.
const CheckedBeforeSending = "checked before sending, so this call changed nothing; it says nothing about an earlier call with the same operation id"

// beforeSending turns what the validator found into the refusal the store would
// have made, or the manifest error a reader is told.
func (o operation) beforeSending(err error) error {
	var (
		me *ManifestError
		re *RuleError
		le *LimitError
	)
	switch {
	case errors.As(err, &re):
		o.member = re.Member
		return &Refusal{Code: re.Code, Location: o.location(), Sentence: re.Detail + "; " + CheckedBeforeSending, Next: o.remedy(), Guarded: true, cause: re}
	case errors.As(err, &le):
		o.member = le.Member
		return &Refusal{Code: "LIMIT", Location: o.location(), Sentence: le.Error() + "; " + le.Advice() + "; " + CheckedBeforeSending, Next: o.remedy(), Guarded: true, cause: le}
	case errors.As(err, &me):
		return fmt.Errorf("%s: invalid batch manifest: %w; %s; changed=no; run: %s", o.location(), me, CheckedBeforeSending, o.remedy())
	}
	return err
}
func (o operation) call(ctx context.Context, c redis.Cmdable, fn string, ro bool, args ...any) ([]any, error) {
	all := append([]any{o.table}, args...)
	key := DefKey(o.table)
	if o.view {
		key = "view:" + o.table
	}
	if fn == "ns_view_list" {
		key = "views"
		all = nil
	}
	var cmd *redis.Cmd
	if ro {
		cmd = c.FCallRO(ctx, fn, []string{key}, all...)
	} else {
		cmd = c.FCall(ctx, fn, []string{key}, all...)
	}
	reply, err := cmd.Slice()
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w%s", o.location(), fn, err, runUnlessNamed(err, o.remedy()))
	}
	if err := o.refused(reply); err != nil {
		return nil, err
	}
	return reply, nil
}
func (o operation) write(ctx context.Context, c redis.Cmdable, fn string, options []WriteOptions, args ...any) ([]any, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("%s: one write-options value is allowed", o.location())
	}
	var opts WriteOptions
	if len(options) == 1 {
		opts = options[0]
	}
	body, err := payload(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{strconv.FormatUint(opts.Epoch, 10), opts.Actor, opts.Fence, opts.Idem})
	if err != nil {
		return nil, err
	}
	reply, err := o.call(ctx, c, fn, false, append(args, body)...)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("%s: missing committed receipt", o.location())
	}
	wire, ok := reply[len(reply)-1].([]any)
	if !ok || (len(wire) != 6 && len(wire) != 7) || fmt.Sprint(wire[0]) != "RECEIPT" {
		return nil, fmt.Errorf("%s: malformed committed receipt", o.location())
	}
	var r Receipt
	r.ID, r.Outcome = fmt.Sprint(wire[1]), fmt.Sprint(wire[5])
	for i, target := range []*uint64{&r.Epoch, &r.Before, &r.After} {
		*target, err = strconv.ParseUint(fmt.Sprint(wire[i+2]), 10, 64)
		if err != nil {
			return nil, err
		}
	}
	if len(wire) >= 7 {
		var delta BatchDelta
		if err := json.Unmarshal([]byte(fmt.Sprint(wire[6])), &delta); err == nil {
			r.BatchDelta = &delta
		}
	}
	if opts.Receipt != nil {
		*opts.Receipt = r
	}
	return reply[:len(reply)-1], nil
}

func replyCount(reply []any) (int64, error) {
	if len(reply) < 2 {
		return 0, fmt.Errorf("table function returned no count: %v", reply)
	}
	return strconv.ParseInt(fmt.Sprint(reply[1]), 10, 64)
}
func definitionPayload(t Table, now time.Time) (map[string]string, error) {
	if !ValidName(t.Name) {
		return nil, fmt.Errorf("table %q wants letters, digits, _ . and -; run: nova-table help", t.Name)
	}
	if err := ValidateColumns(t.Columns); err != nil {
		return nil, fmt.Errorf("table %q: %w; run: nova-table help", t.Name, err)
	}
	fields := definitionFields(t)
	fields["created_at"] = now.UTC().Format(time.RFC3339)
	return fields, nil
}
func payload(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }

// Create checks the definition and writes it atomically in one round trip.
func Create(ctx context.Context, c redis.Cmdable, t Table, now time.Time, opts ...WriteOptions) error {
	fields, err := definitionPayload(t, now)
	if err != nil {
		return err
	}
	body, err := payload(fields)
	if err != nil {
		return err
	}
	_, err = (operation{table: t.Name}).write(ctx, c, FnCreate, opts, body)
	return err
}

// SetOpts changes the active definition while retaining rows and members.
// A removed or hidden occupied owned cell is refused. Rename moves the table
// identity, all retained epochs and its change stream to the new name.
type SetOpts struct {
	Footer     *string
	Rename     string
	Columns    []Column
	Hidden     *[]string
	Hide, Show []string // atomic deltas to the hidden column list
	Visible    *bool
	// One column added (at the end, or where At says), removed (refused
	// while it holds members or text) or moved; the other columns stand.
	ColAdd  *Column
	ColAt   *Place // where ColAdd enters; nil is last
	ColDel  string
	ColMove *Reorder
	// The rows' order: all sorted, then the named rows first, then one row
	// moved, in that order when several are given. A standing sort refuses
	// RowOrder and RowMove unless RowSort clears it in the same call.
	RowSort  *Sort
	RowOrder []string
	RowMove  *Reorder
}

// Place is a position in an order: first, last, or before or after Ref.
type Place struct {
	Where string // first, last, before, after
	Ref   string // the neighbour, for before and after
}

// Reorder puts one column or one row at a place; nothing else moves.
type Reorder struct {
	Item string
	Place
}

// Sort orders the rows once by name, label, or a column's value (a count,
// or a text value). Keep makes it standing (name and label only): every
// row added or rebound later takes its place. Manual ends a standing sort and leaves
// the rows where they are.
type Sort struct {
	By     string
	Desc   bool
	Keep   bool
	Manual bool
}

func (p Place) valid() error {
	switch p.Where {
	case "first", "last":
		if p.Ref != "" {
			return fmt.Errorf("--%s takes no neighbour", p.Where)
		}
		return nil
	case "before", "after":
		if p.Ref == "" {
			return fmt.Errorf("--%s wants a neighbour", p.Where)
		}
		return nil
	}
	return fmt.Errorf("a place is --first, --last, --before <x> or --after <x>")
}

func (p Place) wire(m map[string]any) {
	m["where"] = p.Where
	if p.Ref != "" {
		m["ref"] = p.Ref
	}
}

// Set validates and commits a complete definition edit in one call. The
// return value counts physical keys moved when renaming, otherwise zero.
func Set(ctx context.Context, c redis.Cmdable, name string, change SetOpts, opts ...WriteOptions) (int, error) {
	if err := validateRowKeys(change.RowOrder); err != nil {
		return 0, err
	}
	if m := change.RowMove; m != nil {
		keys := []string{m.Item}
		if m.Ref != "" {
			keys = append(keys, m.Ref)
		}
		if err := validateRowKeys(keys); err != nil {
			return 0, err
		}
	}
	spec := map[string]any{}
	if change.Footer != nil {
		spec["footer"] = *change.Footer
	}
	if change.Rename != "" {
		if !ValidName(change.Rename) {
			return 0, fmt.Errorf("table %q: the new name wants letters, digits, _ . and -", change.Rename)
		}
		spec["rename"] = change.Rename
	}
	if len(change.Columns) > 0 {
		if err := ValidateColumns(change.Columns); err != nil {
			return 0, err
		}
		fields := definitionFields(Table{Name: name, Columns: change.Columns})
		for key := range fields {
			if key != "order" && !strings.HasPrefix(key, "col:") {
				delete(fields, key)
			}
		}
		spec["columns"] = fields
	}
	if change.Hidden != nil {
		spec["hidden"] = strings.Join(*change.Hidden, ",")
	}
	if len(change.Hide) > 0 {
		spec["hide"] = change.Hide
	}
	if len(change.Show) > 0 {
		spec["show"] = change.Show
	}
	if change.Visible != nil {
		spec["visible"] = *change.Visible
	}
	if change.ColAdd != nil {
		if err := validateColumn(*change.ColAdd, nil); err != nil {
			return 0, err
		}
		add := map[string]any{"name": change.ColAdd.Name, "def": encodeColumn(*change.ColAdd)}
		if change.ColAt != nil {
			if err := change.ColAt.valid(); err != nil {
				return 0, err
			}
			change.ColAt.wire(add)
		}
		spec["col_add"] = add
	}
	if change.ColDel != "" {
		spec["col_del"] = change.ColDel
	}
	for key, m := range map[string]*Reorder{"col_move": change.ColMove, "row_move": change.RowMove} {
		if m == nil {
			continue
		}
		if err := m.valid(); err != nil {
			return 0, err
		}
		item := map[string]any{}
		if key == "col_move" {
			item["col"] = m.Item
		} else {
			item["row"] = m.Item
		}
		m.wire(item)
		spec[key] = item
	}
	if len(change.RowOrder) > 0 {
		spec["row_order"] = change.RowOrder
	}
	if change.RowSort != nil {
		spec["row_sort"] = map[string]any{"by": change.RowSort.By, "desc": change.RowSort.Desc, "keep": change.RowSort.Keep, "manual": change.RowSort.Manual}
	}
	if len(spec) == 0 {
		return 0, fmt.Errorf("table %q: set wants a change: --footer <label>, --rename <name>, --columns <spec>, --hide, --show, --hidden or --visible", name)
	}
	body, err := payload(spec)
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name}).write(ctx, c, FnSet, opts, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowSet writes text values as one validated mutation, with a single receipt.
func RowSet(ctx context.Context, c redis.Cmdable, name, key string, texts map[string]string, opts ...WriteOptions) (int, error) {
	if len(texts) == 0 {
		return 0, fmt.Errorf("table %q: row set wants at least one text value", name)
	}
	body, err := payload(texts)
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name, row: key}).write(ctx, c, FnRowSet, opts, key, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowSetMany sets the text cells of several rows of one table in one round
// trip: one ns_table_row_set per row, all in one pipeline, each its own write
// (its own revision and change event), as RowSet writes it. An error names
// the first row the store refused, or the exchange that failed.
func RowSetMany(ctx context.Context, c redis.Cmdable, name string, rows map[string]map[string]string, opts ...WriteOptions) error {
	if len(rows) == 0 {
		return nil
	}
	if len(opts) > 1 {
		return fmt.Errorf("table %q: one write-options value is allowed", name)
	}
	var wo WriteOptions
	if len(opts) == 1 {
		wo = opts[0]
	}
	options, err := payload(struct {
		Epoch string `json:"epoch"`
		Actor string `json:"actor"`
		Fence string `json:"fence"`
		Idem  string `json:"idem"`
	}{strconv.FormatUint(wo.Epoch, 10), wo.Actor, wo.Fence, wo.Idem})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pipe := c.Pipeline()
	cmds := make([]*redis.Cmd, len(keys))
	for i, k := range keys {
		if len(rows[k]) == 0 {
			return fmt.Errorf("table %q: row set wants at least one text value", name)
		}
		body, err := payload(rows[k])
		if err != nil {
			return err
		}
		cmds[i] = pipe.FCall(ctx, FnRowSet, []string{DefKey(name)}, name, k, body, options)
	}
	if _, err := pipe.Exec(ctx); err != nil && !isRedisReply(err) {
		return fmt.Errorf("table %q: row set of %d rows: %w", name, len(keys), err)
	}
	for i, cmd := range cmds {
		o := operation{table: name, row: keys[i]}
		reply, err := cmd.Slice()
		if err != nil {
			return fmt.Errorf("%s: %s: %w%s", o.location(), FnRowSet, err, runUnlessNamed(err, o.remedy()))
		}
		if err := o.refused(reply); err != nil {
			return err
		}
		if len(reply) < 2 {
			return fmt.Errorf("%s: missing committed receipt", o.location())
		}
	}
	return nil
}

// isRedisReply says a pipeline's error is one command's reply, read with
// that command, and not the exchange's.
func isRedisReply(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// Drop removes the active epoch and its owned cells. The template and older
// epochs survive; a later epoch starts with the same definition and no rows.
func Drop(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnDrop, opts)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// DropDefinition also removes the stable template. Materialised epoch
// snapshots and immutable member identities remain available for inspection.
func DropDefinition(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnDropDefinition, opts)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// MemberCreate allocates an unplaced identity in the table's record namespace.
// Existing IDs, including removed members and older epochs, are refused.
func MemberCreate(ctx context.Context, c redis.Cmdable, name, id string, opts ...WriteOptions) error {
	_, err := (operation{table: name, member: id}).write(ctx, c, FnMemberCreate, opts, id)
	return err
}

type RowSpec struct {
	Label   string            `json:"label"`
	Exclude string            `json:"exclude"`
	Owner   string            `json:"owner"`
	Binds   map[string]string `json:"binds,omitempty"`
}

func RowAdd(ctx context.Context, c redis.Cmdable, name, key string, spec RowSpec, opts ...WriteOptions) (Row, error) {
	o := operation{table: name, row: key}
	if !ValidRowKey(key) {
		return Row{}, fmt.Errorf("%s: row wants a non-empty UTF-8 key with no ASCII control characters; run: nova-table row help", o.location())
	}
	body, err := payload(spec)
	if err != nil {
		return Row{}, err
	}
	reply, err := o.write(ctx, c, FnRowAdd, opts, key, body)
	if err != nil {
		return Row{}, err
	}
	if len(reply) != 3 {
		return Row{}, fmt.Errorf("%s: malformed row reply", o.location())
	}
	h, err := flatHash(reply[1])
	if err != nil {
		return Row{}, err
	}
	t, _, err := decodeDefinition(name, h)
	if err != nil {
		return Row{}, err
	}
	h, err = flatHash(reply[2])
	if err != nil {
		return Row{}, err
	}
	return decodeRow(t, key, h), nil
}

// Validate identity before encoding/json can replace invalid bytes with U+FFFD.
func validateRowKeys(keys []string) error {
	for _, key := range keys {
		if !ValidRowKey(key) {
			return fmt.Errorf("row %q: wants a non-empty UTF-8 key with no ASCII control characters; run: nova-table row help", key)
		}
	}
	return nil
}

// RowsAdd adds a list of rows using the same staged writer as RowAdd.
func RowsAdd(ctx context.Context, c redis.Cmdable, name string, keys []string, opts ...WriteOptions) (int, error) {
	return RowsAddWithSpec(ctx, c, name, keys, RowSpec{}, opts...)
}

// RowsAddWithSpec applies one metadata specification to every named row.
func RowsAddWithSpec(ctx context.Context, c redis.Cmdable, name string, keys []string, spec RowSpec, opts ...WriteOptions) (int, error) {
	if err := validateRowKeys(keys); err != nil {
		return 0, err
	}
	body, err := payload(struct {
		Rows []string `json:"rows"`
		Spec RowSpec  `json:"spec"`
	}{keys, spec})
	if err != nil {
		return 0, err
	}
	reply, err := (operation{table: name}).write(ctx, c, FnRowsAdd, opts, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

// RowsHide hides rows from the render while retaining their contribution to folds.
func RowsHide(ctx context.Context, c redis.Cmdable, name string, hide bool, keys []string, opts ...WriteOptions) (int, error) {
	if err := validateRowKeys(keys); err != nil {
		return 0, err
	}
	body, err := payload(keys)
	if err != nil {
		return 0, err
	}
	flag := "0"
	if hide {
		flag = "1"
	}
	reply, err := (operation{table: name}).write(ctx, c, FnRowsHide, opts, flag, body)
	if err != nil {
		return 0, err
	}
	n, err := replyCount(reply)
	return int(n), err
}

func RowDel(ctx context.Context, c redis.Cmdable, name, key string, opts ...WriteOptions) (bool, error) {
	reply, err := (operation{table: name, row: key}).write(ctx, c, FnRowDel, opts, key)
	if err != nil {
		return false, err
	}
	n, err := replyCount(reply)
	return n != 0, err
}

// Bind replaces the caller-owned table shape in one atomic call. Existing
// owned cells of retained rows survive; removed rows leave bound sets alone.
// Omitting a row holding owned members or nonempty text is refused until the
// caller explicitly clears that content (or deletes the row).
func Bind(ctx context.Context, c redis.Cmdable, t Table, now time.Time, opts ...WriteOptions) error {
	fields, err := definitionPayload(t, now)
	if err != nil {
		return err
	}
	// a bind leaves the table with exactly these rows, so the bound is on their number
	if err := over(limitNameRows, LimitRows, len(t.Rows), ""); err != nil {
		return fmt.Errorf("table %q: %w; %s; changed=no; run: %s", t.Name, err, err.(*LimitError).Advice(), (operation{table: t.Name}).remedy())
	}
	type boundRow struct {
		Key string `json:"key"`
		RowSpec
	}
	rows := make([]boundRow, 0, len(t.Rows))
	seen := map[string]bool{}
	for _, r := range t.Rows {
		if !ValidRowKey(r.Key) || seen[r.Key] {
			return fmt.Errorf("table %q row %q: invalid or repeated row key; run: nova-table show %s", t.Name, r.Key, shellWord(t.Name))
		}
		seen[r.Key] = true
		if len(r.Cells) != len(t.Columns) {
			return fmt.Errorf("table %q row %q: %d cells for %d columns", t.Name, r.Key, len(r.Cells), len(t.Columns))
		}
		spec := RowSpec{Label: r.Label, Exclude: r.Exclude, Owner: r.Owner, Binds: map[string]string{}}
		for i, cell := range r.Cells {
			if cell.Bound {
				spec.Binds[t.Columns[i].Name] = cell.Key
			}
		}
		rows = append(rows, boundRow{r.Key, spec})
	}
	body, err := payload(struct {
		Fields map[string]string `json:"fields"`
		Rows   []boundRow        `json:"rows"`
	}{fields, rows})
	if err != nil {
		return err
	}
	_, err = (operation{table: t.Name}).write(ctx, c, FnBind, opts, body)
	return err
}
func Clear(ctx context.Context, c redis.Cmdable, name string, opts ...WriteOptions) (int64, error) {
	reply, err := (operation{table: name}).write(ctx, c, FnClear, opts)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// Single-member helpers use the same list operation and wire protocol.
func CellAdd(ctx context.Context, c redis.Cmdable, name, row, col, member string, score float64, opts ...WriteOptions) (int64, error) {
	return CellsAdd(ctx, c, name, row, col, score, []string{member}, opts...)
}
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string, opts ...WriteOptions) (int64, error) {
	return CellsRemove(ctx, c, name, row, col, []string{member}, opts...)
}
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string, opts ...WriteOptions) (int64, error) {
	return CellsMove(ctx, c, name, row, from, to, []string{member}, opts...)
}

// CellsAdd atomically adds every member at one score and returns the cell count.
func CellsAdd(ctx context.Context, c redis.Cmdable, name, row, col string, score float64, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: col}, FnCellAdd, opts, []any{row, col, strconv.FormatFloat(score, 'g', -1, 64)}, members)
}

// CellsRemove removes a list; absent members are accepted no-ops.
func CellsRemove(ctx context.Context, c redis.Cmdable, name, row, col string, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: col}, FnCellRemove, opts, []any{row, col}, members)
}

// CellsMove keeps scores and refuses the complete list if any member is absent or inconsistent.
func CellsMove(ctx context.Context, c redis.Cmdable, name, row, from, to string, members []string, opts ...WriteOptions) (int64, error) {
	return writeMembers(ctx, c, operation{table: name, row: row, col: from}, FnCellMove, opts, []any{row, from, to}, members)
}
func writeMembers(ctx context.Context, c redis.Cmdable, o operation, fn string, opts []WriteOptions, args []any, members []string) (int64, error) {
	if len(members) == 0 {
		return 0, fmt.Errorf("%s: wants at least one member", o.location())
	}
	o.member = members[0]
	for _, m := range members {
		args = append(args, m)
	}
	reply, err := o.write(ctx, c, fn, opts, args...)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

func CellMembers(ctx context.Context, c redis.Cmdable, name, row, col string) ([]Member, error) {
	reply, err := (operation{table: name, row: row, col: col}).call(ctx, c, FnMembers, true, row, col)
	if err != nil {
		return nil, err
	}
	if len(reply) != 2 {
		return nil, fmt.Errorf("table %q row %q column %q: malformed members reply", name, row, col)
	}
	return flatMembers(reply[1])
}

// A view: a named list of tables with a title, read by watch every frame.
type View struct {
	Name    string
	Tables  []string
	Title   string
	Summary string // the count column of the first table the summary line counts as done ("" for no line)
	// State, when set, is the summary line, alone, in place of the counts:
	// the state of whatever fills the view ("STOPPED"). ViewState writes it;
	// ViewSet leaves it as it is.
	State string
}

// MaxViewState bounds a view's state text, in bytes.
const MaxViewState = 64

// ValidViewState says a state text is one a view takes: empty (none), or one
// line of at most MaxViewState bytes with no control characters.
func ValidViewState(text string) bool {
	if len(text) > MaxViewState {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ErrNoView is a view that is not there.
var ErrNoView = errors.New("no such view")

// ViewState sets a view's state text, the summary line shown alone in place
// of the counts while it is set; "" clears it and the counts show again. The
// view must exist.
func ViewState(ctx context.Context, c redis.Cmdable, name, text string) error {
	if !ValidViewState(text) {
		return fmt.Errorf("view %q: a state is one line of at most %d bytes", name, MaxViewState)
	}
	_, err := (operation{table: name, view: true}).call(ctx, c, fnViewState, false, text)
	return err
}

const fnViewState = "ns_view_state"

// QueueViewState queues ViewState on a pipeline or a transaction, so a caller
// writes a view's state in the same MULTI/EXEC as a record of its own (the
// state it shows); ViewStateResult reads the queued call's answer after Exec.
func QueueViewState(ctx context.Context, p redis.Pipeliner, name, text string) *redis.Cmd {
	return p.FCall(ctx, fnViewState, []string{"view:" + name}, name, text)
}

// ViewStateResult is the answer of a call QueueViewState queued, after Exec:
// nil, a refusal (errors.Is ErrNoView when the view is not there), or the
// store's error.
func ViewStateResult(name string, cmd *redis.Cmd) error {
	o := operation{table: name, view: true}
	reply, err := cmd.Slice()
	if err != nil {
		return fmt.Errorf("%s: %s: %w%s", o.location(), fnViewState, err, runUnlessNamed(err, o.remedy()))
	}
	return o.refused(reply)
}

// ViewSet writes a view; every table must exist.
func ViewSet(ctx context.Context, c redis.Cmdable, v View) error {
	_, err := (operation{table: v.Name, view: true}).call(ctx, c, "ns_view_set", false, strings.Join(v.Tables, ","), v.Title, v.Summary)
	return err
}

// ViewGet reads a view.
func ViewGet(ctx context.Context, c redis.Cmdable, name string) (View, error) {
	reply, err := (operation{table: name, view: true}).call(ctx, c, "ns_view_get", true)
	if err != nil {
		return View{}, err
	}
	if len(reply) < 2 {
		return View{}, fmt.Errorf("view %q: malformed reply", name)
	}
	h, err := flatHash(reply[1])
	if err != nil {
		return View{}, err
	}
	v := View{Name: name, Title: h["title"], Summary: h["summary"], State: h["state"]}
	if t := strings.TrimSpace(h["tables"]); t != "" {
		v.Tables = strings.Split(t, ",")
	}
	return v, nil
}

// ViewList returns stored view names in lexical order, in one exchange.
func ViewList(ctx context.Context, c redis.Cmdable) ([]string, error) {
	reply, err := (operation{view: true}).call(ctx, c, "ns_view_list", true)
	if err != nil {
		return nil, err
	}
	if len(reply) != 2 {
		return nil, fmt.Errorf("views: malformed list reply")
	}
	raw, ok := reply[1].([]any)
	if !ok {
		return nil, fmt.Errorf("views: malformed names")
	}
	names := make([]string, len(raw))
	for i, v := range raw {
		name, ok := v.(string)
		if !ok || !ValidName(name) {
			return nil, fmt.Errorf("views: malformed name")
		}
		names[i] = name
	}
	return names, nil
}

// ViewDelete removes only presentation configuration; tables are untouched.
func ViewDelete(ctx context.Context, c redis.Cmdable, name string) (int64, error) {
	reply, err := (operation{table: name, view: true}).call(ctx, c, "ns_view_del", false)
	if err != nil {
		return 0, err
	}
	return replyCount(reply)
}

// BatchManifest specifies an atomic set of preconditions and mutations across
// members in a table.
type BatchManifest struct {
	Schema                int                `json:"schema"`
	Table                 string             `json:"table"`
	Epoch                 string             `json:"epoch"`
	ExpectedTableRevision string             `json:"expected_table_revision"`
	OperationID           string             `json:"operation_id"`
	Actor                 string             `json:"actor,omitempty"`
	Members               []BatchMemberEntry `json:"members"`
	// Props are the table's properties the batch sets, PropExpect the ones it
	// expects present with a value and PropAbsent the ones it expects absent,
	// checked before any write and applied in the same atomic call as the
	// members (L1 contract amendment, table properties, section 4).
	Props      map[string]string `json:"props,omitempty"`
	PropExpect map[string]string `json:"prop_expect,omitempty"`
	PropAbsent []string          `json:"prop_absent,omitempty"`
}

// BatchMemberEntry defines expectations and mutations for one member.
type BatchMemberEntry struct {
	ID     string            `json:"id"`
	Expect *MemberExpect     `json:"expect,omitempty"`
	Create *MemberCreateOp   `json:"create,omitempty"`
	Move   *MemberMoveOp     `json:"move,omitempty"`
	Remove bool              `json:"remove,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
	Unset  []string          `json:"unset,omitempty"`
}

// MemberExpect guards an existing or absent member record before mutation.
type MemberExpect struct {
	Absent   bool                  `json:"absent,omitempty"`
	Revision string                `json:"revision,omitempty"`
	Place    *PlaceExpect          `json:"place,omitempty"`
	Fields   map[string]FieldGuard `json:"fields,omitempty"`
}

// PlaceExpect checks the expected row and column of a placed member.
type PlaceExpect struct {
	Row string `json:"row"`
	Col string `json:"col"`
}

// FieldGuard checks a member application field's exact value, absence, or inclusion.
type FieldGuard struct {
	Equals *string  `json:"equals,omitempty"`
	Absent *bool    `json:"absent,omitempty"`
	OneOf  []string `json:"one_of,omitempty"`
}

// MemberCreateOp places a new member at row, column, and score.
type MemberCreateOp struct {
	Row   string  `json:"row"`
	Col   string  `json:"col"`
	Score float64 `json:"score"`
}

// MemberMoveOp moves an existing member to row, column, with optional new score.
type MemberMoveOp struct {
	Row   string   `json:"row"`
	Col   string   `json:"col"`
	Score *float64 `json:"score,omitempty"`
}

// BatchDelta records the applied batch outcome for change stream and receipts.
type BatchDelta struct {
	OperationID   string             `json:"operation_id"`
	Digest        string             `json:"digest"`
	Actor         string             `json:"actor"`
	SelectedCount int                `json:"selected_count"`
	GuardCount    int                `json:"guard_count"`
	ChangedCount  int                `json:"changed_count"`
	Members       []BatchMemberDelta `json:"members"`
	// Props are the table's properties the batch changed, name -> new value
	// (L1 contract amendment, table properties).
	Props map[string]string `json:"props,omitempty"`
}

func (b *BatchDelta) UnmarshalJSON(data []byte) error {
	type rawBatchDelta struct {
		OperationID   string            `json:"operation_id"`
		Digest        string            `json:"digest"`
		Actor         string            `json:"actor"`
		SelectedCount int               `json:"selected_count"`
		GuardCount    int               `json:"guard_count"`
		ChangedCount  int               `json:"changed_count"`
		Members       json.RawMessage   `json:"members"`
		Props         map[string]string `json:"props"`
	}
	var raw rawBatchDelta
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	b.OperationID = raw.OperationID
	b.Digest = raw.Digest
	b.Actor = raw.Actor
	b.SelectedCount = raw.SelectedCount
	b.GuardCount = raw.GuardCount
	b.ChangedCount = raw.ChangedCount
	b.Props = raw.Props
	if len(raw.Members) > 0 && string(raw.Members) != "{}" && string(raw.Members) != "null" {
		var m []BatchMemberDelta
		if err := json.Unmarshal(raw.Members, &m); err != nil {
			return err
		}
		b.Members = m
	} else {
		b.Members = []BatchMemberDelta{}
	}
	return nil
}

func parseScoreText(t *string) (*float64, error) {
	if t == nil {
		return nil, nil
	}
	v, err := strconv.ParseFloat(*t, 64)
	if err != nil {
		return nil, fmt.Errorf("score %q is not a number: %w", *t, err)
	}
	return &v, nil
}

// FieldChange records before and after values for an application field.
// Absence is represented by nil, distinguished from a present empty string.
//
// A value longer than ReceiptValueBytes is not recorded: its side is nil and
// carries the value's length and SHA-1 (BeforeBytes, BeforeSHA1), so a nil side
// with no length is an absent field and a nil side with a length is a long one.
type FieldChange struct {
	Before      *string `json:"before"`
	After       *string `json:"after"`
	BeforeBytes int     `json:"before_bytes,omitempty"`
	BeforeSHA1  string  `json:"before_sha1,omitempty"`
	AfterBytes  int     `json:"after_bytes,omitempty"`
	AfterSHA1   string  `json:"after_sha1,omitempty"`
}

// BatchMemberDelta records before and after state for a member affected by a batch.
type BatchMemberDelta struct {
	ID          string `json:"id"`
	BeforePlace string `json:"before_place"`
	AfterPlace  string `json:"after_place"`
	// BeforeScore and AfterScore are the scores parsed; BeforeScoreText and
	// AfterScoreText are the exact decimal strings the store holds, which two
	// different scores never share. Nil is no score (unplaced).
	BeforeScore     *float64               `json:"-"`
	AfterScore      *float64               `json:"-"`
	BeforeScoreText *string                `json:"before_score"`
	AfterScoreText  *string                `json:"after_score"`
	BeforeRev       string                 `json:"before_rev"`
	AfterRev        string                 `json:"after_rev"`
	FieldsSet       map[string]string      `json:"fields_set"`
	FieldsUnset     []string               `json:"fields_unset"`
	Fields          map[string]FieldChange `json:"fields"`
}

func (b *BatchMemberDelta) UnmarshalJSON(data []byte) error {
	type rawMemberDelta struct {
		ID          string          `json:"id"`
		BeforePlace string          `json:"before_place"`
		AfterPlace  string          `json:"after_place"`
		BeforeScore *string         `json:"before_score"`
		AfterScore  *string         `json:"after_score"`
		BeforeRev   string          `json:"before_rev"`
		AfterRev    string          `json:"after_rev"`
		FieldsSet   json.RawMessage `json:"fields_set"`
		FieldsUnset json.RawMessage `json:"fields_unset"`
		Fields      json.RawMessage `json:"fields"`
	}
	var raw rawMemberDelta
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	b.ID = raw.ID
	b.BeforePlace = raw.BeforePlace
	b.AfterPlace = raw.AfterPlace
	b.BeforeScoreText, b.AfterScoreText = raw.BeforeScore, raw.AfterScore
	var err error
	if b.BeforeScore, err = parseScoreText(raw.BeforeScore); err != nil {
		return err
	}
	if b.AfterScore, err = parseScoreText(raw.AfterScore); err != nil {
		return err
	}
	b.BeforeRev = raw.BeforeRev
	b.AfterRev = raw.AfterRev

	if len(raw.FieldsSet) > 0 && string(raw.FieldsSet) != "[]" && string(raw.FieldsSet) != "null" {
		var fs map[string]string
		if err := json.Unmarshal(raw.FieldsSet, &fs); err != nil {
			return err
		}
		b.FieldsSet = fs
	} else {
		b.FieldsSet = map[string]string{}
	}

	if len(raw.FieldsUnset) > 0 && string(raw.FieldsUnset) != "{}" && string(raw.FieldsUnset) != "null" {
		var fu []string
		if err := json.Unmarshal(raw.FieldsUnset, &fu); err != nil {
			return err
		}
		b.FieldsUnset = fu
	} else {
		b.FieldsUnset = []string{}
	}

	if len(raw.Fields) > 0 && string(raw.Fields) != "[]" && string(raw.Fields) != "null" {
		var flds map[string]FieldChange
		if err := json.Unmarshal(raw.Fields, &flds); err != nil {
			return err
		}
		b.Fields = flds
	} else {
		b.Fields = map[string]FieldChange{}
	}
	return nil
}

// ApplyBatch commits an atomic batch of member mutations and preconditions
// against one table in one round trip (ns_table_apply): every guard is read
// against one pre-state, and either every change is written with one receipt or
// nothing is.
//
// It runs the manifest through the Go validator (ValidateBatchManifestRaw)
// before it sends anything, so a manifest that a rule refuses never reaches the
// store. Its errors are of three kinds, told apart with errors.Is and errors.As:
//
//   - a refusal (IsRefusal): the store or a rule said no, nothing changed, and
//     the error says the code, the operation, the member, what was expected
//     against what was found, changed=no and a next command. It wraps the
//     sentinel of its code (ErrNotMember, ErrMemberRevision, ErrLimit, ErrStale,
//     ErrEpochAhead, ErrOpConflict, ...).
//   - a manifest that cannot be read as one (ErrMalformedManifest, a
//     *ManifestError): not JSON, an unknown key, a value of the wrong type. It
//     names the place in the manifest.
//   - a transport failure (ErrUnknownOutcome): the store did not answer, so the
//     batch may or may not have been applied. Send the same manifest again with
//     the same operation id: it returns the original receipt if the batch was
//     applied and applies it if it was not.
//
// Operation identity is the table, the epoch and the operation id. Sending
// the same request again returns the receipt recorded for it (Receipt.Replay is
// true) and writes nothing, even after the table has moved on; a different
// request under the same id is refused (ErrOpConflict). manifest.Epoch is the
// epoch the caller observed: an epoch behind the active one is ErrStale, one
// ahead of it ErrEpochAhead. Operation records do not expire, but a drop of the
// table ends them.
func ApplyBatch(ctx context.Context, c redis.Cmdable, manifest BatchManifest) (Receipt, error) {
	body, err := batchBody(&manifest)
	if err != nil {
		return Receipt{}, err
	}
	return batchReceipt(manifest, c.FCall(ctx, FnApply, []string{DefKey(manifest.Table)}, manifest.Table, body))
}

// ApplyBatches applies manifests of one table in one round trip, in one
// MULTI/EXEC: each is its own batch, applied or refused as ApplyBatch
// applies it, and none runs between them, so a refused one leaves every one
// after it refused on the table revision it expected. The receipts and errors
// are each manifest's; a manifest refused before sending (a rule or a bound
// this process checks) is its error, and none after it is sent.
func ApplyBatches(ctx context.Context, c redis.Cmdable, manifests []BatchManifest) ([]Receipt, []error) {
	rcs, errs := make([]Receipt, len(manifests)), make([]error, len(manifests))
	bodies := make([]string, 0, len(manifests))
	for i := range manifests {
		body, err := batchBody(&manifests[i])
		if err != nil {
			errs[i] = err
			for k := i + 1; k < len(manifests); k++ {
				errs[k] = fmt.Errorf("table %q batch %q: not sent: an earlier batch was refused before sending", manifests[k].Table, manifests[k].OperationID)
			}
			break
		}
		bodies = append(bodies, body)
	}
	if len(bodies) == 0 {
		return rcs, errs
	}
	tx := c.TxPipeline()
	cmds := make([]*redis.Cmd, len(bodies))
	for i, body := range bodies {
		cmds[i] = tx.FCall(ctx, FnApply, []string{DefKey(manifests[i].Table)}, manifests[i].Table, body)
	}
	_, _ = tx.Exec(ctx) // each command's reply, or the exchange's error, is read with it
	for i, cmd := range cmds {
		rcs[i], errs[i] = batchReceipt(manifests[i], cmd)
	}
	return rcs, errs
}

// batchBody is a manifest's request, checked before it is sent.
func batchBody(manifest *BatchManifest) (string, error) {
	if !ValidName(manifest.Table) {
		return "", &ManifestError{Msg: fmt.Sprintf("table %q: invalid name: a table name wants letters, digits, _ . and -; run: nova-table help", manifest.Table)}
	}
	if manifest.Members == nil {
		manifest.Members = []BatchMemberEntry{}
	}
	o := operation{table: manifest.Table, opID: manifest.OperationID, batch: true}
	body, err := payload(*manifest)
	if err != nil {
		return "", err
	}
	if _, verr := ValidateBatchManifestRaw([]byte(body)); verr != nil {
		return "", o.beforeSending(verr)
	}
	return body, nil
}

// batchReceipt is the receipt of a manifest's apply, or its error.
func batchReceipt(manifest BatchManifest, cmd *redis.Cmd) (Receipt, error) {
	o := operation{table: manifest.Table, opID: manifest.OperationID, batch: true}
	reply, err := cmd.Slice()
	if err != nil {
		return Receipt{}, fmt.Errorf("%s: %w: %w (changed=unknown); send the same manifest again with the same operation id %q: it returns the original receipt if the batch was applied and applies it if it was not; run: %s",
			o.location(), ErrUnknownOutcome, err, manifest.OperationID, o.remedy())
	}
	if err := o.refused(reply); err != nil {
		return Receipt{}, err
	}
	if len(reply) < 2 {
		return Receipt{}, fmt.Errorf("%s: missing committed receipt", o.location())
	}
	wire, ok := reply[1].([]any)
	if !ok || (len(wire) != 6 && len(wire) != 7) || fmt.Sprint(wire[0]) != "RECEIPT" {
		return Receipt{}, fmt.Errorf("%s: malformed committed receipt", o.location())
	}
	var r Receipt
	r.Replay = len(reply) >= 3 && fmt.Sprint(reply[2]) == "REPLAY"
	r.ID = fmt.Sprint(wire[1])
	r.Outcome = fmt.Sprint(wire[5])
	for i, target := range []*uint64{&r.Epoch, &r.Before, &r.After} {
		*target, err = strconv.ParseUint(fmt.Sprint(wire[i+2]), 10, 64)
		if err != nil {
			return Receipt{}, err
		}
	}
	if len(wire) >= 7 {
		var delta BatchDelta
		if err := json.Unmarshal([]byte(fmt.Sprint(wire[6])), &delta); err != nil {
			return Receipt{}, fmt.Errorf("unmarshal batch delta: %w (raw: %s)", err, fmt.Sprint(wire[6]))
		}
		r.BatchDelta = &delta
	}
	return r, nil
}

// ReadSetScope specifies members or row/col selections to read atomically.
type ReadSetScope struct {
	Members   []string        `json:"members,omitempty"`
	Selection []CellSelection `json:"selection,omitempty"`
}

// CellSelection specifies a row and column for ReadSet.
type CellSelection struct {
	Row string `json:"row"`
	Col string `json:"col"`
}

// ReadSetResult contains the verified atomic snapshot of members and table revision.
type ReadSetResult struct {
	Table    string
	Epoch    uint64
	Revision uint64
	Members  []ReadSetMember
	Missing  []string
}

// ReadSetMember represents one member returned by ReadSet.
type ReadSetMember struct {
	ID       string
	Revision uint64
	Placed   bool
	Row      string
	Col      string
	Score    float64
	// ScoreText is the score exactly as the store holds it.
	ScoreText string
	Fields    map[string]string
}

// Member returns the member with the id from the members that were found; ok is
// false for an id that is missing (see IsMissing) or was not asked for.
func (r ReadSetResult) Member(id string) (ReadSetMember, bool) {
	for _, m := range r.Members {
		if m.ID == id {
			return m, true
		}
	}
	return ReadSetMember{}, false
}

// IsMissing says the id was asked for and does not exist as a member of the
// table. A missing member is an answer, not an error: it is listed in Missing.
func (r ReadSetResult) IsMissing(id string) bool {
	for _, m := range r.Missing {
		if m == id {
			return true
		}
	}
	return false
}

// ReadSet reads members in one round trip from one consistent snapshot of one
// table (ns_table_read_set, a read-only function): each member's place, score,
// revision and fields, the members that are missing, and the table's epoch and
// revision, which a manifest's expected_table_revision is prepared from.
//
// The scope is one of two shapes, each nonempty: Members (ids) or Selection
// (row and column pairs, every member of each cell). An empty scope, both lists
// together, or more than LimitReadSetMembers members is refused. epoch is
// optional (at most one value): it reads a materialised epoch of the table
// instead of the active one, and an epoch ahead of the active one is refused
// (ErrEpochAhead). Errors are refusals (IsRefusal) that wrote nothing, or a
// transport error from the client.
// ReadSetCmd holds a queued ns_table_read_set command.
type ReadSetCmd struct {
	table string
	cmd   *redis.Cmd
}

// QueueReadSet queues ns_table_read_set on c (a redis.Pipeliner or Cmdable).
func QueueReadSet(ctx context.Context, c redis.Cmdable, table string, scope ReadSetScope, epoch ...uint64) (*ReadSetCmd, error) {
	if !ValidName(table) {
		return nil, fmt.Errorf("table %q: invalid name; run: nova-table help", table)
	}
	scopeBody, err := payload(scope)
	if err != nil {
		return nil, err
	}
	args := []any{table, scopeBody}
	if len(epoch) > 0 {
		args = append(args, strconv.FormatUint(epoch[0], 10))
	}
	key := DefKey(table)
	cmd := c.FCallRO(ctx, FnReadSet, []string{key}, args...)
	return &ReadSetCmd{table: table, cmd: cmd}, nil
}

// QueueReadSetMembers queues ns_table_read_set for a list of member IDs.
func QueueReadSetMembers(ctx context.Context, c redis.Cmdable, table string, members []string, epoch ...uint64) (*ReadSetCmd, error) {
	return QueueReadSet(ctx, c, table, ReadSetScope{Members: members}, epoch...)
}

func fastString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case int64:
		return strconv.FormatInt(s, 10)
	default:
		return fmt.Sprint(v)
	}
}

func fastUint(v any) (uint64, error) {
	switch n := v.(type) {
	case int64:
		return uint64(n), nil
	case string:
		return strconv.ParseUint(n, 10, 64)
	default:
		return strconv.ParseUint(fmt.Sprint(v), 10, 64)
	}
}

func fastFloat(v any) (float64, string, error) {
	switch s := v.(type) {
	case string:
		f, err := strconv.ParseFloat(s, 64)
		return f, s, err
	case float64:
		return s, strconv.FormatFloat(s, 'f', -1, 64), nil
	case int64:
		return float64(s), strconv.FormatInt(s, 10), nil
	default:
		str := fmt.Sprint(v)
		f, err := strconv.ParseFloat(str, 64)
		return f, str, err
	}
}

// Result decodes the result of a queued ReadSet.
func (q *ReadSetCmd) Result() (ReadSetResult, error) {
	reply, err := q.cmd.Slice()
	if err != nil {
		o := operation{table: q.table, readSet: true}
		return ReadSetResult{}, fmt.Errorf("%s: %s: %w; run: %s", o.location(), FnReadSet, err, o.remedy())
	}
	o := operation{table: q.table, readSet: true}
	if err := o.refused(reply); err != nil {
		return ReadSetResult{}, err
	}
	if len(reply) != 6 || fastString(reply[0]) != "SET" {
		return ReadSetResult{}, fmt.Errorf("table %q: malformed read set reply", q.table)
	}
	res := ReadSetResult{
		Table: fastString(reply[1]),
	}
	res.Epoch, err = fastUint(reply[2])
	if err != nil {
		return ReadSetResult{}, err
	}
	res.Revision, err = fastUint(reply[3])
	if err != nil {
		return ReadSetResult{}, err
	}
	if rawMembers, ok := reply[4].([]any); ok {
		res.Members = make([]ReadSetMember, 0, len(rawMembers))
		for _, rm := range rawMembers {
			item, ok := rm.([]any)
			if !ok || len(item) < 7 {
				return ReadSetResult{}, fmt.Errorf("table %q: malformed member in read set", q.table)
			}
			mRev, err := fastUint(item[1])
			if err != nil {
				return ReadSetResult{}, fmt.Errorf("table %q: invalid member revision %v: %w", q.table, item[1], err)
			}
			mScore, scoreText, err := fastFloat(item[5])
			if err != nil {
				return ReadSetResult{}, fmt.Errorf("table %q: invalid member score %v: %w", q.table, item[5], err)
			}
			fieldsRaw, ok := item[6].([]any)
			if !ok || len(fieldsRaw)%2 != 0 {
				return ReadSetResult{}, fmt.Errorf("table %q: malformed member fields shape", q.table)
			}
			fields := make(map[string]string, len(fieldsRaw)/2)
			for i := 0; i < len(fieldsRaw); i += 2 {
				fields[fastString(fieldsRaw[i])] = fastString(fieldsRaw[i+1])
			}
			res.Members = append(res.Members, ReadSetMember{
				ID:        fastString(item[0]),
				Revision:  mRev,
				Placed:    fastString(item[2]) == "1",
				Row:       fastString(item[3]),
				Col:       fastString(item[4]),
				Score:     mScore,
				ScoreText: scoreText,
				Fields:    fields,
			})
		}
	}
	if rawMissing, ok := reply[5].([]any); ok {
		res.Missing = make([]string, len(rawMissing))
		for i, m := range rawMissing {
			res.Missing[i] = fastString(m)
		}
	}
	return res, nil
}

func ReadSet(ctx context.Context, c redis.Cmdable, table string, scope ReadSetScope, epoch ...uint64) (ReadSetResult, error) {
	cmd, err := QueueReadSet(ctx, c, table, scope, epoch...)
	if err != nil {
		return ReadSetResult{}, err
	}
	return cmd.Result()
}

// ReadSetMembers is ReadSet for member ids: one round trip, one snapshot. The
// optional epoch is the epoch to read, as in ReadSet.
func ReadSetMembers(ctx context.Context, c redis.Cmdable, table string, members []string, epoch ...uint64) (ReadSetResult, error) {
	return ReadSet(ctx, c, table, ReadSetScope{Members: members}, epoch...)
}

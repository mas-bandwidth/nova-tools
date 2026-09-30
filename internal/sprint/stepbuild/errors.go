package stepbuild

import (
	"errors"
	"fmt"
)

// ErrLimit is the sentinel of every LimitError: an input the contract's
// bounds cannot carry however it is cut (the contract's LIMIT).
var ErrLimit = errors.New("LIMIT")

// ErrInput is the sentinel of every InputError: an input that is not a
// well-formed request to build from (the contract's REQUEST, or TWICE).
var ErrInput = errors.New("REQUEST")

// ErrCursor is returned by After for a cursor that names no step of the
// steps given: the input the steps were built from is not the input that was
// cut when the cursor was taken.
var ErrCursor = errors.New("cursor names no step")

// LimitError is a refusal by a bound. The build returns it, and no step,
// when a member, a note or a value cannot be carried by any step: the member
// whose own fields exceed a bound, a field value over its size, a name over
// its length. It names the bound, the contract section that sets it, the
// limit and the size found, and where: the input entry (and the member or
// note in it) and the table. It carries identifiers and sizes, never field
// values. It wraps ErrLimit.
type LimitError struct {
	Bound   string // what the bound measures
	Section string // the contract section that sets it
	Limit   int    // the bound
	Actual  int    // what the input needs
	Entry   int    // index of the input entry; -1 for the header (op, intent, result)
	Table   string // its table, when it has one
	Member  string // the member at fault; empty when the value is the entry's own
	Field   string // the entry field at fault, when the bound is on one
	Name    string // the application field whose value is at fault, when the bound is on one
	Note    int    // index of the entry's note at fault; -1 when none
}

// Error is the refusal in one line: where, which bound, the limit and the
// size found.
func (e *LimitError) Error() string {
	where := fmt.Sprintf("entry %d", e.Entry)
	if e.Entry < 0 {
		where = "header"
	}
	if e.Table != "" {
		where += fmt.Sprintf(" (table %s)", e.Table)
	}
	if e.Member != "" {
		where += fmt.Sprintf(", member %q", e.Member)
	}
	if e.Note >= 0 {
		where += fmt.Sprintf(", note %d", e.Note)
	}
	if e.Field != "" {
		where += ", field " + e.Field
	}
	if e.Name != "" {
		where += fmt.Sprintf(" %q", e.Name)
	}
	return fmt.Sprintf("%s: %s: %s: bound %d (%s), needs %d; nothing was built", ErrLimit, where, e.Bound, e.Limit, e.Section, e.Actual)
}

// Unwrap is ErrLimit.
func (e *LimitError) Unwrap() error { return ErrLimit }

// InputError is a refusal of the shape of the input: an empty entry, arrays
// out of step with the ids, a field the kind does not take, a string that is
// not UTF-8, a member named twice. It names the entry, the field and the
// member, never a value. It wraps ErrInput.
type InputError struct {
	Entry  int    // index of the input entry; -1 for the header and bounds
	Field  string // the field at fault
	Member string // the member at fault, when one is
	Reason string
}

// Error is the refusal in one line: where, and what was wrong.
func (e *InputError) Error() string {
	where := fmt.Sprintf("entry %d", e.Entry)
	if e.Entry < 0 {
		where = "header"
	}
	if e.Field != "" {
		where += ", field " + e.Field
	}
	if e.Member != "" {
		where += fmt.Sprintf(", member %q", e.Member)
	}
	return fmt.Sprintf("%s: %s: %s; nothing was built", ErrInput, where, e.Reason)
}

// Unwrap is ErrInput.
func (e *InputError) Unwrap() error { return ErrInput }

// breach is a bound a tentative step would exceed: which, its limit and what
// the step would hold. A conflict (a member already in the step) is a breach
// with no limit, and never refuses a build: it only ends the step.
type breach struct {
	bound         bound
	limit, actual int
	conflict      bool
}

func breachOf(b bound, limit, actual int) *breach {
	return &breach{bound: b, limit: limit, actual: actual}
}

func repeated() *breach {
	return &breach{bound: bound{"member repeated in the step", "section 3"}, conflict: true}
}

// Package stepbuild cuts any list of entries into steps that Layer 1 of the
// sprint's table store accepts: each step inside every modelled bound of
// section 6 of the Layer 1 table contract (tset/1, contract revision 3), such
// that applying the steps in order is applying the whole. It is pure: no store,
// no socket, no clock.
//
// What is modelled, and what is not. The bounds the cut works to are the
// constants of limits.go, each with its section: the encoded request, the
// entries, tables, member candidates and guard-only members, the IDs of an
// entry and of a line, the row names, the notes and about IDs, the field-value
// observations, the generated line's bytes, the planned argv bytes, and the
// bounds of a name, a value, an intent and a result. NOT modelled, and not
// claimed: the planned commands (65,536 a step), the cell and key probes
// (20,000), the raw payload a step fetches (8 MiB: it depends on the values
// the store holds, which the request does not carry), and count, rcount and
// advance entries (they carry cells or an epoch, not members). The generated
// line and the planned argv bytes are counted as upper bounds over a layout
// the contract does not fix (cost.go; layout.go). So a step this package
// emits is inside every modelled bound; it is not claimed to be inside the
// bounds that are not modelled.
//
// An entry names a table, a kind (create, move, remove, guard, rows, or a
// note-only entry), a set of members with the fields to set, and optional
// notes and attached guards. The cut follows six rules.
//
//  1. An entry whose member set is too large is split into several entries,
//     in the order given, over as many steps as the bounds need. Parts of one
//     entry that fit one step share it, as section 6 asks (two thousand
//     distinct 1 KiB fields are never one 1 MiB log line: they are disjoint
//     entries of one step). No step is closed before a bound says so.
//  2. The guards that belong to an entry travel with it: every step that holds
//     a part of the entry holds the entry's guards, once, ahead of the first
//     part in it. A guard is never cut.
//  3. The encoded size of a step is exact, never estimated: the fixed parts of
//     a request are counted by the code that writes them (encode.go), each
//     member's items by the same string sizing, and Step.Bytes is held to
//     len(Step.Encode()) on every step the tests build.
//  4. A member whose own fields exceed a bound of an otherwise empty step
//     cannot be cut: the build refuses, before any step is returned, naming
//     the member and the bound (LimitError). So does a value over its own
//     size bound (a name, a field value, an intent).
//  5. The cut is deterministic: the same input gives the same steps, byte for
//     byte, so a resumed verb rebuilds the same parts.
//  6. Each step carries its part index and the total, and a cursor naming the
//     last member it holds, which After turns into the steps still to run.
//
// A member appears once per table in a step (section 3, TWICE): a piece that
// names a member already in the step starts the next step, which is the same
// as applying the two in order. Notes follow the members of their entry, in
// the last step that holds a part of it or, when that step is full, in the
// steps after it.
//
// Rows and members depend on each other (section 3), and once the bounds cut
// the entries into steps the order they are in decides what each step sees. The
// entries are placed in the order of the input except where that would make
// the result depend on the cut: a rows entry that adds a row goes before the
// member entries whose destination it is, and the member entries whose source
// is a row go before the rows entry that deletes it (order.go). A rows entry
// that deletes a row also ends the step of the member entries that name it, and
// a member entry that goes into a row a rows entry of the step deletes starts
// the next step (place.go).
//
// The contract's bounds are constants (limits.go), each with its section. Where
// the contract is silent (the size of a generated log line, the commands a
// step plans and their argv bytes) this package takes the stricter reading, an
// upper bound, and says so where it applies it (cost.go, layout.go). The planned
// argv bytes are counted over Layer 1's own command layout, one row of a table
// for each command and key with its section beside it, and a step is cut to
// the count with a margin of 25 percent on top.
//
// Rows and members: a rows entry that adds a row and deletes another is placed
// as its adds, the members that move between them and its deletes when it
// cannot be placed whole (the rename of a row), as two wire entries.
//
// Tests. The unit tier holds a sample of the property test's inputs to every
// bound, by the lite accounting, and one in sixteen of those to the full
// accounting (the generated line by encoding/json, the planned argv bytes by
// the model and by Layer 1's own layout counted from real commands) and to the
// fullness check: the unit tier's package budget is 2 s. The slow tier (go test
// -tags slow, make test-slow) cuts more inputs, the unit tier's seeds among them,
// with the full accounting and the fullness check on every one, and cuts the
// worst case of every kind of entry at the contract's own 8 MiB.
//
// The cut is linear in the input: each member's strings are read once, to
// validate and size them, and each member is placed by constant-time checks
// against the step it joins (BenchmarkBuild100kMembersOfOneKiB).
package stepbuild

import "fmt"

// Kind is the kind of an entry, as section 3 names them, plus KindNote.
type Kind string

// The kinds of entry the builder cuts. Count, rcount and advance entries are
// not modelled: they carry cells or an epoch, not members, and no cut applies.
const (
	KindCreate Kind = "create" // places new members: table, to, ids and scores
	KindMove   Kind = "move"   // moves members: table, from, ids
	KindRemove Kind = "remove" // retires members: table, from, ids
	KindGuard  Kind = "guard"  // guard-only members: table, from, ids
	KindRows   Kind = "rows"   // adds and deletes rows of a table
	KindNote   Kind = "note"   // carries notes only: puts no entry on the wire
)

// Entry is one entry of the input, or one part of one in a step. The fields
// are the wire's (section 3), and nil is absent: an empty non-nil Set is a
// present empty object, which the contract keeps distinct.
//
// The member arrays IDs, Scores, Revs, Each and About are dense and, when
// present, aligned with IDs. Set, Unset, BeforeFields, Meta, From and To
// belong to the entry as a whole and repeat in every part of it. Meta holds
// string values only.
type Entry struct {
	Kind  Kind
	Table string

	From string // cell reference "<row>:<col>", split at its last colon
	To   string

	IDs    []string
	Scores []string
	Revs   []string
	Each   []map[string]string
	About  []string

	Set          map[string]string
	Unset        []string
	BeforeFields []string
	Meta         map[string]string

	// Add and Del are the rows of a KindRows entry. The members of a rows
	// entry are its Add names then its Del names, in that order.
	Add []string
	Del []string

	// Guards are guard entries that belong to this one and travel with every
	// step that holds a part of it. They are input only: a Placed guard has
	// none.
	Guards []Entry
	// Notes are emitted after the entry's members. They are input only.
	Notes []Note
}

// Note is a note line: a meta object and the primary IDs it is about.
type Note struct {
	Meta  map[string]string
	About []string
}

// Ident is a step's identity in the request: its op, the intent that op
// replays under and the caller's result. Op and Intent are both set or both
// empty (section 3). Result is empty when there is none.
type Ident struct {
	Op     string
	Intent string
	Result string
}

// Member is one string member of a request object.
type Member struct {
	Key   string
	Value string
}

// Config is what a build needs beside the entries.
type Config struct {
	Epoch string // the request epoch, a canonical decimal string

	// Header is the request's other members that are the same in every
	// step, string valued, written after the epoch in the order given: the
	// sprint's namespace, whose key is the store binding's to name (this
	// package holds no name of a deployment). A key is not epoch, op,
	// intent, result, entries or notes, and is given once; a value is a name
	// of at most LimitNameBytes.
	Header []Member

	// Ident, when set, gives the identity of step number part (from 1, the
	// step's final index, whatever the cut later finds the total to be). It
	// is called once per step, in order, as the step opens, and its size is
	// counted in the step's bytes. The ops it returns must differ from step
	// to step: two steps sharing an op would replay as one, so the build
	// refuses them. Every entry has been validated, and every member has been
	// found to fit an empty step, before the first call; only an identity so
	// large that it leaves a member no room refuses the build later, after
	// the calls made, so Ident is a pure function of part.
	Ident func(part int) Ident

	// Bounds is the cut's bounds; the zero value is Contract().
	Bounds Bounds

	// MemberPrefixBytes is the length of the longest member prefix of the
	// tables the entries name: a record's key is the prefix and the stored ID
	// (section 1.2), and the planned argv bytes of every command that writes a
	// record count that key (layout.go). Zero is DefaultMemberPrefixBytes, the
	// longest Layer 1 accepts; a step is inside the planned argv bound for a
	// prefix of at most this length.
	MemberPrefixBytes int
}

// Step is one Layer 1 write: its request in parts. Entries and notes share
// their arrays with the input; a caller does not change them.
type Step struct {
	Epoch  string
	Header []Member
	Ident  Ident

	Part   int    // this step's index, from 1
	Parts  int    // how many steps the input needs
	Cursor Cursor // the position the input has reached once this step has applied

	Entries []Placed
	Notes   []PlacedNote

	// Bytes is the exact size of Encode's output.
	Bytes int
}

// Placed is one wire entry of a step: a part of an input entry, or one of its
// guards.
type Placed struct {
	Entry
	Source int  // index of the input entry it comes from
	Guard  bool // an attached guard, repeated in every step that holds its owner
}

// PlacedNote is one note of a step, with the input entry it comes from.
type PlacedNote struct {
	Note
	Source int
}

// Cursor is a position in the input: what a step leaves done. Steps' cursors
// strictly increase, so a cursor names one step. The entries are placed in the
// order of the input, except where rows and members need another (order.go);
// Entry is the position in the order they were placed in, which is the input's
// index for an input that needed none (a Placed entry's Source is always the
// input's index).
type Cursor struct {
	Entry int    // position of the entry the step ends in, in the order the entries were placed in
	Done  int    // members of that entry done, this step's included
	Notes int    // notes of that entry done, this step's included
	Table string // the table of the step's last member; empty when it has none
	ID    string // the step's last member (a row name for a rows entry); empty when it has none
}

// After returns the steps that follow the one whose cursor is done: the steps
// a resumed verb still has to send once done is the last cursor it applied.
// The steps are those of a rebuild from the whole input, never of what
// remains, so part numbers and identities do not shift. It refuses a cursor
// that names no step with ErrCursor.
func After(steps []Step, done Cursor) ([]Step, error) {
	for i := range steps {
		if steps[i].Cursor == done {
			return steps[i+1:], nil
		}
	}
	return nil, fmt.Errorf("%w: entry %d, member %q, after %d members", ErrCursor, done.Entry, done.ID, done.Done)
}

// Build cuts the entries into steps. It validates and costs every entry, puts
// them in their order and finds every member no step could hold, all before it
// places any, so a refusal returns no step at all (and, but for an identity
// that leaves a member no room, before cfg.Ident is called). An empty input
// is no steps. The steps are numbered from 1 and each is inside every modelled
// bound of cfg.Bounds (the contract's, by default): not the planned commands,
// the cell and key probes, the fetched payload, nor count, rcount and advance
// entries, which the package does not model (see the package documentation).
func Build(cfg Config, entries []Entry) ([]Step, error) {
	b, err := newBuilder(cfg)
	if err != nil {
		return nil, err
	}
	states, err := b.prepare(entries)
	if err != nil {
		return nil, err
	}
	for _, es := range states {
		if err := b.place(es); err != nil {
			return nil, err
		}
	}
	b.close()
	return b.finish(), nil
}

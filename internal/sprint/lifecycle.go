package sprint

// The lifecycle of a primary: six states and the legal moves between them,
// in one table (docs/SPEC-SPRINT.md section 3). The TLA+ model
// tla/SprintTables.tla has the same states and the same moves; compare the
// two row by row.
//
// A primary's state is the work-table column it is placed in. A primary that
// stops any other way than landing leaves the table (drop): its record,
// outcome and reason are kept, unplaced.

// State is a primary's state: its column in the work table.
type State = string

// The six states. Landed is final.
const (
	Waiting State = "waiting"
	Ready   State = "ready"
	Working State = "working"
	Review  State = "review"
	Merging State = "merging"
	Landed  State = "landed"
)

// States is every state, in the work table's column order.
var States = []State{Waiting, Ready, Working, Review, Merging, Landed}

// The class of a move: mechanical moves need no decision; the coordinator's
// moves are made only by the coordinator's verbs.
const (
	Mechanical  = "mechanical"
	Coordinator = "coordinator"
)

// Move is one legal move of a primary.
type Move struct {
	From, To State
	Verb     string // the verb that makes it
	Class    string // Mechanical or Coordinator
	Cause    string
}

// Moves is the lifecycle: every legal move, and nothing else. Off the table
// (drop) is legal from every open state and is not a state.
var Moves = []Move{
	{Waiting, Ready, "resolve", Mechanical, "everything it needs has landed"},
	{Ready, Working, "start", Mechanical, "a work card is cut and dealt"},
	{Working, Review, "finish", Mechanical, "its work card finished, ok or failed"},
	{Working, Ready, "fleet down", Mechanical, "its work card was withdrawn because no fleet member is up"},
	{Review, Merging, "accept", Coordinator, "two different readers said ok at this head"},
	{Review, Working, "rework", Coordinator, "rework with a fix: the next attempt is delegated at once to an up member"},
	{Review, Ready, "rework", Coordinator, "rework with a fix when no fleet member is up: start delegates it later"},
	{Merging, Review, "return", Coordinator, "the stream's CI went red and the coordinator sent it back, or return"},
	{Merging, Landed, "merge", Mechanical, "its batch, green on the stream branch, merged to the development branch"},
}

// Legal says from -> to is a move of the lifecycle.
func Legal(from, to State) bool {
	for _, m := range Moves {
		if m.From == from && m.To == to {
			return true
		}
	}
	return false
}

// IsOpen says a primary in s has not landed: drop may take it off the table.
func IsOpen(s State) bool {
	switch s {
	case Waiting, Ready, Working, Review, Merging:
		return true
	}
	return false
}

// IsState says s is one of the six states.
func IsState(s string) bool {
	for _, x := range States {
		if x == s {
			return true
		}
	}
	return false
}

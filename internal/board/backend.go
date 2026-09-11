/*
backend.go is the seam. The FORMAT is the five event lines in event.go; a backend decides
only where a line is appended and how the lines are read back, and it decides nothing
about what a line means. The two backends must produce identical listings from identical
histories — that is a test, not an aspiration — and this interface is what makes that
cheap to keep true: nothing above it can learn which backend it has.

THE READ IS THE WHOLE LOG. There is no window. The prototype read the last forty comments,
which means a card older than forty events VANISHES, and a vanished card makes the count
fall without any work being done — the one thing a board may not do. A board is small by
construction: it is what is owed, and what is owed is closed.

A CACHE MAY AVOID RE-READING; IT MAY NEVER TRUNCATE, and it may never outlive the run.
The prototype kept a twenty-second time-to-live cache in a temporary directory, which is exactly
wrong for the duplicate-filing failure this tool exists to close: two reviewers filing
nine seconds apart both read the board as it was before either wrote. The cache here is a
field in memory, held for one invocation, and INVALIDATED BY THIS TOOL'S OWN APPEND, so a
close's read-before-append can never be answered from bytes read before this run wrote.
There is no cache file anywhere, and rule 8 forbids one.
*/
package board

import "errors"

// ErrExists is an append of a card whose id already exists. With a random id that is a
// hand-made file, a copied one, or a broken random source — never an overwrite.
var ErrExists = errors.New("id exists")

// ErrNoCard is an append of a later event to a card the backend does not hold.
var ErrNoCard = errors.New("no card with that id")

// Backend is where a board's lines live.
//
// Two methods, and deliberately no third: nothing above this can ask which backend it
// has, so a rule cannot be written that holds in one and not the other.
type Backend interface {
	// Events reads the WHOLE log.
	Events() (Log, error)
	// Append adds one event line. The line is already rendered and already one line.
	Append(line string) error
}

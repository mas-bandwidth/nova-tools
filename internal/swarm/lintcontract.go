package swarm

import (
	"slices"
	"strings"
)

// THE CONTRACT LINE HAS ONE FORM, AND THIS IS THE ONE PLACE THAT SAYS SO: the
// prefix list below is what every writer of a card and every reader of one uses.
//
// Line 1 of a card is its contract: the swarm hashes everything below it, records the
// line at admission, and `gather` refuses a `RESULT.md` whose line 1 differs
// (the deleted spec-pulse page 10, the-card-as-cut-writes-it, lines 12-14). Three readers touch it and
// two of them disagreed on one character:
//
//   - `cut` writes and accepts `RESULT <label> sha=<sha12>` -- no colon
//     (internal/pulse/cut.go, and the example at
//     that page, line 4);
//   - `lint --card`'s `result-first` wanted `RESULT: ` -- with one
//     colon;
//   - `gather` compares line 1 to line 1 and imposes no prefix of its own, so it follows
//     whichever the other two settle on (internal/pulse/harvest.go, classifyResult).
//
// The cost of the disagreement: every card `cut` writes draws a `result-first` drift, and
// a hand-written card in the colon-less form draws one, for a missing colon.
//
// THE COLON FORM WINS. It is SPEC-SWARM's own law and it is what the majority
// of writers already write -- `cut --kind` (internal/pulse/cutkind.go),
// `internal/pulse/manager.go`. `RESULT: <label>
// sha=<sha12>` is the form to WRITE.
//
// THE NO-COLON FORM IS ACCEPTED AS A STOPGAP, NOT AS A SECOND RULE. The one renderer
// still on it is the plain `cut` template path (`internal/pulse/cut.go`), and the
// follow-up card that closes this stopgap rewrites it and adds the class test
// `every-writer-and-reader-agrees-on-the-result-line`. Until that card lands, refusing
// the no-colon form would refuse cards a tool on dev writes today, so both are read --
// and it is that class test, never a judgement here, that retires the second entry
// below. internal/swarm/lintcontract_test.go goes red the day any of the three readers
// changes form.
//
// The colon form is FIRST in this slice, and it is the only form any message names as
// what to write.
var CardContractPrefixes = []string{"RESULT: ", "RESULT "}

// IsCardContractLine says whether line 1 of a card is a contract line in either form.
// The prefix is anchored at column 0: an indented or quoted RESULT is prose.
func IsCardContractLine(line string) bool {
	return slices.ContainsFunc(CardContractPrefixes, func(p string) bool { return strings.HasPrefix(line, p) })
}

// CardContractWanted is what `result-first` wants, in ONE form, for the remedy line and
// for any tool that has to print the rule rather than apply it. It names the stopgap in
// the same breath so a writer reading a card `cut` wrote is not told it is wrong, and so
// that nobody has to read two documents to learn which of the two to type.
const CardContractWanted = "`RESULT: <label> sha=<sha12>`, with the colon. The colon-less `RESULT <label> sha=` an older renderer prints is accepted as a stopgap, and is not the form to write"

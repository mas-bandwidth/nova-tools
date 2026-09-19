package swarm

import "strings"

// THE CONTRACT LINE, IN THE TWO FORMS THE TOOLS WRITE TODAY (issue #1741).
//
// Line 1 of a card is its contract: the swarm hashes everything below it, records the
// line at admission, and `gather` refuses a `RESULT.md` whose line 1 differs
// (docs/spec-pulse/10-the-card-as-cut-writes-it.md:12-14). Three readers touch it and
// two of them disagreed on one character:
//
//   - `cut` writes and accepts `RESULT <label> sha=<sha12>` -- no colon
//     (internal/pulse/cut.go, and the example at
//     docs/spec-pulse/10-the-card-as-cut-writes-it.md:4);
//   - `lint --card`'s `result-first` wanted `RESULT: ` -- with one
//     (docs/WORKER-CARDS.md practice 1);
//   - `gather` compares line 1 to line 1 and imposes no prefix of its own, so it follows
//     whichever the other two settle on (internal/pulse/harvest.go, classifyResult).
//
// The cost of the disagreement: every card `cut` writes drew a `result-first` drift, and
// six cards written by hand on 2026-09-19 each drew one for a colon.
//
// UNTIL ONE FORM IS SETTLED, BOTH ARE ACCEPTED. Refusing either refuses cards that real
// tools on dev write today, and a lint that cries on every card is a lint nobody reads.
// The disagreement is #1741 against nova-tools, not a judgement to make here, and
// internal/swarm/lintcontract_test.go is the class test that goes red the day either of
// the other two readers changes form. When #1741 closes, one of these two goes.
var CardContractPrefixes = []string{"RESULT ", "RESULT: "}

// IsCardContractLine says whether line 1 of a card is a contract line in either form.
// The prefix is anchored at column 0: an indented or quoted RESULT is prose.
func IsCardContractLine(line string) bool {
	for _, p := range CardContractPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// CardContractWanted is what `result-first` wants, named in both forms, for the remedy
// line and for any tool that has to print the rule rather than apply it.
const CardContractWanted = "`RESULT <label> sha=<sha12>` as `cut` writes it, or `RESULT: <CARD-id> <one line of what done looks like>` as WORKER-CARDS practice 1 writes it"

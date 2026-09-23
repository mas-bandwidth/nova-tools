package swarm

import "strings"

// THE CONTRACT LINE HAS ONE FORM, AND THIS IS THE ONE PLACE THAT SAYS SO (issue #1741,
// SPEC-TOOLWORK.md §5 rule 7).
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
//     (docs/WORKER-CARDS.md practice 1, and docs/SPEC-SWARM.md:969,976);
//   - `gather` compares line 1 to line 1 and imposes no prefix of its own, so it follows
//     whichever the other two settle on (internal/pulse/harvest.go, classifyResult).
//
// The cost of the disagreement: every card `cut` writes drew a `result-first` drift, and
// six cards written by hand on 2026-09-19 each drew one for a colon.
//
// RULED: THE COLON FORM WINS (Rowan, 2026-09-19, on the cold read of PR #1759;
// docs/SPEC-TOOLWORK.md §5 rule 7). It is SPEC-SWARM's own law and it is what the
// majority of writers already write -- `cut --kind` (internal/pulse/cutkind.go),
// `internal/pulse/manager.go`, `internal/worklang/expand.go`. `RESULT: <label>
// sha=<sha12>` is the form to WRITE.
//
// THE NO-COLON FORM IS ACCEPTED AS A STOPGAP, NOT AS A SECOND RULE. The one renderer
// still on it is the plain `cut` template path (`internal/pulse/cut.go`), and the
// follow-up card named in rule 7 rewrites it and adds the class test
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
	for _, p := range CardContractPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// CardContractWanted is what `result-first` wants, in ONE form, for the remedy line and
// for any tool that has to print the rule rather than apply it. It names the stopgap in
// the same breath so a writer reading a card `cut` wrote is not told it is wrong, and so
// that nobody has to read two documents to learn which of the two to type.
const CardContractWanted = "`RESULT: <label> sha=<sha12>`, with the colon (SPEC-TOOLWORK.md §5 rule 7, SPEC-SWARM.md:969). The colon-less `RESULT <label> sha=` the plain `cut` template still renders is accepted as a stopgap until the renderer card of rule 7 lands, and is not the form to write"

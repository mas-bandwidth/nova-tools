package main

// The land pass reconciles its landed records against the base (docs/SPEC-SPRINT.md section 7,
// no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base-bb.w1): after its batches it
// checks each landed card of the streams it was given (every stream, for none) against the
// origin tip of its base, and prints each whose head the base does not hold as one
// LANDED-MISSING line, with a line on stderr naming what blocks the return. It writes nothing
// and changes no exit: landed is final in the lifecycle (section 3), and the move that returns
// a false landing to merging is not one of its moves, so the report names what blocks it.
//
// This is the verify-landed check, not a second one (landverify.go,
// land-verify-landed-ancestry-r.w1): landedChecks names the records as land reads them and
// lander.verify fetches each base and asks git, so the pass and the verb agree line for line
// on which records are missing.

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// reconcileLanded checks the landed records of streams and prints each false one, with the
// blocker named once on stderr. A record it cannot check is left to verify-landed, which
// names every unchecked card and its reason.
func (l *lander) reconcileLanded(ctx context.Context, streams []string, stdout, stderr io.Writer) {
	if l.dry || l.twin {
		return
	}
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Work}, nil)
	l.a.serial.Unlock()
	if err != nil {
		fmt.Fprintf(stderr, "%s land: the landed records were not reconciled with the base: %s; run: nova-sprint verify-landed\n", prog, firstLine("", err))
		return
	}
	checks := landedChecks(s, streams, l.base)
	if len(checks) == 0 {
		return
	}
	l.verify(ctx, checks)
	missing := 0
	for i := range checks {
		ch := &checks[i]
		if ch.Why == "" && ch.Missing {
			missing++
			fmt.Fprintln(stdout, ch.line())
		}
	}
	if missing > 0 {
		fmt.Fprintf(stderr, "%s land: %d landed record(s) are not on their base, printed as LANDED-MISSING above; a false landing is not moved here: landed is final (section 3) and the lifecycle has no move landed -> merging; run: nova-sprint verify-landed\n", prog, missing)
	}
}

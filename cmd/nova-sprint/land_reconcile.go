package main

// Every land pass reconciles the landed records against the base (docs/SPEC-SPRINT.md section
// 7, no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base): after its batches, land
// checks each landed card of the streams it was given (every stream, for none) as
// verify-landed does, and prints each whose head its base does not hold as one LANDED-MISSING
// line, with what to do. It writes nothing and changes no exit: landed is final in the
// lifecycle (section 3), and the move that returns a false landing to merging is not one of
// its moves yet.

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// reconcileLanded checks the landed records of streams and prints the false ones; a card it
// cannot check (no head, no base, no repository) is left to verify-landed, which names why.
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
	for _, ch := range checks {
		if ch.Why == "" && ch.Missing {
			fmt.Fprintln(stdout, ch.line())
			fmt.Fprintf(stderr, "%s land: %s is recorded landed and its head %s is not on %s at %s: a false landing; landed is final, so nothing was moved: tell the owner, and land its head again once it is back in merging\n", prog, ch.ID, ch.Head, ch.Base, dashed(ch.Tip))
		}
	}
}

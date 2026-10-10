package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// stop-return is the worker's receipt after its owned child process stopped.
// The store checks STOPPED, row, generation and current working state; the
// receipt does not itself kill a process.
func (a *app) cmdStopReturn(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stop-return")
	as := fs.String("as", "", "the fleet member, friend.<name>, or reader row that owns these cards")
	reason := fs.String("reason", "", "the observed cancellation acknowledgement")
	words, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "stop-return", err.Error())
	}
	ids, gens, err := cardGens(words)
	if err != nil {
		return refuse(stderr, "stop-return", err.Error())
	}
	if *as == "" || *reason == "" || len(ids) == 0 {
		return refuse(stderr, "stop-return", "wants --as <owner-row>, --reason <cancellation acknowledgement>, and one or more <card>@<gen> from that row")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stop-return", err.Error())
	}
	return a.runStep("stop-return", *c, st, store.StopReturnStep(sprint.StopReturnReq{As: *as, IDs: ids, Gens: gens, Reason: *reason}), stdout, stderr)
}

package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// relink <old-id>[,<old-id>...] <new-id>: the repair of the edges a drop and an add of a
// twin left apart (sprint.Relink; docs/SPEC-SPRINT.md section 2, "A card replaced by its
// twin"). Every waiting card that needs an old id needs the twin instead, and the blocked
// judgments that named only the old ids are answered "replaced by <new>". The
// coordinator's alone; add --replaces makes the drop, the add and the relink one step.
func (a *app) cmdRelink(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("relink")
	reason := fs.String("reason", "", "why the twin replaces the old card, recorded with the answer of each blocked judgment it closes")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "relink", err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, "relink", "wants <old-id>[,<old-id>...] <new-id>: the card dropped and its twin on the table")
	}
	r := sprint.RelinkReq{Old: sprint.Split(pos[0]), New: pos[1], Reason: *reason, Who: c.actor}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "relink", err.Error())
	}
	return a.runStep("relink", *c, st, store.RelinkStep(r), stdout, stderr)
}

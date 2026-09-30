package sprintfn

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The adapter of this package's parts (errata 2, 8.0): one place where IT12's
// shape cannot carry what a part needs, bridged here in this item's own file
// without changing IT12's. It is listed in the pull request as an open question
// and goes away when IT12 (or the item that owns the hook) carries the thing
// itself.

// PartsBefore names what the parts read through S.before in the pre stage
// (1.0: "S.before: the before-state of every id the request names, with the
// fields it asks"): for a beat, the control card of each member it names and
// its status field (1.4.4), which tells a down member and a stranger from an up
// one. IT12's twin asks for the ids of a request's entries and intents and for
// what Phases.Before adds, and Part.Pre has no way to ask for more; so the hook
// that sets Phases.Before (IT13's) appends this function's asks, or, better,
// IT12's twin does once for every request. A beat whose control cards were not
// asked for is refused CONFIG by the beat part, and never read as a stranger.
func PartsBefore(st *State, req *Request) []BeforeAsk {
	if req.Beat == nil {
		return nil
	}
	ids := make([]string, 0, len(req.Beat.Members))
	for _, m := range req.Beat.Members {
		if id, ok := storedControlID(req.Epoch, m.Member); ok && sprint.ValidID(m.Member) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return []BeforeAsk{{Table: sprint.Fleet, IDs: ids, Fields: []string{memberStatus}}}
}

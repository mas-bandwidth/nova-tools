package sprintfn

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The adapters of this package's parts (errata 2, 8.0): two places where IT12's
// shapes cannot carry what a part needs, bridged here in this item's own files
// without changing IT12's. Each is listed in the pull request as an open
// question and goes away when IT12 (or the item that owns the hook) carries the
// thing itself.

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

// TickEnd is R18's write on the last step of a tick (1.4.2, R18): Backlog is
// the loop's backlog at the end of the tick, Layer 2's last less the cursor, as
// an exact decimal. Not zero and not armed, the sprint part enters behind at R
// + 5 min and records the backlog in tick@e as behind_n; zero, it disarms; an
// armed backlog that is not zero is left alone, so the entry is not moved while
// it shrinks (1.2).
//
// Errata 2 (item 3) puts the tick end in the sprint part, and IT12's
// SprintPart has no field for it. The part reads it through one seam,
// requestTickEnd, which returns nil until IT12 adds the field (and its wire
// key "tickend" in sprintPartWire, which sprint_parts.lua already reads); a
// test, or a twin assembled with its own registry, installs a sprint part whose
// seam supplies it.
type TickEnd struct {
	Backlog tset.Decimal
}

// requestTickEnd is the seam: the tick end a request carries. IT12's
// SprintPart carries none, so it is nil.
func requestTickEnd(*Request) *TickEnd { return nil }

package verbs

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// movedOf are the ids a step's entries change, in their order and each once
// (1.5.3: a step names the ids it moves): the ids of its create, move and
// remove entries, by their card ids. A guard or a count changes nothing.
func movedOf(req *sprintfn.Request) []ID {
	if req == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []ID
	for _, en := range req.Body.Entries {
		switch en.Kind {
		case "create", "move", "remove":
		default:
			continue
		}
		for _, id := range en.IDs {
			if c := sprint.CardID(id); !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// stepPlan is a plan that returns its step alone as a Part (1.5.3): the ids it
// moves are its entries' (movedOf). A nil step writes nothing.
func stepPlan(f func(*sprintfn.ReadReply) (*sprintfn.Request, error)) func(*sprintfn.ReadReply) (Part, error) {
	return func(rd *sprintfn.ReadReply) (Part, error) {
		req, err := f(rd)
		if err != nil || req == nil {
			return Part{}, err
		}
		return Part{Req: req, Moved: movedOf(req)}, nil
	}
}

// addUp adds another run's ids and notes to the totals: a verb that runs
// several steps of its own (a worker verb over its set) reports them all.
func (r *Result) addUp(o Result) {
	r.Moved = append(r.Moved, o.Moved...)
	r.Refused = append(r.Refused, o.Refused...)
	r.NotWritten = append(r.NotWritten, o.NotWritten...)
	r.Notes += o.Notes
}

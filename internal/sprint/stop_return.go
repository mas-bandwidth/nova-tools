package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// StopReturnReq is an owner's receipt that the named children have stopped.
// It is deliberately separate from STOP: the table must not say ready while a
// child is still running. The owner keeps the same card and branch for resume.
type StopReturnReq struct {
	As     string
	IDs    []string
	Gens   map[string]int
	Reason string
}

// StopReturn releases acknowledged work and reads to their own row. The new
// generation fences an old child's finish (and a read's late verdict).
func StopReturn(s *Snapshot, r StopReturnReq) Plan {
	return stopReturn(s, r, false)
}

// StopReturnReplay acknowledges only an already-durable same-owner return.
// It cannot change a current assignment after START (StopReturn.tla Return/ExplicitStart).
func StopReturnReplay(s *Snapshot, r StopReturnReq) Plan {
	return stopReturn(s, r, true)
}

// StopReturnOp keeps retries on the existing immutable caller-operation path.
// The reason is a payload, checked by that receipt, rather than part of identity.
func StopReturnOp(row, card string, gen int, epoch string) string {
	return sprintwire.StopReturnOp(row, card, gen, epoch)
}

func stopReturn(s *Snapshot, r StopReturnReq, replayOnly bool) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse("stop-return", "name the observed cancellation acknowledgement in --reason")
		return p
	}
	seen := map[string]bool{}
	for _, id := range r.IDs {
		if seen[id] {
			p.refuse(id, "named twice")
			continue
		}
		seen[id] = true
		c, table := s.Fleet.Card(id), Fleet
		if c == nil || !c.Placed() {
			c, table = s.Readers.Card(id), Readers
		}
		if c == nil || !c.Placed() || c.Row != r.As {
			p.refuse(id, "not a live card on "+r.As)
			continue
		}
		old := r.Gens[id]
		if old < 1 {
			p.refuse(id, "name the generation cancelled as <card>@<gen>")
			continue
		}
		if c.Int("stopped_from_gen") == old && c.Int("gen") == old+1 && (!replayOnly || c.F("stopped_reason") == cutText(r.Reason, MaxCardTextBytes)) {
			p.Said = append(p.Said, id+" was returned already")
			continue
		}
		if replayOnly {
			p.refuse(id, "no matching durable STOP return receipt; a live assignment cannot be returned after START")
			continue
		}
		live := max(c.Int("gen"), 1)
		if live != old {
			p.refuse(id, fmt.Sprintf("stale: generation %d is not the live one (%d)", old, live))
			continue
		}
		want := Working
		to := Ready
		if table == Readers {
			want, to = Reading, Asked
		}
		if c.Col != want {
			p.refuse(id, "not in "+want+" (it is "+c.Col+")")
			continue
		}
		set := map[string]string{
			"gen": itoa(old + 1), "stopped_from_gen": itoa(old),
			"stopped_reason": cutText(r.Reason, MaxCardTextBytes),
			"untaken_since":  stamp(s.Now),
		}
		unset := []string{"taken", "begun", FieldStarted, FieldFriendDeadline}
		if progress := c.F(FieldProgress); progress != "" {
			set["stopped_progress"] = progress
			unset = append(unset, FieldProgress)
		}
		p.Units = append(p.Units, Unit{Key: id, Stream: c.F("stream"),
			Changes: []Change{change(table, moveEntry(c, c.Row, to, set, unset...))},
			Moved:   fmt.Sprintf("%s %s -> %s on %s gen=%d (cancel acknowledged)", id, want, to, c.Row, old+1)})
	}
	return p
}

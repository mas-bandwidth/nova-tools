package sprint

import (
	"sort"
	"time"
)

// StreamClock is a stream's state, since (the last change of its state) and
// progress (the last change of its state or of any of its counts).
type StreamClock struct {
	Stream   string
	State    string
	Since    time.Time
	Progress time.Time
}

// Stalled says a stream that has not landed has made no progress for longer
// than stale. It is pull visibility: nothing here detects a dead process.
func (c StreamClock) Stalled(now time.Time, stale time.Duration) bool {
	return c.State != StreamLanded && stale > 0 && !c.Progress.IsZero() && now.Sub(c.Progress) > stale
}

// InboxReq is what the inbox is computed from, at read time: the open
// judgments, the notifications since the cursor, the streams' clocks, and the
// clock reading of the read. Overdue is computed here, so a dead coordinator
// is visible to anyone who runs inbox.
type InboxReq struct {
	Now      time.Time
	Open     []Open
	Recent   []Note // since the cursor, oldest first
	Streams  []StreamClock
	Deadline time.Duration // a judgment open longer is overdue
	Stale    time.Duration // a moving stream unchanged longer needs a look
}

// Group is notifications of one kind, type and stream, as one line.
type Group struct {
	N         int           `json:"n"`
	Kind      string        `json:"kind"`
	Type      string        `json:"type"`
	Stream    string        `json:"stream,omitempty"`
	Count     int           `json:"count"`
	Primaries []string      `json:"primaries,omitempty"`
	Notes     []string      `json:"notes,omitempty"`
	Marked    bool          `json:"marked,omitempty"`
	Overdue   bool          `json:"overdue,omitempty"`
	Oldest    time.Time     `json:"oldest"`
	Due       time.Time     `json:"due"`
	Waited    time.Duration `json:"waited_ns"`
	Decisions []string      `json:"decisions,omitempty"`
	What      string        `json:"what,omitempty"`
	Before    int           `json:"before,omitempty"`
}

// Inbox groups: open judgments first (marked ones, repeats and overdue, first
// of all, then the longest waiting), then streams that have not moved past
// their deadline, then what happened and what was decided since the cursor, in
// time order.
func Inbox(r InboxReq) []Group {
	var judg []Group
	at := map[string]int{}
	seen := map[string]bool{}
	for _, o := range r.Open {
		n := o.Note
		due := n.Due(r.Deadline)
		overdue := (r.Deadline > 0 || !n.Review.IsZero()) && r.Now.After(due)
		k := n.Type + "\x00" + n.Stream + "\x00" + boolWord(n.Marked || overdue)
		i, ok := at[k]
		if !ok {
			i = len(judg)
			at[k] = i
			judg = append(judg, Group{Kind: Judgment, Type: n.Type, Stream: n.Stream, Oldest: n.At, Due: due, Decisions: n.Decisions})
		}
		g := &judg[i]
		if due.Before(g.Due) {
			g.Due = due
		}
		g.Marked = g.Marked || n.Marked || overdue
		g.Overdue = g.Overdue || overdue
		if n.At.Before(g.Oldest) {
			g.Oldest = n.At
		}
		if n.Before > g.Before {
			g.Before = n.Before
		}
		if g.What == "" {
			g.What = n.What
		}
		if !seen[k+o.Subject()] {
			seen[k+o.Subject()] = true
			g.Count++
			shown := []string{o.Subject()}
			if n.StreamLevel {
				shown = n.Primaries // the cards the stream stopped on
			}
			for _, p := range shown {
				if len(g.Primaries) < MaxListed && !contains(g.Primaries, p) {
					g.Primaries = append(g.Primaries, p)
				}
			}
		}
		if !contains(g.Notes, n.ID) {
			g.Notes = append(g.Notes, n.ID)
		}
		if overdue && !contains(g.Decisions, "act") {
			g.Decisions = append(append([]string{}, g.Decisions...), "act")
		}
	}
	for i := range judg {
		judg[i].Waited = r.Now.Sub(judg[i].Oldest)
		sort.Strings(judg[i].Primaries)
	}
	sort.SliceStable(judg, func(i, j int) bool {
		if judg[i].Marked != judg[j].Marked {
			return judg[i].Marked
		}
		return judg[i].Oldest.Before(judg[j].Oldest)
	})
	out := judg
	for _, st := range r.Streams {
		if !st.Stalled(r.Now, r.Stale) {
			continue
		}
		out = append(out, Group{Kind: Judgment, Type: NStreamStale, Stream: st.Stream, Count: 1, Marked: true, Overdue: true,
			Oldest: st.Progress, Due: st.Progress.Add(r.Stale), Waited: r.Now.Sub(st.Progress), Decisions: Decisions[NStreamStale],
			What: "state " + st.State + " since " + st.Since.UTC().Format(time.RFC3339)})
	}
	var rest []Group
	at = map[string]int{}
	for _, n := range r.Recent {
		if n.Kind == Judgment {
			continue // judgments are shown while open, above
		}
		k := n.Kind + "\x00" + n.Type + "\x00" + n.Stream
		i, ok := at[k]
		if !ok {
			i = len(rest)
			at[k] = i
			rest = append(rest, Group{Kind: n.Kind, Type: n.Type, Stream: n.Stream, Oldest: n.At, What: n.What})
		}
		g := &rest[i]
		c := n.Count
		if c == 0 && len(n.Primaries) == 0 {
			c = 1
		}
		g.Count += c
		for _, p := range n.Primaries {
			if len(g.Primaries) < MaxListed && !contains(g.Primaries, p) {
				g.Primaries = append(g.Primaries, p)
			}
		}
		g.Notes = append(g.Notes, n.ID)
	}
	out = append(out, rest...)
	for i := range out {
		out[i].N = i + 1
	}
	return out
}

package land

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status prints the lander's view: lanes in flight with age, the landable
// count and head, dropped by reason, held by holder with the holder's state,
// and one line of counts per state. A PR with no state prints as MISSING.
func Status(s *Snapshot, now time.Time) []string {
	type lane struct {
		n  int
		at time.Time
		ok bool
	}
	lanes := map[string]*lane{}
	dropped := map[string]int{}
	states := map[string]int{}
	heldBy := map[string]int{}
	var ndropped, nheld int
	for id, f := range s.PRs {
		st := f["state"]
		if st == "" {
			st = "MISSING"
		}
		states[st]++
		switch st {
		case "landing":
			name := f["lane"]
			if name == "" {
				name = "lane-MISSING"
			}
			l := lanes[name]
			if l == nil {
				l = &lane{}
				lanes[name] = l
			}
			l.n++
			if t, ok := parseTime(f["lane_at"]); ok && (!l.ok || t.Before(l.at)) {
				l.at, l.ok = t, true
			}
		case "dropped":
			r := f["drop_reason"]
			if r == "" {
				r = "reason MISSING"
			}
			dropped[r]++
			ndropped++
		}
		seen := map[string]bool{}
		for _, h := range s.Holds[id] {
			if h.Open() && !seen[h.Holder] {
				seen[h.Holder] = true
				heldBy[h.Holder]++
			}
		}
		if len(seen) > 0 {
			nheld++
		}
	}

	var out []string
	names := sortedKeys(lanes)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		l := lanes[name]
		age := "age MISSING"
		if l.ok {
			age = Age(now.Sub(l.at))
		}
		parts = append(parts, fmt.Sprintf("%s (%d PRs, %s)", name, l.n, age))
	}
	line := fmt.Sprintf("lanes %d in flight", len(names))
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, ", ")
	}
	out = append(out, line)

	line = fmt.Sprintf("landable %d", len(s.Landable))
	if len(s.Landable) > 0 {
		line += ", head " + s.Landable[0]
	}
	out = append(out, line)

	out = append(out, countLine("dropped", ndropped, dropped, nil))
	out = append(out, countLine("held", nheld, heldBy, func(f string) string {
		st := s.Friends[f]
		if !st.Known {
			return "state MISSING"
		}
		if st.Since.IsZero() {
			return st.State
		}
		return st.State + " " + Age(now.Sub(st.Since))
	}))

	keys := sortedKeys(states)
	parts = parts[:0]
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, states[k]))
	}
	out = append(out, "states: "+strings.Join(parts, ", "))
	return out
}

func countLine(label string, total int, by map[string]int, note func(string) string) string {
	line := fmt.Sprintf("%s %d", label, total)
	if len(by) == 0 {
		return line
	}
	keys := sortedKeys(by)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		p := fmt.Sprintf("%s %d", k, by[k])
		if note != nil {
			p += " (" + note(k) + ")"
		}
		parts = append(parts, p)
	}
	return line + ": " + strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

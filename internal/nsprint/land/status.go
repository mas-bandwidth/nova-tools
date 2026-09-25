package land

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status prints the lander's view from Redis alone (7.7): units by state,
// per repo and base the chain with its batches, the gates in flight per
// bench with age and the worker's liveness, landable with its head, dropped
// by reason, waiting by condition, freezes, benched benches, and landed in
// the last hour with its p50.
func Status(s *Snapshot, now time.Time) []string {
	states := map[string]int{}
	dropped := map[string]int{}
	waiting := map[string]int{}
	var ndropped, nwaiting, nlanded int
	var lat []time.Duration
	for _, f := range s.Units {
		st := f["state"]
		if st == "" {
			st = "MISSING"
		}
		states[st]++
		switch st {
		case "dropped":
			r := f["drop_reason"]
			if r == "" {
				r = "reason MISSING"
			}
			dropped[r]++
			ndropped++
		case "opened", "reading":
			nwaiting++
			if n, err := strconv.Atoi(f["holds_open"]); err == nil && n > 0 {
				waiting["hold"]++
			} else {
				waiting["other (why <unit> names it)"]++
			}
		case "landed":
			at, ok := parseMS(f["merged_at"])
			if !ok || now.Sub(at) > time.Hour || at.After(now) {
				continue
			}
			nlanded++
			if r, ok := parseMS(f["last_read_at"]); ok && !r.After(at) {
				lat = append(lat, at.Sub(r))
			}
		}
	}

	var out []string
	keys := sortedKeys(states)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, states[k]))
	}
	line := fmt.Sprintf("units %d", len(s.Units))
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, ", ")
	}
	out = append(out, line)

	var gates []string
	for _, rb := range s.Bases {
		chain := s.Chain[rb]
		parts = parts[:0]
		for _, b := range chain {
			parts = append(parts, fmt.Sprintf("%s %s attempt %s units %s", b["id"], b["state"], b["attempt"], members(b["members"])))
			if b["state"] != "gating" {
				continue
			}
			g := fmt.Sprintf("%s/%s %s attempt %s", b["bench"], b["slot"], b["id"], b["attempt"])
			if t, ok := parseMS(b["claimed_at"]); ok {
				g += " " + Age(now.Sub(t))
			} else {
				g += " age MISSING"
			}
			wk := "worker:" + b["bench"] + ":" + b["slot"]
			if s.Workers[wk] {
				g += " worker live"
			} else {
				g += " worker MISSING " + wk
			}
			gates = append(gates, g)
		}
		line = fmt.Sprintf("chain %s %d", rb, len(chain))
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, "; ")
		}
		out = append(out, line)
	}
	sort.Strings(gates)
	line = fmt.Sprintf("gates %d", len(gates))
	if len(gates) > 0 {
		line += ": " + strings.Join(gates, "; ")
	}
	out = append(out, line)

	for _, rb := range s.Bases {
		l := s.Landable[rb]
		line = fmt.Sprintf("landable %s %d", rb, len(l))
		if len(l) > 0 {
			line += ", head " + l[0]
		}
		out = append(out, line)
	}

	out = append(out, countLine("dropped", ndropped, dropped, nil))
	out = append(out, countLine("waiting", nwaiting, waiting, nil))

	parts = parts[:0]
	for _, rb := range s.Bases {
		if fz, ok := s.Freeze[rb]; ok {
			p := rb.String() + " " + fz["reason"]
			if fz["remedy"] != "" {
				p += " (remedy " + fz["remedy"] + ")"
			}
			parts = append(parts, p)
		}
	}
	line = fmt.Sprintf("freezes %d", len(parts))
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, "; ")
	}
	out = append(out, line)

	if len(s.Benches) > 0 {
		out = append(out, countLine("benched", len(s.Benches), map[string]int{}, nil)+": "+benched(s.Benches))
	}

	line = fmt.Sprintf("landed %d in the last hour", nlanded)
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		line += ", p50 read to landed " + Age(lat[(len(lat)-1)/2])
	}
	return append(out, line)
}

// members prints a batch's ordered unit@head list with 8-char heads.
func members(csv string) string {
	if csv == "" {
		return "MISSING"
	}
	var out []string
	for _, m := range strings.Split(csv, ",") {
		u, h, ok := strings.Cut(strings.TrimSpace(m), "@")
		if ok {
			m = u + "@" + short(h)
		}
		out = append(out, m)
	}
	return strings.Join(out, ",")
}

func benched(m map[string]string) string {
	keys := sortedKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+m[k])
	}
	return strings.Join(parts, ", ")
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

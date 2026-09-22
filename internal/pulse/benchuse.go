package pulse

// benchuse.go is the bench-row score nova-tools #2680 adds. It reads cards:done
// entries (a fixture here; the stream once the writers are on dev) and does not
// render the swarm table.
//
// OK IS NOT USEFUL. event=ok means the card ended OK. The score is useful work,
// and a useful card is one of three products, counted once:
//
//   - landed — a pull request merged that day (event=landed)
//   - verified-defect — a defect the card verified
//   - receipted-issue — an issue the card filed that was receipted
//
// event=pr and event=pullreq are the same opened pull request. #2563's wire
// name is pr; the issue's word is pullreq. Opening a pull request is not
// merging one, and neither an opened pull request nor a bare ok is useful.
// event=defect and event=issue are not useful either: a defect that was not
// verified and an issue that was not receipted are the same mistake as
// counting OK. The two useful names are not in #2563's kind list yet; a writer
// that has not emitted them contributes zero, which is not a guess.
//
// ONE CARD, ONCE. The stream delivers at least once. A second landed entry for
// the same label is the same card, and a card that both landed and receipted
// an issue is still one useful card.
//
// THE LANDING JOINS THE BENCH. A landed entry often names no bench. An empty
// bench is filled from that card's pr or pullreq entry, then from any other
// entry of the same label. The day is the UTC day of at. An entry with no
// stamp, and a call with no day, count nothing — never every card the stream
// has ever held.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CardDone is one cards:done entry. pr, head, attempt, model, route and the
// cost fields may be present on the line; they are not a count.
type CardDone struct {
	Label string
	Bench string
	Event string
	At    time.Time
}

// BenchUse is one bench's cards for one UTC day.
type BenchUse struct {
	Bench  string
	OK     int
	Landed int
	Useful int
}

// ParseCardsDone reads the fixture form of cards:done: one entry per line,
// key=value fields, # comments and blank lines skipped. label and event are
// required. at, when present, is RFC3339; a line with a stamp that is not is
// refused rather than counted.
func ParseCardsDone(text string) ([]CardDone, error) {
	var out []CardDone
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e := CardDone{}
		atRaw := ""
		sawAt := false
		for _, field := range strings.Fields(line) {
			key, val, ok := strings.Cut(field, "=")
			if !ok || key == "" {
				return nil, fmt.Errorf("cards:done line %d: %q is not key=value", n, field)
			}
			switch key {
			case "label":
				e.Label = val
			case "bench":
				e.Bench = val
			case "event":
				e.Event = val
			case "at":
				atRaw = val
				sawAt = true
			}
		}
		if e.Label == "" {
			return nil, fmt.Errorf("cards:done line %d: label is required; it is the card the counts join on", n)
		}
		if e.Event == "" {
			return nil, fmt.Errorf("cards:done line %d: event is required (ok, pr, landed, verified-defect, receipted-issue)", n)
		}
		if sawAt {
			at, err := time.Parse(time.RFC3339, atRaw)
			if err != nil {
				return nil, fmt.Errorf("cards:done line %d: at %q is not RFC3339", n, atRaw)
			}
			e.At = at.UTC()
		}
		out = append(out, e)
	}
	return out, nil
}

// CountBenchUse counts ok, landed and useful cards per bench on day's UTC date.
// A zero day counts nothing.
func CountBenchUse(entries []CardDone, day time.Time) []BenchUse {
	if day.IsZero() {
		return nil
	}
	pullreqBench, namedBench := cardBenches(entries)
	type key struct{ bench, label string }
	type marks struct{ ok, landed, useful bool }
	got := map[key]marks{}
	for _, e := range entries {
		if e.Label == "" || !sameUTCDay(e.At, day) {
			continue
		}
		bench := joinedBench(e, pullreqBench, namedBench)
		if bench == "" {
			continue
		}
		k := key{bench, e.Label}
		m := got[k]
		switch e.Event {
		case "ok":
			m.ok = true
		case "landed":
			m.landed = true
			m.useful = true
		case "verified-defect", "receipted-issue":
			m.useful = true
		}
		got[k] = m
	}
	by := map[string]*BenchUse{}
	for k, m := range got {
		if !m.ok && !m.landed && !m.useful {
			continue
		}
		row := by[k.bench]
		if row == nil {
			row = &BenchUse{Bench: k.bench}
			by[k.bench] = row
		}
		if m.ok {
			row.OK++
		}
		if m.landed {
			row.Landed++
		}
		if m.useful {
			row.Useful++
		}
	}
	names := make([]string, 0, len(by))
	for name := range by {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]BenchUse, 0, len(names))
	for _, name := range names {
		out = append(out, *by[name])
	}
	return out
}

// cardBenches remembers, per label, the bench a pr/pullreq entry named and the
// first bench any entry named. A later entry does not move a card.
func cardBenches(entries []CardDone) (pullreq, named map[string]string) {
	pullreq = map[string]string{}
	named = map[string]string{}
	for _, e := range entries {
		if e.Label == "" || e.Bench == "" {
			continue
		}
		if _, ok := named[e.Label]; !ok {
			named[e.Label] = e.Bench
		}
		if (e.Event == "pr" || e.Event == "pullreq") && pullreq[e.Label] == "" {
			pullreq[e.Label] = e.Bench
		}
	}
	return pullreq, named
}

// joinedBench is the entry's own bench, or the card's pullreq bench, or any
// bench the card already named. Empty means the card is not on a row.
func joinedBench(e CardDone, pullreq, named map[string]string) string {
	if e.Bench != "" {
		return e.Bench
	}
	if b := pullreq[e.Label]; b != "" {
		return b
	}
	return named[e.Label]
}

func sameUTCDay(at, day time.Time) bool {
	if at.IsZero() {
		return false
	}
	ay, am, ad := at.UTC().Date()
	dy, dm, dd := day.UTC().Date()
	return ay == dy && am == dm && ad == dd
}

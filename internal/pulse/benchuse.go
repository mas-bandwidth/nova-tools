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
// an issue is still one useful card. The same label on two benches is still
// one card: useful is counted on one bench, not once per bench.
//
// THE LANDING JOINS ITS PULL REQUEST. A landed entry often names no bench.
// It joins the pr or pullreq entry of the same label that is the same
// attempt: pr, head and attempt agree wherever both sides name them, and at
// least one of those is shared. Two benches for that identity, or a landing
// with no identity while the label opened two pull requests, is not a row.
// A landing does not take the first bench the label was seen on, and it does
// not borrow a bench from an ok entry. The day is the UTC day of at. An
// entry with no stamp, and a call with no day, count nothing — never every
// card the stream has ever held.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CardDone is one cards:done entry. PR, Head and Attempt are the identity a
// landing joins on. model, route and the cost fields may be present on the
// line; they are not a count.
type CardDone struct {
	Label   string
	Bench   string
	Event   string
	At      time.Time
	PR      string
	Head    string
	Attempt string
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
			case "pr":
				e.PR = val
			case "head":
				e.Head = val
			case "attempt":
				e.Attempt = val
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
	opened := pullreqsByLabel(entries)
	type key struct{ bench, label string }
	type marks struct{ ok, landed bool }
	got := map[key]marks{}
	// label → bench → whether that bench's qualification was a landing.
	// Useful is decided once per label, after the whole day is seen.
	usefulOn := map[string]map[string]bool{}
	for _, e := range entries {
		if e.Label == "" || !sameUTCDay(e.At, day) {
			continue
		}
		bench := joinedBench(e, opened)
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
			noteUseful(usefulOn, e.Label, bench, true)
		case "verified-defect", "receipted-issue":
			noteUseful(usefulOn, e.Label, bench, false)
		}
		got[k] = m
	}
	keep := map[string]string{}
	for label, benches := range usefulOn {
		keep[label] = benchThatKeepsUseful(benches)
	}
	by := map[string]*BenchUse{}
	for k, m := range got {
		useful := keep[k.label] == k.bench
		if !m.ok && !m.landed && !useful {
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
		if useful {
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

// pullreq is one opened pull request that named a bench. A later open does
// not erase an earlier one: both stay, and a landing that matches both is
// ambiguous.
type pullreq struct {
	bench   string
	pr      string
	head    string
	attempt string
}

// pullreqsByLabel collects pr and pullreq entries that named a bench. The
// day does not matter: a landing joins its pull request even when the open
// and the merge fall on different days.
func pullreqsByLabel(entries []CardDone) map[string][]pullreq {
	out := map[string][]pullreq{}
	for _, e := range entries {
		if e.Label == "" || e.Bench == "" {
			continue
		}
		if e.Event != "pr" && e.Event != "pullreq" {
			continue
		}
		out[e.Label] = append(out[e.Label], pullreq{
			bench: e.Bench, pr: e.PR, head: e.Head, attempt: e.Attempt,
		})
	}
	return out
}

// joinedBench is the entry's own bench. A landed entry that names none joins
// the one pull request it belongs to. Empty means the card is not on a row.
func joinedBench(e CardDone, opened map[string][]pullreq) string {
	if e.Bench != "" {
		return e.Bench
	}
	if e.Event != "landed" {
		return ""
	}
	return landingBench(e, opened[e.Label])
}

// landingBench is the bench of the one pullreq that is this landing's
// attempt. Identity present on the landing must agree; an identity the
// pullreq did not record does not disqualify it. A landing that names no
// pr, head or attempt joins only when the label opened exactly one pull
// request. Zero matches, or two benches, joins nothing.
func landingBench(e CardDone, opened []pullreq) string {
	hasID := e.PR != "" || e.Head != "" || e.Attempt != ""
	matched := map[string]struct{}{}
	for _, p := range opened {
		if hasID {
			if !sameIdentity(e, p) {
				continue
			}
		}
		matched[p.bench] = struct{}{}
	}
	if len(matched) != 1 {
		return ""
	}
	for b := range matched {
		return b
	}
	return ""
}

// sameIdentity reports whether a landing and an opened pull request are the
// same attempt. A field named on only one side is not a disagreement. A
// field named on both is, when the values differ. At least one field has
// to be shared.
func sameIdentity(e CardDone, p pullreq) bool {
	shared := false
	if e.PR != "" && p.pr != "" {
		if e.PR != p.pr {
			return false
		}
		shared = true
	}
	if e.Head != "" && p.head != "" {
		if e.Head != p.head {
			return false
		}
		shared = true
	}
	if e.Attempt != "" && p.attempt != "" {
		if e.Attempt != p.attempt {
			return false
		}
		shared = true
	}
	return shared
}

// noteUseful records that label qualified on bench this day. landed sticks:
// a later defect on the same bench does not erase the landing.
func noteUseful(on map[string]map[string]bool, label, bench string, landed bool) {
	benches := on[label]
	if benches == nil {
		benches = map[string]bool{}
		on[label] = benches
	}
	if landed {
		benches[bench] = true
	} else if _, ok := benches[bench]; !ok {
		benches[bench] = false
	}
}

// benchThatKeepsUseful is the one bench a label's useful count is added to.
// A landing keeps it, so a defect on another bench cannot move the card or
// count it twice. Several landings, or no landing, take the lexicographically
// first qualifying bench, so the count does not follow stream order.
func benchThatKeepsUseful(benches map[string]bool) string {
	var landed, any []string
	for b, wasLanding := range benches {
		any = append(any, b)
		if wasLanding {
			landed = append(landed, b)
		}
	}
	pick := any
	if len(landed) > 0 {
		pick = landed
	}
	sort.Strings(pick)
	if len(pick) == 0 {
		return ""
	}
	return pick[0]
}

func sameUTCDay(at, day time.Time) bool {
	if at.IsZero() {
		return false
	}
	ay, am, ad := at.UTC().Date()
	dy, dm, dd := day.UTC().Date()
	return ay == dy && am == dm && ad == dd
}

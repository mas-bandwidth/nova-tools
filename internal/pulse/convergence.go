package pulse

// Convergence is the health metric Glenn named on 2026-09-15 (#549, #553): contraction,
// tracked every tick, per stream. `run` writes one TICKS line per tick per stream --
// `at=<RFC3339> stream=<name> opened=<n> closed=<n>` -- and `status` folds the rolling
// two-hour window into CONVERGENCE.tsv, prints one `STATUS STREAM` line per stream, and
// prints `STATUS EXPANDING stream=<name> hours=<h>` (exit 2) when a stream's ratio --
// cards opened / cards closed -- has been above 1 for every tick in two hours.
//
// A stream is the grouping the queue already has (a queue subdirectory) or, failing that,
// the card label's prefix before the first `-`: `card-8381.md` is stream `card`. Nothing
// here calls a model, reads a report body or invents a count: an unwritten TICKS file is
// an empty window, which is CONVERGING and never a fabricated ratio.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// streamOf is the stream a card label belongs to: the prefix before the first `-`, with a
// `.md` suffix stripped. A label with no `-` is its own stream.
func streamOf(label string) string {
	label = strings.TrimSuffix(strings.TrimSpace(label), ".md")
	if i := strings.Index(label, "-"); i > 0 {
		return label[:i]
	}
	return label
}

// tickRecord is one TICKS line: one tick for one stream.
type tickRecord struct {
	at     time.Time
	stream string
	opened int
	closed int
}

// aboveOne is the per-tick verdict the two-hour alarm is built from: strictly above 1.
func (r tickRecord) aboveOne() bool { return r.opened > r.closed }

// readTicks parses <queue>/TICKS, one line per tick per stream. A malformed line is
// skipped, never counted: a record that cannot be read is not a tick.
func readTicks(queue string) []tickRecord {
	var out []tickRecord
	for _, line := range readLines(filepath.Join(queue, "TICKS")) {
		var rec tickRecord
		haveAt := false
		for _, f := range strings.Split(line, "\t") {
			k, v, ok := strings.Cut(strings.TrimSpace(f), "=")
			if !ok {
				continue
			}
			switch k {
			case "at":
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					rec.at, haveAt = t, true
				}
			case "stream":
				rec.stream = v
			case "opened":
				rec.opened, _ = strconv.Atoi(v)
			case "closed":
				rec.closed, _ = strconv.Atoi(v)
			}
		}
		if !haveAt {
			continue
		}
		if rec.stream == "" {
			rec.stream = "all"
		}
		rec.stream = streamOf(rec.stream)
		out = append(out, rec)
	}
	return out
}

// streamWindow is one stream's rolling two-hour slice and its verdict.
type streamWindow struct {
	stream              string
	opened, closed      int
	ratio               string
	aboveEvery, twoHour bool
	hours               int
}

// convergence folds the TICKS records into one window per stream. A stream is EXPANDING
// when every tick in the last two hours is above 1 and the above-one run reaches back at
// least two hours. It returns the stream windows sorted by name and the rolling window
// rows for CONVERGENCE.tsv.
func convergence(queue string, now time.Time) []streamWindow {
	windowStart := now.Add(-2 * time.Hour)
	byStream := map[string][]tickRecord{}
	for _, rec := range readTicks(queue) {
		if rec.at.After(now) {
			continue
		}
		byStream[rec.stream] = append(byStream[rec.stream], rec)
	}

	var out []streamWindow
	var windowRows []string
	for name, recs := range byStream {
		sort.Slice(recs, func(i, j int) bool { return recs[i].at.Before(recs[j].at) })
		w := streamWindow{stream: name, aboveEvery: true}
		have := false
		for _, rec := range recs {
			if rec.at.Before(windowStart) {
				continue
			}
			have = true
			w.opened += rec.opened
			w.closed += rec.closed
			if !rec.aboveOne() {
				w.aboveEvery = false
			}
			above := "no"
			if rec.aboveOne() {
				above = "yes"
			}
			windowRows = append(windowRows, fmt.Sprintf("at=%s\tstream=%s\topened=%d\tclosed=%d\tratio=%s\tabove=%s",
				rec.at.Format(time.RFC3339), onelineField(name), rec.opened, rec.closed, ratioOf(rec.opened, rec.closed), above))
		}
		if !have {
			continue
		}
		w.ratio = ratioOf(w.opened, w.closed)

		// The above-one run ending at the latest in-window tick: walk back while the
		// tick is above 1. Its span is what "two hours above 1" measures.
		runStart := recs[len(recs)-1].at
		for i := len(recs) - 1; i >= 0; i-- {
			if recs[i].at.Before(windowStart) || !recs[i].aboveOne() {
				break
			}
			runStart = recs[i].at
		}
		if w.aboveEvery && now.Sub(runStart) >= 2*time.Hour {
			w.twoHour = true
			w.hours = int(now.Sub(runStart).Hours())
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].stream < out[j].stream })

	// The rolling window: the last two hours of ticks, with their ratios, under --queue.
	sort.Strings(windowRows)
	_ = os.WriteFile(filepath.Join(queue, "CONVERGENCE.tsv"), []byte(strings.Join(windowRows, "\n")+"\n"), 0o644)
	return out
}

// ratioOf renders cards opened / cards closed. A zero denominator is a ratio of the
// opened count alone, never a divide-by-zero; both zero is an honest 0.00.
func ratioOf(opened, closed int) string {
	switch {
	case closed > 0:
		return strconv.FormatFloat(float64(opened)/float64(closed), 'f', 2, 64)
	case opened > 0:
		return strconv.FormatFloat(float64(opened), 'f', 2, 64)
	default:
		return "0.00"
	}
}

// onelineField escapes a stream name for a tab-separated record file.
func onelineField(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

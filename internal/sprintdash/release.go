package sprintdash

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The page shows the streams of one release (the owner, 2026-10-05 7:05 PM: "Please make
// sure the sprint dashboard shows only the v1.0.0 work streams."): each stream's release
// is its label (stream set --release, where --json's streams[].Release), and /api/sprint
// and /events take ?release=<name>, or all. With none asked, or one no stream carries, the
// server shows the current release: the earliest, in version order, with cards left
// (where --json's releases), or the last when none has any left. A sprint whose streams carry no label is shown
// whole, its data as where printed it. The view is a pure function of the copy (viewOf).

// AllReleases is the release that is every stream, labelled or not.
const AllReleases = "all"

// releaseView is the copy as one release shows it, with what the page's switch reads.
type releaseView struct {
	Release  string          // the release shown: a stream's label, or all
	Current  string          // the release shown when none is asked
	Releases []string        // every label a stream carries, in version order
	Streams  []string        // the streams shown, by name; nil for all
	Data     json.RawMessage // the copy, its streams the release's alone
}

// streamLabel is a stream's name and its release as where --json's streams carry them.
type streamLabel struct {
	Stream  string `json:"Stream"`
	Release string `json:"Release"`
}

// viewOf is the copy body as release shows it; a release no stream carries shows the current.
func viewOf(body json.RawMessage, release string) releaseView {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return releaseView{Release: AllReleases, Current: AllReleases, Data: body}
	}
	var labels []streamLabel
	_ = json.Unmarshal(top["streams"], &labels) // ignored: no streams is no label
	var left map[string]int64
	_ = json.Unmarshal(top["releases"], &left) // ignored: no counts is none left
	members := map[string][]string{}
	for _, l := range labels {
		if l.Release != "" && !slices.Contains(members[l.Release], l.Stream) {
			members[l.Release] = append(members[l.Release], l.Stream)
		}
	}
	v := releaseView{Release: AllReleases, Current: AllReleases, Data: body}
	for name := range members {
		v.Releases = append(v.Releases, name)
	}
	slices.SortFunc(v.Releases, compareRelease)
	if len(v.Releases) == 0 {
		return v
	}
	v.Current = currentRelease(v.Releases, left)
	v.Release = v.Current
	if release == AllReleases || members[release] != nil {
		v.Release = release
	}
	if v.Release == AllReleases {
		return v
	}
	v.Streams = slices.Sorted(slices.Values(members[v.Release]))
	v.Data = only(top, v.Streams)
	return v
}

// currentRelease is the earliest release with cards left, else the last.
func currentRelease(names []string, left map[string]int64) string {
	for _, n := range names {
		if left[n] > 0 {
			return n
		}
	}
	return names[len(names)-1]
}

// versionPart reads a release name as numbers: "v1.10.0" is 1, 10, 0.
var versionPart = regexp.MustCompile(`\d+`)

// compareRelease orders releases by version, numbers compared as numbers; a name with no
// number sorts after every version, by name.
func compareRelease(a, b string) int {
	na, nb := versionPart.FindAllString(a, -1), versionPart.FindAllString(b, -1)
	if (len(na) == 0) != (len(nb) == 0) {
		return cmp.Compare(len(nb), len(na))
	}
	for i := 0; i < len(na) && i < len(nb); i++ {
		x, _ := strconv.ParseInt(na[i], 10, 64) // ignored: \d+ parses; an overflow reads 0 on both sides
		y, _ := strconv.ParseInt(nb[i], 10, 64)
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Or(cmp.Compare(len(na), len(nb)), cmp.Compare(a, b))
}

// flow is the work table's count columns: every primary is in one.
var flow = []string{"waiting", "ready", "working", "review", "fix", "merging", "landed"}

// only is the copy with the named streams alone: the work and merge tables' rows, the
// stream clocks, costs and stalls, the critical path, the cards dealt and merging; and
// landed, all and the summary line counted over those streams' rows. Whatever names no
// stream (the fleet, the friends, the lanes) is the sprint's and stays.
func only(top map[string]json.RawMessage, streams []string) json.RawMessage {
	in := func(s string) bool { return slices.Contains(streams, s) }
	out := make(map[string]json.RawMessage, len(top))
	for k, raw := range top {
		out[k] = raw
	}
	var tables map[string]map[string]json.RawMessage
	if json.Unmarshal(top["tables"], &tables) == nil {
		for _, t := range []string{"work", "merge"} {
			for row := range tables[t] {
				if !in(row) {
					delete(tables[t], row)
				}
			}
		}
		out["tables"] = mustJSON(tables)
	}
	keepKeys(out, "stream_costs", in)
	keepList(out, "streams", "Stream", in)
	keepList(out, "merging", "stream", in)
	keepList(out, "cards", "stream", in)
	var stalled []string
	if json.Unmarshal(top["stalled"], &stalled) == nil && stalled != nil {
		out["stalled"] = mustJSON(slices.DeleteFunc(stalled, func(s string) bool { return !in(s) }))
	}
	keepCritical(out, in)

	var landed, all int64
	for _, row := range tables["work"] {
		var cells map[string]any
		_ = json.Unmarshal(row, &cells) // ignored: a row of another shape counts nothing
		for _, col := range flow {
			n := count(cells[col])
			all += n
			if col == "landed" {
				landed += n
			}
		}
	}
	var wasLanded, wasAll int64
	var summary string
	_ = json.Unmarshal(top["landed"], &wasLanded) // ignored: an absent count is zero
	_ = json.Unmarshal(top["all"], &wasAll)       // ignored: an absent count is zero
	_ = json.Unmarshal(top["summary"], &summary)  // ignored: no summary is no ETA
	out["landed"], out["all"] = mustJSON(landed), mustJSON(all)
	resetOnly(out, in)
	out["summary"] = mustJSON(releaseSummary(summary, wasAll-wasLanded, landed, all))
	// held is counted over the sprint (sentinels and holds), never per stream
	delete(out, "held")
	return mustJSON(out)
}

// resetOnly is where --json's stats_reset over the streams that pass in: its per-stream
// landed since the mark kept for those alone and its landed summed over them, so the page's
// cost per card divides the release's cost since the mark by the release's cards since it.
func resetOnly(out map[string]json.RawMessage, in func(string) bool) {
	var r map[string]json.RawMessage
	if json.Unmarshal(out["stats_reset"], &r) != nil || r == nil {
		return
	}
	var by map[string]int64
	_ = json.Unmarshal(r["streams"], &by) // ignored: none is no card landed since
	var landed int64
	kept := map[string]int64{}
	for s, n := range by {
		if in(s) {
			kept[s] = n
			landed += n
		}
	}
	r["streams"], r["landed"] = mustJSON(kept), mustJSON(landed)
	out["stats_reset"] = mustJSON(r)
}

// count is a work cell's number: the string where prints, or a number.
func count(v any) int64 {
	switch n := v.(type) {
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64) // ignored: not a number is none
		return i
	case float64:
		return int64(n)
	}
	return 0
}

// keepKeys keeps the object field's keys that pass in.
func keepKeys(out map[string]json.RawMessage, field string, in func(string) bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(out[field], &m) != nil || m == nil {
		return
	}
	for k := range m {
		if !in(k) {
			delete(m, k)
		}
	}
	out[field] = mustJSON(m)
}

// keepList keeps the list field's items whose key names a stream that passes in.
func keepList(out map[string]json.RawMessage, field, key string, in func(string) bool) {
	var items []json.RawMessage
	if json.Unmarshal(out[field], &items) != nil || items == nil {
		return
	}
	kept := []json.RawMessage{}
	for _, it := range items {
		var m map[string]any
		_ = json.Unmarshal(it, &m) // ignored: an item of another shape names no stream
		if s, _ := m[key].(string); in(s) {
			kept = append(kept, it)
		}
	}
	out[field] = mustJSON(kept)
}

// keepCritical keeps the critical path's cards of the streams that pass in: a card's
// stream is its own field (placed gives it from where's rows), else the stream of the
// dealt card or the merging card it is; a card whose stream the copy does not say is left out.
func keepCritical(out map[string]json.RawMessage, in func(string) bool) {
	var items []json.RawMessage
	if json.Unmarshal(out["critical"], &items) != nil || items == nil {
		return
	}
	streamOf := map[string]string{}
	var known []struct {
		ID      string `json:"id"`
		Primary string `json:"primary"`
		Stream  string `json:"stream"`
	}
	for _, f := range []string{"cards", "merging"} {
		known = known[:0]
		_ = json.Unmarshal(out[f], &known) // ignored: none known
		for _, k := range known {
			streamOf[cmp.Or(k.Primary, k.ID)] = k.Stream
		}
	}
	kept := []json.RawMessage{}
	for _, it := range items {
		var c struct {
			ID     string `json:"id"`
			Stream string `json:"stream"`
		}
		_ = json.Unmarshal(it, &c) // ignored: a card of another shape names no stream
		if in(cmp.Or(c.Stream, streamOf[c.ID])) {
			kept = append(kept, it)
		}
	}
	out["critical"] = mustJSON(kept)
}

// placed is the copy as the server keeps it: where's critical cards name no stream
// (sprint.CriticalCard), so each is given the stream of its row in where --json --rows,
// and the rows are dropped, the page reading none of them. A copy with no rows (a
// puller's, already placed) is kept as it came.
func placed(body []byte) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil || top["rows"] == nil {
		return append(json.RawMessage(nil), body...)
	}
	var rows []struct {
		ID     string `json:"id"`
		Stream string `json:"stream"`
	}
	_ = json.Unmarshal(top["rows"], &rows) // ignored: rows of another shape place nothing
	streamOf := make(map[string]string, len(rows))
	for _, r := range rows {
		streamOf[r.ID] = r.Stream
	}
	delete(top, "rows")
	var critical []map[string]json.RawMessage
	if json.Unmarshal(top["critical"], &critical) == nil && critical != nil {
		for _, c := range critical {
			var id string
			_ = json.Unmarshal(c["id"], &id) // ignored: a card with no id has no row
			if _, has := c["stream"]; !has && streamOf[id] != "" {
				c["stream"] = mustJSON(streamOf[id])
			}
		}
		top["critical"] = mustJSON(critical)
	}
	return mustJSON(top)
}

// etaWord reads the summary's ETA: "2d7h", "1h12m", "47m".
var etaWord = regexp.MustCompile(`ETA (?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?\s*$`)

// releaseSummary is the summary line over a release's streams, in where's words (landed /
// all, percent, the ETA): the ETA is the sprint's, which is its cards left at the sprint's
// landing rate, over the release's cards left at the same rate; a dash when the sprint's
// is one.
func releaseSummary(sprint string, sprintLeft, landed, all int64) string {
	pct := "0.0%"
	if all > 0 {
		pct = strconv.FormatFloat(100*float64(landed)/float64(all), 'f', 1, 64) + "%"
	}
	line := fmt.Sprintf("%d/%d %s", landed, all, pct)
	if all > 0 && landed == all {
		return line + " done"
	}
	m := etaWord.FindStringSubmatch(sprint)
	if m == nil || m[0] == "ETA " || sprintLeft <= 0 {
		return line + " -> ETA -"
	}
	n := func(s string) int64 { i, _ := strconv.ParseInt(s, 10, 64); return i } // ignored: "" is 0
	minutes := n(m[1])*24*60 + n(m[2])*60 + n(m[3])
	eta := int64(math.Ceil(float64(minutes) * float64(all-landed) / float64(sprintLeft)))
	switch {
	case eta >= 24*60:
		h := (eta + 59) / 60
		return fmt.Sprintf("%s -> ETA %dd%dh", line, h/24, h%24)
	case eta >= 60:
		return fmt.Sprintf("%s -> ETA %dh%dm", line, eta/60, eta%60)
	case eta > 0:
		return fmt.Sprintf("%s -> ETA %dm", line, eta)
	}
	return line + " -> ETA -"
}

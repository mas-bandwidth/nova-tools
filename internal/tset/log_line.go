package tset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Layer 2's line on the Go side (design/L2-CONTRACT.md revision 1, section 1,
// with the decisions of design/L2-COLD-READ-AND-MODEL-2026-09-29.md): the
// compact body d and the id count n that lua/table_set_log.lua stores, built
// here byte for byte from the same normalized plan, so a twin's log and the
// store's hold the same bytes. The model is tla/SetTableLog.tla (LineBound,
// OneLinePerChange, HistoryExact).

// LogLine is one stored line: its seq, the stream id <seq>-0, and its two
// fields, n and the body d.
type LogLine struct {
	Seq Decimal `json:"seq"`
	N   string  `json:"n"`
	D   string  `json:"d"`
}

// PlannedLine is one line of a step before its seq is allocated: the stored
// fields, the deduplicated about set its seq is appended to (L2 1.2), and the
// note of the step it logs (-1 for a member or topology line).
type PlannedLine struct {
	N, D  string
	About []string
	Note  int
}

// Bytes is the line's encoded size as the cap counts it: the XADD field names
// n and d, the n value and the body (L2 1.2).
func (l PlannedLine) Bytes() int { return 2 + len(l.N) + len(l.D) }

// The body's kind tags (L2 1.1).
var logTags = map[string]string{"create": "c", "move": "m", "remove": "x"}

// LogWords are the semantic words of the kind tags (L2 4).
var LogWords = map[string]string{"c": "create", "m": "move", "x": "remove", "w": "rows", "a": "advance", "n": "note"}

// LogLinesInput is what a step's lines are built from: the call's one TIME
// (L2 5), the epochs, the normalized entries of the table plan in request
// order, the request's own entries (the composed profile's about rule reads
// them), and the step's notes in order.
type LogLinesInput struct {
	NowMS        Decimal
	RequestEpoch Decimal
	WriteEpoch   Decimal
	Entries      []MemPlanEntry
	Request      []Entry
	Notes        []Note
}

// LogLines builds a step's lines as table_set_log.lua's L.plan does, before
// any seq is known: one per emitting entry in entry order (a create, move or
// remove with an effective id, a rows entry that added or deleted a row, an
// advance), then one per note. Guards, counts, rowsets and no-ops emit none.
// A member entry without about is REQUEST, a line over 2,000 ids or 1 MiB is
// LIMIT, and nothing is split or dropped (L2 1.2).
func LogLines(in LogLinesInput) ([]PlannedLine, *Refusal) {
	var lines []PlannedLine
	for ix, e := range in.Entries {
		switch e.Entry.Kind {
		case "create", "move", "remove":
			if ix >= len(in.Request) || len(in.Request[ix].About) == 0 {
				return nil, NewRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(ix), Table: e.Entry.Table})
			}
			if len(e.Entry.IDs) == 0 {
				continue
			}
			line, ref := memberLine(in.NowMS, e, ix)
			if ref != nil {
				return nil, ref
			}
			lines = append(lines, line)
		case "rows":
			if len(e.Added) == 0 && len(e.Deleted) == 0 {
				continue
			}
			lines = append(lines, rowsLine(in.NowMS, e))
		case "advance":
			lines = append(lines, PlannedLine{N: "0", Note: -1,
				D: `{"k":"a","ms":` + cjsonString(string(in.NowMS)) + `,"from":` + cjsonString(string(in.RequestEpoch)) +
					`,"to":` + cjsonString(string(in.WriteEpoch)) + `}`})
		}
	}
	for i, n := range in.Notes {
		line, ref := noteLine(in.NowMS, n, i)
		if ref != nil {
			return nil, ref
		}
		lines = append(lines, line)
	}
	for i, l := range lines {
		if size := l.Bytes(); size > MaxLineBytes {
			d := lineDetail(in.Entries, lines, i)
			d.Budget, d.Limit, d.Actual = "log_line_bytes", memInt64(MaxLineBytes), memInt64(int64(size))
			return nil, NewRefusal("LIMIT", d)
		}
	}
	return lines, nil
}

// lineDetail is the refusal detail of line i: its entry's index and table,
// or nothing for a note.
func lineDetail(entries []MemPlanEntry, lines []PlannedLine, i int) RefusalDetail {
	if lines[i].Note >= 0 {
		return RefusalDetail{}
	}
	n := 0
	for ix, e := range entries {
		if emits(e) {
			if n == i {
				d := RefusalDetail{EntryIndex: memIndex(ix)}
				if e.Entry.Kind != "advance" {
					d.Table = e.Entry.Table
				}
				return d
			}
			n++
		}
	}
	return RefusalDetail{}
}

func emits(e MemPlanEntry) bool {
	switch e.Entry.Kind {
	case "create", "move", "remove":
		return len(e.Entry.IDs) != 0
	case "rows":
		return len(e.Added) != 0 || len(e.Deleted) != 0
	case "advance":
		return true
	}
	return false
}

// memberLine is table_set_log.lua's member_line.
func memberLine(nowMS Decimal, e MemPlanEntry, ix int) (PlannedLine, *Refusal) {
	ids, about := e.Entry.IDs, e.Entry.About
	k := len(ids)
	if k > MaxIDsPerLine {
		return PlannedLine{}, NewRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(ix), Table: e.Entry.Table,
			Budget: "log_line_ids", Limit: memInt64(MaxIDsPerLine), Actual: memInt64(int64(k))})
	}
	if len(about) != k || len(e.Before) != k || len(e.After) != k {
		return PlannedLine{}, NewRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(ix), Table: e.Entry.Table})
	}
	var b strings.Builder
	b.WriteString(`{"k":` + cjsonString(logTags[e.Entry.Kind]) + `,"ms":` + cjsonString(string(nowMS)) +
		`,"tbl":` + cjsonString(e.Entry.Table))
	from := e.Entry.From
	to := e.Entry.To
	if to == "" && e.Entry.Kind != "remove" {
		to = from
	}
	if from != "" {
		b.WriteString(`,"from":` + cjsonString(from))
	}
	// A stay (a move whose destination is its source) omits to.
	if to != "" && to != from {
		b.WriteString(`,"to":` + cjsonString(to))
	}
	b.WriteString(`,"ids":` + cjsonStrings(ids) + `,"about":` + cjsonStrings(about))
	score := make([]string, k)
	rev := make([]string, k)
	for j := 0; j < k; j++ {
		after := e.Before[j].Score
		if e.Entry.Kind == "remove" {
			after = ""
		} else if len(e.Entry.Scores) == k {
			after = e.Entry.Scores[j]
		}
		score[j] = "[" + nullable(e.Before[j].Score) + "," + nullable(after) + "]"
		rev[j] = "[" + nullable(string(e.Before[j].Revision)) + "," + nullable(string(e.After[j].Revision)) + "]"
	}
	b.WriteString(`,"score":[` + strings.Join(score, ",") + `],"rev":[` + strings.Join(rev, ",") + `]`)
	// A field pair identical across every id is stored once in shared; each
	// id's set keeps only what differs. Unset names stay per id.
	sets := make([]map[string]string, k)
	unsets := make([][]string, k)
	anyUnset := false
	for j := 0; j < k; j++ {
		sets[j] = map[string]string{}
		if j < len(e.FieldChanges) {
			for f, v := range e.FieldChanges[j].Set {
				sets[j][f] = v
			}
			unsets[j] = append([]string(nil), e.FieldChanges[j].Unset...)
		}
		sort.Strings(unsets[j])
		if len(unsets[j]) != 0 {
			anyUnset = true
		}
	}
	shared := map[string]string{}
	if k > 0 {
		for f, v := range sets[0] {
			same := true
			for j := 1; j < k; j++ {
				if w, ok := sets[j][f]; !ok || w != v {
					same = false
					break
				}
			}
			if same {
				shared[f] = v
			}
		}
	}
	anySet := false
	rest := make([]map[string]string, k)
	for j := 0; j < k; j++ {
		rest[j] = map[string]string{}
		for f, v := range sets[j] {
			if _, ok := shared[f]; !ok {
				rest[j][f] = v
				anySet = true
			}
		}
	}
	if len(shared) != 0 {
		b.WriteString(`,"shared":` + cjsonObject(shared))
	}
	if anySet {
		out := make([]string, k)
		for j := range rest {
			out[j] = cjsonObject(rest[j])
		}
		b.WriteString(`,"set":[` + strings.Join(out, ",") + `]`)
	}
	if anyUnset {
		out := make([]string, k)
		for j := range unsets {
			out[j] = cjsonStrings(unsets[j])
		}
		b.WriteString(`,"unset":[` + strings.Join(out, ",") + `]`)
	}
	meta, ok, err := encodeMeta(e.Entry.Meta)
	if err != nil {
		return PlannedLine{}, NewRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(ix), Table: e.Entry.Table})
	}
	if ok {
		b.WriteString(`,"meta":` + meta)
	}
	b.WriteString("}")
	return PlannedLine{N: strconv.Itoa(k), D: b.String(), About: dedupStrings(about), Note: -1}, nil
}

// rowsLine is table_set_log.lua's rows_line: the rows added with their ranks
// and the rows deleted, in the plan's order.
func rowsLine(nowMS Decimal, e MemPlanEntry) PlannedLine {
	var b strings.Builder
	b.WriteString(`{"k":"w","ms":` + cjsonString(string(nowMS)) + `,"tbl":` + cjsonString(e.Entry.Table))
	if len(e.Added) != 0 {
		out := make([]string, len(e.Added))
		for i, a := range e.Added {
			out[i] = `{"rank":` + cjsonString(string(a.Rank)) + `,"row":` + cjsonString(a.Row) + `}`
		}
		b.WriteString(`,"add":[` + strings.Join(out, ",") + `]`)
	}
	if len(e.Deleted) != 0 {
		b.WriteString(`,"del":` + cjsonStrings(e.Deleted))
	}
	b.WriteString("}")
	return PlannedLine{N: "0", D: b.String(), Note: -1}
}

// noteLine is table_set_log.lua's note_line: n is the deduplicated about
// count, under the same 2,000 cap as ids.
func noteLine(nowMS Decimal, n Note, i int) (PlannedLine, *Refusal) {
	if n.Line.Kind != "note" {
		return PlannedLine{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	about := dedupStrings(n.About)
	if len(about) > MaxIDsPerLine {
		return PlannedLine{}, NewRefusal("LIMIT", RefusalDetail{Budget: "log_line_ids",
			Limit: memInt64(MaxIDsPerLine), Actual: memInt64(int64(len(about)))})
	}
	var b strings.Builder
	b.WriteString(`{"k":"n","ms":` + cjsonString(string(nowMS)))
	if len(about) != 0 {
		b.WriteString(`,"about":` + cjsonStrings(about))
	}
	meta, ok, err := encodeMeta(n.Line.Meta)
	if err != nil {
		return PlannedLine{}, NewRefusal("REQUEST", RefusalDetail{})
	}
	if ok {
		b.WriteString(`,"meta":` + meta)
	}
	b.WriteString("}")
	if about == nil {
		about = []string{}
	}
	return PlannedLine{N: strconv.Itoa(len(about)), D: b.String(), About: about, Note: i}, nil
}

func nullable(s string) string {
	if s == "" {
		return "null"
	}
	return cjsonString(s)
}

// dedupStrings keeps the first of equal strings, in order (L2 1.2).
func dedupStrings(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// cjsonString is a string as Redis's cjson encodes it: '"', '\' and '/' are
// escaped, the control characters are \b, \t, \n, \f, \r or \u00xx, DEL is
// \u007f, and every other byte is copied as it is.
func cjsonString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '/':
			b.WriteString(`\/`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func cjsonStrings(list []string) string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = cjsonString(s)
	}
	return "[" + strings.Join(out, ",") + "]"
}

// cjsonObject is an object of strings with its keys in byte order.
func cjsonObject(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = cjsonString(k) + ":" + cjsonString(m[k])
	}
	return "{" + strings.Join(out, ",") + "}"
}

// encodeMeta is caller meta as the log stores it: decoded, then written back
// with object keys in byte order, arrays in order, and numbers in cjson's
// fourteen-digit form (the meta exemption, L2 1.1). ok is false when the meta
// is absent, null or empty, which the line omits.
func encodeMeta(raw json.RawMessage) (string, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", false, err
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return "", false, nil
		}
	case []any:
		if len(x) == 0 {
			return "", false, nil
		}
	default:
		return "", false, nil
	}
	out, err := encodeMetaValue(v)
	return out, err == nil, err
}

func encodeMetaValue(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case string:
		return cjsonString(x), nil
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return "", err
		}
		return cjsonNumber(f), nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			s, err := encodeMetaValue(e)
			if err != nil {
				return "", err
			}
			out[i] = s
		}
		return "[" + strings.Join(out, ",") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]string, len(keys))
		for i, k := range keys {
			s, err := encodeMetaValue(x[k])
			if err != nil {
				return "", err
			}
			out[i] = cjsonString(k) + ":" + s
		}
		return "{" + strings.Join(out, ",") + "}", nil
	}
	return "", fmt.Errorf("meta value of type %T", v)
}

// cjsonNumber is a number in cjson's %.14g form.
func cjsonNumber(f float64) string {
	return strconv.FormatFloat(f, 'g', 14, 64)
}

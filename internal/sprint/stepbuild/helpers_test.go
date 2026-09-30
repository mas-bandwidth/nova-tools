package stepbuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The tests measure a step from its encoded request, decoded by
// encoding/json, and count every bound from what came back: an accounting
// independent of the builder's own, sharing only the contract's definitions.

// wireEntry is one entry of a decoded request; an absent key decodes to nil,
// a present empty one to an empty non-nil value.
type wireEntry struct {
	Kind         string              `json:"kind"`
	T            string              `json:"t"`
	From         string              `json:"from"`
	To           string              `json:"to"`
	IDs          []string            `json:"ids"`
	Scores       []string            `json:"scores"`
	Revs         []string            `json:"revs"`
	Set          map[string]string   `json:"set"`
	Each         []map[string]string `json:"each"`
	Unset        []string            `json:"unset"`
	BeforeFields []string            `json:"before_fields"`
	About        []string            `json:"about"`
	Meta         map[string]string   `json:"meta"`
	Add          []string            `json:"add"`
	Del          []string            `json:"del"`
}

type wireNote struct {
	Line struct {
		Kind string            `json:"kind"`
		Meta map[string]string `json:"meta"`
	} `json:"line"`
	About []string `json:"about"`
}

type wireRequest struct {
	Epoch   string      `json:"epoch"`
	Space   string      `json:"space"`
	Op      string      `json:"op"`
	Intent  string      `json:"intent"`
	Result  string      `json:"result"`
	Entries []wireEntry `json:"entries"`
	Notes   []wireNote  `json:"notes"`
}

// decode reads a request strictly: no unknown key, no trailing text.
func decode(t testing.TB, raw []byte) wireRequest {
	t.Helper()
	var req wireRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("the request does not decode: %v\n%.300s", err, raw)
	}
	if dec.More() {
		t.Fatalf("trailing text after the request")
	}
	return req
}

// jsonLen is the size encoding/json writes for v, HTML escaping off.
func jsonLen(t testing.TB, v any) int {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Len() - 1
}

type lineChange struct {
	Set   map[string]string `json:"set"`
	Unset []string          `json:"unset"`
}

type lineBody struct {
	Kind         string             `json:"kind"`
	Table        string             `json:"table"`
	From         string             `json:"from,omitempty"`
	To           string             `json:"to,omitempty"`
	ChangedIDs   []string           `json:"changed_ids"`
	BeforeScores []string           `json:"before_scores"`
	AfterScores  []string           `json:"after_scores"`
	BeforeRevs   []string           `json:"before_revs"`
	AfterRevs    []string           `json:"after_revs"`
	FieldChanges []lineChange       `json:"field_changes"`
	About        *[]string          `json:"about,omitempty"`
	Meta         *map[string]string `json:"meta,omitempty"`
}

type lineEnv struct {
	Seq   string `json:"seq"`
	Epoch string `json:"epoch"`
	AtMs  string `json:"at_ms"`
	Line  any    `json:"line"`
}

const uint64Max = "18446744073709551615"

// modelLine is the bytes of the generated line of a change entry under the
// package's reading of the contract (see cost.go): the event of section 1.3
// in full JSON, store-supplied values at their widest, each member's
// effective fields written out.
func modelLine(t testing.TB, e wireEntry) int {
	t.Helper()
	body := lineBody{Kind: e.Kind, Table: e.T, From: e.From, To: e.To, ChangedIDs: e.IDs}
	widest := strings.Repeat("x", 24)
	rev := strings.Repeat("9", 20)
	for i := range e.IDs {
		after := widest
		if e.Scores != nil {
			if n := jsonLen(t, e.Scores[i]) - 2; n > len(after) {
				after = strings.Repeat("x", n)
			}
		}
		body.BeforeScores = append(body.BeforeScores, widest)
		body.AfterScores = append(body.AfterScores, after)
		body.BeforeRevs = append(body.BeforeRevs, rev)
		body.AfterRevs = append(body.AfterRevs, rev)
		eff := map[string]string{}
		for k, v := range e.Set {
			eff[k] = v
		}
		if e.Each != nil {
			for k, v := range e.Each[i] {
				eff[k] = v
			}
		}
		unset := e.Unset
		if unset == nil {
			unset = []string{}
		}
		body.FieldChanges = append(body.FieldChanges, lineChange{eff, unset})
	}
	for _, p := range []*[]string{&body.BeforeScores, &body.AfterScores, &body.BeforeRevs, &body.AfterRevs} {
		if *p == nil {
			*p = []string{}
		}
	}
	if body.FieldChanges == nil {
		body.FieldChanges = []lineChange{}
	}
	if e.About != nil {
		body.About = &e.About
	}
	if e.Meta != nil {
		body.Meta = &e.Meta
	}
	return jsonLen(t, lineEnv{uint64Max, uint64Max, uint64Max, body})
}

func modelNoteLine(t testing.TB, n wireNote) int {
	t.Helper()
	meta := n.Line.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	return jsonLen(t, lineEnv{uint64Max, uint64Max, uint64Max, struct {
		Kind string            `json:"kind"`
		Meta map[string]string `json:"meta"`
	}{"note", meta}})
}

// measured is a step counted from its decoded request.
type measured struct {
	bytes, entries, tables, candidates, guardOnly, rowPairs int
	notes, about, obs, argv                                 int
	maxEntryIDs, maxLine, maxLineIDs, depth                 int
	repeats                                                 []string
}

func union(sets ...[]string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range sets {
		for _, k := range s {
			out[k] = struct{}{}
		}
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// measure counts every quantity a bound of section 6 names from the request
// bytes alone.
func measure(t testing.TB, raw []byte) measured {
	t.Helper()
	req := decode(t, raw)
	m := measured{bytes: len(raw), entries: len(req.Entries), notes: len(req.Notes), depth: depthOf(raw)}
	tables := map[string]struct{}{}
	seen := map[[2]string]struct{}{}
	rows := map[[2]string]bool{} // (table, row) -> added
	for _, e := range req.Entries {
		tables[e.T] = struct{}{}
		m.about += len(e.About)
		m.maxEntryIDs = max(m.maxEntryIDs, len(e.IDs))
		for _, id := range e.IDs {
			k := [2]string{e.T, id}
			if _, dup := seen[k]; dup {
				m.repeats = append(m.repeats, e.T+"/"+id)
			}
			seen[k] = struct{}{}
		}
		switch e.Kind {
		case "create", "move", "remove":
			m.candidates += len(e.IDs)
			m.maxLineIDs = max(m.maxLineIDs, len(e.IDs))
			line := modelLine(t, e)
			m.maxLine = max(m.maxLine, line)
			m.argv += line
			for i := range e.IDs {
				eff := map[string]string{}
				for k, v := range e.Set {
					eff[k] = v
				}
				if e.Each != nil {
					for k, v := range e.Each[i] {
						eff[k] = v
					}
				}
				for k, v := range eff {
					m.argv += len(k) + len(v)
				}
				for _, u := range e.Unset {
					m.argv += len(u)
				}
				m.obs += len(union(keys(eff), e.Unset, e.BeforeFields))
			}
		case "guard":
			m.guardOnly += len(e.IDs)
			m.obs += len(e.IDs) * len(union(e.BeforeFields))
		case "rows":
			for _, dir := range []struct {
				names []string
				add   bool
			}{{e.Add, true}, {e.Del, false}} {
				for _, r := range dir.names {
					k := [2]string{e.T, r}
					if was, in := rows[k]; in && was != dir.add {
						m.repeats = append(m.repeats, "row "+e.T+"/"+r)
					}
					rows[k] = dir.add
				}
			}
		default:
			t.Fatalf("a kind the builder never writes: %q", e.Kind)
		}
	}
	m.tables, m.rowPairs = len(tables), len(rows)
	for _, n := range req.Notes {
		m.about += len(n.About)
		m.argv += modelNoteLine(t, n)
	}
	if req.Op != "" {
		m.argv += LimitReceiptBytes
	}
	return m
}

// depthOf is the deepest nesting of arrays and objects in JSON text.
func depthOf(raw []byte) int {
	depth, deepest, inStr := 0, 0, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '[' || c == '{':
			depth++
			deepest = max(deepest, depth)
		case c == ']' || c == '}':
			depth--
		}
	}
	return deepest
}

// within is every bound of bd the measure exceeds, named; empty is inside.
func (m measured) within(bd Bounds) []string {
	var out []string
	for _, c := range []struct {
		name       string
		got, limit int
	}{
		{"request bytes", m.bytes, bd.RequestBytes},
		{"entries", m.entries, bd.Entries},
		{"tables", m.tables, bd.Tables},
		{"candidates", m.candidates, bd.Candidates},
		{"guard-only", m.guardOnly, bd.GuardOnly},
		{"entry ids", m.maxEntryIDs, bd.EntryIDs},
		{"row pairs", m.rowPairs, bd.RowPairs},
		{"notes", m.notes, bd.Notes},
		{"about ids", m.about, bd.AboutIDs},
		{"field observations", m.obs, bd.FieldObservations},
		{"line bytes", m.maxLine, bd.LineBytes},
		{"line ids", m.maxLineIDs, bd.LineIDs},
		{"planned argv", m.argv, bd.PlannedArgvBytes},
		{"nesting", m.depth, LimitNesting},
	} {
		if c.got > c.limit {
			out = append(out, fmt.Sprintf("%s %d > %d", c.name, c.got, c.limit))
		}
	}
	if len(m.repeats) > 0 {
		out = append(out, fmt.Sprintf("members named twice: %v", m.repeats))
	}
	return out
}

// hdr is a request header: the namespace member the store binding names.
var hdr = []Member{{"space", "sprint"}}

func cfg() Config { return Config{Epoch: "1", Header: hdr} }

// names is n identifiers with a prefix.
func names(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// mv is a move of the ids in a table, from a cell; the plainest change entry.
func mv(table string, ids []string) Entry {
	return Entry{Kind: KindMove, Table: table, From: "row:col", IDs: ids}
}

// gd is a guard entry of the ids.
func gd(table string, ids []string) Entry {
	return Entry{Kind: KindGuard, Table: table, From: "row:col", IDs: ids}
}

// must builds, failing the test on a refusal.
func must(t testing.TB, c Config, entries []Entry) []Step {
	t.Helper()
	steps, err := Build(c, entries)
	if err != nil {
		t.Fatalf("Build refused: %v", err)
	}
	return steps
}

// refused builds and returns the refusal, failing when there is none; the
// build returns no step with it.
func refused(t testing.TB, c Config, entries []Entry) *LimitError {
	t.Helper()
	steps, err := Build(c, entries)
	if err == nil {
		t.Fatalf("Build took an input that no step can carry: %d steps", len(steps))
	}
	if steps != nil {
		t.Fatalf("a refusal returned %d steps", len(steps))
	}
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("not a LimitError: %T %v", err, err)
	}
	return le
}

// wire is a step's entries, kind by kind, for a shape check.
func kinds(s Step) []string {
	var out []string
	for _, p := range s.Entries {
		out = append(out, string(p.Kind))
	}
	return out
}

// pad is a string of n bytes.
func pad(n int) string { return strings.Repeat("p", n) }

// countMembers is the members (ids) the steps hold in change or guard entries
// that are not attached guards.
func countMembers(steps []Step) int {
	n := 0
	for _, s := range steps {
		for _, p := range s.Entries {
			if !p.Guard {
				n += len(p.IDs)
			}
		}
	}
	return n
}

// measureLite counts the bounds that need no JSON model from the step's own
// entries, and the encoded size from its bytes: every bound but the
// generated line and the planned argv bytes. It is cheap enough to run on
// every input of the property test; measure runs on some of them, and on all
// of them under the slow tag.
func measureLite(s Step, raw []byte) measured {
	m := measured{bytes: len(raw), entries: len(s.Entries), notes: len(s.Notes)}
	tables := map[string]struct{}{}
	seen := map[[2]string]struct{}{}
	rows := map[[2]string]bool{}
	for _, p := range s.Entries {
		tables[p.Table] = struct{}{}
		m.about += len(p.About)
		m.maxEntryIDs = max(m.maxEntryIDs, len(p.IDs))
		for _, id := range p.IDs {
			k := [2]string{p.Table, id}
			if _, dup := seen[k]; dup {
				m.repeats = append(m.repeats, p.Table+"/"+id)
			}
			seen[k] = struct{}{}
		}
		switch p.Kind {
		case KindCreate, KindMove, KindRemove:
			m.candidates += len(p.IDs)
			m.maxLineIDs = max(m.maxLineIDs, len(p.IDs))
			shared := union(keys(p.Set), p.Unset, p.BeforeFields)
			for i := range p.IDs {
				m.obs += len(shared)
				if p.Each != nil {
					for k := range p.Each[i] {
						if _, in := shared[k]; !in {
							m.obs++
						}
					}
				}
			}
		case KindGuard:
			m.guardOnly += len(p.IDs)
			m.obs += len(p.IDs) * len(union(p.BeforeFields))
		case KindRows:
			for _, dir := range []struct {
				names []string
				add   bool
			}{{p.Add, true}, {p.Del, false}} {
				for _, r := range dir.names {
					k := [2]string{p.Table, r}
					if was, in := rows[k]; in && was != dir.add {
						m.repeats = append(m.repeats, "row "+p.Table+"/"+r)
					}
					rows[k] = dir.add
				}
			}
		}
	}
	for _, n := range s.Notes {
		m.about += len(n.About)
	}
	m.tables, m.rowPairs = len(tables), len(rows)
	return m
}

// withinLite is within for a lite measure: the line and argv bounds are not
// counted, so they are not checked.
func (m measured) withinLite(bd Bounds) []string {
	bd.LineBytes, bd.PlannedArgvBytes = 1<<62, 1<<62
	return m.within(bd)
}

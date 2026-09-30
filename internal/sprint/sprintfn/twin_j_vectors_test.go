package sprintfn

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The golden vectors of J: one set of inputs and expected outputs that both
// halves are held to. This file runs the Go half over them; the Lua half runs
// over the same file under gopher-lua (internal/nsprint/fn, sprint_j_lua_test.go),
// against stubs of Layer 1's S. A vector names the sprint's keys as the pre
// stage finds them, the before-state of the cards the step moves, the step's
// entries and note requests, and the seqs Layer 2 gave the lines; it expects the
// notes (what each is about and its meta), or a refusal's code, and the commands.
// With J_VECTORS_PRINT set the test prints what the Go half makes of each vector,
// one line each, which is how the expectations were first written and then read.

const (
	jVectorsPath = "testdata/j_vectors.json"
	jLuaPath     = "../../nsprint/fn/lua/sprint_j.lua"
)

type jVecKey struct {
	Hash map[string]string `json:"hash,omitempty"`
	ZSet map[string]string `json:"zset,omitempty"`
	List []string          `json:"list,omitempty"`
}

type jVecRecord struct {
	Table  string            `json:"table"`
	ID     string            `json:"id"`
	Row    string            `json:"row"`
	Col    string            `json:"col"`
	Fields map[string]string `json:"fields,omitempty"`
}

type jVecEntry struct {
	Kind  string              `json:"kind"`
	Table string              `json:"table"`
	From  string              `json:"from,omitempty"`
	To    string              `json:"to,omitempty"`
	IDs   []string            `json:"ids"`
	Set   map[string]string   `json:"set,omitempty"`
	Unset []string            `json:"unset,omitempty"`
	Each  []map[string]string `json:"each,omitempty"`
}

type jVecReq struct {
	Op        string   `json:"op"`
	Type      string   `json:"type"`
	Cause     string   `json:"cause"`
	Subjects  []string `json:"subjects"`
	Text      string   `json:"text,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Until     string   `json:"until,omitempty"`
}

type jVecNote struct {
	About []string       `json:"about"`
	Meta  map[string]any `json:"meta"`
}

type jVecExpect struct {
	Refusal  string     `json:"refusal"`
	Notes    []jVecNote `json:"notes"`
	Commands [][]string `json:"commands"`
	// Probes is how many typed reads J's pre stage makes: what JCost counts, and
	// what the Lua half's stub of Layer 1's S counts readcmd calls to be.
	Probes int `json:"probes,omitempty"`
}

type jVector struct {
	Name     string             `json:"name"`
	Epoch    string             `json:"epoch"`
	NowMS    string             `json:"now_ms"`
	Keys     map[string]jVecKey `json:"keys,omitempty"`
	Records  []jVecRecord       `json:"records,omitempty"`
	Entries  []jVecEntry        `json:"entries,omitempty"`
	Requests []jVecReq          `json:"requests"`
	NoteSeqs []string           `json:"note_seqs,omitempty"`
	Expect   *jVecExpect        `json:"expect,omitempty"`
}

func loadJVectors(t *testing.T) []jVector {
	t.Helper()
	b, err := os.ReadFile(jVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vs []jVector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatalf("%s: %v", jVectorsPath, err)
	}
	return vs
}

// build is the vector as the Go half takes it: the state, the before-state, the
// entries, the requests and the log plan.
func (v jVector) build() (*State, *Before, []tset.Entry, []NoteReq, LogPlan) {
	ks := newKeyspace()
	var seed []Cmd
	names := make([]string, 0, len(v.Keys))
	for k := range v.Keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		val := v.Keys[k]
		for f, x := range val.Hash {
			seed = append(seed, Command("HSET", k, kindHash, f, x))
		}
		for m, s := range val.ZSet {
			seed = append(seed, Command("ZADD", k, kindZSet, s, m))
		}
		if len(val.List) != 0 {
			seed = append(seed, Command("RPUSH", k, kindList, val.List...))
		}
	}
	ks.apply(seed)
	st := &State{Prefix: testPrefix, Epoch: tset.Decimal(v.Epoch), NowMS: tset.Decimal(v.NowMS), Names: testNames, Keys: &Keys{ks: ks}}
	obs := &Before{Records: map[string]map[string]tset.MemberRecord{}}
	for _, r := range v.Records {
		rec := tset.MemberRecord{ID: r.ID, Exists: true, Place: &tset.CellPlace{Row: r.Row, Col: r.Col}, Fields: map[string]tset.FieldValue{}}
		for f, x := range r.Fields {
			rec.Fields[f] = tset.FieldValue{Present: true, Value: x}
		}
		if obs.Records[r.Table] == nil {
			obs.Records[r.Table] = map[string]tset.MemberRecord{}
		}
		obs.Records[r.Table][r.ID] = rec
	}
	var entries []tset.Entry
	for _, e := range v.Entries {
		entries = append(entries, tset.Entry{Kind: e.Kind, Table: e.Table, From: e.From, To: e.To, IDs: e.IDs, Set: e.Set, Unset: e.Unset, Each: e.Each})
	}
	var reqs []NoteReq
	for _, r := range v.Requests {
		nr := NoteReq{Op: r.Op, Type: r.Type, Cause: r.Cause, Subjects: r.Subjects, Text: r.Text, Decisions: r.Decisions}
		if r.Until != "" {
			nr.Until, _ = strconv.ParseInt(r.Until, 10, 64)
		}
		reqs = append(reqs, nr)
	}
	lp := LogPlan{}
	for _, s := range v.NoteSeqs {
		lp.NoteSeqs = append(lp.NoteSeqs, tset.Decimal(s))
	}
	st.Entries = entries
	return st, obs, entries, reqs, lp
}

// runGo is what the Go half makes of a vector.
func (v jVector) runGo() (jVecExpect, error) {
	st, obs, entries, reqs, lp := v.build()
	notes, jp, ref := JDecideEntries(st, reqs, obs, entries)
	if ref != nil {
		return jVecExpect{Refusal: ref.Code}, nil
	}
	out := jVecExpect{Notes: []jVecNote{}, Commands: [][]string{}}
	for _, n := range notes {
		var meta map[string]any
		if err := json.Unmarshal(n.Line.Meta, &meta); err != nil {
			return out, err
		}
		out.Notes = append(out.Notes, jVecNote{About: n.About, Meta: meta})
	}
	for _, c := range JCmds(st, jp, lp) {
		out.Commands = append(out.Commands, c.Argv)
	}
	cost, ref := JCost(st, reqs, obs, entries)
	if ref != nil {
		return out, fmt.Errorf("JCost refused what JDecideEntries did not: %v", ref)
	}
	out.Probes = cost.Probes
	return out, nil
}

// jNormalized passes an expectation through JSON, so that what a decoder makes of
// the file and what the code makes compare as the same kinds of value.
func jNormalized(e jVecExpect) (jVecExpect, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	var out jVecExpect
	err = json.Unmarshal(b, &out)
	return out, err
}

// TestJVectorsGo: the Go half makes exactly what each vector expects: its notes
// (about ids and meta), its commands in their order, or its refusal's code.
// TestJVectorsLua holds the Lua half to the same file.
func TestJVectorsGo(t *testing.T) {
	t.Parallel()
	vs := loadJVectors(t)
	if len(vs) < 40 {
		t.Fatalf("%d vectors; want the suite of %s", len(vs), jVectorsPath)
	}
	print := os.Getenv("J_VECTORS_PRINT") != ""
	seen := map[string]bool{}
	for _, v := range vs {
		if seen[v.Name] {
			t.Fatalf("two vectors are named %q", v.Name)
		}
		seen[v.Name] = true
		got, err := v.runGo()
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		got, err = jNormalized(got)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if print {
			b, _ := json.Marshal(got)
			fmt.Printf("VEC\t%s\t%s\n", v.Name, b)
			continue
		}
		if v.Expect == nil {
			t.Fatalf("%s: no expectation", v.Name)
		}
		want, err := jNormalized(*v.Expect)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if !reflect.DeepEqual(got, want) {
			gb, _ := json.MarshalIndent(got, "", " ")
			wb, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("%s:\n got %s\nwant %s", v.Name, gb, wb)
		}
	}
}

// jLuaQuote is a string as a Lua single-quoted literal.
func jLuaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// jLuaBlock is the block of sprint_j.lua between its BEGIN and END markers as
// the Go tables say it: the bounds, the judgment types and whether the tick
// keeps each, the notice types, the prefix of a stream's subject, and the
// lateness judgment of each due kind.
func jLuaBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  SP.j_bounds = {subjects = %d, notes = %d, about = %d, name = %d, piece = %d,\n    overdue_ms = %d, digits = %d}\n",
		jSubjectsMax, jNotesMax, jAboutMax, jNameMax, jPiece, jOverdueSpanMS, jClockDigitsMax)
	var judgments []string
	for typ := range sprint.Judgments {
		judgments = append(judgments, typ)
	}
	sort.Strings(judgments)
	b.WriteString("  SP.j_judgments = {\n")
	for _, typ := range judgments {
		fmt.Fprintf(&b, "    [%s] = %v,\n", jLuaQuote(typ), sprint.Judgments[typ].TickKept)
	}
	b.WriteString("  }\n")
	var notices []string
	for typ := range sprint.Notices {
		notices = append(notices, typ)
	}
	sort.Strings(notices)
	b.WriteString("  SP.j_notices = {\n")
	for _, typ := range notices {
		fmt.Fprintf(&b, "    [%s] = true,\n", jLuaQuote(typ))
	}
	b.WriteString("  }\n")
	// The subject of a stream-level judgment is its prefix and the stream's name:
	// sprint.StreamSubject, which R11 raises the stream's lateness judgment on.
	fmt.Fprintf(&b, "  SP.j_stream_prefix = %s\n", jLuaQuote(sprint.StreamSubject("")))
	b.WriteString("  SP.j_late = {\n")
	for _, k := range sprint.DueKinds {
		typ, _, ok := LatenessJudgment(k.Kind)
		if !ok {
			continue
		}
		why := jEndReasons[k.Kind]
		var cols []string
		for col := range why.moved {
			cols = append(cols, col)
		}
		sort.Strings(cols)
		moved := make([]string, len(cols))
		for i, col := range cols {
			moved[i] = fmt.Sprintf("%s = %s", col, jLuaQuote(why.moved[col]))
		}
		fmt.Fprintf(&b, "    {kind = %s, table = %s, col = %s, field = %s, of_row = %v,\n      type = %s, moved = {%s},\n      other = %s, cleared = %s},\n",
			jLuaQuote(k.Kind), jLuaQuote(k.Table), jLuaQuote(k.Col), jLuaQuote(k.Field), k.OfRow, jLuaQuote(typ),
			strings.Join(moved, ", "), jLuaQuote(why.other), jLuaQuote(why.cleared))
	}
	b.WriteString("  }\n")
	return b.String()
}

// TestJStreamVectorsNameTheStreamSubject: the golden vectors of the stream's
// lateness close name the subject R11 raises that judgment on,
// sprint.StreamSubject, in the judgment's jopen key, the note's about and the
// close's commands; the Lua half is held to the same file, and to the prefix
// (SP.j_stream_prefix) by TestJTablesMatchLua. A vector that names the bare row
// fails here, in both halves' suites at once.
func TestJStreamVectorsNameTheStreamSubject(t *testing.T) {
	t.Parallel()
	subject := sprint.StreamSubject("s1")
	seen := 0
	for _, v := range loadJVectors(t) {
		if !strings.HasPrefix(v.Name, "lateness_stream_") {
			continue
		}
		seen++
		jopen := jk("jopen:" + subject)
		if _, ok := v.Keys[jopen]; !ok {
			t.Errorf("%s: no key %s: the judgment is not open on %s", v.Name, jopen, subject)
		}
		if v.Expect == nil || len(v.Expect.Notes) != 1 || !reflect.DeepEqual(v.Expect.Notes[0].About, []string{subject}) {
			t.Errorf("%s: the close is not about %s: %+v", v.Name, subject, v.Expect)
			continue
		}
		closed := false
		for _, c := range v.Expect.Commands {
			closed = closed || (c[0] == "HDEL" && c[1] == jopen)
		}
		if !closed {
			t.Errorf("%s: no command takes the field out of %s", v.Name, jopen)
		}
	}
	if seen != 2 {
		t.Errorf("%d stream vectors, want the two of the close", seen)
	}
}

// TestJTablesMatchLua: the block of sprint_j.lua that renders the judgment and
// notice tables, the lateness judgments and the bounds is the one the Go tables
// make, as the derivation's golden test holds sprint_defs.lua to IndexDefs: a
// change to a table is a changed block. Its failure prints the block to paste.
func TestJTablesMatchLua(t *testing.T) {
	t.Parallel()
	if os.Getenv("J_LUA_PRINT") != "" {
		fmt.Print("BLOCK-BEGIN\n" + jLuaBlock() + "BLOCK-END\n")
		return
	}
	src, err := os.ReadFile(jLuaPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	const beginMark, endMark = "  -- BEGIN generated", "  -- END generated\n"
	b, e := strings.Index(text, beginMark), strings.Index(text, endMark)
	if b < 0 || e < b {
		t.Fatal("sprint_j.lua has no generated block between its BEGIN and END markers")
	}
	// The block starts after the comment lines of the BEGIN marker.
	open := strings.Index(text[b:], "  SP.j_bounds")
	if open < 0 {
		t.Fatal("the generated block has no SP.j_bounds")
	}
	got := text[b+open : e]
	if want := jLuaBlock(); got != want {
		t.Fatalf("the generated block of sprint_j.lua is not what the Go tables make; replace it with:\n%s", want)
	}
}

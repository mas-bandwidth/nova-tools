package stepbuild

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The encoder is the size: these tests hold the size the cut works to to the
// bytes the encoder writes, and the bytes to JSON.

// asciiAndMore is every ASCII byte and a few longer runes, less the two
// control bytes encoding/json spells differently (\b and \f, six bytes here
// and two there): the alphabet on which the sizes can be held to encoding/json.
func asciiAndMore() string {
	var sb strings.Builder
	for c := 0; c < 0x80; c++ {
		if c != 0x08 && c != 0x0c {
			sb.WriteByte(byte(c))
		}
	}
	sb.WriteString("é世界😀")
	return sb.String()
}

func TestQuotedIsTheSizeOfAppendQuotedAndOfJSON(t *testing.T) {
	t.Parallel()
	all := asciiAndMore()
	cases := []string{"", "plain", `quote " and \ backslash`, "line\nbreak\r\ttab", "\x00\x01\x1f", "\x7f", "é世界😀", all}
	for _, r := range all {
		cases = append(cases, string(r))
	}
	for _, s := range cases {
		got := appendQuoted(nil, s)
		if quoted(s) != len(got) {
			t.Errorf("%q: quoted %d, written %d", s, quoted(s), len(got))
		}
		var back string
		if err := json.Unmarshal(got, &back); err != nil || back != s {
			t.Errorf("%q does not round trip: %q %v", s, got, err)
		}
		if want := jsonLen(t, s); want != len(got) {
			t.Errorf("%q: encoding/json writes %d, this writes %d", s, want, len(got))
		}
	}
	// \b and \f are written as \u0008 and \u000c: valid, six bytes each.
	var back string
	got := appendQuoted(nil, "\b\f")
	if err := json.Unmarshal(got, &back); err != nil || back != "\b\f" || len(got) != 14 || quoted("\b\f") != 14 {
		t.Errorf("\\b and \\f: %q %v", got, err)
	}
}

// rich is an input that touches every part of the encoding.
func rich() []Entry {
	create := Entry{
		Kind: KindCreate, Table: "work", To: "todo:a:b", IDs: []string{"c\"1", "c2", "c3"}, Scores: []string{"1", "2.5", "-3e2"},
		Set: map[string]string{"b": "line\nbreak", "a": "é世"}, Each: []map[string]string{{"x": "1"}, nil, {"a": "override", "y": "\t"}},
		BeforeFields: []string{"bf1", "bf2"}, About: []string{"p1", "p2", "p2"}, Meta: map[string]string{"why": "so", "and": "\\"},
		Guards: []Entry{{Kind: KindGuard, Table: "merge", From: "r:c", IDs: []string{"g1", "g2"}, Revs: []string{"7", "18446744073709551615"}, BeforeFields: []string{"f"}}},
		Notes:  []Note{{Meta: map[string]string{"n": "1"}, About: []string{"p1", "p2"}}, {}},
	}
	move := Entry{
		Kind: KindMove, Table: "work", From: "todo:a", To: "done:b", IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}, Revs: []string{"1", "2"},
		Set: map[string]string{}, Unset: []string{"u1", "u2"}, Each: []map[string]string{nil, nil}, About: []string{"p", "q"},
	}
	remove := Entry{Kind: KindRemove, Table: "merge", From: "r:c", IDs: []string{"r1"}, Unset: []string{}, Set: map[string]string{"retired": "yes"}}
	rows := Entry{Kind: KindRows, Table: "work", Add: []string{"new:row", "r\"2"}, Del: []string{"old"}}
	return []Entry{create, move, remove, rows, gd("fleet", []string{"z"}), {Kind: KindNote, Notes: []Note{{About: []string{"x"}}}}}
}

func TestEveryStepEncodesToItsBytesAsJSON(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Ident = func(part int) Ident { return Ident{Op: fmt.Sprint("op", part), Intent: "int\"ent", Result: "res\nult"} }
	steps := must(t, c, rich())
	if len(steps) != 1 {
		t.Fatalf("%d steps", len(steps))
	}
	s := steps[0]
	raw := s.Encode()
	if len(raw) != s.Bytes {
		t.Fatalf("Bytes %d, encoded %d", s.Bytes, len(raw))
	}
	req := decode(t, raw)
	if req.Epoch != "1" || req.Space != "sprint" || req.Op != "op1" || req.Intent != "int\"ent" || req.Result != "res\nult" {
		t.Fatalf("header: %+v", req)
	}
	// Every wire entry decodes to the entry it was made from.
	if len(req.Entries) != len(s.Entries) {
		t.Fatalf("%d entries, %d placed", len(req.Entries), len(s.Entries))
	}
	for i, p := range s.Entries {
		w := req.Entries[i]
		got := Entry{Kind: Kind(w.Kind), Table: w.T, From: w.From, To: w.To, IDs: w.IDs, Scores: w.Scores, Revs: w.Revs, Each: w.Each, About: w.About,
			Set: w.Set, Unset: w.Unset, BeforeFields: w.BeforeFields, Meta: w.Meta, Add: w.Add, Del: w.Del}
		want := p.Entry
		want.Guards, want.Notes = nil, nil
		if !reflect.DeepEqual(got, normalize(want)) {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, got, normalize(want))
		}
	}
	if len(req.Notes) != len(s.Notes) {
		t.Fatalf("notes: %d, %d", len(req.Notes), len(s.Notes))
	}
	for i, n := range s.Notes {
		w := req.Notes[i]
		wantMeta := n.Meta
		if wantMeta == nil {
			wantMeta = map[string]string{}
		}
		wantAbout := n.About
		if wantAbout == nil {
			wantAbout = []string{}
		}
		if w.Line.Kind != "note" || !reflect.DeepEqual(w.Line.Meta, wantMeta) || !reflect.DeepEqual(w.About, wantAbout) {
			t.Errorf("note %d: %+v", i, w)
		}
	}
}

// normalize is what an entry becomes on the wire and back: the member arrays
// present-empty where the encoding writes [], and the entries' own empty
// values as they are.
func normalize(e Entry) Entry {
	if e.Kind == KindRows {
		if len(e.Add) == 0 {
			e.Add = nil
		}
		if len(e.Del) == 0 {
			e.Del = nil
		}
		return e
	}
	for i, m := range e.Each {
		if m == nil {
			e.Each[i] = map[string]string{}
		}
	}
	return e
}

func TestBytesAreTheEncodedLengthOfEveryStepOfEveryShape(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 2, 3, 50, 2001, 4100} {
		big := mv("work", names("m", n))
		big.Each = make([]map[string]string, n)
		big.About = make([]string, n)
		for i := range big.Each {
			big.Each[i] = map[string]string{fmt.Sprintf("k%d", i%5): "v\"" + fmt.Sprint(i) + "\n"}
			big.About[i] = fmt.Sprintf("p%d", i%9)
		}
		big.Set = map[string]string{"s": "é\t"}
		big.Guards = []Entry{gd("merge", names("g", 3))}
		big.Notes = []Note{{Meta: map[string]string{"n": "\\"}, About: []string{"p1"}}}
		entries := append([]Entry{big}, cloneAll(rich(), "-tail")...)
		for _, s := range must(t, cfg(), entries) {
			if got := len(s.Encode()); got != s.Bytes {
				t.Fatalf("n=%d step %d: Bytes %d, encoded %d", n, s.Part, s.Bytes, got)
			}
		}
	}
}

// cloneAll is entries with every member name suffixed, so they name members
// no other entry names.
func cloneAll(in []Entry, suffix string) []Entry {
	out := make([]Entry, len(in))
	for i, e := range in {
		out[i] = cloneWithSuffix(e, suffix)
	}
	return out
}

// cloneWithSuffix is an entry with every member name suffixed.
func cloneWithSuffix(e Entry, suffix string) Entry {
	ids := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = s + suffix
		}
		return out
	}
	e.IDs = ids(e.IDs)
	e.Add, e.Del = ids(e.Add), ids(e.Del)
	guards := make([]Entry, len(e.Guards))
	for i, g := range e.Guards {
		g.IDs = ids(g.IDs)
		guards[i] = g
	}
	e.Guards = guards
	return e
}

func TestObjectKeysAreWrittenInByteOrder(t *testing.T) {
	t.Parallel()
	e := mv("work", []string{"a"})
	e.Set = map[string]string{}
	for i := 0; i < 60; i++ {
		e.Set[fmt.Sprintf("k%02d", (i*37)%60)] = fmt.Sprint(i)
	}
	first := must(t, cfg(), []Entry{e})[0].Encode()
	for i := 0; i < 20; i++ {
		if !bytes.Equal(first, must(t, cfg(), []Entry{e})[0].Encode()) {
			t.Fatalf("run %d encodes differently", i)
		}
	}
	if k0, k1 := bytes.Index(first, []byte(`"k00"`)), bytes.Index(first, []byte(`"k01"`)); k0 < 0 || k1 < k0 {
		t.Fatalf("keys are not in byte order: %.200s", first)
	}
}

func TestObjectAndArrayBytesAreTheEncodersSizes(t *testing.T) {
	t.Parallel()
	for _, m := range []map[string]string{nil, {}, {"a": "b"}, {"a": "b", "c\"": "d\n", "é": ""}} {
		var w buffer
		emitObject(&w, m)
		if objectBytes(m) != len(w.b) {
			t.Errorf("%v: objectBytes %d, written %d", m, objectBytes(m), len(w.b))
		}
	}
	for _, ss := range [][]string{nil, {}, {"a"}, {"a", "b\"", "é"}} {
		var w buffer
		emitStrings(&w, ss)
		if arrayBytes(ss) != len(w.b) {
			t.Errorf("%v: arrayBytes %d, written %d", ss, arrayBytes(ss), len(w.b))
		}
	}
}

// The line model is held to encoding/json's own count of the same event: the
// builder's incremental line size against a marshalled line, per entry.
func TestTheLineModelIsTheSizeOfTheEventWrittenOut(t *testing.T) {
	t.Parallel()
	for i, e := range rich()[:3] {
		steps := must(t, cfg(), []Entry{e})
		b, _ := newBuilder(cfg())
		es, err := b.costEntry(0, &e)
		if err != nil {
			t.Fatal(err)
		}
		if es.class != classChange {
			continue
		}
		got := es.lineHead
		for j := 0; j < es.n; j++ {
			got += es.cost[j].line
			if j > 0 {
				got += es.lineArrays
			}
		}
		var placed *Placed
		for k := range steps[0].Entries {
			if !steps[0].Entries[k].Guard {
				placed = &steps[0].Entries[k]
			}
		}
		req := decode(t, steps[0].Encode())
		var w wireEntry
		for k := range req.Entries {
			if req.Entries[k].Kind == string(placed.Kind) {
				w = req.Entries[k]
			}
		}
		if want := modelLine(t, w); got != want {
			t.Errorf("entry %d (%s): the line is %d bytes by the builder, %d by encoding/json", i, e.Kind, got, want)
		}
	}
}

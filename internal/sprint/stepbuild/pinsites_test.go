package stepbuild

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The builder sizes a request and an entry at a fixed set of places, and each
// place is a call of one of four helpers (encode.go): quoted, objectBytes,
// arrayBytes and count. A place that sized its string raw would be right for
// every plain name and wrong for the ones cjson spells wider, and a fix of one
// place leaves the next one as it was. So the calls are counted from the source with
// go/ast, named in sizingSitePins, and each is held by a request built to sit
// exactly at the cut, as the real encoding (the cjson twin of cjson_test.go, and
// the strict counters of helpers_test.go) measures it: a bound set to exactly
// that size leaves the step whole, and a bound one byte under cuts it (or
// refuses it, where the piece cannot be cut: a note). A call added to the
// package that the table does not name fails TestStepBuildSizingSitesAllPinned,
// and so does a name in the table that the source no longer has.

// sizingHelpers are the functions that size a string, an object, an array or the
// output of an emitter. Every place the builder estimates the size of a request
// or of an entry is a call of one of them.
var sizingHelpers = map[string]bool{"quoted": true, "objectBytes": true, "arrayBytes": true, "count": true}

// sizingCallSites walks every non-test file of the package and names each call
// of a sizing helper as "<file> <function>: <helper>#<n>", n the call's order
// among that function's calls of that helper, in source order.
func sizingCallSites(t testing.TB) []string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, de := range files {
		name := de.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				rt := fd.Recv.List[0].Type
				if st, ok := rt.(*ast.StarExpr); ok {
					rt = st.X
				}
				if id, ok := rt.(*ast.Ident); ok {
					fn = id.Name + "." + fn
				}
			}
			n := map[string]int{}
			ast.Inspect(fd.Body, func(x ast.Node) bool {
				call, ok := x.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && sizingHelpers[id.Name] {
					n[id.Name]++
					out = append(out, name+" "+fn+": "+id.Name+"#"+strconv.Itoa(n[id.Name]))
				}
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}

// pinVariant is one input that puts a string at a sizing site: build makes the
// config and entries around the string s, so that a step of two members (or two
// rows, or two notes) has s at the site, and bytes are the strings s is made of
// in turn (nil is the four bytes cjson spells wider, and the plain one). A byte
// the builder refuses at the site is left out with without.
type pinVariant struct {
	name    string
	bytes   []string
	without string
	// requestOnly is a variant the strict measure cannot read, because it decodes
	// a request strictly and knows one header key (the request bound alone is
	// held, by the cjson twin).
	requestOnly bool
	build       func(s string) (Config, []Entry)
}

// pinSite is a call of a sizing helper and the inputs that pin it. Some calls
// are pinned by the inputs of another that reaches them (encode.go's own calls
// are reached through the sites that call objectBytes, arrayBytes and count),
// which is said in via.
type pinSite struct {
	site     string
	via      string
	variants []pinVariant
}

// plainAndWider is every string a variant is tried with unless it says: the
// plain one and each of the bytes of widerBytes (sites_test.go), each made
// into a name that is far from its length as cjson writes it.
func plainAndWider() []string {
	out := []string{"p"}
	for _, w := range widerBytes {
		out = append(out, w.bytes)
	}
	return out
}

func two(s string) []string { return []string{s + "1", s + "2"} }

func inCfg(entries ...Entry) (Config, []Entry) { return cfg(), entries }

// fn is a variant of a plain config whose one entry is made from s.
func fn(name string, mk func(s string) Entry) pinVariant {
	return pinVariant{name: name, build: func(s string) (Config, []Entry) { return inCfg(mk(s)) }}
}

// The variants, each written once and named by the sites that use it.
var (
	vKind = []pinVariant{
		{name: "a create", bytes: []string{"p"}, build: func(string) (Config, []Entry) {
			return inCfg(Entry{Kind: KindCreate, Table: "work", To: "row:col", IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}})
		}},
		{name: "a move", bytes: []string{"p"}, build: func(string) (Config, []Entry) { return inCfg(mv("work", []string{"m1", "m2"})) }},
		{name: "a remove", bytes: []string{"p"}, build: func(string) (Config, []Entry) {
			return inCfg(Entry{Kind: KindRemove, Table: "work", From: "row:col", IDs: []string{"m1", "m2"}})
		}},
	}
	vTable = []pinVariant{
		fn("the table of a change entry", func(s string) Entry { return mv(s, []string{"m1", "m2"}) }),
		fn("the table of a create entry", func(s string) Entry {
			return Entry{Kind: KindCreate, Table: s, To: "row:col", IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}}
		}),
		fn("the table of a guard entry", func(s string) Entry { return gd(s, []string{"m1", "m2"}) }),
	}
	vRowsTable = []pinVariant{
		fn("the table of a rows entry", func(s string) Entry { return Entry{Kind: KindRows, Table: s, Add: []string{"r1", "r2"}} }),
	}
	vFrom = []pinVariant{
		fn("the row of a from cell", func(s string) Entry { e := mv("work", []string{"m1", "m2"}); e.From = s + ":col"; return e }),
		fn("the column of a from cell", func(s string) Entry { e := mv("work", []string{"m1", "m2"}); e.From = "row:" + s; return e }),
	}
	vTo = []pinVariant{
		fn("the row of a to cell", func(s string) Entry {
			return Entry{Kind: KindCreate, Table: "work", To: s + ":col", IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}}
		}),
		fn("the column of a to cell", func(s string) Entry {
			return Entry{Kind: KindCreate, Table: "work", To: "row:" + s, IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}}
		}),
	}
	vMeta = []pinVariant{
		fn("a key of an entry's meta", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Meta = map[string]string{s: "v"}
			return e
		}),
		fn("a value of an entry's meta", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Meta = map[string]string{"k": s}
			return e
		}),
	}
	vSet = []pinVariant{
		fn("a key of the shared set", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Set = map[string]string{s: "v"}
			return e
		}),
		fn("a value of the shared set", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Set = map[string]string{"k": s}
			return e
		}),
	}
	vUnset = []pinVariant{
		fn("the unset names", func(s string) Entry { e := mv("work", []string{"m1", "m2"}); e.Unset = two(s); return e }),
	}
	vBefore = []pinVariant{
		fn("the before_fields names", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.BeforeFields = two(s)
			return e
		}),
	}
	vID = []pinVariant{
		fn("the members' IDs", func(s string) Entry { return mv("work", two(s)) }),
	}
	vScore = []pinVariant{
		fn("the members' scores", func(s string) Entry {
			return Entry{Kind: KindCreate, Table: "work", To: "row:col", IDs: []string{"m1", "m2"}, Scores: []string{s, s + "2"}}
		}),
	}
	vRev = []pinVariant{
		{name: "the members' revisions", bytes: []string{"p"}, build: func(string) (Config, []Entry) {
			e := mv("work", []string{"m1", "m2"})
			e.Revs = []string{"18446744073709551615", "1844674407370955161"} // a revision is a decimal: digits are all it is made of
			return inCfg(e)
		}},
	}
	vAbout = []pinVariant{
		fn("the members' about IDs", func(s string) Entry { e := mv("work", []string{"m1", "m2"}); e.About = two(s); return e }),
	}
	vEach = []pinVariant{
		fn("a key of a member's own fields", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Each = []map[string]string{{s: "v"}, {s: "w"}}
			return e
		}),
		fn("a value of a member's own fields", func(s string) Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Each = []map[string]string{{"k": s}, {"k": s + "2"}}
			return e
		}),
	}
	// A row name with a control character is refused, DEL among them (costRows).
	vRowAdd = []pinVariant{{
		name: "a row added", without: "\x7f",
		build: func(s string) (Config, []Entry) {
			return inCfg(Entry{Kind: KindRows, Table: "work", Add: two(s)})
		},
	}}
	vRowDel = []pinVariant{{
		name: "a row deleted", without: "\x7f",
		build: func(s string) (Config, []Entry) {
			return inCfg(Entry{Kind: KindRows, Table: "work", Del: two(s)})
		},
	}}
	// A note is one wire member of a notes entry that puts no entry on the
	// wire; two of them, so that a request bound one byte under cuts them apart
	// and a line bound one byte under refuses (a note is not cut).
	vNote = []pinVariant{
		fn("a key of a note's meta", func(s string) Entry {
			return Entry{Kind: KindNote, Notes: []Note{{Meta: map[string]string{s: "v"}, About: []string{"p1"}}, {Meta: map[string]string{s: "w"}, About: []string{"p2"}}}}
		}),
		fn("a value of a note's meta", func(s string) Entry {
			return Entry{Kind: KindNote, Notes: []Note{{Meta: map[string]string{"k": s}, About: []string{"p1"}}, {Meta: map[string]string{"k": s + "2"}, About: []string{"p2"}}}}
		}),
		fn("the IDs a note is about", func(s string) Entry {
			return Entry{Kind: KindNote, Notes: []Note{{Meta: map[string]string{"k": "v"}, About: two(s)}, {Meta: map[string]string{"k": "w"}, About: two(s + "3")}}}
		}),
	}
	// The request's header: the epoch, the members every step has, and the
	// identity, which the request repeats in every step.
	vHeader = []pinVariant{
		{name: "a header value", build: func(s string) (Config, []Entry) {
			c := cfg()
			c.Header = []Member{{"space", s}}
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
		{name: "a header key", requestOnly: true, build: func(s string) (Config, []Entry) {
			c := cfg()
			c.Header = []Member{{s, "sprint"}}
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
		{name: "the epoch", bytes: []string{"p"}, build: func(string) (Config, []Entry) {
			c := cfg()
			c.Epoch = "18446744073709551615" // an epoch is a decimal: digits are all it is made of
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
		{name: "an op", build: func(s string) (Config, []Entry) {
			c := cfg()
			c.Ident = func(part int) Ident { return Ident{Op: s + strconv.Itoa(part), Intent: "i"} }
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
		{name: "an intent", build: func(s string) (Config, []Entry) {
			c := cfg()
			c.Ident = func(part int) Ident { return Ident{Op: "op" + strconv.Itoa(part), Intent: s} }
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
		{name: "a result", build: func(s string) (Config, []Entry) {
			c := cfg()
			c.Ident = func(part int) Ident { return Ident{Result: s} }
			return c, []Entry{mv("work", []string{"m1", "m2"})}
		}},
	}
)

func cat(vs ...[]pinVariant) []pinVariant {
	var out []pinVariant
	for _, v := range vs {
		out = append(out, v...)
	}
	return out
}

// sizingSitePins is every call of a sizing helper in the package, by the name
// sizingCallSites gives it, and what pins it. A call that is a place the input's
// own string is sized at has a variant that makes the string at that place, of
// the bytes cjson spells wider and of plain ones.
var sizingSitePins = []pinSite{
	// cost.go: the fixed part of a generated line and of a wire entry.
	{site: "cost.go rowsLineHead: quoted#1", variants: vRowsTable},
	{site: "cost.go lineHead: quoted#1", variants: vKind},
	{site: "cost.go lineHead: quoted#2", variants: vTable[:2]},
	{site: "cost.go lineHead: quoted#3", variants: vFrom},
	{site: "cost.go lineHead: quoted#4", variants: vTo},
	{site: "cost.go lineHead: objectBytes#1", variants: vMeta},
	{site: "cost.go builder.costMembers: count#1", variants: cat(vTable, vFrom, vTo, vMeta, vSet, vUnset, vBefore)},

	// cost.go: what an entry shares among its members.
	{site: "cost.go builder.costShared: quoted#1", variants: vSet[1:]},
	{site: "cost.go builder.costShared: quoted#2", variants: vSet[:1]},
	{site: "cost.go builder.costShared: arrayBytes#1", variants: vUnset},

	// cost.go: each member's items.
	{site: "cost.go builder.costMember: quoted#1", variants: vID},
	{site: "cost.go builder.costMember: quoted#2", variants: vScore},
	{site: "cost.go builder.costMember: quoted#3", variants: vRev},
	{site: "cost.go builder.costMember: quoted#4", variants: vAbout},
	{site: "cost.go builder.effective: quoted#1", variants: vEach[:1]},
	{site: "cost.go builder.effective: quoted#2", variants: vEach[1:]},

	// cost.go: a rows entry, and a note.
	{site: "cost.go builder.costRows: quoted#1", variants: vRowAdd},
	{site: "cost.go builder.costRows: quoted#2", variants: vRowDel},
	{site: "cost.go builder.costRows: quoted#3", variants: cat(vRowAdd, vRowDel)},
	{site: "cost.go builder.costRows: count#1", variants: vRowsTable},
	{site: "cost.go builder.costNote: objectBytes#1", variants: vNote[:2]},
	{site: "cost.go builder.costNote: arrayBytes#1", variants: vNote[2:]},
	{site: "cost.go builder.costNote: count#1", variants: vNote},

	// place.go: the header and identity a step opens with.
	{site: "place.go builder.newStep: count#1", variants: vHeader},

	// encode.go: the sizes themselves, reached through the sites above.
	{site: "encode.go counter.str: quoted#1", via: "every count site", variants: cat(vHeader, vNote, vTable, vMeta)},
	{site: "encode.go objectBytes: quoted#1", via: "the objectBytes sites (a key)", variants: cat(vMeta[:1], vNote[:1])},
	{site: "encode.go objectBytes: quoted#2", via: "the objectBytes sites (a value)", variants: cat(vMeta[1:], vNote[1:2])},
	{site: "encode.go arrayBytes: quoted#1", via: "the arrayBytes sites", variants: cat(vUnset, vNote[2:])},
}

func TestStepBuildSizingSitesAllPinned(t *testing.T) {
	t.Parallel()
	found := sizingCallSites(t)
	var named []string
	for _, p := range sizingSitePins {
		named = append(named, p.site)
	}
	sort.Strings(named)
	if strings.Join(found, "\n") != strings.Join(named, "\n") {
		in := func(list []string, s string) bool {
			for _, x := range list {
				if x == s {
					return true
				}
			}
			return false
		}
		for _, s := range found {
			if !in(named, s) {
				t.Errorf("the package sizes at %s (go/ast finds %d calls of %v), and sizingSitePins does not name it (names %d): add it, with an input that puts a string at it", s, len(found), sortedKeys(sizingHelpers), len(named))
			}
		}
		for _, s := range named {
			if !in(found, s) {
				t.Errorf("sizingSitePins names %s, and the package has no such call (go/ast finds %d, the table names %d)", s, len(found), len(named))
			}
		}
		t.FailNow()
	}
	for _, p := range sizingSitePins {
		t.Run(p.site, func(t *testing.T) {
			t.Parallel()
			for _, v := range p.variants {
				strs := v.bytes
				if strs == nil {
					strs = plainAndWider()
				}
				for _, b := range strs {
					if b == v.without {
						continue
					}
					c, entries := v.build(widerRun(b))
					pinAtTheCut(t, v.name+" of "+strconv.Quote(b), c, entries, v.requestOnly)
				}
			}
		})
	}
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// shapeOf is what the steps hold, entry by entry and note by note.
func shapeOf(steps []Step) [3]int {
	var s [3]int
	s[0] = len(steps)
	for _, st := range steps {
		s[1] += len(st.Entries)
		s[2] += len(st.Notes)
	}
	return s
}

// pinAtTheCut builds the entries at the contract's bounds into the one step
// they make, measures that step from its encoded request by the strict counters
// (the cjson twin's size of the request, the model of the line, the planned argv
// count), and holds the builder to each: a bound of exactly that size leaves the
// step whole, and a bound of one byte less does not (the step is cut, or refused
// when it cannot be).
func pinAtTheCut(t *testing.T, what string, c Config, entries []Entry, requestOnly bool) {
	t.Helper()
	whole := c
	whole.Bounds = Contract()
	steps, err := Build(whole, entries)
	if err != nil || len(steps) != 1 {
		t.Errorf("%s: %d steps, %v", what, len(steps), err)
		return
	}
	want := shapeOf(steps)
	raw := steps[0].Encode()
	tree := cjsonTree(t, raw)
	if scan := cjsonReEncode(t, raw); len(raw) != steps[0].Bytes || tree != steps[0].Bytes || scan != steps[0].Bytes {
		t.Errorf("%s: the builder counted %d bytes, %d were written, cjson's tree counts %d and its scan %d", what, steps[0].Bytes, len(raw), tree, scan)
	}
	var m measured
	if requestOnly {
		m = measured{}
	} else {
		m = measure(t, raw)
		if bad := m.within(Contract()); len(bad) != 0 {
			t.Errorf("%s: %v", what, bad)
		}
	}
	for _, b := range []struct {
		bound string
		size  int // what the strict counters say
		set   func(bd *Bounds, n int)
	}{
		{"request bytes", tree, func(bd *Bounds, n int) { bd.RequestBytes = n }},
		{"line bytes", m.maxLine, func(bd *Bounds, n int) { bd.LineBytes = n }},
		{"planned argv bytes with the margin", charged(m.argv), func(bd *Bounds, n int) { bd.PlannedArgvBytes = n }},
	} {
		if b.size == 0 {
			continue // no line: a rows entry's topology line is not bounded by the line bound
		}
		at, under := Contract(), Contract()
		b.set(&at, b.size)
		b.set(&under, b.size-1)
		cat, cunder := c, c
		cat.Bounds, cunder.Bounds = at, under
		if got, err := Build(cat, entries); err != nil || shapeOf(got) != want {
			t.Errorf("%s: a %s bound of exactly the strict count, %d, does not leave the step whole: %d steps, %v: the builder counts more than the real encoding", what, b.bound, b.size, len(got), err)
		}
		got, err := Build(cunder, entries)
		var le *LimitError
		switch {
		case err != nil && !errors.As(err, &le):
			t.Errorf("%s: a %s bound of the strict count less one, %d, is refused as %v, not as a limit", what, b.bound, b.size-1, err)
		case err == nil && shapeOf(got) == want:
			t.Errorf("%s: a %s bound of the strict count less one, %d, does not cut the step: %d steps: the builder counts less than the real encoding", what, b.bound, b.size-1, len(got))
		}
	}
	if m.strict > m.argv {
		t.Errorf("%s: the strict planned argv count is %d, the model %d", what, m.strict, m.argv)
	}
}

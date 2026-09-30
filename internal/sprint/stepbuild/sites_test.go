package stepbuild

import (
	"errors"
	"strings"
	"testing"
)

// The builder sizes a string that goes into JSON at a great many places, and
// each is one call of quoted. A call that sized its string raw (len + 2) would be
// wrong for exactly the strings cjson spells wider, the slash, DEL, the quote
// and the backslash among them, and would be right for every plain name, which
// is what most tests are made of. So each place the input can put a string that
// no other place has already pinned gets a test of its own: the string at that
// place is made of the four bytes in turn, and the step is held to the strict
// counter written apart from the builder (cjson_test.go, helpers_test.go).
//
// The places, by the name of the function and the line the sizing is in:
//
//	S01 rowsLineHead  the table in the topology line of a rows entry
//	S02 lineHead      the table in the line of a change entry
//	S07 costShared    a key of the set an entry shares among its members
//	S10 costMember    a member's score
//	S11 costMember    a member's about ID
//	S13 costRows      a row a rows entry adds, in its topology line
//	S14 costRows      a row a rows entry deletes, in its topology line

// widerBytes are the bytes cjson spells in more than one byte and that a name
// can hold, each on its own, so that a mistake about one is not hidden by the
// others: a slash and the quote and the backslash are two bytes, DEL is six.
var widerBytes = []struct{ name, bytes string }{
	{"a slash", "/"},
	{"DEL", "\x7f"},
	{"a quote", `"`},
	{"a backslash", `\`},
}

// widerRun is n of b between two plain letters, so that the string is a name
// (not empty, UTF-8, well inside 256 bytes) whose size as cjson writes it is far
// from its length: each of the 20 costs one or five bytes more.
func widerRun(b string) string { return "a" + strings.Repeat(b, 20) + "z" }

// sizingSites are the places above: the input that puts a string s at the
// place, of two members or two rows so that a bound one byte under the exact
// size cuts it, and whether the builder takes b there.
var sizingSites = []struct {
	name    string
	entries func(s string) []Entry
	refuses func(b string) string // the field the builder refuses b in, or empty
}{
	{
		"S01 rowsLineHead: the table of a rows entry",
		func(s string) []Entry { return []Entry{{Kind: KindRows, Table: s, Add: []string{"r1", "r2"}}} },
		nil,
	},
	{
		"S02 lineHead: the table of a change entry",
		func(s string) []Entry { return []Entry{mv(s, []string{"m1", "m2"})} },
		nil,
	},
	{
		"S07 costShared: a key of the shared set",
		func(s string) []Entry {
			e := mv("work", []string{"m1", "m2"})
			e.Set = map[string]string{s: "v"}
			return []Entry{e}
		},
		nil,
	},
	{
		// The builder takes any non-empty UTF-8 for a score and Layer 1 refuses
		// what is not a number (question 7), so the four are legal here. It is a
		// score of a length a store's is not, wide enough that the line carries
		// it as its own width (a store's widest is 24 bytes).
		"S10 costMember: a score",
		func(s string) []Entry {
			return []Entry{{Kind: KindCreate, Table: "work", To: "row:col", IDs: []string{"m1", "m2"}, Scores: []string{s, s + "2"}}}
		},
		nil,
	},
	{
		"S11 costMember: an about ID",
		func(s string) []Entry {
			e := mv("work", []string{"m1", "m2"})
			e.About = []string{s, s + "2"}
			return []Entry{e}
		},
		nil,
	},
	{
		// A row name is refused when it has a control character, DEL among them
		// (costRows): the three others are legal.
		"S13 costRows: a row added",
		func(s string) []Entry {
			return []Entry{{Kind: KindRows, Table: "work", Add: []string{s + "1", s + "2"}}}
		},
		func(b string) string {
			if b == "\x7f" {
				return "add"
			}
			return ""
		},
	},
	{
		"S14 costRows: a row deleted",
		func(s string) []Entry {
			return []Entry{{Kind: KindRows, Table: "work", Del: []string{s + "1", s + "2"}}}
		},
		func(b string) string {
			if b == "\x7f" {
				return "del"
			}
			return ""
		},
	},
}

func TestEverySizingSiteCountsWhatCJSONWrites(t *testing.T) {
	t.Parallel()
	for _, site := range sizingSites {
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			for _, w := range widerBytes {
				entries := site.entries(widerRun(w.bytes))
				if site.refuses != nil {
					if field := site.refuses(w.bytes); field != "" {
						checkRowNameIsRefused(t, w.name, field, entries)
						continue
					}
				}
				checkSizedAtCJSONWidth(t, w.name, entries)
			}
		})
	}
}

// checkRowNameIsRefused holds a row name of bytes the builder does not take to
// its refusal, naming the field.
func checkRowNameIsRefused(t *testing.T, what, field string, entries []Entry) {
	t.Helper()
	steps, err := Build(cfg(), entries)
	var ie *InputError
	if !errors.As(err, &ie) || steps != nil || ie.Field != field || !strings.Contains(ie.Reason, "control character") {
		t.Errorf("%s in a row name: %d steps, %v: want a refusal of field %s for a control character", what, len(steps), err, field)
	}
}

// checkSizedAtCJSONWidth builds entries into the one step they make at the
// contract's bounds and holds the step to the strict counter: the request is the
// size the builder counted, sent and as cjson encodes it again; every bound of
// the contract is kept, counted from the request; and each bound the input's
// strings are counted against (the request, the line, the planned argv bytes) is
// set to exactly what the strict counter says the step is, which leaves the
// step whole, and to one byte less, which cuts it. A builder that counted a
// string short is the second; one that counted it long is the first.
func checkSizedAtCJSONWidth(t *testing.T, what string, entries []Entry) {
	t.Helper()
	steps, err := Build(cfgAt(Contract()), entries)
	if err != nil || len(steps) != 1 {
		t.Errorf("%s: %d steps, %v", what, len(steps), err)
		return
	}
	s := steps[0]
	raw := s.Encode()
	if tree, scan := cjsonTree(t, raw), cjsonReEncode(t, raw); len(raw) != s.Bytes || tree != s.Bytes || scan != s.Bytes {
		t.Errorf("%s: the builder counted %d bytes, %d were written, cjson's tree counts %d and its scan %d", what, s.Bytes, len(raw), tree, scan)
	}
	m := measure(t, raw)
	if bad := m.within(Contract()); len(bad) != 0 {
		t.Errorf("%s: %v", what, bad)
	}
	wire := len(s.Entries)
	same := func(got []Step) bool { return len(got) == 1 && len(got[0].Entries) == wire }
	for _, b := range []struct {
		bound string
		size  int // what the strict counter says
		set   func(bd *Bounds, n int)
		on    bool
	}{
		{"request bytes", cjsonTree(t, raw), func(bd *Bounds, n int) { bd.RequestBytes = n }, true},
		{"line bytes", m.maxLine, func(bd *Bounds, n int) { bd.LineBytes = n }, s.Entries[0].Kind != KindRows},
		{"planned argv bytes with the margin", charged(m.argv), func(bd *Bounds, n int) { bd.PlannedArgvBytes = n }, true},
	} {
		if !b.on {
			continue
		}
		at, under := Contract(), Contract()
		b.set(&at, b.size)
		b.set(&under, b.size-1)
		if got, err := Build(cfgAt(at), entries); err != nil || !same(got) {
			t.Errorf("%s: %s bound of exactly the strict count, %d, does not leave the step whole: %d steps, %v: the builder counts more than cjson does", what, b.bound, b.size, len(got), err)
		}
		if got, err := Build(cfgAt(under), entries); err != nil || same(got) {
			t.Errorf("%s: a %s bound of the strict count less one, %d, does not cut the step: %d steps, %v: the builder counts less than cjson does", what, b.bound, b.size-1, len(got), err)
		}
	}
	if m.strict > m.argv {
		t.Errorf("%s: the strict planned argv count is %d, the model %d", what, m.strict, m.argv)
	}
}

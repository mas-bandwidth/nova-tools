package stepbuild

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// Layer 1 does not only read the raw request. S.plan decodes it and encodes
// the decoded request again with cjson, and refuses LIMIT request_bytes when
// that encoding is over 4 MiB (the tset-l1 branch at 72b425b6d,
// table_set.lua:328-331; the codec is table_set_validate.lua:5-20). cjson
// spells some bytes wider than encoding/json does: a slash is two bytes, DEL
// is six. So the builder sizes a request, and the generated lines a Layer 2
// that encodes with cjson would write, by cjson's escaping, and writes the
// request in it, which makes Step.Bytes the size of the request and of its
// re-encoding at once.
//
// This file is the strict counter that holds the builder's model to cjson,
// written apart from the builder (none of encode.go's tables or widths is
// used): the escape table of the cjson that Redis bundles, a re-encoder of a
// decoded request, and the tests of the bytes and of the bounds.

// cjsonEscape is what cjson writes for each byte inside a string: char2escape
// of the lua_cjson.c that Redis bundles. Every byte from 0x80 up, and every
// printable one but the quote, the backslash and the slash, is itself; so are
// the HTML characters and U+2028 and U+2029 (Layer 1's Go twin,
// internal/tset/mem_receipt.go:26-29, says the same). Lower-case hex. When it
// was written the width of each of the 256 bytes and the spelling of each class
// were checked against a live cjson.encode (Redis 8.10.2).
var cjsonEscape = func() (t [256]string) {
	for i := range t {
		t[i] = string([]byte{byte(i)})
	}
	for i := 0; i < 0x20; i++ {
		t[i] = fmt.Sprintf(`\u%04x`, i)
	}
	t[0x7f] = `\u007f`
	t['\b'], t['\t'], t['\n'], t['\f'], t['\r'] = `\b`, `\t`, `\n`, `\f`, `\r`
	t['"'], t['\\'], t['/'] = `\"`, `\\`, `\/`
	return t
}()

// cjsonString is s as cjson writes a string.
func cjsonString(s string) string {
	buf := make([]byte, 0, len(s)+2)
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		if e := cjsonEscape[s[i]]; len(e) == 1 {
			buf = append(buf, s[i])
		} else {
			buf = append(buf, e...)
		}
	}
	return string(append(buf, '"'))
}

// cjsonWrite writes a decoded JSON value the way cjson encodes it, with the
// keys of an object in byte order (cjson's order is its table's, which does not
// change the length). Numbers are written as their text.
func cjsonWrite(sb *strings.Builder, v any) {
	switch v := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		fmt.Fprint(sb, v)
	case string:
		sb.WriteString(cjsonString(v))
	case json.Number:
		sb.WriteString(string(v))
	case []string:
		sb.WriteByte('[')
		for i, s := range v {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(cjsonString(s))
		}
		sb.WriteByte(']')
	case []any:
		sb.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				sb.WriteByte(',')
			}
			cjsonWrite(sb, e)
		}
		sb.WriteByte(']')
	case map[string]string:
		m := make(map[string]any, len(v))
		for k, s := range v {
			m[k] = s
		}
		cjsonWrite(sb, m)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(cjsonString(k))
			sb.WriteByte(':')
			cjsonWrite(sb, v[k])
		}
		sb.WriteByte('}')
	default:
		panic(fmt.Sprintf("the cjson counter does not write a %T", v))
	}
}

// cjsonTree is what Layer 1's S.plan measures: the request decoded, then
// encoded again by cjson, counted by building the decoded tree and writing it
// with the strict counter above.
func cjsonTree(t testing.TB, raw []byte) int {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		t.Fatalf("the request does not decode: %v\n%.300s", err, raw)
	}
	var sb strings.Builder
	sb.Grow(len(raw))
	cjsonWrite(&sb, v)
	return sb.Len()
}

// cjsonReEncode is cjsonTree without the tree: a scan of the JSON text that
// decodes each string literal and counts the bytes cjson writes for it, the
// other bytes as they are and the white space outside strings not at all. It is
// the counter the measure uses on every step (the tree is the slower second
// opinion, and TestTheScanIsTheTree holds the two together). A number is
// counted as its text, which is all the requests the builder writes have to say:
// none holds one.
func cjsonReEncode(t testing.TB, raw []byte) int {
	t.Helper()
	n := 0
	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '"' {
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				n++
			}
			i++
			continue
		}
		n += 2
		for i++; ; {
			if i >= len(raw) {
				t.Fatalf("a string is not closed: %.300s", raw)
			}
			c := raw[i]
			if c == '"' {
				i++
				break
			}
			if c != '\\' {
				n += widthOf(c)
				i++
				continue
			}
			if i+1 >= len(raw) {
				t.Fatalf("an escape is not finished: %.300s", raw)
			}
			switch e := raw[i+1]; e {
			case '"', '\\', '/':
				n += widthOf(e) // the byte itself
				i += 2
			case 'b':
				n += widthOf('\b')
				i += 2
			case 'f':
				n += widthOf('\f')
				i += 2
			case 'n':
				n += widthOf('\n')
				i += 2
			case 'r':
				n += widthOf('\r')
				i += 2
			case 't':
				n += widthOf('\t')
				i += 2
			case 'u':
				r, size := unescape(t, raw[i:])
				if r < 0x80 {
					n += widthOf(byte(r))
				} else {
					n += utf8.RuneLen(r)
				}
				i += size
			default:
				t.Fatalf("an escape the scan does not know: %q", raw[i:i+2])
			}
		}
	}
	return n
}

// unescape reads \uXXXX (a surrogate pair as two) at the start of b: the rune
// and the bytes of text it took.
func unescape(t testing.TB, b []byte) (rune, int) {
	t.Helper()
	hex4 := func(b []byte) rune {
		if len(b) < 6 || b[0] != '\\' || b[1] != 'u' {
			return -1
		}
		var r rune
		for _, c := range b[2:6] {
			switch {
			case c >= '0' && c <= '9':
				r = r<<4 | rune(c-'0')
			case c >= 'a' && c <= 'f':
				r = r<<4 | rune(c-'a'+10)
			case c >= 'A' && c <= 'F':
				r = r<<4 | rune(c-'A'+10)
			default:
				return -1
			}
		}
		return r
	}
	r := hex4(b)
	if r < 0 {
		t.Fatalf("a \\u escape that is not four hex digits: %.20q", b)
	}
	if utf16.IsSurrogate(r) {
		if low := hex4(b[6:]); low >= 0 {
			if d := utf16.DecodeRune(r, low); d != utf8.RuneError {
				return d, 12
			}
		}
		return utf8.RuneError, 6
	}
	return r, 6
}

// cjsonLen is the bytes cjson writes for v: a string as it is, anything else
// through its JSON (HTML escaping off) and back.
func cjsonLen(t testing.TB, v any) int {
	t.Helper()
	if s, ok := v.(string); ok {
		return len(cjsonString(s))
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return cjsonReEncode(t, buf.Bytes())
}

// widthOf is the bytes cjson writes for a byte inside a string, typed here
// class by class and not read from the table above.
func widthOf(b byte) int {
	switch {
	case b == '"' || b == '\\' || b == '/':
		return 2
	case b == '\b' || b == '\t' || b == '\n' || b == '\f' || b == '\r':
		return 2
	case b < 0x20 || b == 0x7f:
		return 6
	}
	return 1
}

// The size the builder works to is cjson's, byte by byte and string by string.
func TestTheModelIsCJSONsEscapeTableByteForByte(t *testing.T) {
	t.Parallel()
	for b := 0; b < 256; b++ {
		s := string([]byte{byte(b)})
		if got := len(cjsonEscape[b]); got != widthOf(byte(b)) {
			t.Fatalf("byte %#02x: the counter's table is %d bytes, the class table says %d", b, got, widthOf(byte(b)))
		}
		if got := quoted(s) - 2; got != widthOf(byte(b)) {
			t.Errorf("byte %#02x: the builder sizes it at %d bytes, cjson writes %d", b, got, widthOf(byte(b)))
		}
		if got := string(appendQuoted(nil, s)); got != cjsonString(s) {
			t.Errorf("byte %#02x: the builder writes %s, cjson writes %s", b, got, cjsonString(s))
		}
	}

	var all, backwards []byte
	for b := 0; b < 256; b++ {
		all = append(all, byte(b))
		backwards = append([]byte{byte(b)}, backwards...)
	}
	var others strings.Builder // the controls with no short form
	for b := 0; b < 0x20; b++ {
		if !strings.ContainsRune("\b\t\n\f\r", rune(b)) {
			others.WriteByte(byte(b))
		}
	}
	classes := []struct {
		name  string
		bytes string
		width int // the bytes cjson writes for the whole of it
	}{
		{"quote", `"`, 2},
		{"backslash", `\`, 2},
		{"slash", "/", 2},
		{"DEL", "\x7f", 6},
		{"backspace, tab, line feed, form feed, carriage return", "\b\t\n\f\r", 10},
		{"the other 27 controls", others.String(), 27 * 6},
		{"HTML characters", "<>&", 3},
		{"U+2028 and U+2029", "  ", 6},
		{"a C1 control", "\u0085", 2},
		{"multi-byte runes", "é世😀", 2 + 3 + 4},
		{"every byte, up", string(all), 0},
		{"every byte, down", string(backwards), 0},
	}
	for _, c := range classes {
		for _, n := range []int{1, 2, 7, 1000} {
			s := strings.Repeat(c.bytes, n)
			want := len(cjsonString(s))
			if c.width != 0 && want != 2+n*c.width {
				t.Fatalf("%s: the counter writes %d bytes for %d of it, the class table says %d", c.name, want, n, 2+n*c.width)
			}
			if got := quoted(s); got != want {
				t.Errorf("%s x %d: the builder sizes it at %d bytes, cjson writes %d", c.name, n, got, want)
			}
			if got := string(appendQuoted(nil, s)); got != cjsonString(s) {
				t.Errorf("%s x %d: the builder writes %.60q, cjson writes %.60q", c.name, n, got, cjsonString(s))
			}
		}
	}

	// Strings of any bytes, and the arrays and objects of them.
	r := rand.New(rand.NewPCG(9, 0xc150))
	rnd := func() string {
		b := make([]byte, r.IntN(80))
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		return string(b)
	}
	for i := 0; i < 300; i++ {
		s := rnd()
		if got, want := quoted(s), len(cjsonString(s)); got != want {
			t.Fatalf("%q: the builder sizes it at %d bytes, cjson writes %d", s, got, want)
		}
		ss := []string{rnd(), rnd(), rnd()}[:r.IntN(4)]
		var sb strings.Builder
		cjsonWrite(&sb, ss)
		if got := arrayBytes(ss); got != sb.Len() {
			t.Fatalf("%q: arrayBytes %d, cjson writes %d", ss, got, sb.Len())
		}
		m := map[string]string{}
		for j, n := 0, r.IntN(4); j < n; j++ {
			m[rnd()] = rnd()
		}
		sb.Reset()
		cjsonWrite(&sb, m)
		if got := objectBytes(m); got != sb.Len() {
			t.Fatalf("%q: objectBytes %d, cjson writes %d", m, got, sb.Len())
		}
	}
}

// The scan the measure uses is the tree: on documents written by encoding/json
// (which spells \b, \f, DEL, the HTML characters and U+2028 its own ways, and
// U+1F600 as itself) and by the builder, of strings of any runes, the two count
// the same bytes.
func TestTheScanIsTheTree(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(4, 0x5ca9))
	rnd := func() string {
		var sb strings.Builder
		for i, n := 0, r.IntN(40); i < n; i++ {
			switch r.IntN(4) {
			case 0:
				sb.WriteByte(byte(r.IntN(0x80)))
			case 1:
				sb.WriteRune(rune(0x80 + r.IntN(0x700)))
			case 2:
				sb.WriteRune(rune(0x800 + r.IntN(0xd000)))
			default:
				sb.WriteRune(rune(0x10000 + r.IntN(0x10000)))
			}
		}
		return sb.String()
	}
	for i := 0; i < 200; i++ {
		doc := map[string]any{rnd(): []string{rnd(), rnd()}, rnd(): map[string]string{rnd(): rnd()}, "n": 7, "t": true, "z": nil}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		indented, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range [][]byte{raw, indented} {
			if got, want := cjsonReEncode(t, b), cjsonTree(t, b); got != want {
				t.Fatalf("%s: the scan counts %d bytes, the tree %d", b, got, want)
			}
		}
	}
	for i, s := range must(t, cfg(), rich()) {
		if got, want := cjsonReEncode(t, s.Encode()), cjsonTree(t, s.Encode()); got != want || got != s.Bytes {
			t.Errorf("step %d: the scan counts %d bytes, the tree %d, the builder %d", i+1, got, want, s.Bytes)
		}
	}
}

// A '/'-rich or DEL-rich fill for a value of n bytes.
var fills = []struct {
	name string
	fill func(n int) string
}{
	{"plain text", func(n int) string { return strings.Repeat("abcdefghij", n/10+1)[:n] }},
	{"paths, one byte in ten a slash", func(n int) string { return strings.Repeat("abcdefghi/", n/10+1)[:n] }},
	{"all slashes", func(n int) string { return strings.Repeat("/", n) }},
	{"all DEL", func(n int) string { return strings.Repeat("\x7f", n) }},
	{"every ASCII byte in turn", func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i % 128)
		}
		return string(b)
	}},
}

// The second read's scenario: notes of one meta value each of 45,000 bytes are
// cut by the request bound. Each step is held to the size Layer 1 measures (its
// request decoded and encoded again by cjson): inside 4 MiB, the size the
// builder counted, and as full as the bound lets it be. By the encoding/json
// model a step of paths was 4,190,633 bytes and re-encoded to 4,609,133, over
// the bound; a step of slashes to twice the bound. The slow tier cuts the
// reader's 300 notes and counts every step in full.
func TestARequestAtTheBoundIsInsideLayerOnesReEncoding(t *testing.T) {
	t.Parallel()
	for i, f := range fills[1:4] { // paths, slashes, DEL: the fills that were over the bound
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			counted := 0 // the independent measure counts one step in full, of the reader's own case
			if i == 0 {
				counted = 1
			}
			checkNotesAtTheRequestBound(t, f.fill, 0, 45000, counted)
		})
	}
}

// checkNotesAtTheRequestBound cuts n notes of one meta value of valueBytes
// bytes made by fill (n of zero is one more than a request holds), and holds
// every step to the bound as Layer 1 measures it; the first countSteps steps are
// counted in full by the independent measure.
func checkNotesAtTheRequestBound(t *testing.T, fill func(int) string, n, valueBytes, countSteps int) {
	t.Helper()
	val := fill(valueBytes)
	if n == 0 {
		one := map[string]any{"line": map[string]any{"kind": "note", "meta": map[string]string{"k": val}}, "about": []string{"p00"}}
		var sb strings.Builder
		cjsonWrite(&sb, one)
		n = LimitRequestBytes/(sb.Len()+1) + 2
	}
	in := make([]Note, n)
	for j := range in {
		in[j] = Note{Meta: map[string]string{"k": val}, About: []string{fmt.Sprintf("p%02d", j)}}
	}
	steps := must(t, cfg(), []Entry{{Kind: KindNote, Notes: in}})
	if len(steps) < 2 {
		t.Fatalf("%d steps: the notes are %d bytes, over the request bound", len(steps), n*valueBytes)
	}
	done := 0
	for k, s := range steps {
		raw := s.Encode()
		if len(raw) != s.Bytes {
			t.Fatalf("step %d: Bytes %d, encoded %d", k+1, s.Bytes, len(raw))
		}
		re := cjsonTree(t, raw)
		if re > LimitRequestBytes {
			t.Errorf("step %d: %d bytes as sent, %d when cjson encodes it again: over the 4 MiB Layer 1 refuses with LIMIT request_bytes (%.1f percent)", k+1, len(raw), re, 100*float64(re)/LimitRequestBytes)
		}
		if re != s.Bytes {
			t.Errorf("step %d: the builder counted %d bytes, cjson encodes the request again as %d", k+1, s.Bytes, re)
		}
		if len(s.Notes) > LimitNotes {
			t.Errorf("step %d holds %d notes", k+1, len(s.Notes))
		}
		done += len(s.Notes)
		// As full as the bound lets it be: the next note, as cjson writes it (and
		// a comma), would not fit.
		if k < len(steps)-1 {
			next := map[string]any{"line": map[string]any{"kind": "note", "meta": in[done].Meta}, "about": in[done].About}
			var sb strings.Builder
			cjsonWrite(&sb, next)
			if room := LimitRequestBytes - re; room >= sb.Len()+1 {
				t.Errorf("step %d has %d bytes of room and the next note is %d: it is cut short", k+1, room, sb.Len()+1)
			}
		}
		// Every bound, counted from the request by the independent measure (the
		// line by cjson, the planned argv bytes two ways).
		if k < countSteps {
			if w := measure(t, raw).within(Contract()); len(w) != 0 {
				t.Errorf("step %d: %v", k+1, w)
			}
		}
	}
	if done != n {
		t.Fatalf("the steps hold %d notes of %d", done, n)
	}
}

// A note is never cut, and its line is the biggest thing a note can be: the
// line bound is 1 MiB of the line as cjson writes it. A value of DEL and
// slashes is a fraction of that in raw bytes: 100,000 of each and a padding
// that tunes the line to the bound by the independent measure (which counts the
// line by cjson) is one note and one step; one byte more is refused.
func TestALineAtTheBoundIsCountedAtCJSONWidth(t *testing.T) {
	t.Parallel()
	build := func(pad int) []Entry {
		val := strings.Repeat("\x7f", 100000) + strings.Repeat("/", 100000) + strings.Repeat("p", pad)
		return []Entry{{Kind: KindNote, Notes: []Note{{Meta: map[string]string{"m": val}, About: []string{"p"}}}}}
	}
	base := must(t, cfg(), build(0))
	line0 := measure(t, base[0].Encode()).maxLine
	if line0 < 700000 {
		t.Fatalf("the probe's line is %d bytes: not near the bound", line0)
	}
	pad := LimitLineBytes - line0
	if pad < 0 {
		t.Fatalf("the probe's line is over the bound already: %d", line0)
	}
	steps := must(t, cfg(), build(pad))
	if m := measure(t, steps[0].Encode()); len(steps) != 1 || m.maxLine != LimitLineBytes || len(m.within(Contract())) != 0 {
		t.Fatalf("a note whose line is exactly 1 MiB by cjson: %d steps, line %d, %v", len(steps), m.maxLine, m.within(Contract()))
	}
	le := refused(t, cfg(), build(pad+1))
	if le.Bound != boundLineBytes.name || le.Limit != LimitLineBytes || le.Actual != LimitLineBytes+1 || le.Field != "notes" {
		t.Fatalf("a note whose line is 1 MiB + 1 by cjson: %+v", le)
	}
}

// The members of an entry with a value of each of them of slashes or DEL: the
// request bound, the line bound (wire entries of a step are cut by it) and the
// planned argv bytes are all counted at cjson's width, and every step is inside
// all of them counted from its request by the independent measure. The members
// are enough for a little more than two steps of each fill.
func TestMembersOfSlashesAndDELAreCutAtCJSONWidth(t *testing.T) {
	t.Parallel()
	for i, f := range fills[1:4] { // paths, slashes, DEL
		members := []int{160, 110, 40}[i]
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			const valueBytes = 20000
			e := mv("work", names("m", members))
			e.About = make([]string, members)
			e.Each = make([]map[string]string, members)
			val := f.fill(valueBytes)
			for i := range e.Each {
				e.Each[i] = map[string]string{"f": val}
				e.About[i] = fmt.Sprintf("p%d", i%7)
			}
			steps := must(t, cfg(), []Entry{e})
			if len(steps) < 2 {
				t.Fatalf("%d steps", len(steps))
			}
			done := 0
			for k, s := range steps {
				raw := s.Encode()
				re := cjsonTree(t, raw)
				if re != s.Bytes || len(raw) != s.Bytes || re > LimitRequestBytes {
					t.Errorf("step %d: Bytes %d, sent %d, encoded again by cjson %d (bound %d)", k+1, s.Bytes, len(raw), re, LimitRequestBytes)
				}
				done += countMembers([]Step{s})
				if k > 0 { // the first step is counted in full; the rest by their sizes above
					continue
				}
				if w := measure(t, raw).within(Contract()); len(w) != 0 {
					t.Errorf("step %d: %v", k+1, w)
				}
			}
			if done != members {
				t.Fatalf("the steps hold %d members of %d", done, members)
			}
		})
	}
}

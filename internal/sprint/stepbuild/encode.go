package stepbuild

import "sort"

// The encoding of a step's request is written once, here, against a sink: the
// same code counts the bytes the cut checks against the bounds (counter) and
// writes the bytes a caller sends (buffer), so the size the cut works to is
// the size of what is sent. The cut adds member by member with the item
// sizes below rather than re-counting a part on every member; the tests hold
// the two to equality on every step they build.
//
// The text is JSON, and its strings are spelled the way cjson spells them: the
// quote, the backslash and the slash as a backslash and themselves, the
// controls with a short form (backspace, tab, line feed, form feed, carriage
// return) as \b \t \n \f \r, every other control and DEL as \u00XX, and every
// other byte, the UTF-8 the input already is, as itself (escExtra). The
// spelling is not a matter of taste. Layer 1's S.plan decodes a request and
// encodes it again with cjson, and refuses LIMIT request_bytes when that
// encoding is over 4 MiB (the tset-l1 branch at 72b425b6d, table_set.lua:
// 328-331; table_set_validate.lua:5-20 has the codec), and cjson writes a slash
// in two bytes and DEL in six. A request written in cjson's own spelling is
// its own re-encoding, so one size, Step.Bytes, is the size of the request as
// sent and as Layer 1 measures it; and the generated lines, which a Layer 2 in
// the same function most likely encodes with cjson too, are sized by the same
// spelling. Object keys are written in byte order, entry keys in the fixed order
// of emitMember, so the same step always encodes to the same bytes.

// sink is what the emitters write to.
type sink interface {
	raw(s string) // literal text, already JSON
	str(s string) // a JSON string of s
}

// counter counts the bytes a sink would write.
type counter int

func (c *counter) raw(s string) { *c += counter(len(s)) }
func (c *counter) str(s string) { *c += counter(quoted(s)) }

// buffer collects the bytes.
type buffer struct{ b []byte }

func (w *buffer) raw(s string) { w.b = append(w.b, s...) }
func (w *buffer) str(s string) { w.b = appendQuoted(w.b, s) }

// escExtra is the bytes past its own that a byte costs inside a string, the
// char2escape table of the lua_cjson.c that Redis bundles: one for the quote, the
// backslash, the slash and the five controls with a short form (\b \t \n \f \r),
// five for every other control and for DEL (the six bytes of \u00XX).
var escExtra = func() (t [256]uint8) {
	for i := 0; i < 0x20; i++ {
		t[i] = 5
	}
	t[0x7f] = 5
	for _, c := range []byte{'"', '\\', '/', '\b', '\t', '\n', '\f', '\r'} {
		t[c] = 1
	}
	return
}()

// quoted is the encoded size of s as a JSON string, its quotes included.
func quoted(s string) int {
	n := len(s) + 2
	for i := 0; i < len(s); i++ {
		n += int(escExtra[s[i]])
	}
	return n
}

// appendQuoted appends s as a string, spelled as cjson spells it.
func appendQuoted(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escExtra[c] == 0 {
			continue
		}
		dst = append(dst, s[start:i]...)
		start = i + 1
		switch c {
		case '"', '\\', '/':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\r':
			dst = append(dst, '\\', 'r')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
		}
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// Encode is the step's request: the JSON Layer 1's FCALL takes, exactly
// Bytes long.
func (s *Step) Encode() []byte {
	w := &buffer{b: make([]byte, 0, s.Bytes)}
	emitRequest(w, s)
	return w.b
}

// emitRequest writes the request: epoch, the header members, the identity
// when there is one, the entries (always present, empty for a notes-only step) and the
// notes when there are any.
func emitRequest(w sink, s *Step) {
	w.raw(`{"epoch":`)
	w.str(s.Epoch)
	for _, m := range s.Header {
		w.raw(",")
		w.str(m.Key)
		w.raw(":")
		w.str(m.Value)
	}
	if s.Ident.Op != "" {
		w.raw(`,"op":`)
		w.str(s.Ident.Op)
		w.raw(`,"intent":`)
		w.str(s.Ident.Intent)
	}
	if s.Ident.Result != "" {
		w.raw(`,"result":`)
		w.str(s.Ident.Result)
	}
	w.raw(`,"entries":[`)
	for i := range s.Entries {
		if i > 0 {
			w.raw(",")
		}
		emitEntry(w, &s.Entries[i].Entry)
	}
	w.raw("]")
	if len(s.Notes) > 0 {
		w.raw(`,"notes":[`)
		for i := range s.Notes {
			if i > 0 {
				w.raw(",")
			}
			emitNote(w, &s.Notes[i].Note)
		}
		w.raw("]")
	}
	w.raw("}")
}

// emitEntry writes one wire entry.
func emitEntry(w sink, e *Entry) {
	if e.Kind == KindRows {
		emitRows(w, e)
		return
	}
	emitMember(w, e)
}

// emitMember writes a create, move, remove or guard entry: the kind and
// table, the cells when given, then the member arrays that are present, the
// shared fields and the meta.
func emitMember(w sink, e *Entry) {
	w.raw(`{"kind":`)
	w.str(string(e.Kind))
	w.raw(`,"t":`)
	w.str(e.Table)
	if e.From != "" {
		w.raw(`,"from":`)
		w.str(e.From)
	}
	if e.To != "" {
		w.raw(`,"to":`)
		w.str(e.To)
	}
	w.raw(`,"ids":`)
	emitStrings(w, e.IDs)
	if e.Scores != nil {
		w.raw(`,"scores":`)
		emitStrings(w, e.Scores)
	}
	if e.Revs != nil {
		w.raw(`,"revs":`)
		emitStrings(w, e.Revs)
	}
	if e.Set != nil {
		w.raw(`,"set":`)
		emitObject(w, e.Set)
	}
	if e.Each != nil {
		w.raw(`,"each":[`)
		for i, m := range e.Each {
			if i > 0 {
				w.raw(",")
			}
			emitObject(w, m)
		}
		w.raw("]")
	}
	if e.Unset != nil {
		w.raw(`,"unset":`)
		emitStrings(w, e.Unset)
	}
	if e.BeforeFields != nil {
		w.raw(`,"before_fields":`)
		emitStrings(w, e.BeforeFields)
	}
	if e.About != nil {
		w.raw(`,"about":`)
		emitStrings(w, e.About)
	}
	if e.Meta != nil {
		w.raw(`,"meta":`)
		emitObject(w, e.Meta)
	}
	w.raw("}")
}

// emitRows writes a rows entry; add and del are on the wire when they hold a
// row.
func emitRows(w sink, e *Entry) {
	w.raw(`{"kind":"rows","t":`)
	w.str(e.Table)
	if len(e.Add) > 0 {
		w.raw(`,"add":`)
		emitStrings(w, e.Add)
	}
	if len(e.Del) > 0 {
		w.raw(`,"del":`)
		emitStrings(w, e.Del)
	}
	w.raw("}")
}

// emitNote writes a note: its line, a note-kind line carrying the meta, and
// the primary IDs it is about.
func emitNote(w sink, n *Note) {
	w.raw(`{"line":{"kind":"note","meta":`)
	emitObject(w, n.Meta)
	w.raw(`},"about":`)
	emitStrings(w, n.About)
	w.raw("}")
}

// emitStrings writes a JSON array of strings; a nil slice is [].
func emitStrings(w sink, ss []string) {
	w.raw("[")
	for i, s := range ss {
		if i > 0 {
			w.raw(",")
		}
		w.str(s)
	}
	w.raw("]")
}

// emitObject writes a string-to-string object with its keys in byte order; a
// nil map is {}.
func emitObject(w sink, m map[string]string) {
	w.raw("{")
	if len(m) > 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				w.raw(",")
			}
			w.str(k)
			w.raw(":")
			w.str(m[k])
		}
	}
	w.raw("}")
}

// objectBytes is the encoded size of a string-to-string object, the size
// emitObject writes, counted without sorting or allocating.
func objectBytes(m map[string]string) int {
	n := 2
	for k, v := range m {
		n += quoted(k) + 1 + quoted(v)
	}
	if len(m) > 1 {
		n += len(m) - 1
	}
	return n
}

// arrayBytes is the encoded size of an array of strings, the size
// emitStrings writes.
func arrayBytes(ss []string) int {
	n := 2
	for _, s := range ss {
		n += quoted(s)
	}
	if len(ss) > 1 {
		n += len(ss) - 1
	}
	return n
}

// count is the encoded size of an emitter's output.
func count(emit func(sink)) int {
	var c counter
	emit(&c)
	return int(c)
}

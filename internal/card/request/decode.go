package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Parse reads one request document. It is strict where encoding/json is
// lenient: a duplicate key at any depth refuses (the standard decoder keeps the
// last silently), an unknown field refuses, trailing data after the document
// refuses, a number where a string is required refuses (a counter is never a
// float), and the document is bounded before it is read. It then runs Validate.
// It returns the request, or nil and a *Refusals naming everything found; a
// syntax error stops the read, so it is the one refusal then reported.
func Parse(data []byte) (*Request, error) {
	c := newCollector()
	req := parse(data, c)
	if err := c.err(); err != nil {
		return nil, err
	}
	return req, nil
}

func parse(data []byte, c *collector) *Request {
	switch {
	case len(data) == 0:
		c.add(-1, "", "", CauseSyntax, "empty input", "", "send a JSON request document")
		return nil
	case len(data) > MaxInputBytes:
		c.add(-1, "", "", CauseTooLarge, strconv.Itoa(len(data))+" bytes", strconv.Itoa(MaxInputBytes)+" bytes",
			"send fewer or smaller entries; the request is never split for you")
		return nil
	}
	if !utf8.Valid(data) {
		off := 0
		for off < len(data) {
			r, n := utf8.DecodeRune(data[off:])
			if r == utf8.RuneError && n <= 1 {
				break
			}
			off += n
		}
		c.add(-1, "", "", CauseInvalidUTF8, "invalid byte at offset "+strconv.Itoa(off), "valid UTF-8", "send the document as UTF-8")
		return nil
	}
	p := &parser{dec: json.NewDecoder(bytes.NewReader(data)), c: c}
	p.dec.UseNumber()
	root, ok := p.value(0, nil)
	if !ok {
		return nil
	}
	if _, err := p.dec.Token(); !errors.Is(err, io.EOF) {
		c.add(-1, "", "", CauseTrailingData, "data after the document", "one document", "send exactly one JSON document")
	}
	if root.kind != nObject {
		c.add(-1, "", "", CauseWrongType, root.kind.String(), "an object", "send a request object")
		return nil
	}
	b := &binder{c: c}
	req := b.request(root)
	if req.Operation.Valid() {
		// Faults found before the operation was read carry it now.
		for i := range c.list {
			if c.list[i].Operation == "" {
				c.list[i].Operation = req.Operation
			}
		}
	}
	validate(req, c)
	return req
}

type nodeKind int

const (
	nObject nodeKind = iota
	nArray
	nString
	nNumber
	nBool
	nNull
)

func (k nodeKind) String() string {
	return [...]string{"an object", "an array", "a string", "a number", "a boolean", "null"}[k]
}

// node is one parsed JSON value. An object keeps its first value for a key; a
// repeated key is refused as it is read.
type node struct {
	kind  nodeKind
	s     string
	keys  []string
	vals  map[string]*node
	elems []*node
}

type parser struct {
	dec *json.Decoder
	c   *collector
}

// pathField renders a path of keys and indexes as a field: keys joined by dots,
// each index in brackets.
func pathField(path []string) string {
	var b strings.Builder
	for _, seg := range path {
		if seg != "" && seg[0] == '[' {
			b.WriteString(seg)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}

var payloadKeys = map[string]bool{"admissions": true, "events": true, "evidence": true, "replacements": true}

// locate turns a path into an entry index and a field within the entry: a path
// under a payload array is the entry at that index, anything else is the
// envelope or the scope.
func locate(path []string) (int, string) {
	if len(path) >= 2 && payloadKeys[path[0]] && len(path[1]) > 2 && path[1][0] == '[' {
		if n, err := strconv.Atoi(path[1][1 : len(path[1])-1]); err == nil {
			return n, pathField(path[2:])
		}
	}
	return -1, pathField(path)
}

func (p *parser) value(depth int, path []string) (*node, bool) {
	tok, err := p.dec.Token()
	if err != nil {
		p.c.add(-1, "", "", CauseSyntax, quote(err.Error()), "", "send well-formed JSON")
		return nil, false
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth >= MaxDepth {
			idx, f := locate(path)
			p.c.add(idx, "", f, CauseTooDeep, "nesting deeper than "+strconv.Itoa(MaxDepth), strconv.Itoa(MaxDepth)+" levels", "flatten the document")
			return nil, false
		}
		if t == '{' {
			n := &node{kind: nObject, vals: map[string]*node{}}
			for p.dec.More() {
				kt, err := p.dec.Token()
				if err != nil {
					p.c.add(-1, "", "", CauseSyntax, quote(err.Error()), "", "send well-formed JSON")
					return nil, false
				}
				key, _ := kt.(string)
				child, ok := p.value(depth+1, append(path[:len(path):len(path)], key))
				if !ok {
					return nil, false
				}
				if _, dup := n.vals[key]; dup {
					idx, f := locate(append(path[:len(path):len(path)], key))
					p.c.add(idx, "", f, CauseDuplicateKey, quote(key), "each key once", "remove the repeated key")
					continue
				}
				n.keys = append(n.keys, key)
				n.vals[key] = child
			}
			if _, err := p.dec.Token(); err != nil {
				p.c.add(-1, "", "", CauseSyntax, quote(err.Error()), "", "send well-formed JSON")
				return nil, false
			}
			return n, true
		}
		n := &node{kind: nArray, elems: []*node{}}
		for i := 0; p.dec.More(); i++ {
			child, ok := p.value(depth+1, append(path[:len(path):len(path)], "["+strconv.Itoa(i)+"]"))
			if !ok {
				return nil, false
			}
			n.elems = append(n.elems, child)
		}
		if _, err := p.dec.Token(); err != nil {
			p.c.add(-1, "", "", CauseSyntax, quote(err.Error()), "", "send well-formed JSON")
			return nil, false
		}
		return n, true
	case string:
		return &node{kind: nString, s: t}, true
	case json.Number:
		return &node{kind: nNumber, s: t.String()}, true
	case bool:
		return &node{kind: nBool}, true
	default:
		return &node{kind: nNull}, true
	}
}

// binder turns the parsed tree into a Request, refusing what is not the shape.
type binder struct{ c *collector }

// objReader reads the fields of one object and reports the ones nobody asked for.
type objReader struct {
	b      *binder
	n      *node
	index  int
	id     string
	prefix string
	used   map[string]bool
}

func (b *binder) object(n *node, index int, id, prefix string) *objReader {
	o := &objReader{b: b, index: index, id: id, prefix: prefix, used: map[string]bool{}}
	if n == nil {
		return o
	}
	if n.kind != nObject {
		b.c.add(index, id, prefix, CauseWrongType, n.kind.String(), "an object", "send an object")
		return o
	}
	o.n = n
	return o
}

func (o *objReader) field(key string) string {
	if o.prefix == "" {
		return key
	}
	return o.prefix + "." + key
}

func (o *objReader) get(key string) *node {
	if o.n == nil {
		return nil
	}
	o.used[key] = true
	return o.n.vals[key]
}

func (o *objReader) wrong(key string, n *node, want string) {
	o.b.c.add(o.index, o.id, o.field(key), CauseWrongType, n.kind.String(), want, "send "+want)
}

func (o *objReader) str(key string, dst *string) {
	n := o.get(key)
	if n == nil {
		return
	}
	if n.kind != nString {
		o.wrong(key, n, "a string")
		return
	}
	*dst = n.s
}

var intRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)

func (o *objReader) integer(key string, dst *int) {
	n := o.get(key)
	if n == nil {
		return
	}
	if n.kind != nNumber || !intRE.MatchString(n.s) {
		if n.kind == nNumber {
			o.b.c.add(o.index, o.id, o.field(key), CauseWrongType, quote(n.s), "an integer of at most 9 digits", "send a whole number; floats and exponents are refused")
		} else {
			o.wrong(key, n, "an integer")
		}
		return
	}
	*dst, _ = strconv.Atoi(n.s)
}

func (o *objReader) sub(key string) *objReader {
	n := o.get(key)
	child := o.field(key)
	if n == nil {
		return &objReader{b: o.b, index: o.index, id: o.id, prefix: child, used: map[string]bool{}}
	}
	return o.b.object(n, o.index, o.id, child)
}

// array returns the elements of an array field, and whether it is present as an array.
func (o *objReader) array(key string) ([]*node, bool) {
	n := o.get(key)
	if n == nil {
		return nil, false
	}
	if n.kind != nArray {
		o.wrong(key, n, "an array")
		return nil, false
	}
	return n.elems, true
}

// finish refuses every key that was not read, in sorted order.
func (o *objReader) finish() {
	if o.n == nil {
		return
	}
	keys := append([]string(nil), o.n.keys...)
	sort.Strings(keys)
	for _, k := range keys {
		if !o.used[k] {
			o.b.c.add(o.index, o.id, o.field(k), CauseUnknownField, quote(k), "the fields of the schema", "remove the field")
		}
	}
}

func (b *binder) request(root *node) *Request {
	req := &Request{}
	o := b.object(root, -1, "", "")
	if o.n == nil {
		return req
	}
	var op string
	o.integer("schema", &req.Schema)
	o.str("operation", &op)
	req.Operation = Operation(op)
	if req.Operation.Valid() {
		b.c.op = req.Operation
	}
	o.str("table", &req.Table)
	o.str("epoch", &req.Epoch)
	o.str("expected_table_revision", &req.TableRevision)
	o.str("operation_id", &req.OperationID)
	o.str("actor", &req.Actor)

	want := map[Operation]string{OpAdmit: "admissions", OpResolve: "scope", OpApplyEvents: "events", OpRecordEvidence: "evidence", OpReplace: "replacements", OpInspect: "scope"}[req.Operation]
	for _, name := range []string{"admissions", "events", "evidence", "replacements", "scope"} {
		if o.n.vals[name] == nil {
			continue
		}
		if want != "" && name != want {
			o.used[name] = true
			b.c.add(-1, "", name, CauseNotApplicable, "", "", string(req.Operation)+" takes "+want+"; remove "+name)
			continue
		}
		switch name {
		case "admissions":
			if elems, ok := o.array(name); ok {
				req.Admissions = make([]Admission, len(elems))
				for i, e := range elems {
					b.admission(e, i, "", &req.Admissions[i])
				}
			}
		case "events":
			if elems, ok := o.array(name); ok {
				req.Events = make([]Event, len(elems))
				for i, e := range elems {
					b.event(e, i, &req.Events[i])
				}
			}
		case "evidence":
			if elems, ok := o.array(name); ok {
				req.Evidence = make([]Evidence, len(elems))
				for i, e := range elems {
					b.evidence(e, i, &req.Evidence[i])
				}
			}
		case "replacements":
			if elems, ok := o.array(name); ok {
				req.Replacements = make([]Replacement, len(elems))
				for i, e := range elems {
					b.replacement(e, i, &req.Replacements[i])
				}
			}
		case "scope":
			req.Scope = &Scope{}
			b.scope(o.get("scope"), req.Scope)
		}
	}
	o.finish()
	return req
}

func (b *binder) place(o *objReader, key string, p *Place) {
	po := o.sub(key)
	var col string
	po.str("row", &p.Row)
	po.str("col", &col)
	p.Col = State(col)
	po.finish()
}

func (b *binder) expect(o *objReader, key string, e *Expect) {
	eo := o.sub(key)
	eo.str("revision", &e.Revision)
	b.place(eo, "place", &e.Place)
	eo.finish()
}

func (b *binder) admission(n *node, index int, prefix string, a *Admission) {
	o := b.object(n, index, "", prefix)
	o.readAdmission(a)
}

func (o *objReader) readAdmission(a *Admission) {
	var id, digest string
	o.str("id", &id)
	a.ID = ID(id)
	o.id = knownID(id)
	o.str("digest", &digest)
	a.Digest = Digest(digest)
	o.str("object_id", &a.ObjectID)
	o.str("commit", &a.Commit)
	o.str("repository", &a.Repository)
	o.str("path", &a.Path)
	o.str("row", &a.Row)
	o.finish()
}

func (b *binder) event(n *node, index int, e *Event) {
	o := b.object(n, index, "", "")
	var id, typ, digest, dep string
	o.str("id", &id)
	e.ID = ID(id)
	o.id = knownID(id)
	o.str("type", &typ)
	e.Type = EventType(typ)
	b.expect(o, "expect", &e.Expect)
	o.str("digest", &digest)
	e.Digest = Digest(digest)
	o.str("issuer", &e.Issuer)
	o.str("source", &e.Source)
	o.str("head", &e.Head)
	o.str("result", &e.Result)
	o.str("reason", &e.Reason)
	o.str("dependency", &dep)
	e.Dependency = ID(dep)
	o.str("landing", &e.Landing)
	o.finish()
}

func (b *binder) evidence(n *node, index int, e *Evidence) {
	o := b.object(n, index, "", "")
	var id, digest string
	o.str("id", &id)
	e.ID = ID(id)
	o.id = knownID(id)
	o.str("digest", &digest)
	e.Digest = Digest(digest)
	b.expect(o, "expect", &e.Expect)
	if elems, ok := o.array("records"); ok {
		e.Records = make([]EvidenceRecord, len(elems))
		for j, re := range elems {
			ro := b.object(re, index, o.id, "records["+strconv.Itoa(j)+"]")
			r := &e.Records[j]
			var eid string
			ro.str("evidence_id", &eid)
			r.EvidenceID = ID(eid)
			ro.str("kind", &r.Kind)
			ro.str("disposition", &r.Disposition)
			ro.str("head", &r.Head)
			ro.str("issuer", &r.Issuer)
			ro.str("source", &r.Source)
			ro.finish()
		}
	}
	o.finish()
}

func (b *binder) replacement(n *node, index int, r *Replacement) {
	o := b.object(n, index, "", "")
	oo := o.sub("old")
	var id, digest string
	oo.str("id", &id)
	r.Old.ID = ID(id)
	oo.id = knownID(id)
	oo.str("digest", &digest)
	r.Old.Digest = Digest(digest)
	b.expect(oo, "expect", &r.Old.Expect)
	oo.finish()
	no := o.sub("new")
	no.readAdmission(&r.New)
	o.finish()
}

func (b *binder) scope(n *node, s *Scope) {
	o := b.object(n, -1, "", "scope")
	if elems, ok := o.array("ids"); ok {
		s.IDs = make([]ID, len(elems))
		for i, e := range elems {
			if e.kind != nString {
				b.c.add(-1, "", "scope.ids["+strconv.Itoa(i)+"]", CauseWrongType, e.kind.String(), "a string", "send a card ID string")
				continue
			}
			s.IDs[i] = ID(e.s)
		}
	}
	o.str("row", &s.Row)
	var col string
	o.str("col", &col)
	s.Col = State(col)
	o.integer("bound", &s.Bound)
	o.finish()
}

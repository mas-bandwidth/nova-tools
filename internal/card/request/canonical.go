package request

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
)

// obj is an object node of the canonical tree.
type obj map[string]any

func setStr(m obj, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func (e Expect) tree() obj {
	p := obj{}
	setStr(p, "row", e.Place.Row)
	setStr(p, "col", string(e.Place.Col))
	m := obj{"place": p}
	setStr(m, "revision", e.Revision)
	return m
}

func (a Admission) tree() obj {
	m := obj{}
	setStr(m, "id", string(a.ID))
	setStr(m, "digest", string(a.Digest))
	setStr(m, "object_id", a.ObjectID)
	setStr(m, "commit", a.Commit)
	setStr(m, "repository", a.Repository)
	setStr(m, "path", a.Path)
	setStr(m, "row", a.Row)
	return m
}

func (e Event) tree() obj {
	m := obj{"expect": e.Expect.tree()}
	setStr(m, "id", string(e.ID))
	setStr(m, "type", string(e.Type))
	setStr(m, "digest", string(e.Digest))
	setStr(m, "issuer", e.Issuer)
	setStr(m, "source", e.Source)
	setStr(m, "head", e.Head)
	setStr(m, "result", e.Result)
	setStr(m, "reason", e.Reason)
	setStr(m, "dependency", string(e.Dependency))
	setStr(m, "landing", e.Landing)
	return m
}

func (e Evidence) tree() obj {
	m := obj{"expect": e.Expect.tree()}
	setStr(m, "id", string(e.ID))
	setStr(m, "digest", string(e.Digest))
	if e.Records != nil {
		recs := make([]any, len(e.Records))
		for i, r := range e.Records {
			rm := obj{}
			setStr(rm, "evidence_id", string(r.EvidenceID))
			setStr(rm, "kind", r.Kind)
			setStr(rm, "disposition", r.Disposition)
			setStr(rm, "head", r.Head)
			setStr(rm, "issuer", r.Issuer)
			setStr(rm, "source", r.Source)
			recs[i] = rm
		}
		m["records"] = recs
	}
	return m
}

func (r Replacement) tree() obj {
	old := obj{"expect": r.Old.Expect.tree()}
	setStr(old, "id", string(r.Old.ID))
	setStr(old, "digest", string(r.Old.Digest))
	return obj{"old": old, "new": r.New.tree()}
}

func (s Scope) tree() obj {
	m := obj{}
	if s.IDs != nil {
		ids := make([]any, len(s.IDs))
		for i, id := range s.IDs {
			ids[i] = string(id)
		}
		m["ids"] = ids
	}
	setStr(m, "row", s.Row)
	setStr(m, "col", string(s.Col))
	if s.Bound != 0 {
		m["bound"] = s.Bound
	}
	return m
}

func (r *Request) tree() obj {
	m := obj{"schema": r.Schema}
	setStr(m, "operation", string(r.Operation))
	setStr(m, "table", r.Table)
	setStr(m, "epoch", r.Epoch)
	setStr(m, "expected_table_revision", r.TableRevision)
	setStr(m, "operation_id", r.OperationID)
	setStr(m, "actor", r.Actor)
	if r.Admissions != nil {
		l := make([]any, len(r.Admissions))
		for i, a := range r.Admissions {
			l[i] = a.tree()
		}
		m["admissions"] = l
	}
	if r.Events != nil {
		l := make([]any, len(r.Events))
		for i, e := range r.Events {
			l[i] = e.tree()
		}
		m["events"] = l
	}
	if r.Evidence != nil {
		l := make([]any, len(r.Evidence))
		for i, e := range r.Evidence {
			l[i] = e.tree()
		}
		m["evidence"] = l
	}
	if r.Replacements != nil {
		l := make([]any, len(r.Replacements))
		for i, e := range r.Replacements {
			l[i] = e.tree()
		}
		m["replacements"] = l
	}
	if r.Scope != nil {
		m["scope"] = r.Scope.tree()
	}
	return m
}

// writeCanon writes one canonical value: object keys sorted bytewise, no
// insignificant whitespace, integers only, strings escaped without the HTML
// escapes.
func writeCanon(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case obj:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			writeCanon(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanon(buf, e)
		}
		buf.WriteByte(']')
	case string:
		writeString(buf, t)
	case int:
		buf.WriteString(strconv.Itoa(t))
	case uint64:
		buf.WriteString(strconv.FormatUint(t, 10))
	default:
		buf.WriteString("null")
	}
}

func writeString(buf *bytes.Buffer, s string) {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	buf.Truncate(buf.Len() - 1)
}

func canonTree(m obj) []byte {
	var buf bytes.Buffer
	writeCanon(&buf, m)
	return buf.Bytes()
}

// Canonical returns the request's one deterministic encoding: sorted keys, no
// floats, no HTML escaping, no insignificant whitespace, optional fields that
// are empty omitted, arrays in their given order. Parse(Canonical(x)) equals x
// for a valid request, and Canonical of a parsed document is stable. A nil
// request encodes as null.
func Canonical(req *Request) []byte {
	if req == nil {
		return []byte("null")
	}
	return canonTree(req.tree())
}

func sum(b []byte) Digest {
	h := sha256.Sum256(b)
	return Digest(hex.EncodeToString(h[:]))
}

// Hash is the SHA-256 of the request's canonical bytes.
func Hash(req *Request) Digest { return sum(Canonical(req)) }

// SameRequest says whether two requests are the same by comparing their
// canonical bytes: recordedCanonical is what was stored for an operation
// identity, incomingCanonical is Canonical of the request now arriving. It
// compares bytes and nothing else: a caller-supplied digest, never trusted to
// stand for the bytes, is not an input. Empty input is never the same request.
func SameRequest(recordedCanonical, incomingCanonical []byte) bool {
	return len(recordedCanonical) > 0 && bytes.Equal(recordedCanonical, incomingCanonical)
}

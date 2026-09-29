package request

import (
	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// The canonical form of a request is card.Encode of the tree these functions
// build. Arrays whose order carries no meaning (admissions, lifecycle inputs and
// evidence entries by card ID, replacements by the old card's ID, records by
// their record ID, the IDs of a scope and the dependencies of a card) are
// card.Set values: the encoder sorts them, so two requests that list the same
// entries in another order have the same bytes and the same hash. An empty
// optional field and an absent one are the same bytes: the key is left out.

func (e Expect) tree() card.Obj {
	p := card.Obj{}
	p.Str("row", e.Place.Row)
	p.Str("col", string(e.Place.Col))
	m := card.Obj{"place": p}
	m.Str("revision", e.Revision)
	return m
}

func (a Admission) tree() card.Obj {
	m := card.Obj{}
	m.Str("id", string(a.ID))
	m.Str("digest", string(a.Digest))
	m.Str("object_id", a.ObjectID)
	m.Str("commit", a.Commit)
	m.Str("repository", string(a.Repository))
	m.Str("path", a.Path)
	m.Str("kind", a.Kind)
	m.OptSet("depends_on", card.Strings(a.DependsOn))
	m.Str("entry", a.Entry)
	m.Str("title", a.Title)
	m.Str("row", a.Row)
	m.Str("policy_version", a.PolicyVersion)
	m.Str("policy_digest", string(a.PolicyDigest))
	return m
}

func (e Input) tree() card.Obj {
	m := card.Obj{"expect": e.Expect.tree()}
	m.Str("id", string(e.ID))
	m.Str("type", string(e.Type))
	m.Str("digest", string(e.Digest))
	m.Str("issuer", e.Issuer)
	m.Str("source", e.Source)
	m.Str("head", e.Head)
	m.Str("result", string(e.Result))
	m.Str("reason", e.Reason)
	m.Str("dependency", string(e.Dependency))
	m.Str("landing", e.Landing)
	return m
}

func (r Record) tree() card.Obj {
	m := card.Obj{}
	m.Str("kind", string(r.Kind))
	m.Str("issuer", r.Issuer)
	m.Str("disposition", string(r.Disposition))
	m.Str("head", r.Head)
	m.Str("digest", string(r.Def))
	m.Str("verifier", r.Verifier)
	m.Str("artifact", r.Artifact)
	return m
}

func (e Evidence) tree() card.Obj {
	m := card.Obj{"expect": e.Expect.tree()}
	m.Str("id", string(e.ID))
	if e.Records != nil {
		// Records are keyed by their record ID: the same records in another
		// order are the same evidence.
		byID := make(map[string]string, len(e.Records))
		items := make([]any, len(e.Records))
		for i, r := range e.Records {
			t := r.tree()
			items[i] = t
			byID[string(card.Encode(t))] = r.ID()
		}
		m["records"] = card.Set{Items: items, KeyOf: func(v any) string { return byID[string(card.Encode(v))] }}
	}
	return m
}

func (r Replacement) tree() card.Obj {
	old := card.Obj{"expect": r.Old.Expect.tree()}
	old.Str("id", string(r.Old.ID))
	old.Str("digest", string(r.Old.Digest))
	return card.Obj{"old": old, "new": r.New.tree()}
}

func (s Scope) tree() card.Obj {
	m := card.Obj{}
	m.OptSet("ids", card.Strings(s.IDs))
	m.OptSet("rows", card.Strings(s.Rows))
	if s.All {
		m["all"] = true
	}
	return m
}

// set builds a Set of the entries of an array, keyed by an entry field.
func set[T any](list []T, key string, tree func(T) card.Obj) card.Set {
	items := make([]any, len(list))
	for i, e := range list {
		items[i] = tree(e)
	}
	return card.Set{Key: key, Items: items}
}

func (r *Request) tree(withOperationID bool) card.Obj {
	m := card.Obj{"schema": r.Schema}
	m.Str("operation", string(r.Operation))
	m.Str("table", r.Table)
	m.Str("epoch", r.Epoch)
	m.Str("expected_table_revision", r.TableRevision)
	if withOperationID {
		m.Str("operation_id", r.OperationID)
	}
	m.Str("actor", r.Actor)
	if r.Admissions != nil {
		m["admissions"] = set(r.Admissions, "id", Admission.tree)
	}
	if r.Inputs != nil {
		m["inputs"] = set(r.Inputs, "id", Input.tree)
	}
	if r.Evidence != nil {
		m["evidence"] = set(r.Evidence, "id", Evidence.tree)
	}
	if r.Replacements != nil {
		s := set(r.Replacements, "", Replacement.tree)
		s.KeyOf = func(v any) string { return v.(card.Obj)["old"].(card.Obj)["id"].(string) }
		m["replacements"] = s
	}
	if r.Scope != nil {
		m["scope"] = r.Scope.tree()
	}
	return m
}

// Canonical returns the request's one deterministic encoding: sorted keys, no
// floats, no HTML escaping, no insignificant whitespace, empty optional fields
// left out, order-free arrays sorted. Parse(Canonical(x)) has the same Canonical
// as x for a valid request. A nil request encodes as null.
func Canonical(req *Request) []byte {
	if req == nil {
		return []byte("null")
	}
	return card.Encode(req.tree(true))
}

// CanonicalWithoutOperationID is Canonical with the operation ID left out: the
// bytes of what is asked, whatever it is called.
func CanonicalWithoutOperationID(req *Request) []byte {
	if req == nil {
		return []byte("null")
	}
	return card.Encode(req.tree(false))
}

func sum(b []byte) Digest { return card.Sum(b) }

// Hash is the SHA-256 of the request's canonical bytes.
func Hash(req *Request) Digest { return sum(Canonical(req)) }

// HashWithoutOperationID is the SHA-256 of the request's canonical bytes with the
// operation ID left out. Two requests that ask the same thing under different
// operation IDs have one; it is the source of a default operation ID.
func HashWithoutOperationID(req *Request) Digest { return sum(CanonicalWithoutOperationID(req)) }

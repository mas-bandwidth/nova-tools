package records

import "sort"

// The construction half of the boundary: how a caller outside this package builds a record
// and gets the bytes to write, without reaching a private field and without hand-rolling
// JSON.
//
// Until this file the package could only READ. Object's members are private and its setter
// is unexported; Canonicalize, ContentID and Seal take a Value, and the only producer of a
// Value was the unexported strict parser. So the package exported a sealer nobody outside
// it could feed. That left an adapter two ways in, and the record contract's owner ruled
// out both: assemble the JSON by hand in every adapter -- the format's "Never serialize an
// arbitrary source object" becoming string concatenation with a fresh escaping bug per
// harness -- or move the adapter inside this package to reach the private fields, which
// would put six provider decoders in the one package that is supposed to have none.
//
// The way out is the rule this family already runs on: THE SERIALIZER IS THE PARSER'S
// INVERSE (lesson 113, and the same rule that makes `nova-tokens report` write exactly
// what `fold --bus` parses). Observation is what the validator produced; Body writes the
// body that validator would have read; SealObservation seals it and then reads its own
// bytes back through ValidateEnvelope before returning them. A record this package writes
// is therefore a record this package accepts, by construction rather than by review, and
// the fixtures prove the round trip is byte-identical -- which is the property that matters,
// because a byte that moves moves the ID, and an ID that moves is a second identity for one
// spend event.
//
// What is deliberately NOT here:
//
//   - No JSON struct tags on Observation, and no encoding/json marshalling of it. The wire
//     is RFC 8785 canonical JSON: members ordered by UTF-16 code unit, exactly seven escapes
//     and \u00xx for the other control characters, and no raw JSON numbers at all.
//     encoding/json escapes <, > and & as <, > and & by default, emits map
//     keys in its own order, and turns a Go number into a number rather than a lexeme. Tags
//     would produce bytes that look right and hash differently, which is the single worst
//     failure this format can have. The canonicaliser is the one writer, and it stays that
//     way.
//   - No repair. Body writes what the observation says. An unsorted model_usage, an enum
//     outside its set, a field outside the mapping's allowlist: each is refused by name,
//     with the same rule and the same field a record arriving over the wire would get,
//     because SealObservation validates the bytes it just wrote. Nothing is sorted into
//     place, defaulted or dropped on the way out.
//   - No adapters. What a claude_code transcript line or an Antigravity blob MEANS is a
//     mapping's business and its owner's; this file only gives that owner a wire.

// ObservationMembers are the twelve members of a nova.tokens.observation/2 body, in the
// order the format's table writes them. The canonicaliser sorts, so the order here is for a
// person reading a diagnostic; the LIST is load-bearing, and the validator's exact-keys
// check reads it from here so a member cannot be added to one side alone.
var ObservationMembers = []string{
	"schema", "source", "kind", "revision", "time", "origin", "model",
	"repository", "raw_usage", "model_usage", "mapping_id", "receipt",
}

// NewObject returns an empty object to build a body in. It is the only exported way to make
// one, and it exists so a caller can construct the Value that Canonicalize, ContentID and
// Seal have always taken.
func NewObject() *Object { return &Object{} }

// Set adds one member and returns a refusal rather than overwriting.
//
// A name already present is RuleDuplicateKey, the same rule the strict parser applies to
// the bytes: a writer that kept the last of two would produce a body its own reader refuses,
// and the two sides of one grammar would disagree about a record that already had an ID.
// The first value stands; a refused Set changes nothing.
//
// A value with no wire form -- a Go map, a struct, an int -- is refused HERE, naming the
// member. The canonicaliser would refuse it too, three levels down, by which point the
// diagnostic says "the value has no canonical form" about a path and not about a field a
// caller can find.
func (o *Object) Set(name string, v Value) error {
	if _, dup := o.vals[name]; dup {
		return refuse(RuleDuplicateKey, name, "the member is already set; a body carries one of each")
	}
	if err := checkWireForm(v, name); err != nil {
		return err
	}
	o.set(name, v)
	return nil
}

// Strings is the array member a caller wants most: event_key, supersedes and touched are all
// arrays of strings. An empty one seals as [] and never as null -- the format says "Empty
// means no split supplied, not no usage", and a null where an array belongs is a different
// fact. (A nil []Value would also canonicalise as [], because the dynamic type is what the
// canonicaliser switches on; the slice is returned non-nil for the caller who inspects it,
// not to fix the bytes, and a test measured that before this sentence was written.)
func Strings(ss []string) []Value {
	out := make([]Value, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// checkWireForm walks a value and refuses anything the canonical form has no writing for.
// json.Number is refused by its own rule: this format's bodies carry no raw JSON numbers,
// so a number here is a caller reaching for the wrong type for a usage value.
func checkWireForm(v Value, field string) error {
	switch t := v.(type) {
	case nil, bool, string:
		return nil
	case []Value:
		for i, e := range t {
			if err := checkWireForm(e, indexPath(field, i)); err != nil {
				return err
			}
		}
		return nil
	case *Object:
		if t == nil {
			return refuse(RuleWrongType, field, "the object is nil; an empty object is NewObject()")
		}
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			if err := checkWireForm(val, field+"."+k); err != nil {
				return err
			}
		}
		return nil
	}
	if _, ok := v.(interface{ String() string }); ok {
		// json.Number lands here, and so would any other stringer a caller reached for.
		return refuse(RuleRawJSONNumber, field, "usage and identity values are JSON strings, never numbers")
	}
	return refuse(RuleNotJSON, field, "the value has no canonical form; a member is null, a bool, a string, []Value or *Object")
}

// builder accumulates the first refusal so an encoder reads as a list of members rather than
// as a stack of error checks. The first refusal is the one returned: a body that failed at
// its third member has nothing useful to say about its ninth.
type builder struct {
	o   *Object
	err error
}

func newBuilder() *builder { return &builder{o: NewObject()} }

func (b *builder) set(name string, v Value) {
	if b.err != nil {
		return
	}
	if err := b.o.Set(name, v); err != nil {
		b.err = err
	}
}

// setObject nests a sub-object built by f, carrying its refusal up.
func (b *builder) setObject(name string, f func(*builder)) {
	if b.err != nil {
		return
	}
	sub := newBuilder()
	f(sub)
	if sub.err != nil {
		b.err = sub.err
		return
	}
	b.set(name, sub.o)
}

func (b *builder) done() (Value, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.o, nil
}

// nullable writes a *string as its string or as null. The two are different facts and the
// format keeps them apart everywhere: a null producer_version is "the source does not say",
// and an omitted one is a body that does not fit the schema.
func nullable(s *string) Value {
	if s == nil {
		return nil
	}
	return *s
}

// Body returns the observation's body, the value Seal and ContentID take. It is the inverse
// of the validator's read: every member the schema names is written, present even when null,
// and nothing else is.
func (o Observation) Body() (Value, error) {
	b := newBuilder()
	b.set("schema", o.Schema)
	b.setObject("source", func(s *builder) {
		s.set("kind", o.Source.Kind)
		s.set("producer_version", nullable(o.Source.ProducerVersion))
		s.set("namespace", o.Source.Namespace)
		s.set("session_id", o.Source.SessionID)
		s.set("event_key", Strings(o.Source.EventKey))
	})
	b.set("kind", o.Kind)
	b.setObject("revision", func(s *builder) {
		s.set("native", nullable(o.Revision.Native))
		s.set("supersedes", Strings(o.Revision.Supersedes))
		s.set("basis", o.Revision.Basis)
	})
	b.setObject("time", func(s *builder) {
		s.set("occurred_at", nullable(o.Time.OccurredAt))
		s.set("start", nullable(o.Time.Start))
		s.set("end", nullable(o.Time.End))
		s.set("basis", o.Time.Basis)
	})
	b.setObject("origin", func(s *builder) {
		s.set("friend", nullable(o.Origin.Friend))
		s.set("bench", nullable(o.Origin.Bench))
		s.set("basis", o.Origin.Basis)
		s.set("binding_id", nullable(o.Origin.BindingID))
	})
	b.setObject("model", func(s *builder) {
		s.set("id", nullable(o.Model.ID))
		s.set("basis", o.Model.Basis)
	})
	b.setObject("repository", func(s *builder) {
		s.set("id", nullable(o.Repository.ID))
		s.set("basis", o.Repository.Basis)
		s.set("policy_id", nullable(o.Repository.PolicyID))
		s.set("touched", Strings(o.Repository.Touched))
	})
	b.setObject("raw_usage", func(s *builder) { writeRawUsage(s, o.RawUsage) })
	// model_usage is written in the order the caller holds it, NOT sorted into place. The
	// format requires it sorted by model_id with duplicates refused; sorting here would be
	// a repair, and this package repairs nothing. An unsorted slice is refused by name when
	// SealObservation reads its own bytes back.
	usage := make([]Value, 0, len(o.ModelUsage))
	for _, mu := range o.ModelUsage {
		sub := newBuilder()
		sub.set("model_id", mu.ModelID)
		sub.setObject("raw_usage", func(s *builder) { writeRawUsage(s, mu.RawUsage) })
		if sub.err != nil {
			return nil, sub.err
		}
		usage = append(usage, sub.o)
	}
	b.set("model_usage", usage)
	b.set("mapping_id", o.MappingID)
	b.setObject("receipt", func(s *builder) {
		for _, k := range sortedKeys(o.Receipt) {
			s.set(k, o.Receipt[k])
		}
	})
	return b.done()
}

// writeRawUsage writes one raw_usage map: every field the observation carries, each with all
// five members. An absent or unavailable field is written WITH its null value and its reason
// rather than omitted, because "Every supported field has an entry, including absent fields"
// -- and because an omitted absent field would come back as a field the mapping never had,
// which is a different claim about the source.
func writeRawUsage(b *builder, fields map[string]RawField) {
	for _, name := range sortedKeys(fields) {
		f := fields[name]
		b.setObject(name, func(s *builder) {
			s.set("presence", f.Presence)
			s.set("value", nullable(f.Value))
			s.set("number_kind", f.NumberKind)
			s.set("unit", f.Unit)
			s.set("reason", nullable(f.Reason))
		})
	}
}

// sortedKeys orders a map's keys so the Keys() a diagnostic reports are stable. The digest
// does not depend on it -- the canonicaliser sorts by UTF-16 code unit whatever order the
// members were set in -- so this is for the person reading the refusal.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SealObservation is the one call an adapter needs: the envelope bytes and the ID for an
// observation it has assembled.
//
// It seals and then VALIDATES ITS OWN BYTES through ValidateEnvelope, under this validator's
// mapping allowlists. So a record that would be refused at the publisher boundary is refused
// here instead, with the same rule and the same field named, before it reaches a shard --
// and the writer cannot drift from the reader, because the writer runs the reader. The bytes
// returned have already passed the boundary; nothing else needs to trust them.
//
// The returned bytes are the envelope exactly as it goes on the wire: canonical, with no
// trailing newline. A shard writer adds the newline that separates JSONL records; it is not
// part of the digest.
func (v *Validator) SealObservation(o Observation) ([]byte, string, error) {
	body, err := o.Body()
	if err != nil {
		return nil, "", err
	}
	raw, id, err := Seal(body)
	if err != nil {
		return nil, "", err
	}
	if _, err := v.ValidateEnvelope(raw); err != nil {
		return nil, "", err
	}
	return raw, id, nil
}

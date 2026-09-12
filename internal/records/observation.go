package records

import (
	"regexp"
	"time"
)

// The shapes a caller gets back once a record is accepted. They are plain data: cmd
// /nova-tokens can import this package and read an accepted observation without touching
// the parse tree, and nothing here is a handle to anything on disk.
type (
	// Envelope is a validated {"id":..., "body":...}.
	Envelope struct {
		ID          string
		Body        *Object
		Observation *Observation
	}

	// Source is source: the declared shape and the identity within it.
	Source struct {
		Kind            string
		ProducerVersion *string
		Namespace       string
		SessionID       string
		EventKey        []string
	}

	// Revision is revision: the predecessors this observation supersedes and why.
	Revision struct {
		Native     *string
		Supersedes []string
		Basis      string
	}

	// Times keeps the ORIGINAL offset lexemes. Reporting converts known instants to UTC;
	// this package does not, because a converted timestamp has lost the offset that said
	// which day the source meant.
	Times struct {
		OccurredAt *string
		Start      *string
		End        *string
		Basis      string
	}

	// Origin is whose work and where it ran. The collector host never supplies it.
	Origin struct {
		Friend    *string
		Bench     *string
		Basis     string
		BindingID *string
	}

	// Model is the source model ID with the basis that says how well it is known.
	Model struct {
		ID    *string
		Basis string
	}

	// Repository is the repository identity and the evidence that assigned it.
	Repository struct {
		ID       *string
		Basis    string
		PolicyID *string
		Touched  []string
	}

	// ModelUsage is one entry of the source-provided per-model split.
	ModelUsage struct {
		ModelID  string
		RawUsage map[string]RawField
	}

	// Observation is a validated nova.tokens.observation/2 body.
	Observation struct {
		Schema     string
		Source     Source
		Kind       string
		Revision   Revision
		Time       Times
		Origin     Origin
		Model      Model
		Repository Repository
		RawUsage   map[string]RawField
		ModelUsage []ModelUsage
		MappingID  string
		Receipt    map[string]string
	}
)

// The three lexemes the format fixes by grammar.
var (
	// labelLexeme is the portable friend/bench/repository label: [a-z0-9][a-z0-9-]{0,31}.
	labelLexeme = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// contentIDLexeme is "sha256:" and 64 LOWERCASE hex digits. Uppercase is refused
	// rather than folded, because two spellings of one digest are two identities.
	contentIDLexeme = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// namespaceLexeme is the mapping-defined stable source domain; a dotted label so it
	// can never be a path to open.
	namespaceLexeme = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// offsetTimestamp is RFC 3339 with an explicit offset or Z. A local time with no
	// offset is refused: the format says preserve source offset, and there is no offset
	// to preserve in one.
	offsetTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	// noOffsetTimestamp is the same instant written without one, checked only so the
	// refusal can name the missing offset instead of a generic syntax failure.
	noOffsetTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?$`)
)

// A Validator performs the publisher boundary's structural validation. It holds the
// mapping-supplied allowlists and nothing else: it opens no file, resolves no path and
// loads no code, which is the property the format asks of the publisher.
type Validator struct {
	allow Allowlists
	skip  map[string]bool
}

// NewValidator returns a validator over one mapping's allowlists.
func NewValidator(a Allowlists) *Validator { return &Validator{allow: a} }

// Without returns a copy of the validator with the named rules not enforced. It exists for
// exactly one caller: the test that proves each refusal rule is load-bearing by showing
// the fixture that fails only because of it. It is never used by the publisher; a record
// that fails is refused with the field named, never repaired and never waved through.
func (v *Validator) Without(rules ...string) *Validator {
	cp := &Validator{allow: v.allow, skip: map[string]bool{}}
	for k := range v.skip {
		cp.skip[k] = true
	}
	for _, r := range rules {
		cp.skip[r] = true
	}
	return cp
}

func (v *Validator) refused(rule, field, note string) error {
	if v.skip[rule] {
		return nil
	}
	return refuse(rule, field, note)
}

// ValidateEnvelope is the boundary. It takes the bytes as they were written, refuses with
// the field named, and returns the accepted record otherwise. It NEVER returns a repaired
// record: there is no path through this function that edits a value.
func (v *Validator) ValidateEnvelope(raw []byte) (*Envelope, error) {
	parsed, err := parseStrict(raw, v.skip, "envelope")
	if err != nil {
		return nil, err
	}
	obj, ok := parsed.(*Object)
	if !ok {
		return nil, refuse(RuleEnvelopeShape, "envelope", "an envelope is a JSON object")
	}
	if err := v.exactKeys(obj, "envelope", "id", "body"); err != nil {
		return nil, err
	}
	idv, _ := obj.Get("id")
	id, ok := idv.(string)
	if !ok {
		return nil, refuse(RuleWrongType, "envelope.id", "the ID is a string")
	}
	if !contentIDLexeme.MatchString(id) {
		if err := v.refused(RuleEnvelopeIDSyntax, "envelope.id", "an ID is sha256: and 64 lowercase hex digits"); err != nil {
			return nil, err
		}
	}
	bodyv, _ := obj.Get("body")
	body, ok := bodyv.(*Object)
	if !ok {
		return nil, refuse(RuleEnvelopeShape, "envelope.body", "a body is a JSON object")
	}
	want, err := contentID(body, v.skip)
	if err != nil {
		return nil, err
	}
	if want != id {
		if err := v.refused(RuleEnvelopeIDMismatch, "envelope.id", "the ID is not the digest of the body's canonical JSON"); err != nil {
			return nil, err
		}
	}
	obs, err := v.validateObservation(body)
	if err != nil {
		return nil, err
	}
	return &Envelope{ID: id, Body: body, Observation: obs}, nil
}

// Seal returns the envelope bytes for a body: the ID derived from the body's canonical
// JSON, and the body itself in canonical form. It is the only writer here, and it writes
// no trailing newline.
func Seal(body Value) ([]byte, string, error) {
	id, err := ContentID(body)
	if err != nil {
		return nil, "", err
	}
	canon, err := Canonicalize(body)
	if err != nil {
		return nil, "", err
	}
	out := append([]byte(`{"id":`), []byte(`"`+id+`"`)...)
	out = append(out, []byte(`,"body":`)...)
	out = append(out, canon...)
	out = append(out, '}')
	return out, id, nil
}

func (v *Validator) validateObservation(body *Object) (*Observation, error) {
	schemav, ok := body.Get("schema")
	if !ok {
		return nil, refuse(RuleMissingField, "body.schema", "a body names its schema")
	}
	schema, _ := schemav.(string)
	if schema != SchemaObservation {
		if err := v.refused(RuleUnknownSchema, "body.schema", "the only supported body schema is "+SchemaObservation); err != nil {
			return nil, err
		}
	}
	if err := v.exactKeys(body, "body",
		"schema", "source", "kind", "revision", "time", "origin", "model",
		"repository", "raw_usage", "model_usage", "mapping_id", "receipt"); err != nil {
		return nil, err
	}
	obs := &Observation{Schema: schema}
	var err error
	if obs.Source, err = v.source(body); err != nil {
		return nil, err
	}
	if obs.Kind, err = v.enumField(body, "body", "kind", observationKinds); err != nil {
		return nil, err
	}
	if obs.Revision, err = v.revision(body); err != nil {
		return nil, err
	}
	if obs.Time, err = v.times(body); err != nil {
		return nil, err
	}
	if obs.Origin, err = v.origin(body); err != nil {
		return nil, err
	}
	if obs.Model, err = v.model(body); err != nil {
		return nil, err
	}
	if obs.Repository, err = v.repository(body); err != nil {
		return nil, err
	}
	ru, err := v.object(body, "body", "raw_usage")
	if err != nil {
		return nil, err
	}
	if obs.RawUsage, err = v.rawUsage(ru, "body.raw_usage"); err != nil {
		return nil, err
	}
	if obs.ModelUsage, err = v.modelUsage(body); err != nil {
		return nil, err
	}
	if obs.MappingID, err = v.contentIDField(body, "body", "mapping_id"); err != nil {
		return nil, err
	}
	if obs.Receipt, err = v.receipt(body); err != nil {
		return nil, err
	}
	return obs, nil
}

func (v *Validator) source(body *Object) (Source, error) {
	var s Source
	o, err := v.object(body, "body", "source")
	if err != nil {
		return s, err
	}
	p := "body.source"
	if err := v.exactKeys(o, p, "kind", "producer_version", "namespace", "session_id", "event_key"); err != nil {
		return s, err
	}
	if s.Kind, err = v.stringField(o, p, "kind"); err != nil {
		return s, err
	}
	if !sourceKinds[s.Kind] {
		if err := v.refused(RuleUnknownSourceKind, p+".kind", "the kind is not one this format names"); err != nil {
			return s, err
		}
	}
	if s.ProducerVersion, err = v.nullableString(o, p, "producer_version"); err != nil {
		return s, err
	}
	if s.Namespace, err = v.stringField(o, p, "namespace"); err != nil {
		return s, err
	}
	if !namespaceLexeme.MatchString(s.Namespace) {
		if err := v.refused(RuleNamespaceSyntax, p+".namespace", "a namespace is a dotted lowercase label, never a path"); err != nil {
			return s, err
		}
	}
	if s.SessionID, err = v.stringField(o, p, "session_id"); err != nil {
		return s, err
	}
	if s.SessionID == "" {
		if err := v.refused(RuleEmptyString, p+".session_id", "session identity is provenance and is not empty"); err != nil {
			return s, err
		}
	}
	keys, err := v.array(o, p, "event_key")
	if err != nil {
		return s, err
	}
	if len(keys) == 0 {
		if err := v.refused(RuleEmptyArray, p+".event_key", "the spend key needs at least one native identity string"); err != nil {
			return s, err
		}
	}
	for i, k := range keys {
		str, ok := k.(string)
		if !ok {
			return s, refuse(RuleWrongType, indexPath(p+".event_key", i), "a native identity is a string, never a number")
		}
		if str == "" {
			if err := v.refused(RuleEmptyString, indexPath(p+".event_key", i), "a native identity is not empty"); err != nil {
				return s, err
			}
		}
		s.EventKey = append(s.EventKey, str)
	}
	return s, nil
}

func (v *Validator) revision(body *Object) (Revision, error) {
	var r Revision
	o, err := v.object(body, "body", "revision")
	if err != nil {
		return r, err
	}
	p := "body.revision"
	if err := v.exactKeys(o, p, "native", "supersedes", "basis"); err != nil {
		return r, err
	}
	if r.Native, err = v.nullableString(o, p, "native"); err != nil {
		return r, err
	}
	if r.Basis, err = v.enumField(o, p, "basis", revisionBases); err != nil {
		return r, err
	}
	ids, err := v.array(o, p, "supersedes")
	if err != nil {
		return r, err
	}
	for i, e := range ids {
		str, ok := e.(string)
		if !ok {
			return r, refuse(RuleWrongType, indexPath(p+".supersedes", i), "a predecessor is an observation ID string")
		}
		if !contentIDLexeme.MatchString(str) {
			if err := v.refused(RuleContentIDSyntax, indexPath(p+".supersedes", i), "a predecessor is sha256: and 64 lowercase hex digits"); err != nil {
				return r, err
			}
		}
		r.Supersedes = append(r.Supersedes, str)
	}
	if err := v.sortedUnique(r.Supersedes, p+".supersedes"); err != nil {
		return r, err
	}
	return r, nil
}

func (v *Validator) times(body *Object) (Times, error) {
	var t Times
	o, err := v.object(body, "body", "time")
	if err != nil {
		return t, err
	}
	p := "body.time"
	if err := v.exactKeys(o, p, "occurred_at", "start", "end", "basis"); err != nil {
		return t, err
	}
	if t.Basis, err = v.enumField(o, p, "basis", timeBases); err != nil {
		return t, err
	}
	for _, name := range []string{"occurred_at", "start", "end"} {
		s, err := v.nullableString(o, p, name)
		if err != nil {
			return t, err
		}
		if s != nil {
			if err := v.timestamp(*s, p+"."+name); err != nil {
				return t, err
			}
		}
		switch name {
		case "occurred_at":
			t.OccurredAt = s
		case "start":
			t.Start = s
		case "end":
			t.End = s
		}
	}
	// An interval is both ends or neither: half of one allocates nothing and cannot be
	// checked, and the format's unallocated case is exactly the one with no interval.
	if (t.Start == nil) != (t.End == nil) {
		if err := v.refused(RuleIntervalIncomplete, p, "an interval carries both start and end or neither"); err != nil {
			return t, err
		}
	}
	if t.Start != nil && t.End != nil {
		start, errA := time.Parse(time.RFC3339, *t.Start)
		end, errB := time.Parse(time.RFC3339, *t.End)
		if errA == nil && errB == nil && !end.After(start) {
			if err := v.refused(RuleIntervalOrder, p+".end", "an interval end is after its start; the end is exclusive"); err != nil {
				return t, err
			}
		}
	}
	return t, nil
}

func (v *Validator) timestamp(s, field string) error {
	if offsetTimestamp.MatchString(s) {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return v.refused(RuleTimestampSyntax, field, "the timestamp is not a real instant")
		}
		return nil
	}
	if noOffsetTimestamp.MatchString(s) {
		return v.refused(RuleTimestampNoOffset, field, "a timestamp carries its source offset or Z")
	}
	return v.refused(RuleTimestampSyntax, field, "a timestamp is RFC 3339 with an offset")
}

func (v *Validator) origin(body *Object) (Origin, error) {
	var o Origin
	obj, err := v.object(body, "body", "origin")
	if err != nil {
		return o, err
	}
	p := "body.origin"
	if err := v.exactKeys(obj, p, "friend", "bench", "basis", "binding_id"); err != nil {
		return o, err
	}
	if o.Friend, err = v.nullableLabel(obj, p, "friend"); err != nil {
		return o, err
	}
	if o.Bench, err = v.nullableLabel(obj, p, "bench"); err != nil {
		return o, err
	}
	if o.Basis, err = v.enumField(obj, p, "basis", originBases); err != nil {
		return o, err
	}
	if o.BindingID, err = v.nullableString(obj, p, "binding_id"); err != nil {
		return o, err
	}
	return o, nil
}

func (v *Validator) model(body *Object) (Model, error) {
	var m Model
	o, err := v.object(body, "body", "model")
	if err != nil {
		return m, err
	}
	p := "body.model"
	if err := v.exactKeys(o, p, "id", "basis"); err != nil {
		return m, err
	}
	if m.ID, err = v.nullableString(o, p, "id"); err != nil {
		return m, err
	}
	if m.Basis, err = v.enumField(o, p, "basis", modelBases); err != nil {
		return m, err
	}
	return m, nil
}

func (v *Validator) repository(body *Object) (Repository, error) {
	var r Repository
	o, err := v.object(body, "body", "repository")
	if err != nil {
		return r, err
	}
	p := "body.repository"
	if err := v.exactKeys(o, p, "id", "basis", "policy_id", "touched"); err != nil {
		return r, err
	}
	if r.ID, err = v.nullableString(o, p, "id"); err != nil {
		return r, err
	}
	if r.Basis, err = v.enumField(o, p, "basis", repoBases); err != nil {
		return r, err
	}
	if r.PolicyID, err = v.nullableString(o, p, "policy_id"); err != nil {
		return r, err
	}
	touched, err := v.array(o, p, "touched")
	if err != nil {
		return r, err
	}
	for i, e := range touched {
		str, ok := e.(string)
		if !ok {
			return r, refuse(RuleWrongType, indexPath(p+".touched", i), "a touched repository is a label string")
		}
		if !labelLexeme.MatchString(str) {
			if err := v.refused(RuleLabelSyntax, indexPath(p+".touched", i), "a label is [a-z0-9][a-z0-9-]{0,31}"); err != nil {
				return r, err
			}
		}
		r.Touched = append(r.Touched, str)
	}
	if err := v.sortedUnique(r.Touched, p+".touched"); err != nil {
		return r, err
	}
	return r, nil
}

func (v *Validator) rawUsage(o *Object, p string) (map[string]RawField, error) {
	allow := v.allow.rawUsage()
	out := map[string]RawField{}
	for i, name := range o.Keys() {
		if !allow[name] {
			if err := v.refused(RuleFieldNotAllowlisted, indexPath(p, i), "the field is not in the mapping's raw usage allowlist"); err != nil {
				return nil, err
			}
		}
		fo, err := v.object(o, p, name)
		if err != nil {
			return nil, err
		}
		fp := p + "." + name
		if err := v.exactKeys(fo, fp, "presence", "value", "number_kind", "unit", "reason"); err != nil {
			return nil, err
		}
		var f RawField
		if f.Presence, err = v.stringField(fo, fp, "presence"); err != nil {
			return nil, err
		}
		if !presences[f.Presence] {
			if err := v.refused(RuleUnknownPresence, fp+".presence", "presence is present, absent or unavailable"); err != nil {
				return nil, err
			}
		}
		if f.NumberKind, err = v.stringField(fo, fp, "number_kind"); err != nil {
			return nil, err
		}
		if !numberKinds[f.NumberKind] {
			if err := v.refused(RuleUnknownNumberKind, fp+".number_kind", "number_kind is integer or decimal"); err != nil {
				return nil, err
			}
		}
		if f.Unit, err = v.stringField(fo, fp, "unit"); err != nil {
			return nil, err
		}
		if !unitLexeme.MatchString(f.Unit) {
			if err := v.refused(RuleUnitSyntax, fp+".unit", "a unit is a lowercase label"); err != nil {
				return nil, err
			}
		}
		if f.Value, err = v.nullableString(fo, fp, "value"); err != nil {
			return nil, err
		}
		if f.Reason, err = v.nullableString(fo, fp, "reason"); err != nil {
			return nil, err
		}
		if f.Presence == "present" {
			if f.Value == nil {
				if err := v.refused(RulePresentValueNull, fp+".value", "a present field carries its original lexeme"); err != nil {
					return nil, err
				}
			}
			if f.Reason != nil {
				if err := v.refused(RulePresentWithReason, fp+".reason", "a present field has no reason code"); err != nil {
					return nil, err
				}
			}
		} else {
			if f.Value != nil {
				if err := v.refused(RuleAbsentValueNotNull, fp+".value", "absent and unavailable require value:null"); err != nil {
					return nil, err
				}
			}
			if f.Reason == nil {
				if err := v.refused(RuleAbsentReasonMissing, fp+".reason", "absent and unavailable require a bounded reason code"); err != nil {
					return nil, err
				}
			} else if !reasonCodes[*f.Reason] {
				if err := v.refused(RuleUnknownReasonCode, fp+".reason", "the reason code is not one of the bounded set"); err != nil {
					return nil, err
				}
			}
		}
		if f.Value != nil {
			if err := checkValueLexeme(*f.Value, f.NumberKind, fp+".value"); err != nil {
				if rf, ok := err.(*Refusal); !ok || !v.skip[rf.Rule] {
					return nil, err
				}
			}
		}
		out[name] = f
	}
	if p == "body.raw_usage" {
		for _, req := range sortedSet(v.allow.RawUsageFields) {
			if _, ok := o.Get(req); !ok {
				if err := v.refused(RuleMissingField, p+"."+req, "the mapping requires every supported field to have an entry"); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

func (v *Validator) modelUsage(body *Object) ([]ModelUsage, error) {
	arr, err := v.array(body, "body", "model_usage")
	if err != nil {
		return nil, err
	}
	p := "body.model_usage"
	var out []ModelUsage
	var ids []string
	for i, e := range arr {
		ep := indexPath(p, i)
		o, ok := e.(*Object)
		if !ok {
			return nil, refuse(RuleWrongType, ep, "a model usage entry is an object")
		}
		if err := v.exactKeys(o, ep, "model_id", "raw_usage"); err != nil {
			return nil, err
		}
		id, err := v.stringField(o, ep, "model_id")
		if err != nil {
			return nil, err
		}
		if id == "" {
			if err := v.refused(RuleEmptyString, ep+".model_id", "a model usage entry names its model"); err != nil {
				return nil, err
			}
		}
		ru, err := v.object(o, ep, "raw_usage")
		if err != nil {
			return nil, err
		}
		fields, err := v.rawUsage(ru, ep+".raw_usage")
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		out = append(out, ModelUsage{ModelID: id, RawUsage: fields})
	}
	if err := v.sortedUnique(ids, p); err != nil {
		return nil, err
	}
	return out, nil
}

func (v *Validator) receipt(body *Object) (map[string]string, error) {
	o, err := v.object(body, "body", "receipt")
	if err != nil {
		return nil, err
	}
	p := "body.receipt"
	allow := v.allow.receipt()
	out := map[string]string{}
	for i, name := range o.Keys() {
		if !allow[name] {
			// This is the rule that keeps a prompt, a private filename or a source blob
			// out of a shared file: a receipt carries mapping-allowlisted native locator
			// fields and nothing else, and an unknown one is refused rather than dropped.
			if err := v.refused(RuleReceiptNotAllowed, indexPath(p, i), "a receipt carries only mapping-allowlisted native locator fields"); err != nil {
				return nil, err
			}
		}
		val, _ := o.Get(name)
		str, ok := val.(string)
		if !ok {
			loc := indexPath(p, i)
			if allow[name] {
				loc = p + "." + name
			}
			return nil, refuse(RuleWrongType, loc, "a receipt value is a string")
		}
		out[name] = str
	}
	return out, nil
}

// The small accessors. Each refuses with the field named rather than returning a zero
// value that a later line would mistake for data.

func (v *Validator) exactKeys(o *Object, path string, want ...string) error {
	allowed := set(want)
	for i, k := range o.Keys() {
		if !allowed[k] {
			if err := v.refused(RuleUnknownField, indexPath(path, i), "the field is outside this schema's allowlist"); err != nil {
				return err
			}
		}
	}
	for _, w := range sortedSet(want) {
		if _, ok := o.Get(w); !ok {
			if err := v.refused(RuleMissingField, path+"."+w, "the schema requires the field, present even when null"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *Validator) member(o *Object, path, name string) (Value, error) {
	val, ok := o.Get(name)
	if !ok {
		if v.skip[RuleMissingField] {
			// Only reachable from the load-bearing test, which asks what a reader that
			// did not require the field would see: the same thing it sees for null.
			return nil, nil
		}
		return nil, refuse(RuleMissingField, path+"."+name, "the schema requires the field")
	}
	return val, nil
}

func (v *Validator) object(o *Object, path, name string) (*Object, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return nil, err
	}
	obj, ok := val.(*Object)
	if !ok {
		return nil, refuse(RuleWrongType, path+"."+name, "the field is an object")
	}
	return obj, nil
}

func (v *Validator) array(o *Object, path, name string) ([]Value, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return nil, err
	}
	arr, ok := val.([]Value)
	if !ok {
		return nil, refuse(RuleWrongType, path+"."+name, "the field is an array")
	}
	return arr, nil
}

func (v *Validator) stringField(o *Object, path, name string) (string, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return "", err
	}
	s, ok := val.(string)
	if !ok {
		return "", refuse(RuleWrongType, path+"."+name, "the field is a string")
	}
	return s, nil
}

func (v *Validator) nullableString(o *Object, path, name string) (*string, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, refuse(RuleWrongType, path+"."+name, "the field is a string or null")
	}
	return &s, nil
}

func (v *Validator) nullableLabel(o *Object, path, name string) (*string, error) {
	s, err := v.nullableString(o, path, name)
	if err != nil || s == nil {
		return s, err
	}
	if !labelLexeme.MatchString(*s) {
		if err := v.refused(RuleLabelSyntax, path+"."+name, "a label is [a-z0-9][a-z0-9-]{0,31}"); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (v *Validator) enumField(o *Object, path, name string, allowed map[string]bool) (string, error) {
	s, err := v.stringField(o, path, name)
	if err != nil {
		return "", err
	}
	if !allowed[s] {
		if err := v.refused(RuleUnknownEnum, path+"."+name, "the value is not one this schema names"); err != nil {
			return "", err
		}
	}
	return s, nil
}

func (v *Validator) contentIDField(o *Object, path, name string) (string, error) {
	s, err := v.stringField(o, path, name)
	if err != nil {
		return "", err
	}
	if !contentIDLexeme.MatchString(s) {
		if err := v.refused(RuleContentIDSyntax, path+"."+name, "a content ID is sha256: and 64 lowercase hex digits"); err != nil {
			return "", err
		}
	}
	return s, nil
}

// sortedUnique enforces the format's "sorted unique" on a list of identities. Sorting is
// checked, never performed: a list arriving out of order is a record written against a
// different rule, and re-sorting it here would change the bytes the ID was taken over.
func (v *Validator) sortedUnique(ss []string, path string) error {
	for i := 1; i < len(ss); i++ {
		switch {
		case ss[i] == ss[i-1]:
			if err := v.refused(RuleDuplicateElement, indexPath(path, i), "the element occurs twice"); err != nil {
				return err
			}
		case ss[i] < ss[i-1]:
			if err := v.refused(RuleNotSorted, indexPath(path, i), "the list is sorted and unique"); err != nil {
				return err
			}
		}
	}
	return nil
}

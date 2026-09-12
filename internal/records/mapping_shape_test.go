package records

import (
	"strings"
	"testing"
)

// Can this wire carry the two source contracts proposed in #142
// (docs/MAPPING-TOKENS-CODEX.md and docs/MAPPING-TOKENS-GROK.md at 5e40d20)?
//
// THESE ARE NOT DECODERS. Nothing here reads a Codex rollout file or a Grok export; the two
// decoders are their contract author's and are not this lane's. What these tests do is build
// the observation each contract DESCRIBES, by hand from the contract's own sentences, seal it
// through the exported interface, and read it back -- so "the API can carry what the mappings
// define" is a measurement rather than a claim in a PR body. If a contract needs something
// the wire has no member for, the test that tries to build it is where that shows up, and it
// showed up in exactly one place (see the PR body's "Against the mapping contracts").
//
// The numbers are the contracts' own public ones: Codex's 120/80/30 producer regression
// fixture, and Grok's synthetic fixture, which its own document marks "invented values, not
// actual usage". No private session, export or transcript is touched by either test.

// The Codex contract, clause by clause:
//
//   - "Use its usage object once per stable namespaced response_id; retain thread/session/
//     turn IDs as provenance" -- the spend key is (namespace, [response_id]); the thread and
//     the turn ride in the receipt under mapping-allowlisted locator names, NOT in event_key,
//     because a key with the containing thread in it would make a resumed thread a second
//     spend for one response.
//   - "TokenUsageRecord carries no model field ... the available ID preserved as model.id and
//     model.basis=requested".
//   - "Preserve ... total_tokens exactly ... Do not add those subsets again" -- total_tokens is
//     its own retained raw field beside input and output, and this package does no arithmetic
//     at all, so there is no code path that could add a subset twice.
//   - The zero-detail caveat: "A stored raw 0 in those fields therefore does not prove the
//     provider measured zero. Preserve raw 0 and label its provenance" -- the raw zero is
//     retained as a PRESENT zero here, and what makes its normalisation unknown is the
//     mapping's field_rules.zero_semantics, which Normalize applies. The observation states
//     what was on the wire; the mapping states what it means.
//   - "Repository defaults to unattributed for this multi-repo sitting".
func TestTheWireCarriesTheCodexSourceContract(t *testing.T) {
	v := NewValidator(Allowlists{
		// A CLOSED allowlist, written here from the contract's field names rather than
		// derived from the record under test: "Unknown source fields are excluded, not
		// dynamically admitted into an allowlist derived from the input."
		RawUsageFields: []string{
			"input_tokens", "output_tokens", "total_tokens",
			"cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens",
		},
		ReceiptFields: []string{"response_id", "thread_id", "turn_id"},
	})
	occurred := "2026-09-12T00:05:00Z"
	requested := "gpt-5-codex"
	binding := "binding-studio-stella"
	producer := "Codex Desktop 0.154.0-alpha.6.2"
	// The producer's own regression fixture: response usage 120, 80, 30.
	in, out, total := "120", "80", "200"
	// "missing cache-write to 0, and absent output-details to reasoning 0": a raw present
	// zero whose measurement semantics the mapping, not the record, decides.
	zero := "0"
	reasoning := "30"

	obs := Observation{
		Schema: SchemaObservation,
		Source: Source{
			Kind:            "codex_desktop",
			ProducerVersion: &producer,
			Namespace:       "nova.codex-desktop",
			SessionID:       "sess-0199",
			EventKey:        []string{"resp_01J9Z"},
		},
		Kind:     "request",
		Revision: Revision{Basis: "none"},
		// "Keep the rollout observation timestamp with its meaning: the record's timestamp,
		// not a claimed exact request-start timestamp."
		Time:   Times{OccurredAt: &occurred, Basis: "response_observation"},
		Origin: Origin{Basis: "owner_binding", BindingID: &binding},
		Model:  Model{ID: &requested, Basis: "requested"},
		// No repo field in the source, and a multi-repo sitting.
		Repository: Repository{Basis: "unattributed"},
		RawUsage: map[string]RawField{
			"input_tokens":             {Presence: "present", Value: &in, NumberKind: "integer", Unit: "tokens"},
			"output_tokens":            {Presence: "present", Value: &out, NumberKind: "integer", Unit: "tokens"},
			"total_tokens":             {Presence: "present", Value: &total, NumberKind: "integer", Unit: "tokens"},
			"cached_input_tokens":      {Presence: "present", Value: &zero, NumberKind: "integer", Unit: "tokens"},
			"cache_write_input_tokens": {Presence: "present", Value: &zero, NumberKind: "integer", Unit: "tokens"},
			"reasoning_output_tokens":  {Presence: "present", Value: &reasoning, NumberKind: "integer", Unit: "tokens"},
		},
		MappingID: "sha256:" + strings.Repeat("cd", 32),
		// Thread and turn as provenance, in the receipt, out of the key.
		Receipt: map[string]string{"response_id": "resp_01J9Z", "thread_id": "thread-7", "turn_id": "3"},
	}

	raw, id, err := v.SealObservation(obs)
	if err != nil {
		t.Fatalf("the wire cannot carry the Codex contract's observation: %v", err)
	}
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the sealed record does not validate: %v", err)
	}
	if env.ID != id {
		t.Errorf("the envelope's ID is %s, the seal said %s", env.ID, id)
	}
	got := env.Observation

	// The spend key is the response, and the containing thread is not in it: "A fork may
	// carry an earlier response in a different containing session, so the containing session
	// cannot create fresh spend."
	if len(got.Source.EventKey) != 1 || got.Source.EventKey[0] != "resp_01J9Z" {
		t.Errorf("the spend key came back %v", got.Source.EventKey)
	}
	if got.Receipt["thread_id"] != "thread-7" || got.Receipt["turn_id"] != "3" {
		t.Errorf("the thread and turn provenance did not survive: %v", got.Receipt)
	}
	// The native numeric turn identifier is an exact decimal STRING, and the builder refuses
	// a number outright, so it cannot be written any other way.
	if err := NewObject().Set("turn_id", 3); err == nil {
		t.Error("the builder accepted a Go int; a native numeric identifier is an exact decimal string")
	}
	// The model is the configured one, labelled requested, never promoted.
	if got.Model.Basis != "requested" || got.Model.ID == nil || *got.Model.ID != requested {
		t.Errorf("the model came back %+v; the contract says model.basis=requested", got.Model)
	}
	// total_tokens is retained beside its components and nothing derived it.
	if got.RawUsage["total_tokens"].Value == nil || *got.RawUsage["total_tokens"].Value != total {
		t.Errorf("total_tokens came back %+v", got.RawUsage["total_tokens"])
	}
	// The defaulted zeros are retained as PRESENT zeros, and the mapping's zero semantics --
	// not the record -- decide whether that is a measurement. Under the contract's caveat the
	// rule for these two fields is default_may_mask_absence, and then it is not.
	for _, name := range []string{"cached_input_tokens", "cache_write_input_tokens"} {
		f := got.RawUsage[name]
		if !f.Present() || !f.IsZero() {
			t.Errorf("%s came back %+v, want a present zero", name, f)
		}
		if m := Normalize(f, ZeroMayMaskAbsent); m.Measured || m.SubsetComplete {
			t.Errorf("%s normalised as measured under the contract's zero-detail caveat: %+v", name, m)
		}
		if m := Normalize(f, ZeroMeasured); !m.Measured {
			t.Errorf("%s did not normalise as measured under measured zero semantics: %+v", name, m)
		}
	}
	// A positive detail count can be retained as measured.
	if m := Normalize(got.RawUsage["reasoning_output_tokens"], ZeroMayMaskAbsent); !m.Measured {
		t.Errorf("a positive reasoning count did not normalise as measured: %+v", m)
	}
	if got.Repository.Basis != "unattributed" || got.Repository.ID != nil {
		t.Errorf("the repository came back %+v, want unattributed with no id", got.Repository)
	}
	if got.Origin.Basis != "owner_binding" {
		t.Errorf("the origin basis came back %q; the collection host does not establish historical origin", got.Origin.Basis)
	}
	// And the closed allowlist is a wall: a source field the mapping did not name is refused,
	// never admitted because the input happened to carry it.
	unknown := "1"
	obs.RawUsage["context_window_left"] = RawField{Presence: "present", Value: &unknown, NumberKind: "integer", Unit: "tokens"}
	if _, _, err := v.SealObservation(obs); err == nil {
		t.Error("an unknown source field was admitted into the wire")
	}
}

// The Grok contract, clause by clause. Its synthetic fixture is the numbers below; the
// document states them as invented.
//
//   - "Candidate spend key: the namespaced native sessionId plus turnNumber" -- a two-part
//     event_key, the turn as an exact decimal string.
//   - "Preserve each turn at that available grain. Do not invent per-call usage" -- kind is
//     turn, and modelCalls is a retained raw field rather than a licence to split.
//   - "Keep primaryModelId and the source's modelUsage detail without collapsing it" -- the
//     turn's model with basis harness_reported, AND model_usage carrying the per-model detail.
//   - "cacheCreationTokens was always 0 ... Preserve that as a raw present zero ... normalized
//     detail zero remains unknown without the source contract."
//   - "Costs remain raw ticks with an unverified unit; no dollars are calculated."
//   - "No repo field exists: unattributed, with an optional separately evidenced touched list
//     carrying no allocated spend."
func TestTheWireCarriesTheGrokSourceContract(t *testing.T) {
	fields := []string{
		"inputTokens", "outputTokens", "cachedReadTokens", "cacheCreationTokens",
		"reasoningTokens", "totalTokens", "modelCalls", "costUsdTicks", "turnCount",
	}
	v := NewValidator(Allowlists{RawUsageFields: fields, ReceiptFields: []string{"turn_number"}})

	lex := map[string]string{
		"inputTokens": "1000", "outputTokens": "100", "cachedReadTokens": "800",
		"cacheCreationTokens": "0", "reasoningTokens": "40", "totalTokens": "1100",
		"modelCalls": "2", "costUsdTicks": "77", "turnCount": "1",
	}
	unit := map[string]string{
		"modelCalls": "calls", "costUsdTicks": "ticks", "turnCount": "turns",
	}
	usage := func(names ...string) map[string]RawField {
		out := map[string]RawField{}
		for _, n := range names {
			u := "tokens"
			if s, ok := unit[n]; ok {
				u = s
			}
			val := lex[n]
			out[n] = RawField{Presence: "present", Value: &val, NumberKind: "integer", Unit: u}
		}
		return out
	}

	ended := "2026-09-12T00:05:00Z"
	primary := "grok-model-example"
	friend, bench := "johnny", "johnny-box"
	obs := Observation{
		Schema: SchemaObservation,
		Source: Source{
			Kind:      "grok",
			Namespace: "nova.grok-usage",
			SessionID: "fixture-grok-session",
			// The session AND the turn number: a turn counter scoped to its session.
			EventKey: []string{"fixture-grok-session", "1"},
		},
		// The available grain is the turn, and its usage is already an aggregate of model
		// calls. Nothing here invents a per-call observation.
		Kind:     "turn",
		Revision: Revision{Basis: "none"},
		// "the report's daily basis is turn completion in UTC ... a labelled accounting
		// convention, not measured call-day allocation": the basis says which, on the record.
		Time:       Times{OccurredAt: &ended, Basis: "turn_completion"},
		Origin:     Origin{Friend: &friend, Bench: &bench, Basis: "owner_binding"},
		Model:      Model{ID: &primary, Basis: "harness_reported"},
		Repository: Repository{Basis: "unattributed", Touched: []string{"nova-tools"}},
		RawUsage:   usage(fields...),
		// The per-model detail, kept beside the aggregate and not collapsed into it.
		ModelUsage: []ModelUsage{{
			ModelID:  primary,
			RawUsage: usage("inputTokens", "outputTokens", "cachedReadTokens", "cacheCreationTokens", "reasoningTokens", "totalTokens", "modelCalls", "costUsdTicks"),
		}},
		MappingID: "sha256:" + strings.Repeat("9a", 32),
		Receipt:   map[string]string{"turn_number": "1"},
	}

	raw, _, err := v.SealObservation(obs)
	if err != nil {
		t.Fatalf("the wire cannot carry the Grok contract's observation: %v", err)
	}
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the sealed record does not validate: %v", err)
	}
	got := env.Observation

	if len(got.Source.EventKey) != 2 || got.Source.EventKey[1] != "1" {
		t.Errorf("the spend key came back %v, want the session and the turn number", got.Source.EventKey)
	}
	if got.Kind != "turn" {
		t.Errorf("the grain came back %q, want turn", got.Kind)
	}
	if got.Time.Basis != "turn_completion" {
		t.Errorf("the time basis came back %q; completion-day attribution is labelled, never implied", got.Time.Basis)
	}
	// The mixed-model gap: the aggregate's model does NOT replace the detail, and the detail
	// is a list this wire keeps whole. A second model added to the turn is a second entry,
	// not a rewrite of the first.
	if len(got.ModelUsage) != 1 || got.ModelUsage[0].ModelID != primary {
		t.Fatalf("the per-model detail came back %+v", got.ModelUsage)
	}
	if got.Model.Basis != "harness_reported" {
		t.Errorf("the turn's model basis came back %q", got.Model.Basis)
	}
	if len(got.ModelUsage[0].RawUsage) != 8 {
		t.Errorf("the per-model detail carries %d fields, want the eight the contract names (turnCount excluded)", len(got.ModelUsage[0].RawUsage))
	}
	if _, ok := got.ModelUsage[0].RawUsage["turnCount"]; ok {
		t.Error("turnCount reached the per-model detail; the contract says the eight without it")
	}
	// A mixed-model turn: two entries, sorted, neither collapsed. Unsorted is refused rather
	// than sorted into place, and a duplicate model is refused outright.
	second := "grok-model-other"
	mixed := obs
	mixed.Model = Model{Basis: "mixed"}
	mixed.ModelUsage = []ModelUsage{
		{ModelID: second, RawUsage: usage("inputTokens", "outputTokens")},
		{ModelID: primary, RawUsage: usage("inputTokens", "outputTokens")},
	}
	if _, _, err := v.SealObservation(mixed); err == nil {
		t.Error("an unsorted model split was accepted; the format says sorted by model_id")
	}
	mixed.ModelUsage = []ModelUsage{
		{ModelID: primary, RawUsage: usage("inputTokens", "outputTokens")},
		{ModelID: second, RawUsage: usage("inputTokens", "outputTokens")},
	}
	rawMixed, _, err := v.SealObservation(mixed)
	if err != nil {
		t.Fatalf("a mixed-model turn does not seal: %v", err)
	}
	mixedEnv, err := v.ValidateEnvelope(rawMixed)
	if err != nil {
		t.Fatalf("a mixed-model turn does not validate: %v", err)
	}
	if len(mixedEnv.Observation.ModelUsage) != 2 {
		t.Errorf("the mixed split came back %+v", mixedEnv.Observation.ModelUsage)
	}
	if mixedEnv.Observation.Model.ID != nil || mixedEnv.Observation.Model.Basis != "mixed" {
		t.Errorf("the mixed turn's model came back %+v; primaryModelId must not stand for every entry", mixedEnv.Observation.Model)
	}

	// The present zero, and what it is not: retained as measured evidence only when the
	// mapping says its zeros are measured. The contract says the sample does not establish it.
	cc := got.RawUsage["cacheCreationTokens"]
	if !cc.Present() || !cc.IsZero() {
		t.Errorf("cacheCreationTokens came back %+v, want a present zero", cc)
	}
	if m := Normalize(cc, ZeroUnknown); m.Measured || m.SubsetComplete {
		t.Errorf("a present zero normalised as measured under unknown zero semantics: %+v", m)
	}
	// A missing key is a different fact from a raw zero, and both fit on the wire at once: the
	// key the source did not carry is written WITH its null and its reason, never dropped --
	// "The adapter must produce every supported raw field, including explicit absent/
	// unavailable entries." Dropping it is refused outright, two blocks down.
	absentUsage := map[string]RawField{}
	for k, f := range got.RawUsage {
		absentUsage[k] = f
	}
	reason := "omitted_from_wire"
	absentUsage["reasoningTokens"] = RawField{Presence: "absent", NumberKind: "integer", Unit: "tokens", Reason: &reason}
	absent := *got
	absent.RawUsage = absentUsage
	rawAbsent, _, err := v.SealObservation(absent)
	if err != nil {
		t.Fatalf("a missing key does not seal: %v", err)
	}
	back, err := v.ValidateEnvelope(rawAbsent)
	if err != nil {
		t.Fatalf("a missing key does not validate: %v", err)
	}
	miss := back.Observation.RawUsage["reasoningTokens"]
	if miss.Present() || miss.Value != nil || miss.Reason == nil || *miss.Reason != reason {
		t.Errorf("the missing key came back %+v; a missing key is not a raw zero", miss)
	}
	if m := Normalize(miss, ZeroMeasured); m.Measured {
		t.Error("a missing field normalised as a measured zero, which is the one thing the format forbids")
	}
	// And the field cannot simply be left out instead: a supported field with no entry at all
	// is refused by name, so an adapter cannot turn "the source did not carry it" into "the
	// mapping never had it" by omission. (This is the wall my own first draft of this test
	// walked into, which is how I know it is one.)
	dropped := *got
	dropped.RawUsage = map[string]RawField{}
	for k, f := range got.RawUsage {
		if k == "reasoningTokens" {
			continue
		}
		dropped.RawUsage[k] = f
	}
	if _, _, err := v.SealObservation(dropped); err == nil {
		t.Error("a supported field was omitted entirely and the record sealed")
	} else {
		var ref *Refusal
		if !asRefusal(err, &ref) || ref.Rule != RuleMissingField || !strings.Contains(ref.Field, "reasoningTokens") {
			t.Errorf("the refusal is %v, want %s naming reasoningTokens", err, RuleMissingField)
		}
	}
	// The cost stays a raw tick with an unverified unit, and no part of this package turns it
	// into anything: there is no arithmetic here at all.
	tick := got.RawUsage["costUsdTicks"]
	if tick.Unit != "ticks" || tick.Value == nil || *tick.Value != "77" {
		t.Errorf("the cost came back %+v, want the raw lexeme with its declared unit", tick)
	}
	// touched carries no spend, and it survives as a list beside an unattributed repository.
	if got.Repository.Basis != "unattributed" || len(got.Repository.Touched) != 1 {
		t.Errorf("the repository came back %+v", got.Repository)
	}
}

// The one thing the two contracts need that this wire cannot yet carry, stated as an
// executable note rather than a sentence in a document: BOTH mappings turn on
// field_rules.zero_semantics -- Codex's defaulted cache-write and reasoning zeros, Grok's
// always-zero cacheCreationTokens -- and that rule lives in a nova.tokens.mapping/2 body,
// which this package has no validator for. Normalize takes the semantics as an argument
// because of that: the observation states what was on the wire, and something else has to
// state what it means.
//
// The builder can already SEAL such a body, so the mapping's content ID (the one an
// observation's mapping_id names) is reachable today and is a real digest over real bytes.
// What is missing is the validator that would refuse a mapping missing zero_semantics for a
// numeric field -- the format packet's own requirement. This test pins both halves of that
// sentence so neither can be forgotten: the seal works, and the schema is unknown here.
func TestAMappingBodyCanBeSealedButNotYetValidated(t *testing.T) {
	body := NewObject()
	for name, v := range map[string]Value{
		"schema":   "nova.tokens.mapping/2",
		"name":     "nova.codex-desktop",
		"revision": "1",
	} {
		if err := body.Set(name, v); err != nil {
			t.Fatalf("building a mapping body: %v", err)
		}
	}
	rules := NewObject()
	for _, field := range []string{"cache_write_input_tokens", "reasoning_output_tokens"} {
		entry := NewObject()
		if err := entry.Set("zero_semantics", string(ZeroMayMaskAbsent)); err != nil {
			t.Fatal(err)
		}
		if err := rules.Set(field, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := body.Set("field_rules", rules); err != nil {
		t.Fatal(err)
	}

	raw, id, err := Seal(body)
	if err != nil {
		t.Fatalf("a mapping body does not seal: %v", err)
	}
	if !strings.HasPrefix(id, "sha256:") || len(id) != len("sha256:")+64 {
		t.Errorf("the mapping's content ID is %q", id)
	}
	// The digest is over the canonical bytes, so the same mapping written with its members in
	// any order is the same mapping.
	if !strings.Contains(string(raw), `"field_rules":{"cache_write_input_tokens"`) {
		t.Errorf("the sealed mapping is not in canonical order:\n%s", raw)
	}

	// And the half that is missing: this package validates the observation schema only.
	v := testValidator(t)
	if _, err := v.ValidateEnvelope(raw); err == nil {
		t.Fatal("a mapping envelope validated; if this package learned the mapping schema, this test is the place that says so")
	} else {
		var ref *Refusal
		if !asRefusal(err, &ref) || ref.Rule != RuleUnknownSchema {
			t.Errorf("the refusal is %v, want %s naming the schema this package does not know", err, RuleUnknownSchema)
		}
	}
}

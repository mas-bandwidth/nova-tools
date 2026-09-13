package tokens

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The Grok turn decoder against the source owner's fixtures, and nothing else. Every input
// here is a file under testdata/tokens/grok: synthetic by contract, and no real `grok usage`
// output is read on any bench. The expected envelopes were written independently of this
// adapter (PR #142, before it existed), so the comparison is byte for byte rather than a
// re-derivation of whatever the decoder happens to produce.
//
// The mapping decisions are docs/MAPPING-TOKENS-GROK.md at d947d46. This file asserts them;
// it does not restate or reinterpret a cell.

const (
	grokSentinelPrompt = "SENTINEL-PRIVATE-PROMPT-DO-NOT-PUBLISH-7f3a"
	grokSentinelPath   = "SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21"
)

func grokDir() string { return filepath.Join("..", "..", "testdata", "tokens", "grok") }

// The manifest members this test reads. The manifest is the contract; the adapter's own
// tables are checked AGAINST it, so a table that drifts from the owner's decision fails
// here rather than shipping.
type grokFieldRuleFixture struct {
	NumberKind      string `json:"number_kind"`
	Unit            string `json:"unit"`
	ZeroSemantics   string `json:"zero_semantics"`
	AbsentPresence  string `json:"absent_presence"`
	AbsentReason    string `json:"absent_reason"`
	InvalidPresence string `json:"invalid_presence"`
	InvalidReason   string `json:"invalid_reason"`
	SpendRole       string `json:"spend_role"`
}

type grokManifestFixture struct {
	Schema     string                          `json:"schema"`
	FieldRules map[string]grokFieldRuleFixture `json:"field_rules"`

	IdentityRule struct {
		SourceKind        string   `json:"source_kind"`
		Namespace         string   `json:"namespace"`
		ObservationKind   string   `json:"observation_kind"`
		EventKey          []string `json:"event_key"`
		ReceiptFields     []string `json:"receipt_fields"`
		NormalizedSupport bool     `json:"normalized_spend_supported"`
	} `json:"identity_rule"`

	ModelRule struct {
		ModelUsageFields []string `json:"model_usage_fields"`
		ForbiddenKeys    []string `json:"forbidden_wire_keys"`
	} `json:"model_rule"`

	OverlapRule struct {
		OwedCoverageTasks []string `json:"owed_coverage_tasks"`
	} `json:"overlap_rule"`

	ImplementationID *string `json:"implementation_id"`
}

// grokSealedLines splits each line of a JSONL fixture and re-derives the sealed ID from the
// body bytes, so an expected ID is the digest of what is on disk.
func grokSealedLines(t *testing.T, name string) [][]byte {
	t.Helper()
	f, err := os.Open(filepath.Join(grokDir(), name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), line...))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s is empty", name)
	}
	return out
}

func grokSealedID(t *testing.T, line []byte) string {
	t.Helper()
	const prefix = `{"id":"`
	const mid = `","body":`
	if !bytes.HasPrefix(line, []byte(prefix)) || !bytes.HasSuffix(line, []byte("}")) {
		t.Fatalf("not a sealed envelope line")
	}
	rest := line[len(prefix):]
	i := bytes.Index(rest, []byte(mid))
	if i < 0 {
		t.Fatalf("sealed envelope has no body member")
	}
	id := string(rest[:i])
	sum := sha256.Sum256(rest[i+len(mid) : len(rest)-1])
	if want := "sha256:" + hex.EncodeToString(sum[:]); want != id {
		t.Fatalf("sealed ID is not the digest of the body bytes: have %s want %s", id, want)
	}
	return id
}

func grokManifest(t *testing.T) (grokManifestFixture, string) {
	t.Helper()
	lines := grokSealedLines(t, "mapping.json")
	if len(lines) != 1 {
		t.Fatalf("mapping.json is one sealed envelope, found %d lines", len(lines))
	}
	id := grokSealedID(t, lines[0])
	var env struct {
		Body grokManifestFixture `json:"body"`
	}
	if err := json.Unmarshal(lines[0], &env); err != nil {
		t.Fatalf("mapping body: %v", err)
	}
	if env.Body.Schema != "nova.tokens.mapping/2" {
		t.Fatalf("mapping schema is %q", env.Body.Schema)
	}
	return env.Body, id
}

func grokSource(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(grokDir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

// grokFixtureBinding is the owner binding these fixtures carry. It is supplied BY THE OWNER
// and never derived from the export or from this bench: the mapping's origin cell is
// "{friend, bench, basis: owner_binding, binding_id} with an explicit stable source-binding
// ID and the supplied original friend/bench; otherwise {null, null, unknown, null}", and the
// fixtures state both branches -- turn 3 is the unbound one. A decoder that invented a
// binding for it, or read one off the collector host, would not reproduce these bytes.
func grokFixtureBinding(sessionID, turnNumber string) *GrokBinding {
	if sessionID != "fixture-grok-session" || turnNumber == "3" {
		return nil
	}
	return &GrokBinding{Friend: "johnny", Bench: "air", ID: "binding-grok-air-1"}
}

func grokFixtureOptions(mappingID string) GrokOptions {
	return GrokOptions{MappingID: mappingID, Binding: grokFixtureBinding}
}

// grokDecodeFixtures decodes the three source exports in the order the expected file states
// them: the export, the byte-for-byte copy, then the changed copy.
func grokDecodeFixtures(t *testing.T, opts GrokOptions) []GrokRecord {
	t.Helper()
	var all []GrokRecord
	for _, name := range []string{"source_export.json", "source_export_copy.json", "source_export_changed.json"} {
		recs, err := DecodeGrokTurns(grokSource(t, name), opts)
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		all = append(all, recs...)
	}
	return all
}

func TestGrokDecoderTablesAreTheMappingsOwn(t *testing.T) {
	m, _ := grokManifest(t)

	allow := GrokAllowlists()
	var want []string
	for name := range m.FieldRules {
		want = append(want, name)
	}
	sort.Strings(want)
	if len(want) != 9 {
		t.Fatalf("the mapping's raw usage allowlist is the closed nine, found %d", len(want))
	}
	have := append([]string(nil), allow.RawUsageFields...)
	sort.Strings(have)
	if strings.Join(have, ",") != strings.Join(want, ",") {
		t.Errorf("raw usage allowlist: have %v want %v", have, want)
	}
	if strings.Join(allow.ReceiptFields, ",") != strings.Join(m.IdentityRule.ReceiptFields, ",") {
		t.Errorf("receipt allowlist: have %v want %v", allow.ReceiptFields, m.IdentityRule.ReceiptFields)
	}
	// The allowlist is a COPY: writing through what the adapter returns cannot widen what a
	// later caller extracts (#146's finding 1, one level down).
	allow.RawUsageFields[0] = "promptText"
	if GrokAllowlists().RawUsageFields[0] == "promptText" {
		t.Errorf("the adapter handed out its own allowlist rather than a copy")
	}

	for name, rule := range m.FieldRules {
		kind, unit, ok := GrokFieldRule(name)
		if !ok {
			t.Errorf("%s is in the mapping and not in the adapter", name)
			continue
		}
		if kind != rule.NumberKind || unit != rule.Unit {
			t.Errorf("%s is %s/%s, the mapping says %s/%s", name, kind, unit, rule.NumberKind, rule.Unit)
		}
		if rule.ZeroSemantics != string(records.ZeroUnknown) {
			t.Errorf("%s declares zero_semantics %s; every Grok field is unknown until the producer semantics are verified", name, rule.ZeroSemantics)
		}
	}
	if _, _, ok := GrokFieldRule("primaryModelId"); ok {
		t.Errorf("primaryModelId is model.id, never a raw usage counter")
	}

	mu := GrokModelUsageFields()
	if strings.Join(mu, ",") != strings.Join(sortedStrings(m.ModelRule.ModelUsageFields), ",") {
		t.Errorf("model_usage fields: have %v want %v", mu, m.ModelRule.ModelUsageFields)
	}
	for _, f := range mu {
		if f == "turnCount" {
			t.Errorf("turnCount is not a model_usage field")
		}
	}
	if m.ImplementationID != nil {
		t.Errorf("the landed manifest revision carries no implementation ID; stamping one is the mapping owner's")
	}
}

func sortedStrings(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestGrokDecoderSealsTheExpectedEnvelopesByteForByte(t *testing.T) {
	m, mappingID := grokManifest(t)
	want := grokSealedLines(t, "expected_records.jsonl")
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))

	if len(recs) != len(want) {
		t.Fatalf("decoded %d records, the fixture states %d", len(recs), len(want))
	}
	for i, rec := range recs {
		wantID := grokSealedID(t, want[i])
		if !bytes.Equal(rec.Envelope, want[i]) {
			t.Errorf("record %d (turn %s) is not the expected bytes\nhave %s\nwant %s",
				i, rec.TurnNumber, rec.Envelope, want[i])
			continue
		}
		if rec.ID != wantID {
			t.Errorf("record %d: sealed ID %s, expected %s", i, rec.ID, wantID)
		}
	}

	// Every sealed record is also a record the landed boundary accepts under the mapping's
	// own allowlists, and the observation it reads back is the one the adapter holds.
	v := records.NewValidator(GrokAllowlists())
	for i, rec := range recs {
		env, err := v.ValidateEnvelope(rec.Envelope)
		if err != nil {
			t.Fatalf("record %d refused by the boundary: %v", i, err)
		}
		o := env.Observation
		if o.Source.Kind != m.IdentityRule.SourceKind || o.Source.Namespace != m.IdentityRule.Namespace ||
			o.Kind != m.IdentityRule.ObservationKind {
			t.Errorf("record %d: source literals are %q/%q/%q", i, o.Source.Kind, o.Source.Namespace, o.Kind)
		}
		if len(o.Source.EventKey) != 2 || o.Source.EventKey[0] != o.Source.SessionID ||
			o.Source.EventKey[1] != rec.TurnNumber {
			t.Errorf("record %d: the spend key is [original_session_id, turn_number]: %v", i, o.Source.EventKey)
		}
		if o.Receipt["turn_number"] != rec.TurnNumber || len(o.Receipt) != 1 {
			t.Errorf("record %d: the receipt is turn_number only: %v", i, o.Receipt)
		}
		if o.MappingID != mappingID {
			t.Errorf("record %d: mapping_id is not this directory's sealed mapping", i)
		}
		if o.Revision.Basis != "none" || o.Revision.Native != nil || len(o.Revision.Supersedes) != 0 {
			t.Errorf("record %d: revision is not {null,[],none}", i)
		}
		if o.Repository.Basis != "unattributed" || o.Repository.ID != nil ||
			o.Repository.PolicyID != nil || len(o.Repository.Touched) != 0 {
			t.Errorf("record %d: repository is not unattributed with no allocated spend", i)
		}
		if o.Time.Start != nil || o.Time.End != nil {
			t.Errorf("record %d: the source supplies no measured interval", i)
		}
		if o.Kind == "aggregate" {
			t.Errorf("record %d: the turn mapping emits no session aggregate; that mapping is owed", i)
		}
		if len(o.RawUsage) != 9 {
			t.Errorf("record %d: raw_usage carries %d of the nine allowlisted fields", i, len(o.RawUsage))
		}
		for _, e := range o.ModelUsage {
			if len(e.RawUsage) != 8 {
				t.Errorf("record %d: a model_usage entry carries %d of the eight numeric fields", i, len(e.RawUsage))
			}
			if _, ok := e.RawUsage["turnCount"]; ok {
				t.Errorf("record %d: turnCount is not a model_usage field", i)
			}
		}
		// A present zero is retained raw and normalises to nothing under unknown zero
		// semantics: it is never a measured zero.
		for name, f := range o.RawUsage {
			if f.IsZero() && records.Normalize(f, records.ZeroUnknown).Measured {
				t.Errorf("record %d: a present zero in %s normalised to a measurement", i, name)
			}
		}
		for _, k := range m.ModelRule.ForbiddenKeys {
			if bytes.Contains(rec.Envelope, []byte(`"`+k+`"`)) {
				t.Errorf("record %d carries the forbidden wire key %s", i, k)
			}
		}
	}
}

// The cost tick lexeme, the mixed-model turn, the unbound origin and the completeness gap,
// read off the decoded records rather than off the fixture file.
func TestGrokDecoderKeepsLexemesPresenceAndBasis(t *testing.T) {
	_, mappingID := grokManifest(t)
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	byTurn := map[string]GrokRecord{}
	for _, r := range recs[:5] {
		byTurn[r.TurnNumber] = r
	}

	one := byTurn["1"].Observation
	if f := one.RawUsage["costUsdTicks"]; !f.Present() || f.Value == nil || *f.Value != "77" ||
		f.NumberKind != "decimal" || f.Unit != "usd_ticks" {
		t.Errorf("turn 1: the cost tick lexeme is retained exactly as decimal usd_ticks \"77\", no dollars conversion: %+v", f)
	}
	if one.Model.ID == nil || *one.Model.ID != "grok-model-example" || one.Model.Basis != "harness_reported" {
		t.Errorf("turn 1: a single reported model ID is harness_reported: %+v", one.Model)
	}
	if one.Origin.Basis != "owner_binding" || one.Origin.Friend == nil || *one.Origin.Friend != "johnny" ||
		one.Origin.Bench == nil || *one.Origin.Bench != "air" ||
		one.Origin.BindingID == nil || *one.Origin.BindingID != "binding-grok-air-1" {
		t.Errorf("turn 1: the owner binding is the supplied friend/bench with its stable ID: %+v", one.Origin)
	}
	if one.Source.ProducerVersion != nil {
		t.Errorf("turn 1: producer_version is null without producer metadata or an owner binding")
	}

	two := byTurn["2"].Observation
	if two.Model.ID != nil || two.Model.Basis != "mixed" {
		t.Errorf("turn 2: more than one reported model ID is {null, mixed}: %+v", two.Model)
	}
	if len(two.ModelUsage) != 2 || two.ModelUsage[0].ModelID != "grok-model-example" ||
		two.ModelUsage[1].ModelID != "grok-model-other" {
		t.Errorf("turn 2: the split is retained entry for entry, sorted by source model ID")
	}

	three := byTurn["3"].Observation
	if three.Time.OccurredAt != nil || three.Time.Basis != "unknown" {
		t.Errorf("turn 3: a turn with no endedAt is occurred_at null, basis unknown: %+v", three.Time)
	}
	if three.Model.ID != nil || three.Model.Basis != "unknown" {
		t.Errorf("turn 3: an absent model ID is {null, unknown}: %+v", three.Model)
	}
	if three.Origin.Basis != "unknown" || three.Origin.Friend != nil || three.Origin.Bench != nil ||
		three.Origin.BindingID != nil {
		t.Errorf("turn 3: an unbound turn is {null, null, unknown, null}: %+v", three.Origin)
	}
	if len(three.ModelUsage) != 0 {
		t.Errorf("turn 3: no modelUsage in the source is an empty split")
	}
	for _, name := range []string{"cachedReadTokens", "cacheCreationTokens", "reasoningTokens", "costUsdTicks"} {
		f := three.RawUsage[name]
		if f.Presence != "absent" || f.Value != nil || f.Reason == nil || *f.Reason != "not_supplied" {
			t.Errorf("turn 3: a key the source omits is absent/not_supplied with a null value: %s %+v", name, f)
		}
	}

	four := byTurn["4"].Observation
	for _, name := range []string{"inputTokens", "outputTokens", "totalTokens", "cacheCreationTokens"} {
		f := four.RawUsage[name]
		if !f.Present() || !f.IsZero() {
			t.Errorf("turn 4: a raw present zero is retained as present: %s %+v", name, f)
		}
	}
	if len(four.ModelUsage) != 0 {
		t.Errorf("turn 4: an empty source split is an empty model_usage")
	}

	// The explicit null, the wrong-typed sentinel value and the negative count are each
	// unavailable/parse_failed with a null value, in the turn and in its split alike. The
	// other fields of the turn survive.
	five := byTurn["5"].Observation
	for _, name := range []string{"inputTokens", "outputTokens", "cacheCreationTokens"} {
		f := five.RawUsage[name]
		if f.Presence != "unavailable" || f.Value != nil || f.Reason == nil || *f.Reason != "parse_failed" {
			t.Errorf("turn 5: an invalid raw source shape is unavailable/parse_failed: %s %+v", name, f)
		}
		if len(five.ModelUsage) == 1 {
			e := five.ModelUsage[0].RawUsage[name]
			if e.Presence != "unavailable" || e.Value != nil {
				t.Errorf("turn 5: the same rule applies inside a model_usage entry: %s %+v", name, e)
			}
		}
	}
	for _, name := range []string{"cachedReadTokens", "reasoningTokens", "totalTokens", "modelCalls", "costUsdTicks", "turnCount"} {
		if f := five.RawUsage[name]; !f.Present() {
			t.Errorf("turn 5: the valid fields of a turn survive its invalid ones: %s %+v", name, f)
		}
	}
	if !GrokHasCompletenessGap(byTurn["5"]) {
		t.Errorf("turn 5 carries a completeness gap rather than a fabricated total")
	}
	if GrokHasCompletenessGap(byTurn["1"]) {
		t.Errorf("turn 1 has every supported field and no gap")
	}
}

// Day allocation: the UTC day of a known completion instant whatever its source offset, and
// unallocated when the instant is unknown. Turn 2 completes at 23:30-04:00, the next UTC day.
func TestGrokDayAllocationIsTheUTCDayOfAKnownInstant(t *testing.T) {
	_, mappingID := grokManifest(t)
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	days := map[string]string{}
	for _, r := range recs[:5] {
		days[r.TurnNumber] = GrokDay(r)
	}
	for turn, want := range map[string]string{
		"1": "2026-09-12", "2": "2026-09-12", "3": "unallocated", "4": "2026-09-12", "5": "2026-09-12",
	} {
		if days[turn] != want {
			t.Errorf("turn %s allocates to %q, want %q", turn, days[turn], want)
		}
	}
}

// A copied export is the same observation; the same key with changed content is a conflict
// retained and excluded from spend. There is no newest-wins rule: the changed file is not a
// winner for being decoded last.
func TestGrokCopiedExportDeduplicatesAndAChangedOneConflicts(t *testing.T) {
	_, mappingID := grokManifest(t)
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	if len(recs) != 7 {
		t.Fatalf("the three fixtures decode to 7 records, found %d", len(recs))
	}
	if !bytes.Equal(recs[0].Envelope, recs[5].Envelope) {
		t.Errorf("a byte-for-byte copy on another bench is the same observation, including its origin")
	}

	groups := GrokKeyGroups(recs)
	if len(groups) != 5 {
		t.Fatalf("five spend keys, found %d", len(groups))
	}
	conflicts := 0
	for _, g := range groups {
		switch {
		case g.TurnNumber == "1":
			if len(g.IDs) != 2 {
				t.Errorf("turn 1 holds the deduplicated copy and the changed observation: %v", g.IDs)
			}
			if !g.Conflict {
				t.Errorf("turn 1 is a conflict: two distinct observations of one spend key")
			}
			conflicts++
		default:
			if g.Conflict || len(g.IDs) != 1 {
				t.Errorf("turn %s is one observation of one key: %v", g.TurnNumber, g.IDs)
			}
		}
	}
	if conflicts != 1 {
		t.Errorf("one spend key conflicts, found %d", conflicts)
	}
	if !sort.StringsAreSorted([]string{groups[0].TurnNumber, groups[len(groups)-1].TurnNumber}) {
		t.Errorf("the groups are in a stable order")
	}
}

// The owner's unsupported and owed cells are refusals in the adapter, not quiet numbers.
func TestGrokUnsupportedAndOwedCellsRefuse(t *testing.T) {
	m, mappingID := grokManifest(t)
	if m.IdentityRule.NormalizedSupport {
		t.Fatalf("the manifest must declare normalized spend unsupported for this key")
	}
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))

	if _, err := GrokNormalizedSpend(recs); !errors.Is(err, ErrGrokNormalizedSpendUnsupported) {
		t.Errorf("normalized spend from a Grok turn key is unsupported until identity stability is evidenced: %v", err)
	}
	if _, err := GrokSessionAggregate(grokSource(t, "source_export.json"), grokFixtureOptions(mappingID)); !errors.Is(err, ErrGrokSessionAggregateOwed) {
		t.Errorf("the session aggregate is a separate owed mapping: %v", err)
	}
	if _, err := GrokRequestObservations(grokSource(t, "source_export.json"), grokFixtureOptions(mappingID)); !errors.Is(err, ErrGrokRequestGrainOutsideMapping) {
		t.Errorf("request grain is outside this mapping: %v", err)
	}
	if len(m.OverlapRule.OwedCoverageTasks) == 0 {
		t.Fatalf("the owed session aggregate mapping must be named in the manifest")
	}
	for _, task := range m.OverlapRule.OwedCoverageTasks {
		if !grokContains(GrokOwedCoverageTasks(), task) {
			t.Errorf("the adapter does not carry the manifest's owed task %q", task)
		}
	}
}

func grokContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// The origin is the owner's binding or nothing. The collector host, the file it read and the
// bench this test runs on are not historical evidence.
func TestGrokOriginNeverComesFromTheCollector(t *testing.T) {
	_, mappingID := grokManifest(t)
	recs, err := DecodeGrokTurns(grokSource(t, "source_export.json"), GrokOptions{MappingID: mappingID})
	if err != nil {
		t.Fatalf("decode with no binding: %v", err)
	}
	for _, r := range recs {
		o := r.Observation.Origin
		if o.Basis != "unknown" || o.Friend != nil || o.Bench != nil || o.BindingID != nil {
			t.Errorf("turn %s: with no owner binding the origin is {null, null, unknown, null}: %+v", r.TurnNumber, o)
		}
	}
	// A binding that names a different bench produces different bytes, which is the point of
	// the fixture's copy: the binding describes ORIGINAL execution, so the copy on another
	// bench keeps the original one and seals identically.
	other := GrokOptions{MappingID: mappingID, Binding: func(string, string) *GrokBinding {
		return &GrokBinding{Friend: "johnny", Bench: "studio", ID: "binding-grok-air-1"}
	}}
	moved, err := DecodeGrokTurns(grokSource(t, "source_export_copy.json"), other)
	if err != nil {
		t.Fatalf("decode the copy: %v", err)
	}
	bound := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	if bytes.Equal(moved[0].Envelope, bound[5].Envelope) {
		t.Errorf("a different bench binding is a different observation; the collector host cannot supply it")
	}
}

// producer_version is the exact bounded string an owner supplies, or null. It is never the
// `grok usage v1.0.30` an owner once reported, stamped on every export.
func TestGrokProducerVersionIsExactOrNull(t *testing.T) {
	_, mappingID := grokManifest(t)
	exact := "grok usage v1.0.30"
	opts := grokFixtureOptions(mappingID)
	opts.ProducerVersion = &exact
	recs, err := DecodeGrokTurns(grokSource(t, "source_export.json"), opts)
	if err != nil {
		t.Fatalf("decode with a producer binding: %v", err)
	}
	for _, r := range recs {
		pv := r.Observation.Source.ProducerVersion
		if pv == nil || *pv != exact {
			t.Errorf("turn %s: the supplied version keeps its spelling: %v", r.TurnNumber, pv)
		}
	}
	for _, bad := range []string{"", "v1\n0", strings.Repeat("v", 200)} {
		b := bad
		opts.ProducerVersion = &b
		if _, err := DecodeGrokTurns(grokSource(t, "source_export.json"), opts); err == nil {
			t.Errorf("an unbounded or control-carrying version string is refused, not trimmed")
		}
	}
}

// Every refused fixture refuses under the adapter's own allowlists, with the rule and field
// the owner named. The adapter's closed set is the one the wire enforces.
func TestGrokRefusedFixturesRefuseWithTheNamedRuleAndField(t *testing.T) {
	v := records.NewValidator(GrokAllowlists())
	lines := grokSealedLines(t, "refused_records.jsonl")
	for _, line := range lines {
		var rc struct {
			Case     string          `json:"case"`
			Rule     string          `json:"rule"`
			Field    string          `json:"field"`
			Note     string          `json:"note"`
			Envelope json.RawMessage `json:"envelope"`
		}
		if err := json.Unmarshal(line, &rc); err != nil {
			t.Fatalf("refused case: %v", err)
		}
		t.Run(rc.Case, func(t *testing.T) {
			_, err := v.ValidateEnvelope(rc.Envelope)
			if err == nil {
				t.Fatalf("the adapter's allowlists accepted a shape the mapping forbids")
			}
			var ref *records.Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("refusal is not a *records.Refusal: %v", err)
			}
			if ref.Rule != rc.Rule {
				t.Errorf("rule: have %s want %s", ref.Rule, rc.Rule)
			}
			if !strings.HasPrefix(ref.Field, rc.Field) {
				t.Errorf("field: have %s want prefix %s", ref.Field, rc.Field)
			}
			if msg := ref.Error(); strings.Contains(msg, grokSentinelPrompt) || strings.Contains(msg, grokSentinelPath) {
				t.Errorf("a refusal echoed a private sentinel")
			}
		})
	}
	if len(lines) != 5 {
		t.Errorf("the fixture ships five refused shapes, found %d", len(lines))
	}
}

// Both sides of the privacy check: the sentinels are in the source fixtures' unsupported
// fields, and they reach no record, no manifest and no diagnostic.
func TestGrokPrivacySentinelsReachNoRecordOrDiagnostic(t *testing.T) {
	for _, name := range []string{"source_export.json", "source_export_copy.json", "source_export_changed.json"} {
		raw := grokSource(t, name)
		if !bytes.Contains(raw, []byte(grokSentinelPrompt)) || !bytes.Contains(raw, []byte(grokSentinelPath)) {
			t.Fatalf("%s must carry the privacy sentinels in its unsupported fields, or this check proves nothing", name)
		}
	}
	for _, name := range []string{"expected_records.jsonl", "refused_records.jsonl", "mapping.json"} {
		raw := grokSource(t, name)
		for _, s := range []string{grokSentinelPrompt, grokSentinelPath} {
			if bytes.Contains(raw, []byte(s)) {
				t.Errorf("%s carries a private sentinel from an unsupported source field", name)
			}
		}
	}

	_, mappingID := grokManifest(t)
	recs := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	for _, r := range recs {
		for _, s := range []string{grokSentinelPrompt, grokSentinelPath} {
			if bytes.Contains(r.Envelope, []byte(s)) {
				t.Errorf("turn %s: a sealed record carries a private sentinel", r.TurnNumber)
			}
			if strings.Contains(GrokDay(r), s) || strings.Contains(strings.Join(r.EventKey, ""), s) {
				t.Errorf("turn %s: a sentinel reached a spend key or a day", r.TurnNumber)
			}
		}
		// The wrong-typed sentinel value of turn 5 is unavailable, and its lexeme is nowhere.
		for _, f := range r.Observation.RawUsage {
			if f.Value != nil && strings.Contains(*f.Value, "SENTINEL") {
				t.Errorf("turn %s: a source value was stringified onto the wire", r.TurnNumber)
			}
		}
	}

	// And no diagnostic: a source shape the adapter refuses names the field, never the value.
	for _, bad := range []string{
		`{"sessionId":"s","turns":[{"turnNumber":"SENTINEL-PRIVATE-PROMPT-DO-NOT-PUBLISH-7f3a"}]}`,
		`{"sessionId":"s","turns":[{"turnNumber":1,"endedAt":"SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21"}]}`,
		`{"sessionId":"s","turns":[{"turnNumber":1,"modelUsage":"SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21"}]}`,
		`{"sessionId":"SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21","turns":"x"}`,
	} {
		_, err := DecodeGrokTurns([]byte(bad), grokFixtureOptions(mappingID))
		if err == nil {
			t.Errorf("an invalid raw source shape is refused")
			continue
		}
		if strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("a diagnostic echoed a source value: %v", err)
		}
	}
}

// The decoder is a pure function of the bytes it is handed and the owner's options: it opens
// no file and runs no program, so no private path can reach a record through it.
func TestGrokDecoderIsDeterministicAndSourceShapesAreNamed(t *testing.T) {
	_, mappingID := grokManifest(t)
	a := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	b := grokDecodeFixtures(t, grokFixtureOptions(mappingID))
	for i := range a {
		if !bytes.Equal(a[i].Envelope, b[i].Envelope) {
			t.Fatalf("record %d is not the same bytes twice", i)
		}
	}
	for _, bad := range []string{
		`not json`,
		`{"turns":[]}`,
		`{"sessionId":"","turns":[]}`,
		`{"sessionId":"s","turns":[{}]}`,
		`{"sessionId":"s","turns":[{"turnNumber":1.5}]}`,
		`{"sessionId":"s","turns":[{"turnNumber":-1}]}`,
		`{"sessionId":"s","turns":[{"turnNumber":1,"modelUsage":{"m":3}}]}`,
	} {
		if _, err := DecodeGrokTurns([]byte(bad), grokFixtureOptions(mappingID)); err == nil {
			t.Errorf("this source shape is refused rather than decoded: %s", bad)
		}
	}
	// A mapping ID that is not a content ID is the caller's bad declaration, refused before
	// any record is sealed.
	if _, err := DecodeGrokTurns(grokSource(t, "source_export.json"), GrokOptions{MappingID: "mapping.json"}); err == nil {
		t.Errorf("the mapping ID is a sha256 content ID")
	}
}

// Stella's two source-shape witnesses, kept verbatim. A retained identity must not depend on
// a model ID inferred from the split, JSON last-key-wins, or case aliasing.
func TestStellaGrokMissingPrimaryDoesNotInventModel(t *testing.T) {
	raw := []byte(`{"sessionId":"synthetic","turns":[{"turnNumber":1,"modelUsage":{"split-only":{}}}]}`)
	recs, err := DecodeGrokTurns(raw, GrokOptions{MappingID: "sha256:" + strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	m := recs[0].Observation.Model
	if m.ID != nil || m.Basis != "unknown" {
		t.Fatalf("missing primary must remain unknown, got %+v", m)
	}
}

func TestStellaGrokRejectsAmbiguousExport(t *testing.T) {
	for name, raw := range map[string]string{
		"trailing":   `{"sessionId":"a","turns":[]} {"sessionId":"b","turns":[]}`,
		"duplicate":  `{"sessionId":"a","sessionId":"b","turns":[]}`,
		"case_alias": `{"SESSIONID":"a","TURNS":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeGrokTurns([]byte(raw), GrokOptions{MappingID: "sha256:" + strings.Repeat("0", 64)})
			if err == nil {
				t.Fatal("accepted ambiguous or unmapped source shape")
			}
		})
	}
}

func TestGrokSourceBoundaryAllowsTrailingWhitespaceAndExcludesUnknownFields(t *testing.T) {
	raw := []byte("{\"sessionId\":\"a\",\"turns\":[{\"turnNumber\":1,\"extraField\":123}],\"extraTop\":true} \n\t")
	recs, err := DecodeGrokTurns(raw, GrokOptions{MappingID: "sha256:" + strings.Repeat("0", 64)})
	if err != nil {
		t.Fatalf("trailing whitespace and unknown extra fields are allowed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("one turn decoded, got %d", len(recs))
	}
	for _, s := range []string{"extraField", "extraTop"} {
		if bytes.Contains(recs[0].Envelope, []byte(s)) {
			t.Errorf("unknown extra field %s reached retained output", s)
		}
	}
}

// A non-empty primary incompatible with the single split stays mixed: the split is retained,
// and no winner is picked.
func TestGrokPrimaryIncompatibleWithSingleSplitStaysMixed(t *testing.T) {
	raw := []byte(`{"sessionId":"a","turns":[{"turnNumber":1,"primaryModelId":"primary-model","modelUsage":{"split-model":{}}}]}`)
	recs, err := DecodeGrokTurns(raw, GrokOptions{MappingID: "sha256:" + strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	m := recs[0].Observation.Model
	if m.ID != nil || m.Basis != "mixed" {
		t.Fatalf("incompatible primary and single split must stay mixed, got %+v", m)
	}
	if len(recs[0].Observation.ModelUsage) != 1 || recs[0].Observation.ModelUsage[0].ModelID != "split-model" {
		t.Fatalf("the split entry is retained in model_usage: %+v", recs[0].Observation.ModelUsage)
	}
}

// A duplicate decoded member name inside a modelUsage object (a nested object), and two
// spellings that escape to the same name, are both refusals: no retained identity may depend
// on JSON last-key-wins or escape aliasing.
func TestGrokSourceBoundaryRefusesNestedAndEscapedDuplicateMembers(t *testing.T) {
	for name, raw := range map[string]string{
		"nested_usage_duplicate_key": `{"sessionId":"a","turns":[{"turnNumber":1,"modelUsage":{"m":{"inputTokens":1},"m":{"inputTokens":2}}}]}`,
		"nested_field_duplicate_key": `{"sessionId":"a","turns":[{"turnNumber":1,"modelUsage":{"m":{"inputTokens":1,"inputTokens":2}}}]}`,
		"escaped_equivalent_key":     `{"sessionId":"a","session\u0049d":"b","turns":[]}`,
		"escaped_equivalent_in_turn": `{"sessionId":"a","turns":[{"turnNumber":1,"turn\u004eumber":2}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeGrokTurns([]byte(raw), GrokOptions{MappingID: "sha256:" + strings.Repeat("0", 64)})
			if err == nil {
				t.Fatal("accepted a duplicate decoded member name")
			}
		})
	}
}

// The hand-written lexeme grammars, which replaced three compiled patterns because
// internal/tokens keeps its only two to repo.go and bus.go. Two halves: the spellings
// directly, including the ones no JSON document can carry, and then a synthetic turn per
// valid-JSON spelling decoded end to end -- if this file called a lexeme valid and the wire
// refused it, SealObservation would refuse the record and the decode would fail here.
func TestGrokLexemeGrammarsAcceptWhatTheWireAccepts(t *testing.T) {
	for _, c := range []struct {
		lexeme string
		intOK  bool
		decOK  bool
	}{
		{"0", true, true},
		{"1", true, true},
		{"1100", true, true},
		{"1180591620717411303424", true, true}, // above 2^53: no float, no rounding
		{"007", false, false},
		{"", false, false},
		{"1.0", false, true},
		{"0.50", false, true},
		{"1.", false, false},
		{".5", false, false},
		{"1e3", false, true},
		{"7E+2", false, true},
		{"1e", false, false},
		{"1e+", false, false},
		{"+1", false, false},
		{"-1", false, false},
		{"-0.5", false, false},
		{"77x", false, false},
		{"0x10", false, false},
	} {
		if got := grokIsInteger(c.lexeme); got != c.intOK {
			t.Errorf("grokIsInteger(%q) = %v, want %v", c.lexeme, got, c.intOK)
		}
		if got := grokIsDecimal(c.lexeme); got != c.decOK {
			t.Errorf("grokIsDecimal(%q) = %v, want %v", c.lexeme, got, c.decOK)
		}
		// A negative is never valid under either kind: a negative count is an explicit
		// failure and not a spelling problem.
		if strings.HasPrefix(c.lexeme, "-") && grokValidLexeme(c.lexeme, "integer") {
			t.Errorf("a negative counter is never a valid lexeme: %q", c.lexeme)
		}
	}
	for _, c := range []struct{ id string }{
		{"sha256:00b0ebe373b6f79f0ca1f029246273d75f84517e57a2fb99c5cd486b2dcb526c"},
	} {
		if !grokIsContentID(c.id) {
			t.Errorf("grokIsContentID(%q) = false", c.id)
		}
	}
	for _, bad := range []string{
		"", "mapping.json", "sha256:", "sha1:00b0ebe373b6f79f0ca1f029246273d75f84517e57a2fb99c5cd486b2dcb526c",
		"sha256:00B0EBE373B6F79F0CA1F029246273D75F84517E57A2FB99C5CD486B2DCB526C",
		"sha256:00b0ebe373b6f79f0ca1f029246273d75f84517e57a2fb99c5cd486b2dcb526",
	} {
		if grokIsContentID(bad) {
			t.Errorf("grokIsContentID(%q) = true; an ID is sha256: and 64 lowercase hex digits", bad)
		}
	}

	// End to end, through the seal. Only spellings a JSON document can carry appear here.
	_, mappingID := grokManifest(t)
	for _, c := range []struct {
		name        string
		input, cost string
		wantIn      string // "" means unavailable
		wantCost    string
	}{
		{"plain", "1000", "77", "1000", "77"},
		{"zero", "0", "0", "0", "0"},
		{"above 2^53", "1180591620717411303424", "1180591620717411303424", "1180591620717411303424", "1180591620717411303424"},
		{"a decimal cost keeps its fraction", "10", "0.50", "10", "0.50"},
		{"a decimal cost keeps its exponent", "10", "7E+2", "10", "7E+2"},
		{"an integer counter is not a decimal", "1.0", "77", "", "77"},
		{"an integer counter carries no exponent", "1e3", "77", "", "77"},
		{"a negative counter is unavailable", "-5", "-7", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := `{"sessionId":"lexeme-fixture","updatedAt":"2026-09-12T13:00:00Z","session":{},"turns":[{` +
				`"turnNumber":1,"endedAt":"2026-09-12T00:05:00Z","inputTokens":` + c.input +
				`,"outputTokens":1,"cachedReadTokens":0,"cacheCreationTokens":0,"reasoningTokens":0,` +
				`"totalTokens":1,"modelCalls":1,"costUsdTicks":` + c.cost + `,"turnCount":1}]}`
			recs, err := DecodeGrokTurns([]byte(raw), GrokOptions{MappingID: mappingID})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			for field, want := range map[string]string{"inputTokens": c.wantIn, "costUsdTicks": c.wantCost} {
				f := recs[0].Observation.RawUsage[field]
				if want == "" {
					if f.Presence != "unavailable" || f.Value != nil || f.Reason == nil || *f.Reason != "parse_failed" {
						t.Errorf("%s: an invalid lexeme is unavailable/parse_failed: %+v", field, f)
					}
					continue
				}
				if !f.Present() || f.Value == nil || *f.Value != want {
					t.Errorf("%s: the original lexeme is kept byte for byte: %+v want %q", field, f, want)
				}
			}
		})
	}
}

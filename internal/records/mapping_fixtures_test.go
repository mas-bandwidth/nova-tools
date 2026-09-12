package records

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The Codex and Grok source mappings of PR #142, made executable. Every expected
// observation in these fixtures is stated independently of any adapter -- there is no
// adapter -- and validated by the landed record validator in this package, so a shape the
// documents describe but the wire refuses shows up here as a failure rather than as prose.
// Nothing in this file relaxes a rule: the refused fixtures assert the refusal a mapping
// mistake must produce, with the rule and field named.

const (
	// The sentinels sit in unsupported source fields of both source fixtures. They must
	// reach no shared record and no diagnostic.
	mappingSentinelPrompt = "SENTINEL-PRIVATE-PROMPT-DO-NOT-PUBLISH-7f3a"
	mappingSentinelPath   = "SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21"
)

// mappingManifest is the sealed nova.tokens.mapping/2 body each fixture directory ships.
// The allowlists the validator needs come from it, so the test cannot quietly widen them.
type fieldRule struct {
	NumberKind      string `json:"number_kind"`
	Unit            string `json:"unit"`
	ZeroSemantics   string `json:"zero_semantics"`
	AbsentPresence  string `json:"absent_presence"`
	AbsentReason    string `json:"absent_reason"`
	InvalidPresence string `json:"invalid_presence"`
	InvalidReason   string `json:"invalid_reason"`
	SpendRole       string `json:"spend_role"`
}

type mappingManifest struct {
	Schema       string               `json:"schema"`
	Name         string               `json:"name"`
	Revision     string               `json:"revision"`
	FieldRules   map[string]fieldRule `json:"field_rules"`
	IdentityRule struct {
		SourceKind        string   `json:"source_kind"`
		Namespace         string   `json:"namespace"`
		ObservationKind   string   `json:"observation_kind"`
		EventKey          []string `json:"event_key"`
		ReceiptFields     []string `json:"receipt_fields"`
		NormalizedSupport bool     `json:"normalized_spend_supported"`
		UnsupportedReason string   `json:"unsupported_reason"`
	} `json:"identity_rule"`
	RevisionRule struct {
		Basis          string `json:"basis"`
		IdenticalCopy  string `json:"identical_copy"`
		ChangedSameKey string `json:"changed_same_key"`
		NewestWins     bool   `json:"newest_wins"`
	} `json:"revision_rule"`
	TimeRule struct {
		OccurredAt       string `json:"occurred_at"`
		Basis            string `json:"basis"`
		DayAllocation    string `json:"day_allocation"`
		MissingTimestamp struct {
			OccurredAt *string `json:"occurred_at"`
			Basis      string  `json:"basis"`
		} `json:"missing_timestamp"`
	} `json:"time_rule"`
	ModelRule struct {
		ModelUsageFields []string `json:"model_usage_fields"`
		ForbiddenKeys    []string `json:"forbidden_wire_keys"`
	} `json:"model_rule"`
	OverlapRule struct {
		OwedCoverageTasks []string `json:"owed_coverage_tasks"`
	} `json:"overlap_rule"`
	FixtureDigests   map[string]string `json:"fixture_digests"`
	ImplementationID *string           `json:"implementation_id"`
}

// sealedBody splits the exact bytes Seal writes and re-derives the ID from them, so a
// fixture's ID is the digest of bytes on disk rather than a placeholder someone typed.
func sealedBody(t *testing.T, line []byte) (string, []byte) {
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
	body := rest[i+len(mid) : len(rest)-1]
	sum := sha256.Sum256(body)
	if want := "sha256:" + hex.EncodeToString(sum[:]); want != id {
		t.Errorf("sealed ID is not the digest of the body bytes: have %s want %s", id, want)
	}
	return id, body
}

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", filepath.Base(path), err)
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
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return out
}

// loadMapping reads the sealed manifest, checks its own ID, checks the digest of every
// source fixture it claims, and returns the allowlists it declares.
func loadMapping(t *testing.T, dir string) (mappingManifest, string, Allowlists) {
	t.Helper()
	lines := readLines(t, filepath.Join(dir, "mapping.json"))
	if len(lines) != 1 {
		t.Fatalf("mapping.json is one sealed envelope, found %d lines", len(lines))
	}
	id, body := sealedBody(t, lines[0])
	var m mappingManifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("mapping body: %v", err)
	}
	if m.Schema != "nova.tokens.mapping/2" {
		t.Errorf("mapping schema is %q", m.Schema)
	}
	if len(m.FixtureDigests) == 0 {
		t.Errorf("mapping declares no fixture digests")
	}
	for name, want := range m.FixtureDigests {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("fixture %s named by the mapping is missing: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		if have := "sha256:" + hex.EncodeToString(sum[:]); have != want {
			t.Errorf("fixture digest for %s: have %s want %s", name, have, want)
		}
	}
	var fields []string
	for name := range m.FieldRules {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return m, id, Allowlists{RawUsageFields: fields, ReceiptFields: m.IdentityRule.ReceiptFields}
}

// accepted validates every expected record with the landed boundary and returns them.
func accepted(t *testing.T, dir string, v *Validator) []*Envelope {
	t.Helper()
	var out []*Envelope
	for i, line := range readLines(t, filepath.Join(dir, "expected_records.jsonl")) {
		id, _ := sealedBody(t, line)
		env, err := v.ValidateEnvelope(line)
		if err != nil {
			t.Fatalf("expected record %d (%s) refused: %v", i, id, err)
		}
		if env.ID != id {
			t.Errorf("record %d: validator ID %s, file ID %s", i, env.ID, id)
		}
		out = append(out, env)
	}
	if len(out) == 0 {
		t.Fatalf("no expected records in %s", dir)
	}
	return out
}

// refusedCase is one line of refused_records.jsonl: the envelope bytes are kept verbatim,
// because the rule under test can be a property of the bytes.
type refusedCase struct {
	Case     string          `json:"case"`
	Rule     string          `json:"rule"`
	Field    string          `json:"field"`
	Note     string          `json:"note"`
	Envelope json.RawMessage `json:"envelope"`
}

func checkRefused(t *testing.T, dir string, v *Validator) int {
	t.Helper()
	lines := readLines(t, filepath.Join(dir, "refused_records.jsonl"))
	for _, line := range lines {
		var rc refusedCase
		if err := json.Unmarshal(line, &rc); err != nil {
			t.Fatalf("refused case: %v", err)
		}
		t.Run(rc.Case, func(t *testing.T) {
			sealedBody(t, []byte(rc.Envelope))
			_, err := v.ValidateEnvelope(rc.Envelope)
			if err == nil {
				t.Fatalf("the wire accepted a shape the mapping forbids")
			}
			ref, ok := err.(*Refusal)
			if !ok {
				t.Fatalf("refusal is not a *Refusal: %v", err)
			}
			if ref.Rule != rc.Rule {
				t.Errorf("rule: have %s want %s", ref.Rule, rc.Rule)
			}
			if !strings.HasPrefix(ref.Field, rc.Field) {
				t.Errorf("field: have %s want prefix %s", ref.Field, rc.Field)
			}
			// The diagnostic names the field and never echoes a value, so a sentinel
			// sitting in the refused field cannot reach a shared note.
			if msg := ref.Error(); strings.Contains(msg, mappingSentinelPrompt) ||
				strings.Contains(msg, mappingSentinelPath) {
				t.Errorf("a refusal echoed a private sentinel")
			}
		})
	}
	return len(lines)
}

func sentinelsInSource(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Contains(raw, []byte(mappingSentinelPrompt)) ||
			!bytes.Contains(raw, []byte(mappingSentinelPath)) {
			t.Errorf("%s must carry the privacy sentinels in its unsupported fields, or the exclusion check proves nothing", name)
		}
	}
}

func noSentinels(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, s := range []string{mappingSentinelPrompt, mappingSentinelPath} {
			if bytes.Contains(raw, []byte(s)) {
				t.Errorf("%s carries a private sentinel from an unsupported source field", name)
			}
		}
	}
}

// shardDay is the format's placement rule: the UTC day of a known point instant, and
// `unallocated` otherwise. It never invents a day for an unknown time.
func shardDay(tm Times) string {
	if tm.OccurredAt == nil {
		return "unallocated"
	}
	inst, err := time.Parse(time.RFC3339, *tm.OccurredAt)
	if err != nil {
		return "unallocated"
	}
	return inst.UTC().Format("2006-01-02")
}

// eventKeyOf is the canonical spend key [namespace, event_key...] as one comparable string.
func eventKeyOf(o *Observation) string {
	return o.Source.Namespace + "\x00" + strings.Join(o.Source.EventKey, "\x00")
}

// keyGroups groups accepted observations by spend key after deduplicating byte-identical
// observations by their ID, which is the whole of the copy rule: a copied source is the
// same observation, and a changed one is a second observation of one key.
func keyGroups(envs []*Envelope) map[string][]string {
	byID := map[string]*Envelope{}
	for _, e := range envs {
		byID[e.ID] = e
	}
	groups := map[string][]string{}
	for id, e := range byID {
		k := eventKeyOf(e.Observation)
		groups[k] = append(groups[k], id)
	}
	for k := range groups {
		sort.Strings(groups[k])
	}
	return groups
}

// rawMatches reports whether an observation's raw_usage is exactly what this source row
// wrote: every present field carries the row's own lexeme, and every key the row omits is
// absent with the mapping's reason. It is how a copied source row and a changed one are
// told apart without an adapter: one observation matches one row.
func rawMatches(raw map[string]RawField, src map[string]interface{}, rules map[string]fieldRule) bool {
	for name, rule := range rules {
		f := raw[name]
		v, inSource := src[name]
		if !inSource {
			if f.Presence != "absent" || f.Value != nil || f.Reason == nil || *f.Reason != rule.AbsentReason {
				return false
			}
			continue
		}
		// An explicit null, a wrong-typed, a negative or a non-integer supported counter is
		// an invalid raw source shape: unavailable with reason parse_failed, the source
		// value never stringified onto the wire. Validity is decided by the same lexeme
		// rule the wire enforces, not by a second grammar written here.
		n, isNum := v.(json.Number)
		if !isNum || checkValueLexeme(n.String(), rule.NumberKind, "source") != nil {
			if f.Presence != rule.InvalidPresence || f.Value != nil ||
				f.Reason == nil || *f.Reason != rule.InvalidReason {
				return false
			}
			continue
		}
		if !f.Present() || f.Value == nil || *f.Value != n.String() {
			return false
		}
	}
	return true
}

// gapped reports whether any supported field of this observation is unavailable, which is
// the completeness gap a normalized result must carry instead of a fabricated total.
func gapped(raw map[string]RawField) bool {
	for _, f := range raw {
		if f.Presence == "unavailable" {
			return true
		}
	}
	return false
}

func lexeme(t *testing.T, f RawField, field string) string {
	t.Helper()
	if !f.Present() || f.Value == nil {
		t.Fatalf("%s is not present", field)
	}
	return *f.Value
}

// ------------------------------------------------------------------- Codex

// codexRecord is the synthetic rollout shape: the fields the mapping names and nothing
// else is read. UseNumber keeps the source lexeme, so the wire's string can be compared to
// the bytes the source wrote rather than to a float64 round trip.
type codexRecord struct {
	Type             string                 `json:"type"`
	Timestamp        *string                `json:"timestamp"`
	ResponseID       string                 `json:"response_id"`
	SessionID        string                 `json:"session_id"`
	TurnID           string                 `json:"turn_id"`
	ProducerVersion  string                 `json:"producer_version"`
	TurnContextModel *string                `json:"turn_context_model"`
	Usage            map[string]interface{} `json:"usage"`
}

func readCodexSource(t *testing.T, path string) []codexRecord {
	t.Helper()
	var out []codexRecord
	for _, line := range readLines(t, path) {
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var rec codexRecord
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("codex source line: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

func TestCodexRetainedMappingFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "tokens", "codex")
	m, mappingID, allow := loadMapping(t, dir)
	v := NewValidator(allow)
	envs := accepted(t, dir, v)
	if n := checkRefused(t, dir, v); n == 0 {
		t.Errorf("no refused fixtures")
	}
	noSentinels(t, dir, "expected_records.jsonl", "refused_records.jsonl", "mapping.json")
	sentinelsInSource(t, dir, "source_rollout.jsonl", "source_rollout_copy.jsonl")
	if len(m.OverlapRule.OwedCoverageTasks) == 0 {
		t.Errorf("the owed token_count snapshot mapping must be named in the manifest")
	}

	if m.IdentityRule.SourceKind != "codex_desktop" ||
		m.IdentityRule.Namespace != "nova.codex-desktop.responses" ||
		m.IdentityRule.ObservationKind != "request" ||
		len(m.IdentityRule.EventKey) != 1 || m.IdentityRule.EventKey[0] != "response_id" {
		t.Errorf("mapping identity rule is not the decided one: %+v", m.IdentityRule)
	}
	if !m.IdentityRule.NormalizedSupport {
		t.Errorf("the Codex response mapping is supported for normalized spend")
	}

	// Every accepted observation is one response record, with the decided literals.
	rows := map[string][]codexRecord{}
	for _, name := range []string{"source_rollout.jsonl", "source_rollout_copy.jsonl"} {
		for _, rec := range readCodexSource(t, filepath.Join(dir, name)) {
			if rec.Type == "token_usage_record" {
				rows[rec.ResponseID] = append(rows[rec.ResponseID], rec)
			}
		}
	}
	seen := map[string]bool{}
	for _, env := range envs {
		o := env.Observation
		if o.Source.Kind != "codex_desktop" {
			t.Errorf("source.kind is %q", o.Source.Kind)
		}
		if o.Source.Namespace != "nova.codex-desktop.responses" {
			t.Errorf("namespace is %q", o.Source.Namespace)
		}
		if o.Kind != "request" {
			t.Errorf("kind is %q", o.Kind)
		}
		if o.Revision.Basis != "none" || o.Revision.Native != nil || len(o.Revision.Supersedes) != 0 {
			t.Errorf("revision is not {null,[],none}: %+v", o.Revision)
		}
		if o.Repository.Basis != "unattributed" || o.Repository.ID != nil ||
			o.Repository.PolicyID != nil || len(o.Repository.Touched) != 0 {
			t.Errorf("repository is not the unattributed default: %+v", o.Repository)
		}
		if len(o.ModelUsage) != 0 {
			t.Errorf("the source supplies no per-model split, so model_usage is empty")
		}
		if o.MappingID != mappingID {
			t.Errorf("mapping_id %s is not this directory's sealed mapping %s", o.MappingID, mappingID)
		}
		if len(o.Source.EventKey) != 1 {
			t.Fatalf("the spend key is [response_id]: %v", o.Source.EventKey)
		}
		id := o.Source.EventKey[0]
		cands := rows[id]
		if len(cands) == 0 {
			t.Fatalf("no source record for event key %q", id)
		}
		var rec codexRecord
		matches := 0
		for _, c := range cands {
			if rawMatches(o.RawUsage, c.Usage, m.FieldRules) {
				rec = c
				matches++
			}
		}
		// A byte-identical row copied to another bench matches the same observation, which
		// is the copy rule; a row with changed counters matches none of it, which is the
		// conflict rule. Both are asserted over the spend-key groups further down.
		if matches == 0 {
			t.Fatalf("%s: no source row matches observation %s", id, env.ID)
		}
		seen[id] = true
		if o.Source.ProducerVersion != nil {
			t.Errorf("%s: producer_version is null until an allowlisted verified source field or an owner binding supplies it", id)
		}
		if o.Source.SessionID != rec.SessionID {
			t.Errorf("%s: session_id %q is not the native containing thread %q", id, o.Source.SessionID, rec.SessionID)
		}
		if o.Receipt["response_id"] != id {
			t.Errorf("%s: receipt response_id %q", id, o.Receipt["response_id"])
		}
		// Time: the rollout record's own timestamp, or unknown with no invented instant.
		if rec.Timestamp == nil {
			if o.Time.OccurredAt != nil || o.Time.Basis != "unknown" {
				t.Errorf("%s: a record with no timestamp is occurred_at null, basis unknown", id)
			}
			if shardDay(o.Time) != "unallocated" {
				t.Errorf("%s: an unknown instant allocates no day", id)
			}
		} else if o.Time.OccurredAt == nil || *o.Time.OccurredAt != *rec.Timestamp ||
			o.Time.Basis != "response_observation" {
			t.Errorf("%s: time is the record timestamp with basis response_observation", id)
		}
		if o.Time.Start != nil || o.Time.End != nil {
			t.Errorf("%s: a response observation carries no interval", id)
		}
		// Model: the matching turn context's configured model, basis requested.
		if rec.TurnContextModel == nil {
			if o.Model.ID != nil || o.Model.Basis != "unknown" {
				t.Errorf("%s: with no turn context model the model is null/unknown", id)
			}
		} else if o.Model.ID == nil || *o.Model.ID != *rec.TurnContextModel || o.Model.Basis != "requested" {
			t.Errorf("%s: model is the turn context model with basis requested", id)
		}
		// Raw usage: the closed six-field allowlist, original lexemes, absent named.
		if len(o.RawUsage) != len(m.FieldRules) {
			t.Errorf("%s: raw_usage has %d entries, the allowlist has %d", id, len(o.RawUsage), len(m.FieldRules))
		}
		for name, rule := range m.FieldRules {
			f := o.RawUsage[name]
			if f.NumberKind != rule.NumberKind || f.Unit != rule.Unit {
				t.Errorf("%s.%s: number_kind/unit are %s/%s, the mapping says %s/%s",
					id, name, f.NumberKind, f.Unit, rule.NumberKind, rule.Unit)
			}
			// zero_semantics, applied by the landed normaliser rather than restated.
			meas := Normalize(f, ZeroSemantics(rule.ZeroSemantics))
			if f.Present() && f.IsZero() {
				want := rule.ZeroSemantics == string(ZeroMeasured)
				if meas.Measured != want || meas.SubsetComplete != want {
					t.Errorf("%s.%s: a present zero under %s normalises to measured=%v",
						id, name, rule.ZeroSemantics, meas.Measured)
				}
			}
			if !f.Present() && meas.Measured {
				t.Errorf("%s.%s: an absent field normalised to a measurement", id, name)
			}
		}
	}
	// The invalid-counter shapes are retained observations now, not gaps in the fixture:
	// their other valid fields survive and the affected field is unavailable.
	for _, id := range []string{"resp-r1", "resp-u1", "resp-u2", "resp-u3"} {
		if !seen[id] {
			t.Errorf("%s is a retained observation with an unavailable counter", id)
		}
	}
	// The cumulative token_count snapshot beside the response records is owed to a separate
	// mapping: it maps to no observation here, and the coverage limit is explicit.
	for _, rec := range readCodexSource(t, filepath.Join(dir, "source_rollout.jsonl")) {
		if rec.Type == "token_count" && seen[rec.ResponseID] {
			t.Errorf("a cumulative snapshot must synthesize no request identity")
		}
	}
	// Copy and conflict: the identical copy is one observation, the changed one conflicts.
	groups := keyGroups(envs)
	if len(envs) != 10 {
		t.Errorf("the fixture ships 10 expected record lines, found %d", len(envs))
	}
	conflicts, singles := 0, map[string]string{}
	for k, ids := range groups {
		switch len(ids) {
		case 1:
			singles[k] = ids[0]
		default:
			conflicts++
			if !strings.HasSuffix(k, "resp-c1") {
				t.Errorf("unexpected conflicting key %q", k)
			}
		}
	}
	if conflicts != 1 {
		t.Errorf("one spend key conflicts (the changed copy of resp-c1), found %d", conflicts)
	}
	if len(singles) != 7 {
		t.Errorf("seven spend keys have a single observation, found %d", len(singles))
	}
	if m.RevisionRule.NewestWins || m.RevisionRule.ChangedSameKey == "" {
		t.Errorf("the revision rule must refuse a newest-wins resolution")
	}

	// Arithmetic: a mismatch conflicts and is excluded; a missing total stays absent and is
	// only derived in a view, labelled derived, without touching the raw field.
	spendable, gaps := 0, 0
	for _, env := range envs {
		o := env.Observation
		if len(groups[eventKeyOf(o)]) != 1 {
			continue
		}
		if gapped(o.RawUsage) {
			// An unavailable counter is a named completeness gap, never a fabricated total.
			gaps++
			continue
		}
		in, out, total := o.RawUsage["input_tokens"], o.RawUsage["output_tokens"], o.RawUsage["total_tokens"]
		if in.Present() && out.Present() && total.Present() {
			sum := new(big.Int)
			a, _ := new(big.Int).SetString(lexeme(t, in, "input_tokens"), 10)
			b, _ := new(big.Int).SetString(lexeme(t, out, "output_tokens"), 10)
			c, _ := new(big.Int).SetString(lexeme(t, total, "total_tokens"), 10)
			sum.Add(a, b)
			if sum.Cmp(c) != 0 {
				continue // a retained mapping conflict, excluded from normalized spend
			}
		}
		if !total.Present() && in.Present() && out.Present() {
			a, _ := new(big.Int).SetString(lexeme(t, in, "input_tokens"), 10)
			b, _ := new(big.Int).SetString(lexeme(t, out, "output_tokens"), 10)
			derived := new(big.Int).Add(a, b)
			if derived.String() != "100" {
				t.Errorf("derived total for resp-c3 is %s", derived.String())
			}
			if total.Value != nil || total.Reason == nil {
				t.Errorf("a derived total must not fill in the raw absent field")
			}
		}
		spendable++
	}
	if spendable != 2 {
		t.Errorf("two keys are spendable (one conflicts on identity, one on arithmetic, four carry a completeness gap), found %d", spendable)
	}
	if gaps != 4 {
		t.Errorf("four keys carry an unavailable counter, found %d", gaps)
	}
	if m.ImplementationID != nil {
		t.Errorf("no adapter implementation exists at this revision")
	}
}

// -------------------------------------------------------------------- Grok

type grokExport struct {
	SessionID string                   `json:"sessionId"`
	UpdatedAt string                   `json:"updatedAt"`
	Session   map[string]interface{}   `json:"session"`
	Turns     []map[string]interface{} `json:"turns"`
}

func readGrokSource(t *testing.T, path string) grokExport {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var e grokExport
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("grok source: %v", err)
	}
	return e
}

func TestGrokRetainedMappingFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "tokens", "grok")
	m, mappingID, allow := loadMapping(t, dir)
	v := NewValidator(allow)
	envs := accepted(t, dir, v)
	if n := checkRefused(t, dir, v); n == 0 {
		t.Errorf("no refused fixtures")
	}
	noSentinels(t, dir, "expected_records.jsonl", "refused_records.jsonl", "mapping.json")
	sentinelsInSource(t, dir, "source_export.json", "source_export_copy.json", "source_export_changed.json")
	if len(m.OverlapRule.OwedCoverageTasks) == 0 {
		t.Errorf("the owed session aggregate mapping must be named in the manifest")
	}

	if m.IdentityRule.SourceKind != "grok" || m.IdentityRule.Namespace != "nova.grok.turns" ||
		m.IdentityRule.ObservationKind != "turn" ||
		strings.Join(m.IdentityRule.EventKey, ",") != "original_session_id,turn_number_as_string" {
		t.Errorf("mapping identity rule is not the decided one: %+v", m.IdentityRule)
	}
	// The identity gate: raw retention proceeds, normalized spend does not.
	if m.IdentityRule.NormalizedSupport {
		t.Errorf("Grok turn identity stability is unverified, so normalized spend stays unsupported")
	}
	if m.IdentityRule.UnsupportedReason == "" {
		t.Errorf("the unsupported normalization must name its reason")
	}
	if len(m.ModelRule.ModelUsageFields) != 8 {
		t.Fatalf("model_usage carries the eight numeric fields, the mapping names %d", len(m.ModelRule.ModelUsageFields))
	}
	for _, f := range m.ModelRule.ModelUsageFields {
		if f == "turnCount" {
			t.Errorf("turnCount is not a model_usage field")
		}
	}

	export := readGrokSource(t, filepath.Join(dir, "source_export.json"))
	turns := map[string][]map[string]interface{}{}
	for _, name := range []string{"source_export.json", "source_export_copy.json", "source_export_changed.json"} {
		for _, turn := range readGrokSource(t, filepath.Join(dir, name)).Turns {
			n, ok := turn["turnNumber"].(json.Number)
			if !ok {
				t.Fatalf("a turn has no turnNumber")
			}
			turns[n.String()] = append(turns[n.String()], turn)
		}
	}

	days := map[string]string{}
	for _, env := range envs {
		o := env.Observation
		if o.Source.Kind != "grok" || o.Source.Namespace != "nova.grok.turns" || o.Kind != "turn" {
			t.Errorf("source/kind literals are %q/%q/%q", o.Source.Kind, o.Source.Namespace, o.Kind)
		}
		if len(o.Source.EventKey) != 2 {
			t.Fatalf("the spend key is [original_session_id, turn_number]: %v", o.Source.EventKey)
		}
		if o.Source.EventKey[0] != export.SessionID || o.Source.SessionID != export.SessionID {
			t.Errorf("the key and session_id use the original native session ID, not a containing one")
		}
		num := o.Source.EventKey[1]
		cands := turns[num]
		if len(cands) == 0 {
			t.Fatalf("no source turn %q", num)
		}
		var turn map[string]interface{}
		matches := 0
		for _, c := range cands {
			if rawMatches(o.RawUsage, c, m.FieldRules) {
				turn = c
				matches++
			}
		}
		if matches == 0 {
			t.Fatalf("turn %s: no source turn matches observation %s", num, env.ID)
		}
		if o.Source.ProducerVersion != nil {
			t.Errorf("turn %s: producer_version is null without producer metadata or an owner binding", num)
		}
		if o.Receipt["turn_number"] != num {
			t.Errorf("turn %s: receipt turn_number %q", num, o.Receipt["turn_number"])
		}
		if o.MappingID != mappingID {
			t.Errorf("turn %s: mapping_id is not this directory's sealed mapping", num)
		}
		if o.Revision.Basis != "none" || o.Revision.Native != nil || len(o.Revision.Supersedes) != 0 {
			t.Errorf("turn %s: revision is not {null,[],none}", num)
		}
		if o.Repository.Basis != "unattributed" || o.Repository.ID != nil {
			t.Errorf("turn %s: repository is not unattributed", num)
		}
		// Time: endedAt with basis turn_completion, or unknown; the export's updatedAt is
		// never the event time, and no collection timestamp is substituted.
		ended, hasEnded := turn["endedAt"].(string)
		if !hasEnded {
			if o.Time.OccurredAt != nil || o.Time.Basis != "unknown" {
				t.Errorf("turn %s: a turn with no endedAt is occurred_at null, basis unknown", num)
			}
		} else {
			if o.Time.OccurredAt == nil || *o.Time.OccurredAt != ended {
				t.Errorf("turn %s: occurred_at is endedAt with its original offset", num)
			}
			if o.Time.Basis != "turn_completion" {
				t.Errorf("turn %s: time basis is turn_completion", num)
			}
		}
		if o.Time.OccurredAt != nil && *o.Time.OccurredAt == export.UpdatedAt {
			t.Errorf("turn %s: the export update time is not the turn's event time", num)
		}
		if o.Time.Start != nil || o.Time.End != nil {
			t.Errorf("turn %s: a turn observation carries no interval", num)
		}
		days[num] = shardDay(o.Time)
		// Model: one reported ID is harness_reported, several are mixed, none is unknown.
		mu, hasMU := turn["modelUsage"].(map[string]interface{})
		primary, hasPrimary := turn["primaryModelId"].(string)
		switch {
		case hasMU && len(mu) > 1:
			if o.Model.ID != nil || o.Model.Basis != "mixed" {
				t.Errorf("turn %s: more than one model ID is {null, mixed}", num)
			}
			if len(o.ModelUsage) != len(mu) {
				t.Errorf("turn %s: the source split is retained entry for entry", num)
			}
		case hasPrimary:
			if o.Model.ID == nil || *o.Model.ID != primary || o.Model.Basis != "harness_reported" {
				t.Errorf("turn %s: a single reported ID is harness_reported", num)
			}
		default:
			if o.Model.ID != nil || o.Model.Basis != "unknown" {
				t.Errorf("turn %s: an absent model ID is {null, unknown}", num)
			}
		}
		// model_usage: sorted by source model ID, and every entry carries all eight
		// numeric fields independently of the turn aggregate.
		var ids []string
		for _, e := range o.ModelUsage {
			ids = append(ids, e.ModelID)
			if len(e.RawUsage) != 8 {
				t.Errorf("turn %s: model %s carries %d of the eight numeric fields", num, e.ModelID, len(e.RawUsage))
			}
			for _, name := range m.ModelRule.ModelUsageFields {
				f, ok := e.RawUsage[name]
				if !ok {
					t.Errorf("turn %s: model %s has no %s entry", num, e.ModelID, name)
					continue
				}
				rule := m.FieldRules[name]
				if f.NumberKind != rule.NumberKind || f.Unit != rule.Unit {
					t.Errorf("turn %s: model %s field %s is %s/%s", num, e.ModelID, name, f.NumberKind, f.Unit)
				}
			}
		}
		if !sort.StringsAreSorted(ids) {
			t.Errorf("turn %s: model_usage is sorted by source model ID: %v", num, ids)
		}
		// Raw usage: the closed nine-field allowlist with original camelCase names.
		if len(o.RawUsage) != 9 {
			t.Errorf("turn %s: raw_usage has %d of the nine allowlisted fields", num, len(o.RawUsage))
		}
		for name, rule := range m.FieldRules {
			f := o.RawUsage[name]
			if f.NumberKind != rule.NumberKind || f.Unit != rule.Unit {
				t.Errorf("turn %s: %s is %s/%s, the mapping says %s/%s", num, name,
					f.NumberKind, f.Unit, rule.NumberKind, rule.Unit)
			}
			if rule.ZeroSemantics != string(ZeroUnknown) {
				t.Errorf("turn %s: %s declares zero_semantics %s; every Grok field is unknown until the producer semantics are verified", num, name, rule.ZeroSemantics)
			}
			if _, inSource := turn[name]; !inSource && f.Present() {
				t.Errorf("turn %s: %s is not in the source turn", num, name)
			}
			// A present zero under unknown zero semantics is retained and normalises to
			// nothing: it is never a measured zero.
			if f.IsZero() && Normalize(f, ZeroSemantics(rule.ZeroSemantics)).Measured {
				t.Errorf("turn %s: a present zero in %s normalised to a measurement", num, name)
			}
		}
		// The non-spend fields keep their own units and are never token spend.
		if u := o.RawUsage["costUsdTicks"]; u.Present() {
			if u.NumberKind != "decimal" || u.Unit != "usd_ticks" {
				t.Errorf("turn %s: costUsdTicks is decimal/usd_ticks", num)
			}
			if num == "1" && lexeme(t, u, "costUsdTicks") != "77" {
				t.Errorf("turn %s: the cost tick lexeme is retained exactly as 77, no dollars conversion", num)
			}
		}
		if u := o.RawUsage["modelCalls"]; u.Unit != "calls" {
			t.Errorf("turn %s: modelCalls is counted in calls", num)
		}
		if u := o.RawUsage["turnCount"]; u.Unit != "turns" {
			t.Errorf("turn %s: turnCount is counted in turns", num)
		}
	}
	// Day allocation: the UTC day of a known completion instant, whatever its offset, and
	// `unallocated` when the instant is unknown. Turn 2 completes at 23:30-04:00, which is
	// the next UTC day: the completion-day convention, not a call-day claim.
	for num, want := range map[string]string{
		"1": "2026-09-12", "2": "2026-09-12", "3": "unallocated", "4": "2026-09-12",
		"5": "2026-09-12"} {
		if days[num] != want {
			t.Errorf("turn %s allocates to %q, want %q", num, days[num], want)
		}
	}
	// Copied and changed exports: one key, two observations, a visible conflict.
	groups := keyGroups(envs)
	if len(envs) != 7 {
		t.Errorf("the fixture ships 7 expected record lines, found %d", len(envs))
	}
	// The export's session totals are owed to a separate aggregate mapping: no observation
	// here carries them, and they can never fill a missing turn.
	if len(export.Session) == 0 {
		t.Fatalf("the source export must keep its session totals visible as owed evidence")
	}
	for _, env := range envs {
		if env.Observation.Kind == "aggregate" {
			t.Errorf("the turn mapping emits no session aggregate; that mapping is owed")
		}
	}
	conflicts := 0
	for k, ids := range groups {
		if len(ids) > 1 {
			conflicts++
			if !strings.HasSuffix(k, "\x001") {
				t.Errorf("unexpected conflicting key %q", k)
			}
		}
	}
	if conflicts != 1 {
		t.Errorf("one spend key conflicts (the changed copy of turn 1), found %d", conflicts)
	}
	copyExport := readGrokSource(t, filepath.Join(dir, "source_export_copy.json"))
	if copyExport.SessionID != export.SessionID {
		t.Errorf("a copied export keeps the original session ID")
	}
	changed := readGrokSource(t, filepath.Join(dir, "source_export_changed.json"))
	if changed.Turns[0]["inputTokens"].(json.Number).String() ==
		export.Turns[0]["inputTokens"].(json.Number).String() {
		t.Errorf("the changed-copy fixture must change a counter")
	}
	if m.RevisionRule.NewestWins {
		t.Errorf("no newest-wins rule resolves a Grok same-key conflict")
	}
	if m.ImplementationID != nil {
		t.Errorf("no adapter implementation exists at this revision")
	}
}

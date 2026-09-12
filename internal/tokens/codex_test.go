package tokens

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The decoder's own tests, over the synthetic fixtures of PR #142 and nothing else. No
// real Codex session store is opened here or anywhere in codex.go: the fixtures are
// invented, and the unsupported source fields carry privacy sentinels so the exclusion
// checks below prove something.
//
// The mapping decisions are the source owner's, tabulated in docs/MAPPING-TOKENS-CODEX.md
// and made executable by testdata/tokens/codex/mapping.json. Nothing here restates a cell:
// the manifest is read, and the expected envelopes are compared byte for byte.

const (
	codexSentinelPrompt = "SENTINEL-PRIVATE-PROMPT-DO-NOT-PUBLISH-7f3a"
	codexSentinelPath   = "SENTINEL-PRIVATE-PATH-DO-NOT-PUBLISH-9c21"
)

// codexFixtureBinding is the owner-supplied binding the expected envelopes were authored
// against. It is test data, not a default: the collection host never supplies origin.
var codexFixtureBinding = CodexBinding{
	ID:     "binding-codex-desktop-studio-1",
	Friend: "rowan",
	Bench:  "studio",
}

func codexDir() string { return filepath.Join("..", "..", "testdata", "tokens", "codex") }

func codexMapping(t *testing.T) *CodexMapping {
	t.Helper()
	m, err := ReadCodexMapping(filepath.Join(codexDir(), "mapping.json"))
	if err != nil {
		t.Fatalf("the sealed mapping manifest: %v", err)
	}
	if err := CheckCodexFixtureDigests(m, codexDir()); err != nil {
		t.Fatalf("the source fixtures are not the bytes the manifest names: %v", err)
	}
	return m
}

func codexSourcePaths() []string {
	return []string{
		filepath.Join(codexDir(), "source_rollout.jsonl"),
		filepath.Join(codexDir(), "source_rollout_copy.jsonl"),
	}
}

func codexDecode(t *testing.T) (*CodexMapping, *CodexDecoding) {
	t.Helper()
	m := codexMapping(t)
	d, err := DecodeCodexRollout(m, codexFixtureBinding, codexSourcePaths())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m, d
}

func codexLines(t *testing.T, name string) [][]byte {
	t.Helper()
	f, err := os.Open(filepath.Join(codexDir(), name))
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
	return out
}

func codexByResponse(d *CodexDecoding, id string) []CodexObservation {
	var out []CodexObservation
	for _, o := range d.Observations {
		if o.ResponseID == id {
			out = append(out, o)
		}
	}
	return out
}

func codexOne(t *testing.T, d *CodexDecoding, id string) CodexObservation {
	t.Helper()
	got := codexByResponse(d, id)
	if len(got) != 1 {
		t.Fatalf("%s: %d observations, want 1", id, len(got))
	}
	return got[0]
}

// TestCodexDecoderMatchesTheExpectedEnvelopesByteForByte is the contract. The expected
// envelopes were stated independently of any adapter; if the decoder's sealed bytes differ
// by one byte the ID differs, and a moved ID is a second identity for one spend event.
//
// The comparison is over the MULTISET of sealed lines, sorted on both sides: the mapping
// document fixes every byte of an envelope and fixes no emission order, so an order
// assertion here would be this test inventing a cell. The count is asserted too, and the
// exact duplicate is a line on both sides rather than a collapsed one.
func TestCodexDecoderMatchesTheExpectedEnvelopesByteForByte(t *testing.T) {
	_, d := codexDecode(t)
	want := codexLines(t, "expected_records.jsonl")
	have := make([][]byte, 0, len(d.Observations))
	for _, o := range d.Observations {
		have = append(have, o.Envelope)
	}
	if len(have) != len(want) {
		t.Fatalf("the decoder sealed %d envelopes, the fixture states %d", len(have), len(want))
	}
	sortBytes := func(ls [][]byte) {
		sort.Slice(ls, func(i, j int) bool { return bytes.Compare(ls[i], ls[j]) < 0 })
	}
	sortBytes(have)
	sortBytes(want)
	for i := range want {
		if !bytes.Equal(have[i], want[i]) {
			// The diagnostic names the sealed IDs and the first differing offset, never a
			// source value: an envelope this decoder rejected can hold a sentinel.
			hid, _, _ := codexSealedForTest(have[i])
			wid, _, _ := codexSealedForTest(want[i])
			at := 0
			for at < len(have[i]) && at < len(want[i]) && have[i][at] == want[i][at] {
				at++
			}
			t.Fatalf("sealed envelope %d differs from the fixture at offset %d: decoder %s, fixture %s",
				i, at, hid, wid)
		}
	}
}

func codexSealedForTest(line []byte) (string, []byte, error) { return codexSealed(line) }

// TestCodexDecoderSealsThroughTheLandedBoundary proves the bytes are not merely equal to a
// file but accepted by the record validator under this mapping's own allowlists, with the
// ID the validator derives.
func TestCodexDecoderSealsThroughTheLandedBoundary(t *testing.T) {
	m, d := codexDecode(t)
	v := records.NewValidator(m.Allowlists())
	for _, o := range d.Observations {
		env, err := v.ValidateEnvelope(o.Envelope)
		if err != nil {
			t.Fatalf("%s: the decoder sealed bytes the boundary refuses: %v", o.ResponseID, err)
		}
		if env.ID != o.ID {
			t.Errorf("%s: the decoder reports %s, the validator derives %s", o.ResponseID, o.ID, env.ID)
		}
		if env.Observation.MappingID != m.ID {
			t.Errorf("%s: mapping_id %s is not the sealed manifest %s", o.ResponseID, env.Observation.MappingID, m.ID)
		}
		if env.Observation.Source.Kind != m.SourceKind || env.Observation.Source.Namespace != m.Namespace {
			t.Errorf("%s: source is %+v", o.ResponseID, env.Observation.Source)
		}
		if len(env.Observation.RawUsage) != len(m.Fields) {
			t.Errorf("%s: raw_usage has %d entries, the closed allowlist has %d",
				o.ResponseID, len(env.Observation.RawUsage), len(m.Fields))
		}
		if len(env.Observation.ModelUsage) != 0 {
			t.Errorf("%s: the source supplies no per-model split", o.ResponseID)
		}
		if env.Observation.Source.ProducerVersion != nil {
			t.Errorf("%s: producer_version is null in this mapping revision", o.ResponseID)
		}
	}
}

// TestCodexDecoderRefusesTheFixtureRefusals: every refused shape refuses with the rule and
// the field the fixture names, and the decoder emits none of them.
func TestCodexDecoderRefusesTheFixtureRefusals(t *testing.T) {
	m, d := codexDecode(t)
	v := records.NewValidator(m.Allowlists())
	produced := map[string]bool{}
	for _, o := range d.Observations {
		produced[o.ID] = true
	}
	lines := codexLines(t, "refused_records.jsonl")
	if len(lines) == 0 {
		t.Fatalf("no refused fixtures")
	}
	for _, line := range lines {
		var rc struct {
			Case     string          `json:"case"`
			Rule     string          `json:"rule"`
			Field    string          `json:"field"`
			Envelope json.RawMessage `json:"envelope"`
		}
		if err := json.Unmarshal(line, &rc); err != nil {
			t.Fatalf("refused case: %v", err)
		}
		t.Run(rc.Case, func(t *testing.T) {
			id, _, err := codexSealed(bytes.TrimSpace(rc.Envelope))
			if err != nil {
				t.Fatalf("the refused fixture is not a sealed envelope: %v", err)
			}
			if produced[id] {
				t.Fatalf("the decoder produced a shape the wire refuses")
			}
			_, verr := v.ValidateEnvelope(rc.Envelope)
			if verr == nil {
				t.Fatalf("the boundary accepted a shape the mapping forbids")
			}
			ref, ok := verr.(*records.Refusal)
			if !ok {
				t.Fatalf("the refusal is not a *records.Refusal: %v", verr)
			}
			if ref.Rule != rc.Rule {
				t.Errorf("rule: have %s want %s", ref.Rule, rc.Rule)
			}
			if !strings.HasPrefix(ref.Field, rc.Field) {
				t.Errorf("field: have %s want prefix %s", ref.Field, rc.Field)
			}
			if msg := ref.Error(); strings.Contains(msg, codexSentinelPrompt) || strings.Contains(msg, codexSentinelPath) {
				t.Errorf("a refusal echoed a private sentinel")
			}
		})
	}
}

// TestCodexDecoderLeaksNoPrivacySentinel asserts BOTH sides: the source fixtures carry the
// sentinels in their unsupported fields, and no sealed envelope, no diagnostic and no
// rendering of the whole decoding carries one. resp-u2's output_tokens VALUE is a sentinel,
// so the unavailable path is covered too.
func TestCodexDecoderLeaksNoPrivacySentinel(t *testing.T) {
	for _, name := range []string{"source_rollout.jsonl", "source_rollout_copy.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(codexDir(), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Contains(raw, []byte(codexSentinelPrompt)) || !bytes.Contains(raw, []byte(codexSentinelPath)) {
			t.Fatalf("%s must carry the sentinels, or this check proves nothing", name)
		}
	}
	m, d := codexDecode(t)
	rendered := fmt.Sprintf("%+v", *d)
	for _, o := range d.Observations {
		rendered += string(o.Envelope)
	}
	for _, r := range d.Refusals {
		rendered += r.ResponseID + r.Rule + r.Field
	}
	rendered += strings.Join(d.CoverageLimits(), "\n")
	raw, err := os.ReadFile(filepath.Join(codexDir(), "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	rendered += string(raw) + m.ID
	for _, s := range []string{codexSentinelPrompt, codexSentinelPath} {
		if strings.Contains(rendered, s) {
			t.Errorf("a private sentinel reached a record, a manifest or a diagnostic")
		}
	}
	// A wrong-typed counter holding a sentinel is unavailable/parse_failed with a null
	// value, never the source value stringified onto the wire.
	u2 := codexOne(t, d, "resp-u2")
	if !u2.Gap {
		t.Errorf("resp-u2 carries an unavailable counter and therefore a completeness gap")
	}
}

// TestCodexDecoderPreservesLexemesPresenceAndModelBasis walks the decided cells over the
// accepted envelopes: original lexemes, the three presence states, zero semantics, the
// model basis, and the identity provenance.
func TestCodexDecoderPreservesLexemesPresenceAndModelBasis(t *testing.T) {
	m, d := codexDecode(t)
	v := records.NewValidator(m.Allowlists())
	obs := map[string]*records.Observation{}
	for _, o := range d.Observations {
		env, err := v.ValidateEnvelope(o.Envelope)
		if err != nil {
			t.Fatalf("%s: %v", o.ResponseID, err)
		}
		if _, seen := obs[o.ResponseID]; seen {
			continue // a copy or a conflicting second observation of one key, checked below
		}
		obs[o.ResponseID] = env.Observation
	}

	// resp-c1: every supported counter present with its own lexeme, the turn context's
	// model at basis requested, the containing thread as provenance, the owner binding.
	c1 := obs["resp-c1"]
	for name, want := range map[string]string{
		"input_tokens": "1200", "output_tokens": "340", "total_tokens": "1540",
		"cached_input_tokens": "800", "cache_write_input_tokens": "0", "reasoning_output_tokens": "0",
	} {
		f := c1.RawUsage[name]
		if !f.Present() || f.Value == nil || *f.Value != want {
			t.Errorf("resp-c1.%s is not the source lexeme %s: %+v", name, want, f)
		}
		if f.NumberKind != m.Fields[name].NumberKind || f.Unit != m.Fields[name].Unit {
			t.Errorf("resp-c1.%s declares %s/%s, the mapping says %s/%s",
				name, f.NumberKind, f.Unit, m.Fields[name].NumberKind, m.Fields[name].Unit)
		}
	}
	if c1.Model.ID == nil || *c1.Model.ID != "gpt-5-codex" || c1.Model.Basis != m.ModelBasis {
		t.Errorf("resp-c1 model is %+v; the turn context's configured model at basis %s", c1.Model, m.ModelBasis)
	}
	if c1.Source.SessionID != "thread-55" || len(c1.Source.EventKey) != 1 || c1.Source.EventKey[0] != "resp-c1" {
		t.Errorf("resp-c1 identity is %+v; the key is [response_id] and the thread is provenance", c1.Source)
	}
	if c1.Origin.Basis != "owner_binding" || c1.Origin.BindingID == nil || *c1.Origin.BindingID != codexFixtureBinding.ID ||
		c1.Origin.Friend == nil || *c1.Origin.Friend != codexFixtureBinding.Friend ||
		c1.Origin.Bench == nil || *c1.Origin.Bench != codexFixtureBinding.Bench {
		t.Errorf("resp-c1 origin is %+v; the supplied binding and nothing from the collection host", c1.Origin)
	}
	if c1.Time.OccurredAt == nil || *c1.Time.OccurredAt != "2026-09-12T01:02:03-04:00" || c1.Time.Basis != m.TimeBasis {
		t.Errorf("resp-c1 time is %+v; the record's own timestamp with its offset preserved", c1.Time)
	}
	if c1.Time.Start != nil || c1.Time.End != nil {
		t.Errorf("a response observation carries no interval: %+v", c1.Time)
	}

	// resp-c2: a present zero base counter is measured; the absent details are named
	// absent/not_supplied, not zero.
	c2 := obs["resp-c2"]
	out := c2.RawUsage["output_tokens"]
	if !out.Present() || !out.IsZero() {
		t.Errorf("resp-c2 output_tokens is a present zero: %+v", out)
	}
	if meas := records.Normalize(out, records.ZeroSemantics(m.Fields["output_tokens"].ZeroSemantics)); !meas.Measured {
		t.Errorf("a present zero base counter is measured under %s", m.Fields["output_tokens"].ZeroSemantics)
	}
	for _, name := range []string{"cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens"} {
		f := c2.RawUsage[name]
		if f.Presence != m.Fields[name].AbsentPresence || f.Value != nil ||
			f.Reason == nil || *f.Reason != m.Fields[name].AbsentReason {
			t.Errorf("resp-c2.%s is %+v; a key the source omits is %s/%s",
				name, f, m.Fields[name].AbsentPresence, m.Fields[name].AbsentReason)
		}
	}

	// resp-c1's defaulted-zero details: raw 0 survives, and its uncertainty survives with
	// it. The producer writes 0 where it measured nothing, so this zero is not a
	// measurement and detail completeness is false.
	for _, name := range []string{"cache_write_input_tokens", "reasoning_output_tokens"} {
		f := c1.RawUsage[name]
		if !f.Present() || !f.IsZero() {
			t.Errorf("resp-c1.%s is a present raw zero: %+v", name, f)
		}
		if meas := records.Normalize(f, records.ZeroSemantics(m.Fields[name].ZeroSemantics)); meas.Measured || meas.SubsetComplete {
			t.Errorf("resp-c1.%s: a defaulted zero under %s is not a measurement", name, m.Fields[name].ZeroSemantics)
		}
	}
	if o := codexByResponse(d, "resp-c1"); len(o) == 0 || o[0].DetailComplete {
		t.Errorf("resp-c1 detail completeness is false while a defaulted zero stands")
	}

	// resp-c3: no timestamp and no turn context. No instant is invented, no model is
	// claimed, no day is allocated, and the total the source never wrote stays absent.
	c3 := obs["resp-c3"]
	if c3.Time.OccurredAt != nil || c3.Time.Basis != m.TimeBasisMissing {
		t.Errorf("resp-c3 time is %+v; a missing timestamp is null/%s", c3.Time, m.TimeBasisMissing)
	}
	if c3.Model.ID != nil || c3.Model.Basis != m.ModelBasisAbsent {
		t.Errorf("resp-c3 model is %+v; with no turn context it is null/%s", c3.Model, m.ModelBasisAbsent)
	}
	if total := c3.RawUsage["total_tokens"]; total.Presence != m.Fields["total_tokens"].AbsentPresence || total.Value != nil {
		t.Errorf("resp-c3 total_tokens is %+v; a total the source never wrote stays absent", total)
	}
	e3 := codexOne(t, d, "resp-c3")
	if e3.Day != "unallocated" {
		t.Errorf("resp-c3 allocates no day, it reports %s", e3.Day)
	}
	if e3.DerivedTotal != "100" {
		t.Errorf("resp-c3's derived total is %q, want 100 -- labelled derived, beside the absent raw field", e3.DerivedTotal)
	}

	// The four invalid supported counters: unavailable/parse_failed, the same number_kind
	// and unit, never a coerced zero, and the rest of the observation intact.
	for _, tc := range []struct{ id, field string }{
		{"resp-r1", "input_tokens"},            // 12.5, a non-integer
		{"resp-u1", "input_tokens"},            // an explicit null
		{"resp-u2", "output_tokens"},           // a wrong-typed value holding a sentinel
		{"resp-u3", "reasoning_output_tokens"}, // a negative count
	} {
		o := obs[tc.id]
		if o == nil {
			t.Fatalf("%s is a retained observation with an unavailable counter", tc.id)
		}
		f := o.RawUsage[tc.field]
		rule := m.Fields[tc.field]
		if f.Presence != rule.InvalidPresence || f.Value != nil ||
			f.Reason == nil || *f.Reason != rule.InvalidReason {
			t.Errorf("%s.%s is %+v; an invalid supported counter is %s/%s with a null value",
				tc.id, tc.field, f, rule.InvalidPresence, rule.InvalidReason)
		}
		if f.NumberKind != rule.NumberKind || f.Unit != rule.Unit {
			t.Errorf("%s.%s keeps its declared number_kind and unit", tc.id, tc.field)
		}
		if meas := records.Normalize(f, records.ZeroSemantics(rule.ZeroSemantics)); meas.Measured {
			t.Errorf("%s.%s: an unavailable counter never normalises to a measurement", tc.id, tc.field)
		}
		// The observation's other valid fields survive it.
		if o.Model.ID == nil || o.Time.OccurredAt == nil || o.Receipt["response_id"] != tc.id {
			t.Errorf("%s: the other valid fields of an observation survive an invalid counter", tc.id)
		}
		e := codexOne(t, d, tc.id)
		if !e.Gap || e.Spendable {
			t.Errorf("%s carries a completeness gap and is excluded from normalized spend", tc.id)
		}
	}

	// The receipt carries the two allowlisted locators and nothing else.
	for id, o := range obs {
		if len(o.Receipt) > len(m.ReceiptFields) {
			t.Errorf("%s: the receipt carries %d fields, the allowlist names %d", id, len(o.Receipt), len(m.ReceiptFields))
		}
		for k := range o.Receipt {
			allowed := false
			for _, want := range m.ReceiptFields {
				allowed = allowed || k == want
			}
			if !allowed {
				t.Errorf("%s: the receipt carries %q, which the mapping does not allowlist", id, k)
			}
		}
	}
}

// TestCodexDecoderCopyConflictAndArithmetic: a byte-identical copy is one observation, a
// changed copy is a conflict with no newest-wins, an arithmetic mismatch is retained and
// excluded, and two keys are spendable.
func TestCodexDecoderCopyConflictAndArithmetic(t *testing.T) {
	_, d := codexDecode(t)
	c1 := codexByResponse(d, "resp-c1")
	if len(c1) != 3 {
		t.Fatalf("resp-c1 has %d source rows, want 3 (the original, its byte copy and the changed copy)", len(c1))
	}
	ids := map[string]int{}
	dups := 0
	for _, o := range c1 {
		ids[o.ID]++
		if o.Duplicate {
			dups++
		}
		if !o.Conflict {
			t.Errorf("resp-c1: every observation of a conflicting key is marked conflicted")
		}
		if o.Spendable {
			t.Errorf("resp-c1: a conflicting key is excluded from normalized spend")
		}
	}
	if len(ids) != 2 {
		t.Errorf("resp-c1 has %d distinct sealed identities, want 2: a copy deduplicates, a change does not", len(ids))
	}
	if dups != 1 {
		t.Errorf("%d byte-identical copies deduplicate, want 1", dups)
	}
	// The changed copy carries the changed lexemes; no rule picks a winner.
	if d.Winner("resp-c1") != "" {
		t.Errorf("there is no newest-wins resolution for a conflicting key")
	}

	// Arithmetic: 100 + 50 is not 900. Retained, never adjusted, excluded from spend.
	c4 := codexOne(t, d, "resp-c4")
	if !c4.ArithmeticMismatch || c4.Spendable {
		t.Errorf("resp-c4 is an arithmetic conflict excluded from normalized spend: %+v", c4)
	}

	spendable := []string{}
	gaps := 0
	for _, o := range d.Observations {
		if o.Spendable {
			spendable = append(spendable, o.ResponseID)
		}
		if o.Gap {
			gaps++
		}
	}
	sort.Strings(spendable)
	if want := []string{"resp-c2", "resp-c3"}; strings.Join(spendable, ",") != strings.Join(want, ",") {
		t.Errorf("spendable keys are %v, want %v (one conflicts on identity, one on arithmetic, four carry a gap)", spendable, want)
	}
	if gaps != 4 {
		t.Errorf("%d observations carry an unavailable counter, want 4", gaps)
	}
}

// TestCodexDecoderLeavesTheOwedSnapshotShapeUncovered: the cumulative token_count line
// beside the response records maps to nothing, is counted as an explicit coverage limit,
// and the owed task the manifest names is reported so a report cannot claim complete
// history. Nothing approximates a request identity or a spend from a cumulative snapshot.
func TestCodexDecoderLeavesTheOwedSnapshotShapeUncovered(t *testing.T) {
	m, d := codexDecode(t)
	if n := d.Unsupported["token_count"]; n != 1 {
		t.Errorf("the token_count snapshot is counted as unsupported once, found %d", n)
	}
	for _, o := range d.Observations {
		if !strings.HasPrefix(o.ResponseID, "resp-") {
			t.Errorf("a cumulative snapshot synthesized the identity %q", o.ResponseID)
		}
	}
	if len(d.Owed) == 0 || d.Owed[0] != "codex_token_count_snapshot_mapping" {
		t.Errorf("the owed task the manifest names is %v", d.Owed)
	}
	limits := d.CoverageLimits()
	if len(limits) == 0 {
		t.Fatalf("an unsupported shape beside the response records is an explicit coverage limit")
	}
	joined := strings.Join(limits, "\n")
	if !strings.Contains(joined, "token_count") || !strings.Contains(joined, m.OwedCoverageTasks[0]) {
		t.Errorf("the coverage limit names the shape and the owed task: %q", joined)
	}
	if len(limits) > 4 {
		t.Errorf("the coverage limits are counts and one line per shape, not a list of records: %d lines", len(limits))
	}
}

// TestCodexDecoderSelectsTopLevelRecordsOnly: the mapping selects a top-level
// token_usage_record. An event_msg carrying the same name inside a payload is the wrapper
// shape the document rules out, and a record with no usage manufactures nothing.
func TestCodexDecoderSelectsTopLevelRecordsOnly(t *testing.T) {
	m := codexMapping(t)
	lines := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"token_usage_record","response_id":"resp-w1","session_id":"t1","turn_id":"1","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3,"cached_input_tokens":0,"cache_write_input_tokens":0,"reasoning_output_tokens":0}}}`,
		`{"type":"token_usage_record","response_id":"resp-w2","session_id":"t1","turn_id":"2","timestamp":"2026-09-12T00:00:00Z"}`,
		`{"type":"token_usage_record","session_id":"t1","turn_id":"3","usage":{"input_tokens":1}}`,
		`{"type":"token_usage_record","response_id":"resp-w4","session_id":"t1","turn_id":"4","timestamp":"2026-09-12T00:00:00Z","usage":{"input_tokens":7,"unknown_producer_metric":9}}`,
	}, "\n")
	d, err := DecodeCodexReaders(m, codexFixtureBinding, []io.Reader{strings.NewReader(lines)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(d.Observations) != 1 || d.Observations[0].ResponseID != "resp-w4" {
		var got []string
		for _, o := range d.Observations {
			got = append(got, o.ResponseID)
		}
		t.Fatalf("only the top-level record with a usage object and an identity maps: %v", got)
	}
	if d.Unsupported["event_msg"] != 1 {
		t.Errorf("a wrapper event_msg is an unsupported shape, counted: %v", d.Unsupported)
	}
	if d.Unsupported["token_usage_record_without_usage"] != 1 {
		t.Errorf("a response with no usage manufactures no record, and the gap is counted: %v", d.Unsupported)
	}
	if d.Unsupported["token_usage_record_without_response_id"] != 1 {
		t.Errorf("a record with no response identity manufactures no key, and the gap is counted: %v", d.Unsupported)
	}
	// An unknown producer metric is excluded, never admitted into the allowlist from the
	// input. The six supported fields all have an entry; the seventh name appears nowhere.
	v := records.NewValidator(m.Allowlists())
	env, err := v.ValidateEnvelope(d.Observations[0].Envelope)
	if err != nil {
		t.Fatalf("resp-w4: %v", err)
	}
	if len(env.Observation.RawUsage) != len(m.Fields) {
		t.Errorf("raw_usage has %d entries, the closed allowlist has %d", len(env.Observation.RawUsage), len(m.Fields))
	}
	if bytes.Contains(d.Observations[0].Envelope, []byte("unknown_producer_metric")) {
		t.Errorf("an unsupported producer metric was promoted into the wire")
	}
}

// TestCodexDecoderKeepsIdentityLexemesAndDoesNotRelabelEarlierModels: a native numeric turn
// identifier is preserved as its exact decimal string, and a later configured model never
// relabels an earlier response.
func TestCodexDecoderKeepsIdentityLexemesAndDoesNotRelabelEarlierModels(t *testing.T) {
	m := codexMapping(t)
	lines := strings.Join([]string{
		`{"type":"token_usage_record","response_id":"resp-m1","session_id":"t1","turn_id":9007199254740993,"timestamp":"2026-09-12T00:00:00Z","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		`{"type":"token_usage_record","response_id":"resp-m2","session_id":"t1","turn_id":2,"timestamp":"2026-09-12T00:01:00Z","turn_context_model":"gpt-5-codex","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	}, "\n")
	d, err := DecodeCodexReaders(m, codexFixtureBinding, []io.Reader{strings.NewReader(lines)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(d.Observations) != 2 {
		t.Fatalf("%d observations, want 2", len(d.Observations))
	}
	v := records.NewValidator(m.Allowlists())
	first, err := v.ValidateEnvelope(d.Observations[0].Envelope)
	if err != nil {
		t.Fatalf("resp-m1: %v", err)
	}
	if got := first.Observation.Receipt["turn_id"]; got != "9007199254740993" {
		t.Errorf("a native numeric turn identifier is its exact decimal string, got %q", got)
	}
	if first.Observation.Model.ID != nil || first.Observation.Model.Basis != m.ModelBasisAbsent {
		t.Errorf("the earlier response has no configured model and a later one does not relabel it: %+v", first.Observation.Model)
	}
	second, err := v.ValidateEnvelope(d.Observations[1].Envelope)
	if err != nil {
		t.Fatalf("resp-m2: %v", err)
	}
	if second.Observation.Model.ID == nil || *second.Observation.Model.ID != "gpt-5-codex" {
		t.Errorf("the later response carries its own configured model: %+v", second.Observation.Model)
	}
	// No binding, no origin: the collection host never supplies one.
	unbound, err := DecodeCodexReaders(m, CodexBinding{}, []io.Reader{strings.NewReader(lines)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, o := range unbound.Observations {
		env, err := v.ValidateEnvelope(o.Envelope)
		if err != nil {
			t.Fatalf("%s: %v", o.ResponseID, err)
		}
		if env.Observation.Origin.Basis != "unknown" || env.Observation.Origin.Friend != nil ||
			env.Observation.Origin.Bench != nil || env.Observation.Origin.BindingID != nil {
			t.Errorf("%s: with no owner binding the origin is {null,null,unknown,null}, got %+v",
				o.ResponseID, env.Observation.Origin)
		}
	}
}

// TestCodexShardDayIsTheUTCDayOfAKnownInstant: the offset decides the day, and an unknown
// instant allocates none. A -04:00 evening is the next UTC day, which is the whole reason
// the format shards on the UTC day of the point instant and not on the source's local date.
func TestCodexShardDayIsTheUTCDayOfAKnownInstant(t *testing.T) {
	at := func(s string) *string { return &s }
	for _, tc := range []struct{ in, want string }{
		{"2026-09-12T01:02:03-04:00", "2026-09-12"},
		{"2026-09-11T21:00:00-04:00", "2026-09-12"},
		{"2026-09-12T00:30:00Z", "2026-09-12"},
		{"2026-09-12T02:00:00+05:00", "2026-09-11"},
	} {
		if got := CodexShardDay(at(tc.in)); got != tc.want {
			t.Errorf("%s allocates to %s, want %s", tc.in, got, tc.want)
		}
	}
	if got := CodexShardDay(nil); got != "unallocated" {
		t.Errorf("an unknown instant allocates no day, got %s", got)
	}
}

// TestCodexMappingManifestIsTheDecidedContract: the decoder reads its decisions from the
// sealed manifest, and the identity literals its code paths were written for are checked
// rather than assumed. A manifest that decided something else must fail here, not be
// silently followed.
func TestCodexMappingManifestIsTheDecidedContract(t *testing.T) {
	m := codexMapping(t)
	if m.Schema != "nova.tokens.mapping/2" {
		t.Errorf("manifest schema is %q", m.Schema)
	}
	if m.SourceKind != "codex_desktop" || m.Namespace != "nova.codex-desktop.responses" ||
		m.ObservationKind != "request" || len(m.EventKey) != 1 || m.EventKey[0] != "response_id" {
		t.Errorf("the identity rule is not the decided one: %+v", m)
	}
	if strings.Join(m.ReceiptFields, ",") != "response_id,turn_id" {
		t.Errorf("the receipt allowlist is %v", m.ReceiptFields)
	}
	if strings.Join(m.FieldNames(), ",") != "cache_write_input_tokens,cached_input_tokens,input_tokens,output_tokens,reasoning_output_tokens,total_tokens" {
		t.Errorf("the raw_usage allowlist is %v", m.FieldNames())
	}
	for _, name := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if m.Fields[name].ZeroSemantics != string(records.ZeroMeasured) {
			t.Errorf("%s zero semantics are %q; the three base counters are measured on this pinned path",
				name, m.Fields[name].ZeroSemantics)
		}
	}
	for _, name := range []string{"cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens"} {
		if m.Fields[name].ZeroSemantics != string(records.ZeroMayMaskAbsent) {
			t.Errorf("%s zero semantics are %q; a defaulted detail may mask absence",
				name, m.Fields[name].ZeroSemantics)
		}
	}
	if !m.NormalizedSpendSupported {
		t.Errorf("the response mapping is supported for normalized spend")
	}
	if m.RevisionBasis != "none" || m.TimeBasis != "response_observation" || m.TimeBasisMissing != "unknown" ||
		m.ModelBasis != "requested" || m.ModelBasisAbsent != "unknown" {
		t.Errorf("a decided basis is not the one this decoder writes: %+v", m)
	}
}

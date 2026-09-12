package tokens

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The retained-token decoder for Codex Desktop rollouts: one top-level `token_usage_record`
// line in, one sealed nova.tokens.observation/2 envelope out.
//
// WHOSE DECISIONS THESE ARE. Every cell this file applies was decided by the source
// mapping's owner in docs/MAPPING-TOKENS-CODEX.md ("Wire literals under the mapping
// decisions"), and the executable form of those decisions is the sealed manifest at
// testdata/tokens/codex/mapping.json. This decoder READS the manifest -- the field
// allowlist, each field's number_kind, unit, zero semantics and what an absent or invalid
// value becomes, the identity rule, the time and model bases, the owed coverage task -- so
// that a decision lives in one place. It re-derives none of them and softens none of them.
// Where the mapping says a shape is owed, this decoder maps it to NOTHING and counts it:
// the cumulative `token_count` snapshot has no request identity and no additive spend, and
// approximating one would be the exact failure the document names.
//
// WHAT IT NEVER DOES. It opens no real Codex session store: the caller names the files, and
// this repository's fixtures are synthetic. It reads only the source fields the mapping
// names, so a prompt, a preview, a rollout path or any other unsupported field cannot reach
// a record -- and cannot reach a DIAGNOSTIC either, which is why every count below is keyed
// by a fixed vocabulary of shape names rather than by a string taken from the source, and
// why no message in this file formats a source value. A sentinel sitting in an unsupported
// field, or in a supported field holding the wrong type, must appear in no record, no
// manifest and no diagnostic; codex_test.go asserts both sides of that.
//
// WHY IT SEALS THROUGH THE RECORDS PACKAGE. records.Validator.SealObservation writes the
// canonical bytes and then reads them back through the boundary, so a record this decoder
// would be refused for is refused HERE with the rule and the field named. There is no
// second serializer in this file: a byte that moves moves the ID, and a moved ID is a
// second identity for one spend event.

// The wire literals the mapping document fixes in its envelope table but the manifest
// carries no rule for. They are named here so that a person reading a record can find the
// cell that decided it, and nothing else in this file writes an enum by hand.
const (
	// repository: "{id: null, basis: unattributed, policy_id: null, touched: []} absent a
	// separately evidenced policy". This source is a multi-repo sitting and the collection
	// host establishes nothing, so the repository is unattributed rather than guessed.
	codexRepositoryBasis = "unattributed"
	// origin: "{friend, bench, basis: owner_binding, binding_id} with an explicit stable
	// source-binding ID and the supplied original friend/bench; otherwise {null, null,
	// unknown, null}. The collection host never supplies origin."
	codexOriginBound   = "owner_binding"
	codexOriginUnknown = "unknown"
)

// The fixed vocabulary of shapes this decoder counts but does not map. A source `type`
// string is never used as a count key: a count is a diagnostic, and a diagnostic that
// echoes source text is a leak with a number beside it.
const (
	codexShapeResponse       = "token_usage_record"
	codexShapeSnapshot       = "token_count"
	codexShapeWrapper        = "event_msg"
	codexShapeOther          = "other_shape"
	codexShapeUntyped        = "untyped_line"
	codexShapeNoUsage        = "token_usage_record_without_usage"
	codexShapeNoResponseID   = "token_usage_record_without_response_id"
	codexShapeNoSessionID    = "token_usage_record_without_session_id"
	codexShapeNoTurnIDLexeme = "token_usage_record_with_unreadable_turn_id"
)

// The two spend roles the manifest's field_rules declare, which is how this decoder knows
// which counters are the bill and which are breakdowns of it. Input includes cached input
// and output contains reasoning: a subset is never added again.
const (
	codexRoleBase   = "base_counter"
	codexRoleDetail = "subset_detail"
)

// CodexFieldRule is one row of the mapping manifest's field_rules: a supported source
// counter's declared shape, its zero semantics, and what the mapping says an absent or an
// invalid value becomes.
type CodexFieldRule struct {
	NumberKind      string
	Unit            string
	ZeroSemantics   string
	AbsentPresence  string
	AbsentReason    string
	InvalidPresence string
	InvalidReason   string
	SpendRole       string
}

// CodexMapping is the sealed nova.tokens.mapping/2 manifest: the decisions this decoder
// applies, read from the file whose content ID every record it writes carries.
type CodexMapping struct {
	// ID is the manifest's own content ID, and therefore the mapping_id of every
	// observation. It is re-derived from the body bytes on disk, never a placeholder.
	ID                       string
	Schema                   string
	Name                     string
	Revision                 string
	Fields                   map[string]CodexFieldRule
	SourceKind               string
	Namespace                string
	ObservationKind          string
	EventKey                 []string
	ReceiptFields            []string
	NormalizedSpendSupported bool
	RevisionBasis            string
	TimeBasis                string
	TimeBasisMissing         string
	ModelBasis               string
	ModelBasisAbsent         string
	OwedCoverageTasks        []string
	FixtureDigests           map[string]string
}

// CodexBinding is the owner-supplied execution-origin binding for one source: the stable
// binding ID and the original friend and bench it names.
//
// It is an INPUT and never a default. "Bench and friend come from an explicit
// execution-origin binding for the source; the collection host does not establish
// historical origin." A zero CodexBinding means no binding was supplied, and every
// observation is then {null, null, unknown, null}.
//
// WHERE IT APPLIES, and the one place this decoder had to choose: the mapping document
// states the two origin outcomes but not which records a binding covers. This decoder
// applies the binding to a response whose turn context resolved, and to no other, because
// the binding binds an EXECUTION context and a response with no matching turn_context has
// none -- the same absence that leaves its model null. It is recorded as an open item on
// the PR rather than settled here: if the owner decides a binding covers every record of a
// bound source, this is a one-line change and the fixture's resp-c3 envelope changes with
// it.
type CodexBinding struct {
	ID     string
	Friend string
	Bench  string
}

// Supplied reports whether the caller named a binding at all.
func (b CodexBinding) Supplied() bool { return b.ID != "" && b.Friend != "" && b.Bench != "" }

// CodexObservation is one sealed envelope and what the mapping says about it. The envelope
// bytes are the record; every other field is a fact ABOUT it that a report needs and the
// wire does not carry.
type CodexObservation struct {
	// ID and Envelope are the sealed record, exactly as it goes on the wire.
	ID       string
	Envelope []byte
	// ResponseID is the native response identity, which is also the spend key's one member.
	ResponseID string
	// SpendKey is [namespace, event_key...] as one comparable string.
	SpendKey string
	// Day is the UTC day of a known point instant, or `unallocated`.
	Day string
	// Duplicate marks a byte-identical copy of an observation already seen: one response
	// copied to another bench or path is the SAME observation and deduplicates.
	Duplicate bool
	// Conflict marks every observation of a spend key that carries more than one distinct
	// sealed identity. Retained, excluded from spend, and never resolved by recency.
	Conflict bool
	// Gap marks an observation carrying an unavailable supported counter: the completeness
	// gap a normalized result must carry instead of a fabricated total.
	Gap bool
	// ArithmeticMismatch marks input + output != total with all three known.
	ArithmeticMismatch bool
	// SubsetImpossible marks a known detail counter larger than the base counter it is a
	// subset of, which is the same conflict outcome.
	SubsetImpossible bool
	// BaseComplete is true when every base counter normalises to a measurement.
	BaseComplete bool
	// DetailComplete is false whenever a defaulted-zero detail might mask an absence, which
	// on this producer is every present zero in the three detail fields.
	DetailComplete bool
	// Spendable is true only for an observation normalized spend may count.
	Spendable bool
	// DerivedTotal is input + output when the source carried no total: a view may show it
	// LABELLED DERIVED, and the raw absent field stays absent.
	DerivedTotal string
}

// CodexRefusal is one source record the wire refused, named by rule and field. It exists so
// a refusal is visible in a report rather than silently dropped, and it carries no source
// value: records.Refusal names a field and never echoes one.
type CodexRefusal struct {
	ResponseID string
	Rule       string
	Field      string
}

// CodexDecoding is what one decode run produced.
type CodexDecoding struct {
	// Observations are the sealed records, in source order.
	Observations []CodexObservation
	// Refusals are the records the boundary refused.
	Refusals []CodexRefusal
	// Unsupported counts the shapes this mapping does not cover, by a fixed vocabulary of
	// shape names. Counts, never a list of records, and never a string from the source.
	Unsupported map[string]int
	// Owed is the coverage task the manifest names for the shapes above.
	Owed []string
}

// ReadCodexMapping reads the sealed manifest and re-derives its ID from its own body bytes,
// so the mapping_id every record carries is the digest of the decisions on disk rather than
// a number somebody typed beside them.
func ReadCodexMapping(path string) (*CodexMapping, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	id, body, err := codexSealed(bytes.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	var m struct {
		Schema     string `json:"schema"`
		Name       string `json:"name"`
		Revision   string `json:"revision"`
		FieldRules map[string]struct {
			NumberKind      string `json:"number_kind"`
			Unit            string `json:"unit"`
			ZeroSemantics   string `json:"zero_semantics"`
			AbsentPresence  string `json:"absent_presence"`
			AbsentReason    string `json:"absent_reason"`
			InvalidPresence string `json:"invalid_presence"`
			InvalidReason   string `json:"invalid_reason"`
			SpendRole       string `json:"spend_role"`
		} `json:"field_rules"`
		IdentityRule struct {
			SourceKind      string   `json:"source_kind"`
			Namespace       string   `json:"namespace"`
			ObservationKind string   `json:"observation_kind"`
			EventKey        []string `json:"event_key"`
			ReceiptFields   []string `json:"receipt_fields"`
			Normalized      bool     `json:"normalized_spend_supported"`
		} `json:"identity_rule"`
		RevisionRule struct {
			Basis string `json:"basis"`
		} `json:"revision_rule"`
		TimeRule struct {
			Basis            string `json:"basis"`
			MissingTimestamp struct {
				Basis string `json:"basis"`
			} `json:"missing_timestamp"`
		} `json:"time_rule"`
		ModelRule struct {
			Basis  string `json:"basis"`
			Absent struct {
				Basis string `json:"basis"`
			} `json:"absent"`
		} `json:"model_rule"`
		OverlapRule struct {
			Owed []string `json:"owed_coverage_tasks"`
		} `json:"overlap_rule"`
		FixtureDigests map[string]string `json:"fixture_digests"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, errors.New("the mapping manifest body is not a nova.tokens.mapping/2 object")
	}
	out := &CodexMapping{
		ID:                       id,
		Schema:                   m.Schema,
		Name:                     m.Name,
		Revision:                 m.Revision,
		Fields:                   map[string]CodexFieldRule{},
		SourceKind:               m.IdentityRule.SourceKind,
		Namespace:                m.IdentityRule.Namespace,
		ObservationKind:          m.IdentityRule.ObservationKind,
		EventKey:                 m.IdentityRule.EventKey,
		ReceiptFields:            m.IdentityRule.ReceiptFields,
		NormalizedSpendSupported: m.IdentityRule.Normalized,
		RevisionBasis:            m.RevisionRule.Basis,
		TimeBasis:                m.TimeRule.Basis,
		TimeBasisMissing:         m.TimeRule.MissingTimestamp.Basis,
		ModelBasis:               m.ModelRule.Basis,
		ModelBasisAbsent:         m.ModelRule.Absent.Basis,
		OwedCoverageTasks:        append([]string(nil), m.OverlapRule.Owed...),
		FixtureDigests:           m.FixtureDigests,
	}
	for name, r := range m.FieldRules {
		out.Fields[name] = CodexFieldRule(r)
	}
	if len(out.Fields) == 0 {
		return nil, errors.New("the mapping manifest declares no field rule, so no source field is supported")
	}
	if len(out.EventKey) != 1 || out.EventKey[0] != "response_id" {
		// This decoder's identity path is written for the decided key. A manifest that
		// decided a different one is a mapping revision and a different decoder, not a
		// shape to guess at.
		return nil, errors.New("the mapping manifest's event key is not [response_id]")
	}
	return out, nil
}

// FieldNames is the mapping's closed raw_usage allowlist, sorted. It is the ONLY source of
// the names this decoder reads out of a usage object: an unknown producer metric is
// excluded rather than admitted into an allowlist derived from the input.
func (m *CodexMapping) FieldNames() []string {
	out := make([]string, 0, len(m.Fields))
	for name := range m.Fields {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Allowlists is what a records.Validator needs to hold this mapping's boundary: the raw
// usage field names and the receipt's native locator fields, both from the manifest.
func (m *CodexMapping) Allowlists() records.Allowlists {
	return records.Allowlists{
		RawUsageFields: m.FieldNames(),
		ReceiptFields:  append([]string(nil), m.ReceiptFields...),
	}
}

// CheckCodexFixtureDigests checks each source file against the digest the manifest names
// for it, so a decode run and the mapping that authorised it are about the same bytes.
func CheckCodexFixtureDigests(m *CodexMapping, dir string) error {
	if len(m.FixtureDigests) == 0 {
		return errors.New("the mapping manifest names no source fixture digest")
	}
	names := make([]string, 0, len(m.FixtureDigests))
	for name := range m.FixtureDigests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if have := "sha256:" + hex.EncodeToString(sum[:]); have != m.FixtureDigests[name] {
			return errors.New("the source fixture " + name + " is not the bytes the mapping manifest names")
		}
	}
	return nil
}

// codexSealed splits a sealed envelope line into its ID and body and checks that the ID is
// the digest of those body bytes. It is the reader's half of records.Seal's writer.
func codexSealed(line []byte) (string, []byte, error) {
	const prefix = `{"id":"`
	const mid = `","body":`
	if !bytes.HasPrefix(line, []byte(prefix)) || !bytes.HasSuffix(line, []byte("}")) {
		return "", nil, errors.New("not a sealed envelope line")
	}
	rest := line[len(prefix):]
	i := bytes.Index(rest, []byte(mid))
	if i < 0 {
		return "", nil, errors.New("the sealed envelope carries no body member")
	}
	id := string(rest[:i])
	body := rest[i+len(mid) : len(rest)-1]
	sum := sha256.Sum256(body)
	if want := "sha256:" + hex.EncodeToString(sum[:]); want != id {
		return "", nil, errors.New("the sealed ID is not the digest of the body bytes")
	}
	return id, body, nil
}

// CodexShardDay is the format's placement rule, and the mapping's day allocation: the UTC
// day of a known point instant, `unallocated` otherwise. A report that groups by it is
// reporting completion/observation day and says so; it is not claiming the call's day.
func CodexShardDay(occurredAt *string) string {
	if occurredAt == nil {
		return "unallocated"
	}
	inst, err := time.Parse(time.RFC3339, *occurredAt)
	if err != nil {
		return "unallocated"
	}
	return inst.UTC().Format("2006-01-02")
}

// CoverageLimits is what a report must name: one line per unsupported shape, with its count
// and the owed task, and never a list of records. A September backfill cannot claim complete
// Codex history while any line of this appears.
func (d *CodexDecoding) CoverageLimits() []string {
	if len(d.Unsupported) == 0 {
		return nil
	}
	owed := "owed: " + joinCodex(d.Owed)
	shapes := make([]string, 0, len(d.Unsupported))
	for shape := range d.Unsupported {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	out := make([]string, 0, len(shapes))
	for _, shape := range shapes {
		out = append(out, shape+"="+strconv.Itoa(d.Unsupported[shape])+" not covered by this mapping ("+owed+")")
	}
	return out
}

// Winner is the empty string, always. It exists so that the absence of a newest-wins rule is
// a call a caller can make and get one answer from: "there is no newest-wins rule", and
// changing the source path, the collector run or the collection bench cannot authorise
// choosing between two observations of one key.
func (d *CodexDecoding) Winner(string) string { return "" }

func joinCodex(ss []string) string {
	if len(ss) == 0 {
		return "none named"
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += "," + s
	}
	return out
}

// codexLine is the rollout shape, and it is deliberately the mapping's fields and nothing
// else: an unsupported source field has no Go field to land in, so it cannot reach a record
// by accident. The identity members are raw so a native numeric identifier keeps its exact
// decimal lexeme instead of going through a float64.
type codexLine struct {
	Type             string                      `json:"type"`
	Timestamp        *string                     `json:"timestamp"`
	ResponseID       json.RawMessage             `json:"response_id"`
	SessionID        json.RawMessage             `json:"session_id"`
	ThreadID         json.RawMessage             `json:"thread_id"`
	TurnID           json.RawMessage             `json:"turn_id"`
	TurnContextModel *string                     `json:"turn_context_model"`
	Usage            *map[string]json.RawMessage `json:"usage"`
}

// DecodeCodexRollout decodes the named rollout files in order. The caller names the paths;
// this function opens them and nothing else, and no path reaches a record or a diagnostic.
func DecodeCodexRollout(m *CodexMapping, b CodexBinding, paths []string) (*CodexDecoding, error) {
	readers := make([]io.Reader, 0, len(paths))
	closers := make([]io.Closer, 0, len(paths))
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		closers = append(closers, f)
		readers = append(readers, f)
	}
	return DecodeCodexReaders(m, b, readers)
}

// DecodeCodexReaders decodes rollout lines from readers, in order. It is the whole decoder:
// select, map, seal, then decide copy, conflict and spendability over the whole run.
func DecodeCodexReaders(m *CodexMapping, b CodexBinding, readers []io.Reader) (*CodexDecoding, error) {
	if m == nil {
		return nil, errors.New("a decode needs the sealed mapping manifest")
	}
	d := &CodexDecoding{Unsupported: map[string]int{}, Owed: append([]string(nil), m.OwedCoverageTasks...)}
	v := records.NewValidator(m.Allowlists())
	for at, r := range readers {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for n := 1; sc.Scan(); n++ {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			if err := d.readCodexLine(m, b, v, line); err != nil {
				// The location is the source's index and line number. Not its path: a
				// caller's path can be private, and a refusal is a shared file too.
				return nil, errors.New("source " + strconv.Itoa(at) + " line " + strconv.Itoa(n) + ": " + err.Error())
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	d.resolveCodexKeys(m)
	return d, nil
}

// readCodexLine selects one rollout line and maps it, or counts the shape it is instead.
func (d *CodexDecoding) readCodexLine(m *CodexMapping, b CodexBinding, v *records.Validator, line []byte) error {
	var rec codexLine
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&rec); err != nil {
		// The source line is never quoted back: it can hold a prompt.
		return errors.New("the rollout line is not a JSON object of the shape this mapping reads")
	}
	// SELECTION. A top-level token_usage_record and nothing else. An event_msg carrying the
	// same name inside a payload is the wrapper shape the mapping rules out, and an older
	// cumulative token_count is owed to a separate mapping: both are counted, neither is
	// mapped. A shape is counted by a fixed name, never by the source's own `type` string.
	if rec.Type != codexShapeResponse {
		switch rec.Type {
		case codexShapeSnapshot, codexShapeWrapper:
			d.Unsupported[rec.Type]++
		case "":
			d.Unsupported[codexShapeUntyped]++
		default:
			d.Unsupported[codexShapeOther]++
		}
		return nil
	}
	// IDENTITY. No response ID, no request identity: nothing is synthesised from a line
	// that cannot be named.
	responseID, ok := codexIdentity(rec.ResponseID)
	if !ok {
		d.Unsupported[codexShapeNoResponseID]++
		return nil
	}
	// The native containing thread or session, retained as provenance only. It never enters
	// the event key, so a forked or containing thread cannot fork the spend identity.
	sessionID, ok := codexIdentity(rec.SessionID)
	if !ok {
		if sessionID, ok = codexIdentity(rec.ThreadID); !ok {
			d.Unsupported[codexShapeNoSessionID]++
			return nil
		}
	}
	// "A response with no usage does not manufacture a record." A usage member that is
	// absent or null is no usage; an explicitly empty object is a usage object that
	// measured nothing, and its six absent entries are retained evidence of that.
	if rec.Usage == nil {
		d.Unsupported[codexShapeNoUsage]++
		return nil
	}
	usage := *rec.Usage

	obs := records.Observation{
		Schema: records.SchemaObservation,
		Source: records.Source{
			Kind: m.SourceKind,
			// producer_version: null for this mapping revision. No guessed key, no
			// installed collector version, no version inferred from a filename.
			ProducerVersion: nil,
			Namespace:       m.Namespace,
			SessionID:       sessionID,
			EventKey:        []string{responseID},
		},
		Kind: m.ObservationKind,
		// revision: {native: null, supersedes: [], basis: none}. No source ordering is
		// inferred from an export update time.
		Revision:   records.Revision{Basis: m.RevisionBasis},
		Repository: records.Repository{Basis: codexRepositoryBasis},
		MappingID:  m.ID,
		Receipt:    map[string]string{"response_id": responseID},
	}
	// TIME. The rollout record's own timestamp, offset and precision preserved, with the
	// basis that says what it means. A missing one stays unknown: no instant is invented
	// from a neighbouring line or from the file's mtime.
	if rec.Timestamp != nil {
		obs.Time = records.Times{OccurredAt: rec.Timestamp, Basis: m.TimeBasis}
	} else {
		obs.Time = records.Times{Basis: m.TimeBasisMissing}
	}
	// MODEL. The matching turn context's configured model at basis `requested`, which says
	// what was asked for and not what a server reported. A later configured model never
	// relabels an earlier response, because each record reads only its own turn context.
	if rec.TurnContextModel != nil && *rec.TurnContextModel != "" {
		obs.Model = records.Model{ID: rec.TurnContextModel, Basis: m.ModelBasis}
	} else {
		obs.Model = records.Model{Basis: m.ModelBasisAbsent}
	}
	// ORIGIN. The owner's binding, and only where the turn context this response ran in
	// resolved. See CodexBinding for why, and for the open item it is.
	if b.Supplied() && rec.TurnContextModel != nil && *rec.TurnContextModel != "" {
		friend, bench, id := b.Friend, b.Bench, b.ID
		obs.Origin = records.Origin{Friend: &friend, Bench: &bench, Basis: codexOriginBound, BindingID: &id}
	} else {
		obs.Origin = records.Origin{Basis: codexOriginUnknown}
	}
	// RECEIPT. response_id and turn_id only, strings only. A native numeric turn identifier
	// is its exact decimal string; an unreadable one is omitted rather than approximated.
	if len(bytes.TrimSpace(rec.TurnID)) > 0 && !bytes.Equal(bytes.TrimSpace(rec.TurnID), []byte("null")) {
		turnID, ok := codexIdentity(rec.TurnID)
		if !ok {
			d.Unsupported[codexShapeNoTurnIDLexeme]++
		} else {
			obs.Receipt["turn_id"] = turnID
		}
	}
	// RAW USAGE. Every field of the closed allowlist has an entry, absent ones included.
	obs.RawUsage = codexRawUsage(m, usage)
	// model_usage: TokenUsageRecord carries no model field, and an empty array is the only
	// faithful mapping of a source that supplies no per-model split.
	obs.ModelUsage = nil

	raw, id, err := v.SealObservation(obs)
	if err != nil {
		// A record the boundary refuses is REFUSED, with the rule and the field the wire
		// named, and never repaired into something acceptable.
		ref := CodexRefusal{ResponseID: responseID}
		var r *records.Refusal
		if errors.As(err, &r) {
			ref.Rule, ref.Field = r.Rule, r.Field
		}
		d.Refusals = append(d.Refusals, ref)
		return nil
	}
	out := CodexObservation{
		ID:         id,
		Envelope:   raw,
		ResponseID: responseID,
		SpendKey:   m.Namespace + "\x00" + responseID,
		Day:        CodexShardDay(obs.Time.OccurredAt),
	}
	codexMeasure(m, obs.RawUsage, &out)
	d.Observations = append(d.Observations, out)
	return nil
}

// codexRawUsage maps one usage object onto the mapping's closed allowlist. It reads the
// allowlist's names out of the source and never the source's names out of the allowlist, so
// an unknown producer metric is excluded rather than promoted.
func codexRawUsage(m *CodexMapping, usage map[string]json.RawMessage) map[string]records.RawField {
	out := make(map[string]records.RawField, len(m.Fields))
	for _, name := range m.FieldNames() {
		rule := m.Fields[name]
		f := records.RawField{NumberKind: rule.NumberKind, Unit: rule.Unit}
		raw, inSource := usage[name]
		switch {
		case !inSource:
			// A missing supported key is absent with the mapping's reason: the source did
			// not carry it, which is a different fact from a zero.
			reason := rule.AbsentReason
			f.Presence, f.Reason = rule.AbsentPresence, &reason
		default:
			lexeme, valid := codexCounter(raw, rule.NumberKind)
			if !valid {
				// An explicit null, a wrong-typed, a negative or a non-integer value:
				// unavailable with reason parse_failed, the same declared number_kind and
				// unit, and NEVER a coerced zero. The source value is not stringified onto
				// the wire and does not reach a diagnostic either.
				reason := rule.InvalidReason
				f.Presence, f.Reason = rule.InvalidPresence, &reason
			} else {
				// A present numeric key retains its original lexeme, byte for byte: no
				// float64 round trip, so a counter above 2^53 survives.
				value := lexeme
				f.Presence, f.Value = "present", &value
			}
		}
		out[name] = f
	}
	return out
}

// codexMeasure applies the mapping's zero semantics and its arithmetic, overlap and day
// rules to one observation. Nothing here changes a raw field: this is what a normaliser may
// BELIEVE about the record, kept beside it.
func codexMeasure(m *CodexMapping, raw map[string]records.RawField, out *CodexObservation) {
	out.BaseComplete, out.DetailComplete = true, true
	for _, name := range m.FieldNames() {
		f := raw[name]
		if f.Presence == "unavailable" {
			out.Gap = true
		}
		meas := records.Normalize(f, records.ZeroSemantics(m.Fields[name].ZeroSemantics))
		switch m.Fields[name].SpendRole {
		case codexRoleBase:
			out.BaseComplete = out.BaseComplete && meas.Measured
		case codexRoleDetail:
			// The producer defaults an absent detail to 0, so a present zero here does not
			// prove the provider measured zero: detail completeness is false and the
			// normalized detail stays unknown.
			out.DetailComplete = out.DetailComplete && meas.Measured && meas.SubsetComplete
		}
	}
	in, output, total := raw["input_tokens"], raw["output_tokens"], raw["total_tokens"]
	inN, inOK := codexBig(in)
	outN, outOK := codexBig(output)
	totalN, totalOK := codexBig(total)
	if inOK && outOK && totalOK {
		// A mismatch is a retained mapping conflict, excluded from normalized spend and
		// never adjusted: neither counter is corrected towards the other.
		if new(big.Int).Add(inN, outN).Cmp(totalN) != 0 {
			out.ArithmeticMismatch = true
		}
	}
	if inOK && outOK && !total.Present() {
		// A missing raw total stays absent; the derived sum is offered LABELLED DERIVED and
		// the raw field is not filled in.
		out.DerivedTotal = new(big.Int).Add(inN, outN).String()
	}
	// An impossible known subset relationship is the same conflict outcome as a mismatched
	// total: input includes cached input, and output contains reasoning.
	for _, pair := range [][2]string{
		{"cached_input_tokens", "input_tokens"},
		{"cache_write_input_tokens", "input_tokens"},
		{"reasoning_output_tokens", "output_tokens"},
	} {
		sub, subOK := codexBig(raw[pair[0]])
		base, baseOK := codexBig(raw[pair[1]])
		if subOK && baseOK && sub.Cmp(base) > 0 {
			out.SubsetImpossible = true
		}
	}
}

// codexBig is a present counter's exact value, or not-ok for anything else. big.Int because
// a token counter is an arbitrary-precision integer on this wire and float64 would round it.
func codexBig(f records.RawField) (*big.Int, bool) {
	if !f.Present() || f.Value == nil {
		return nil, false
	}
	n, ok := new(big.Int).SetString(*f.Value, 10)
	return n, ok
}

// resolveCodexKeys decides copy, conflict and spendability over the whole run, which is the
// only thing that cannot be decided from one line.
func (d *CodexDecoding) resolveCodexKeys(m *CodexMapping) {
	ids := map[string]map[string]bool{}
	for i := range d.Observations {
		o := &d.Observations[i]
		seen := ids[o.SpendKey]
		if seen == nil {
			seen = map[string]bool{}
			ids[o.SpendKey] = seen
		}
		// A byte-identical copy is the same observation: one response copied to another
		// bench or path deduplicates, and a fresh collector run creates no new identity.
		o.Duplicate = seen[o.ID]
		seen[o.ID] = true
	}
	for i := range d.Observations {
		o := &d.Observations[i]
		// The same response_id with changed counters is a conflict: retained, excluded from
		// spend, with no newest-wins rule to resolve it.
		o.Conflict = len(ids[o.SpendKey]) > 1
		o.Spendable = m.NormalizedSpendSupported && !o.Conflict && !o.Duplicate &&
			!o.Gap && !o.ArithmeticMismatch && !o.SubsetImpossible
	}
}

// codexIdentity is a native identity as an exact string. A JSON string is itself; a JSON
// number keeps its own decimal lexeme, because "Native numeric turn identifiers are
// preserved as exact decimal strings" and a float64 round trip would lose the largest of
// them. Anything else is no identity at all.
func codexIdentity(raw json.RawMessage) (string, bool) {
	v, ok := codexScalar(raw)
	if !ok {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, t != ""
	case json.Number:
		if !codexNumberLexeme(t.String(), "integer") {
			return "", false
		}
		return t.String(), true
	}
	return "", false
}

// codexCounter is one supported counter's original lexeme, and whether the mapping's
// declared number_kind accepts it. The validity rule is the wire's own lexeme rule rather
// than a second grammar: what the boundary would refuse is what becomes unavailable here.
func codexCounter(raw json.RawMessage, numberKind string) (string, bool) {
	v, ok := codexScalar(raw)
	if !ok {
		return "", false
	}
	n, isNumber := v.(json.Number)
	if !isNumber {
		return "", false
	}
	return n.String(), codexNumberLexeme(n.String(), numberKind)
}

// codexScalar decodes one JSON value, keeping a number as its lexeme. A null, or anything
// that is not one complete JSON value, is not a value this mapping reads.
func codexScalar(raw json.RawMessage) (any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// codexNumberLexeme is the format's own lexeme rule, spelled out rather than compiled:
// nothing in internal/tokens carries a regexp of its own. An integer counter is `0` or
// `[1-9][0-9]*`, so `007`, `1.0`, `1e3`, `+1` and `-5` are all refused rather than
// trimmed; a decimal keeps any valid non-negative JSON number lexeme.
func codexNumberLexeme(value, numberKind string) bool {
	if value == "" || value[0] == '-' || value[0] == '+' {
		return false
	}
	if numberKind != "integer" {
		// The decoder has already accepted it as a JSON number, which is RFC 8259's
		// grammar; the sign is the only part this format has no evidence for.
		return true
	}
	if value == "0" {
		return true
	}
	if value[0] < '1' || value[0] > '9' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

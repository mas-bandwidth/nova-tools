package tokens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The retained Grok turn decoder: one `grok usage` turn export in, sealed
// nova.tokens.observation/2 envelopes out.
//
// THE MAPPING IS NOT THIS FILE'S. docs/MAPPING-TOKENS-GROK.md is the source owner's
// decision, cell by cell, and this decoder applies it without reinterpreting one. Where the
// mapping says a thing is owed, unsupported or unverified, the answer here is a refusal with
// that reason named -- ErrGrokSessionAggregateOwed, ErrGrokNormalizedSpendUnsupported,
// ErrGrokRequestGrainOutsideMapping -- and never a number produced anyway. The cells this
// decoder could not have decided on its own:
//
//   - The spend key is the ARRAY [original_session_id, turn_number_as_string]. Never a joined
//     string: two natives that concatenate to one text are one identity that belongs to
//     neither.
//   - The turn aggregate is the counting grain. The session totals stay in the export and map
//     to nothing here; they are a separate retained mapping with its own namespace, and using
//     them to fill a missing turn or counting them beside the turns are both forbidden.
//   - Normalized spend from this key is unsupported until resume/fork/renumber stability is
//     evidenced. Raw retention proceeds; the gap is reported rather than smoothed.
//   - Origin is the owner's explicit binding or nothing. This process, the file it was handed
//     and the bench it runs on are not historical evidence, which is why a copied export
//     seals to the same bytes as its original.
//
// It reads BYTES a caller hands it. It opens no file, resolves no path and runs no program,
// so a private filename cannot reach a record or a diagnostic through it -- and a diagnostic
// here names a field and a turn's index, never a source value. That is the same rule
// internal/records applies to a Refusal, for the same reason: an unsupported source field can
// hold a prompt, and a diagnostic is a shared file too.

// The literals of the envelope that do not depend on the turn.
const (
	GrokSourceKind      = "grok"
	GrokNamespace       = "nova.grok.turns"
	GrokObservationKind = "turn"

	grokTimeBasisKnown   = "turn_completion"
	grokTimeBasisUnknown = "unknown"
	grokUnallocated      = "unallocated"

	grokReasonAbsent  = "not_supplied"
	grokReasonInvalid = "parse_failed"
)

// grokFieldRule is one row of the mapping's raw_usage table: the number kind and the unit a
// field's entry declares whatever its presence turns out to be.
type grokFieldRule struct {
	numberKind string
	unit       string
}

// grokFields is the mapping's own closed nine-field allowlist, under the ORIGINAL camelCase
// source names. It is this mapping's set and not a global field vocabulary, and it is closed:
// an unknown source field is excluded, never admitted because the input carried it.
var grokFields = map[string]grokFieldRule{
	"inputTokens":         {"integer", "tokens"},
	"outputTokens":        {"integer", "tokens"},
	"totalTokens":         {"integer", "tokens"},
	"cachedReadTokens":    {"integer", "tokens"},
	"cacheCreationTokens": {"integer", "tokens"},
	"reasoningTokens":     {"integer", "tokens"},
	"modelCalls":          {"integer", "calls"},
	"costUsdTicks":        {"decimal", "usd_ticks"},
	"turnCount":           {"integer", "turns"},
}

// grokModelUsageFields are the eight numeric fields a model_usage entry carries: everything
// except turnCount, which is a property of the turn and not of a model within it.
var grokModelUsageFields = []string{
	"cacheCreationTokens", "cachedReadTokens", "costUsdTicks", "inputTokens",
	"modelCalls", "outputTokens", "reasoningTokens", "totalTokens",
}

// grokReceiptFields is the whole receipt: the native turn number, as a string. A receipt
// carries mapping-allowlisted native locator fields and nothing else.
var grokReceiptFields = []string{"turn_number"}

// grokOwedCoverageTasks are the mappings this one does not cover, named so a coverage report
// can say so. The session aggregate is the manifest's own owed task; the request grain is
// owed "in the same sense" by the mapping's closing paragraph.
var grokOwedCoverageTasks = []string{"grok_request_grain_mapping", "grok_session_aggregate_mapping"}

// The refusals that ARE the mapping's unsupported and owed cells. They are sentinel errors so
// a caller can tell the three apart with errors.Is and report the specific gap.
var (
	ErrGrokNormalizedSpendUnsupported = errors.New("tokens: normalized spend from a Grok turn key is unsupported: resume/fork/renumber identity stability is unverified, so raw retention proceeds and the gap is reported")
	ErrGrokSessionAggregateOwed       = errors.New("tokens: the Grok session aggregate is a separate retained mapping with its own namespace and identity contract, owed and not covered here: it never fills a missing turn and is never counted beside the turns")
	ErrGrokRequestGrainOutsideMapping = errors.New("tokens: a request-grain Grok mapping is outside this mapping, with no literals invented: decode at the grain the source supplies")
)

// GrokAllowlists is the closed extraction allowlist this decoder seals under, as a COPY. A
// caller can read the set without being able to widen what the boundary then enforces.
func GrokAllowlists() records.Allowlists {
	raw := make([]string, 0, len(grokFields))
	for name := range grokFields {
		raw = append(raw, name)
	}
	sort.Strings(raw)
	return records.Allowlists{
		RawUsageFields: raw,
		ReceiptFields:  append([]string(nil), grokReceiptFields...),
	}
}

// GrokFieldRule returns the number kind and unit the mapping declares for a source field.
func GrokFieldRule(name string) (numberKind, unit string, ok bool) {
	r, ok := grokFields[name]
	return r.numberKind, r.unit, ok
}

// GrokModelUsageFields is the eight-field split set, sorted, as a copy.
func GrokModelUsageFields() []string { return append([]string(nil), grokModelUsageFields...) }

// GrokOwedCoverageTasks names the mappings this decoder does not cover.
func GrokOwedCoverageTasks() []string { return append([]string(nil), grokOwedCoverageTasks...) }

// A GrokBinding is the owner's explicit statement about where a turn originally ran. All
// three members are required: a binding without a stable ID is not an explicit binding, and
// the mapping's alternative is not a partial origin but no origin at all.
type GrokBinding struct {
	Friend string
	Bench  string
	ID     string
}

// GrokOptions are the declarations only the caller can make. None of them is derivable from
// the export, and the decoder invents none of them.
type GrokOptions struct {
	// MappingID is the content ID of the sealed mapping manifest these records are mapped
	// under. It is the caller's declaration, checked for shape and never read off a file by
	// this package.
	MappingID string

	// ProducerVersion is the exact bounded version string the original producer metadata or
	// an explicit owner binding supplies, with its spelling intact. Nil is the fixtures'
	// case: without a binding for the source, producer_version is null. An owner's earlier
	// REPORT of a version is not a version to stamp on every export.
	ProducerVersion *string

	// Binding resolves the owner binding for one turn of one session. Nil, or a nil return,
	// is the unbound branch: origin {null, null, unknown, null}. It takes the turn's native
	// identity rather than anything about this collection, so the same turn copied to another
	// bench resolves to the same original origin and seals to the same bytes.
	Binding func(sessionID, turnNumber string) *GrokBinding
}

// A GrokRecord is one turn's sealed observation with the identity a caller groups by.
type GrokRecord struct {
	// EventKey is [original_session_id, turn_number_as_string], the array itself.
	EventKey []string
	// TurnNumber is the exact decimal lexeme the source wrote.
	TurnNumber string
	// Envelope is the sealed {"id":...,"body":...} exactly as it goes on the wire: canonical,
	// with no trailing newline. A shard writer adds the JSONL newline.
	Envelope []byte
	ID       string
	// Observation is what the boundary read back out of those bytes.
	Observation records.Observation
}

// A GrokKeyGroup is one spend key after the copy rule: byte-identical observations
// deduplicate, and a key holding more than one distinct observation is a conflict retained
// and excluded from spend.
type GrokKeyGroup struct {
	Key        []string
	TurnNumber string
	IDs        []string
	Conflict   bool
}

// DecodeGrokTurns decodes one export's turns, in source order, into sealed observations.
//
// Every record is sealed through records.SealObservation, which validates its own bytes under
// this mapping's allowlists before returning them: a record this decoder emits is a record the
// publisher boundary accepts, by construction rather than by review.
func DecodeGrokTurns(raw []byte, opts GrokOptions) ([]GrokRecord, error) {
	if !grokIsContentID(opts.MappingID) {
		return nil, errors.New("grok: the mapping ID is a sha256 content ID of the sealed mapping manifest")
	}
	if err := grokCheckProducerVersion(opts.ProducerVersion); err != nil {
		return nil, err
	}
	sessionID, turns, err := grokParse(raw)
	if err != nil {
		return nil, err
	}
	v := records.NewValidator(GrokAllowlists())
	out := make([]GrokRecord, 0, len(turns))
	for i, turn := range turns {
		rec, err := grokTurnRecord(v, sessionID, turn, opts)
		if err != nil {
			// The turn's INDEX, never its number: a turn number is source data until the
			// integer lexeme rule has accepted it.
			return nil, fmt.Errorf("grok: turns[%d]: %w", i, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// GrokNormalizedSpend is the identity gate. The mapping declares normalized spend
// unsupported for this key, so there is no total to return and this reports which gap.
func GrokNormalizedSpend([]GrokRecord) (map[string]string, error) {
	return nil, ErrGrokNormalizedSpendUnsupported
}

// GrokSessionAggregate is the owed mapping. The export's session totals are visible evidence
// for it and are mapped by nothing here.
func GrokSessionAggregate([]byte, GrokOptions) ([]GrokRecord, error) {
	return nil, ErrGrokSessionAggregateOwed
}

// GrokRequestObservations is the grain that is outside this mapping. A generic request
// fixture is structural evidence, not a Grok producer contract.
func GrokRequestObservations([]byte, GrokOptions) ([]GrokRecord, error) {
	return nil, ErrGrokRequestGrainOutsideMapping
}

// GrokDay is the mapping's day allocation: the UTC day of a known completion instant,
// whatever its source offset, and `unallocated` when the instant is unknown. A report using
// it labels the completion-day convention and never claims call-day allocation for a turn
// spanning midnight.
func GrokDay(rec GrokRecord) string {
	at := rec.Observation.Time.OccurredAt
	if at == nil {
		return grokUnallocated
	}
	inst, err := time.Parse(time.RFC3339, *at)
	if err != nil {
		return grokUnallocated
	}
	return inst.UTC().Format(dayLayout)
}

// GrokHasCompletenessGap reports whether any supported field of this turn is unavailable,
// which is the gap a normalized result carries instead of a fabricated total.
func GrokHasCompletenessGap(rec GrokRecord) bool {
	if grokGapped(rec.Observation.RawUsage) {
		return true
	}
	for _, e := range rec.Observation.ModelUsage {
		if grokGapped(e.RawUsage) {
			return true
		}
	}
	return false
}

func grokGapped(fields map[string]records.RawField) bool {
	for _, f := range fields {
		if f.Presence == "unavailable" {
			return true
		}
	}
	return false
}

// GrokKeyGroups groups records by spend key after deduplicating identical observations by
// their sealed ID. That is the whole of the copy rule: a copied source is the same
// observation, and a changed one is a second observation of one key -- a conflict, with no
// newest-wins rule and no order taken from a filename, a commit or the argument order here.
func GrokKeyGroups(recs []GrokRecord) []GrokKeyGroup {
	type group struct {
		key   []string
		turn  string
		ids   map[string]bool
		order []string
	}
	byKey := map[string]*group{}
	var keys []string
	for _, r := range recs {
		k := GrokNamespace + "\x00" + strings.Join(r.EventKey, "\x00")
		g, ok := byKey[k]
		if !ok {
			g = &group{key: append([]string(nil), r.EventKey...), turn: r.TurnNumber, ids: map[string]bool{}}
			byKey[k] = g
			keys = append(keys, k)
		}
		if !g.ids[r.ID] {
			g.ids[r.ID] = true
			g.order = append(g.order, r.ID)
		}
	}
	sort.Strings(keys)
	out := make([]GrokKeyGroup, 0, len(keys))
	for _, k := range keys {
		g := byKey[k]
		ids := append([]string(nil), g.order...)
		sort.Strings(ids)
		out = append(out, GrokKeyGroup{Key: g.key, TurnNumber: g.turn, IDs: ids, Conflict: len(ids) > 1})
	}
	return out
}

// ------------------------------------------------------------------ the turn

func grokTurnRecord(v *records.Validator, sessionID string, turn map[string]interface{}, opts GrokOptions) (GrokRecord, error) {
	var rec GrokRecord
	num, err := grokTurnNumber(turn)
	if err != nil {
		return rec, err
	}
	occurred, basis, err := grokTime(turn)
	if err != nil {
		return rec, err
	}
	split, err := grokSplit(turn)
	if err != nil {
		return rec, err
	}
	model, err := grokModel(turn, split)
	if err != nil {
		return rec, err
	}
	origin, err := grokOrigin(sessionID, num, opts)
	if err != nil {
		return rec, err
	}
	rawUsage := make(map[string]records.RawField, len(grokFields))
	for name := range grokFields {
		rawUsage[name] = grokRawField(turn, name)
	}
	key := []string{sessionID, num}
	obs := records.Observation{
		Schema: records.SchemaObservation,
		Source: records.Source{
			Kind:            GrokSourceKind,
			ProducerVersion: grokCopyString(opts.ProducerVersion),
			Namespace:       GrokNamespace,
			SessionID:       sessionID,
			EventKey:        key,
		},
		Kind:     GrokObservationKind,
		Revision: records.Revision{Basis: "none"},
		Time:     records.Times{OccurredAt: occurred, Basis: basis},
		Origin:   origin,
		Model:    model,
		// No repo field exists in the source: `unattributed`, with no policy and no touched
		// list. A touched list is separately evidenced and carries no allocated spend.
		Repository: records.Repository{Basis: "unattributed"},
		RawUsage:   rawUsage,
		ModelUsage: split,
		MappingID:  opts.MappingID,
		Receipt:    map[string]string{"turn_number": num},
	}
	env, id, err := v.SealObservation(obs)
	if err != nil {
		return rec, err
	}
	// Read the observation back out of the bytes that were sealed, so what a caller inspects
	// is what the boundary accepted and not the struct that went in.
	read, err := records.NewValidator(GrokAllowlists()).ValidateEnvelope(env)
	if err != nil {
		return rec, err
	}
	return GrokRecord{EventKey: key, TurnNumber: num, Envelope: env, ID: id, Observation: *read.Observation}, nil
}

// grokTurnNumber is the native numeric turn identifier, preserved as its exact decimal
// string. A missing, non-numeric, negative or non-integer turn number is an invalid source
// shape rather than a turn with a guessed identity.
func grokTurnNumber(turn map[string]interface{}) (string, error) {
	v, ok := turn["turnNumber"]
	if !ok {
		return "", errors.New("a turn carries turnNumber")
	}
	n, ok := v.(json.Number)
	if !ok {
		return "", errors.New("turnNumber is a JSON number")
	}
	if !grokIsInteger(n.String()) {
		return "", errors.New("turnNumber is an exact decimal integer: 0 or [1-9][0-9]*")
	}
	return n.String(), nil
}

// grokTime is endedAt with its offset and precision preserved, or the unknown case. The
// export's updatedAt is collection evidence and is never read here: it is neither the event
// time nor a source order, and a turn without endedAt is never assigned the export's date.
func grokTime(turn map[string]interface{}) (*string, string, error) {
	v, ok := turn["endedAt"]
	if !ok || v == nil {
		return nil, grokTimeBasisUnknown, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, "", errors.New("endedAt is an RFC 3339 string with its offset")
	}
	// The lexeme itself is the wire's grammar to enforce: SealObservation refuses a stamp
	// with no offset by name (timestamp_no_offset) rather than this file keeping a second
	// timestamp grammar that could disagree with it.
	return &s, grokTimeBasisKnown, nil
}

// grokRawField is the presence table, field by field. The three states stay three: a key the
// source never carried is absent/not_supplied; an explicit null, a wrong-typed, a negative or
// a non-integer supported counter is unavailable/parse_failed, and its value NEVER reaches
// the wire; anything else is present with the source's own lexeme, byte for byte, through no
// float.
func grokRawField(src map[string]interface{}, name string) records.RawField {
	rule := grokFields[name]
	f := records.RawField{NumberKind: rule.numberKind, Unit: rule.unit}
	v, ok := src[name]
	if !ok {
		f.Presence = "absent"
		f.Reason = grokReason(grokReasonAbsent)
		return f
	}
	n, isNum := v.(json.Number)
	if !isNum || !grokValidLexeme(n.String(), rule.numberKind) {
		f.Presence = "unavailable"
		f.Reason = grokReason(grokReasonInvalid)
		return f
	}
	lexeme := n.String()
	f.Presence = "present"
	f.Value = &lexeme
	return f
}

// grokSplit is the source modelUsage, one entry per source model ID, sorted by that ID, each
// entry carrying the eight numeric fields under the same presence, type, unit and zero rules.
// An absent or empty split is no entries: empty means no split supplied, which is a different
// fact from no usage.
func grokSplit(turn map[string]interface{}) ([]records.ModelUsage, error) {
	v, ok := turn["modelUsage"]
	if !ok || v == nil {
		return nil, nil
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return nil, errors.New("modelUsage is a JSON object keyed by source model ID")
	}
	ids := make([]string, 0, len(obj))
	for id := range obj {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]records.ModelUsage, 0, len(ids))
	for _, id := range ids {
		entry, ok := obj[id].(map[string]interface{})
		if !ok {
			// By position in the sorted split, never by the ID itself: a source model ID is
			// source data, and this message is a diagnostic.
			return nil, fmt.Errorf("modelUsage entry %d is a JSON object of the eight numeric fields", len(out))
		}
		fields := make(map[string]records.RawField, len(grokModelUsageFields))
		for _, name := range grokModelUsageFields {
			fields[name] = grokRawField(entry, name)
		}
		out = append(out, records.ModelUsage{ModelID: id, RawUsage: fields})
	}
	return out, nil
}

// grokModel is the model cell. One reported model ID is that ID with harness_reported; more
// than one reported ID is {null, mixed} and the split is retained without being counted
// again; none is {null, unknown}. primaryModelId is a REPORTED harness identifier and never a
// raw usage counter, and it must not replace the entries of a mixed-model turn.
//
// The set is the split's IDs together with primaryModelId, so a primary that does not match a
// single-model split is more than one reported ID rather than a silent winner. The mapping
// marks the mixed case unverified; mixed asserts no ID, which is the shape that claims least.
func grokModel(turn map[string]interface{}, split []records.ModelUsage) (records.Model, error) {
	reported := map[string]bool{}
	for _, e := range split {
		reported[e.ModelID] = true
	}
	if v, ok := turn["primaryModelId"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return records.Model{}, errors.New("primaryModelId is a reported harness identifier string")
		}
		if s != "" {
			reported[s] = true
		}
	}
	switch len(reported) {
	case 0:
		return records.Model{Basis: "unknown"}, nil
	case 1:
		for id := range reported {
			only := id
			return records.Model{ID: &only, Basis: "harness_reported"}, nil
		}
	}
	return records.Model{Basis: "mixed"}, nil
}

// grokOrigin is the owner binding or nothing. Friend/bench binding describes ORIGINAL
// execution: the current collector host is not historical evidence, and there is no path here
// from this process to an origin.
func grokOrigin(sessionID, turnNumber string, opts GrokOptions) (records.Origin, error) {
	if opts.Binding == nil {
		return records.Origin{Basis: "unknown"}, nil
	}
	b := opts.Binding(sessionID, turnNumber)
	if b == nil {
		return records.Origin{Basis: "unknown"}, nil
	}
	if b.Friend == "" || b.Bench == "" || b.ID == "" {
		return records.Origin{}, errors.New("an owner binding names the original friend, the original bench and a stable binding ID")
	}
	friend, bench, id := b.Friend, b.Bench, b.ID
	return records.Origin{Friend: &friend, Bench: &bench, Basis: "owner_binding", BindingID: &id}, nil
}

// ---------------------------------------------------------------- the export

// grokParse reads the export's shape and nothing else. The four declared top-level keys are
// sessionId, updatedAt, session and turns; only sessionId and turns are mapped here, and
// unknown extra fields are excluded rather than admitted into an allowlist derived from the
// input.
//
// A parse failure is reported as this fixed phrase and never by wrapping the decoder's own
// error: a source document is untrusted text, and a diagnostic is a shared file.
func grokParse(raw []byte) (string, []map[string]interface{}, error) {
	var export struct {
		SessionID string                    `json:"sessionId"`
		Turns     *[]map[string]interface{} `json:"turns"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber is load-bearing: a counter is kept as its ORIGINAL lexeme, and float64 would
	// round an integer above 2^53 and rewrite a decimal's spelling on the way to the wire.
	dec.UseNumber()
	if err := dec.Decode(&export); err != nil {
		return "", nil, errors.New("grok: the export is not a JSON object with a sessionId string and a turns array")
	}
	if export.SessionID == "" {
		return "", nil, errors.New("grok: the export carries its original native sessionId; a containing session ID is never substituted")
	}
	if export.Turns == nil {
		return "", nil, errors.New("grok: the export carries a turns array")
	}
	return export.SessionID, *export.Turns, nil
}

// grokCheckProducerVersion keeps producer_version an EXACT BOUNDED string: a version a caller
// cannot bound is not a version this wire carries, and a control character in one would render
// a refusal as two lines.
func grokCheckProducerVersion(pv *string) error {
	if pv == nil {
		return nil
	}
	s := *pv
	if s == "" || len(s) > grokProducerVersionMax || !utf8.ValidString(s) {
		return errors.New("grok: producer_version is the exact bounded version string the producer or an owner binding supplies, or null")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			return errors.New("grok: producer_version carries no control characters")
		}
	}
	return nil
}

const grokProducerVersionMax = 64

func grokCopyString(s *string) *string {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

func grokReason(code string) *string {
	cp := code
	return &cp
}

// grokValidLexeme is the same grammar the wire enforces, applied one step earlier so an
// invalid source value becomes unavailable/parse_failed instead of being refused at the seal
// or, worse, coerced to zero. A negative count is an explicit failure and not a spelling
// problem, which is why the sign is checked on its own.
func grokValidLexeme(lexeme, numberKind string) bool {
	if strings.HasPrefix(lexeme, "-") {
		return false
	}
	switch numberKind {
	case "integer":
		return grokIsInteger(lexeme)
	case "decimal":
		return grokIsDecimal(lexeme)
	}
	return false
}

// The three lexeme grammars, HAND-WRITTEN. internal/tokens keeps exactly two files that
// compile a pattern -- repo.go's one attribution rule and bus.go's note grammar -- because the
// prototype had two repo tables in two scripts and they disagreed about three repos, and
// cmd/nova-tokens' TestOnlyRepoGoCarriesTheAttributionRule holds that line for every other
// file in the package. These are scanners over bytes instead. They accept exactly what
// internal/records enforces at the seal, which is the property that matters: a lexeme this
// file calls valid and the wire refuses would be a record refused after its turn had already
// been classified as present.

// grokIsInteger: `0` or [1-9][0-9]*. `007`, `1.0`, `1e3`, `+1`, `-1` and "" are all invalid
// rather than trimmed into shape.
func grokIsInteger(s string) bool {
	if s == "0" {
		return true
	}
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// grokIsDecimal: (0|[1-9][0-9]*)(.[0-9]+)?([eE][-+]?[0-9]+)? -- RFC 8259's number grammar
// without the sign this format has no evidence for. A cost tick keeps its own spelling, so
// the check never rewrites one.
func grokIsDecimal(s string) bool {
	i := 0
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

// grokIsContentID: `sha256:` and 64 LOWERCASE hex digits. Uppercase is refused rather than
// folded, because two spellings of one digest are two identities.
func grokIsContentID(s string) bool {
	const prefix = "sha256:"
	if len(s) != len(prefix)+64 || !strings.HasPrefix(s, prefix) {
		return false
	}
	for i := len(prefix); i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

package tokens

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The retained-token decoder for Antigravity step-generator turns:
// Joins SQLite gen_metadata proto blobs with transcript.jsonl lines into sealed
// nova.tokens.observation/2 envelopes under docs/MAPPING-TOKENS-ANTIGRAVITY.md.

const (
	AntigravitySourceKind      = "antigravity"
	AntigravityNamespace       = "antigravity"
	AntigravityObservationKind = "turn"

	antigravityTimeBasisResponse = "response_observation"
	antigravityTimeBasisUnknown  = "unknown"

	antigravityOriginBound   = "owner_binding"
	antigravityOriginUnknown = "unknown"

	antigravityRepoBound        = "source_binding"
	antigravityRepoUnattributed = "unattributed"

	antigravityModelBasisHarness = "harness_reported"
	antigravityModelBasisUnknown = "unknown"

	antigravityRevisionBasis = "source_order"

	antigravityReasonOmitted = "omitted_from_wire"
	antigravityReasonInvalid = "parse_failed"
)

var (
	// ErrAntigravityNormalizedSpendUnsupported is returned when normalized spend is requested
	// from an Antigravity turn key, preserving the unverified inclusion semantics gap.
	ErrAntigravityNormalizedSpendUnsupported = errors.New("tokens: normalized spend from an Antigravity turn key is unsupported: cache and thinking inclusion semantics are unverified, so raw retention proceeds and the gap is reported")
)

// antigravityIsContentID checks `sha256:` and 64 lowercase hex digits without compiling a regexp.
func antigravityIsContentID(s string) bool {
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

// AntigravityProtoUsage represents decoded ModelUsageStats from Proto3 wire bytes.
type AntigravityProtoUsage struct {
	Model                string
	InputTokens          *uint64
	CacheReadTokens      *uint64
	OutputTokens         *uint64
	TotalTokens          *uint64 // Uninterpreted producer total (preserved provisionally outside spend)
	ThinkingOutputTokens *uint64
}

// decodeVarint reads a varint from wire bytes starting at offset.
// Protects against 64-bit overflow and truncation.
func decodeVarint(data []byte, offset int) (uint64, int, error) {
	var val uint64
	var shift uint
	for i := offset; i < len(data); i++ {
		b := data[i]
		if shift == 63 {
			// 10th byte: in proto3 uint64 varint, 63 bits are already set.
			// The 10th byte cannot have continuation bit and cannot exceed 1.
			if b > 1 {
				return 0, 0, fmt.Errorf("varint 64-bit overflow")
			}
			val |= uint64(b) << shift
			return val, i + 1, nil
		}
		val |= uint64(b&0x7F) << shift
		if (b & 0x80) == 0 {
			return val, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("truncated varint")
}

// DecodeAntigravityProto walks CortexStepGeneratorMetadata -> ChatModelMetadata -> ModelUsageStats.
func DecodeAntigravityProto(blob []byte) (*AntigravityProtoUsage, error) {
	usage := &AntigravityProtoUsage{}
	offset := 0

	for offset < len(blob) {
		tagKey, nextOffset, err := decodeVarint(blob, offset)
		if err != nil {
			return nil, err
		}
		offset = nextOffset
		fieldNum := int(tagKey >> 3)
		wireType := int(tagKey & 0x07)

		if wireType == 2 { // Length-delimited
			length, nextOffset, err := decodeVarint(blob, offset)
			if err != nil {
				return nil, err
			}
			offset = nextOffset
			if length > uint64(len(blob)-offset) {
				return nil, fmt.Errorf("length exceeds payload bounds")
			}
			subBytes := blob[offset : offset+int(length)]
			offset += int(length)

			if fieldNum == 1 { // ChatModelMetadata
				if err := decodeChatModelMetadata(subBytes, usage); err != nil {
					return nil, err
				}
			}
		} else if wireType == 0 { // Varint
			_, nextOffset, err := decodeVarint(blob, offset)
			if err != nil {
				return nil, err
			}
			offset = nextOffset
		} else {
			return nil, fmt.Errorf("unsupported wire type: %d", wireType)
		}
	}

	return usage, nil
}

func decodeChatModelMetadata(blob []byte, usage *AntigravityProtoUsage) error {
	offset := 0
	for offset < len(blob) {
		tagKey, nextOffset, err := decodeVarint(blob, offset)
		if err != nil {
			return err
		}
		offset = nextOffset
		fieldNum := int(tagKey >> 3)
		wireType := int(tagKey & 0x07)

		if wireType == 2 {
			length, nextOffset, err := decodeVarint(blob, offset)
			if err != nil {
				return err
			}
			offset = nextOffset
			if length > uint64(len(blob)-offset) {
				return fmt.Errorf("sub-length exceeds payload bounds")
			}
			subBytes := blob[offset : offset+int(length)]
			offset += int(length)

			if fieldNum == 4 { // ModelUsageStats
				if err := decodeModelUsageStats(subBytes, usage); err != nil {
					return err
				}
			} else if fieldNum == 19 { // response_model
				usage.Model = string(subBytes)
			}
		} else if wireType == 0 {
			_, nextOffset, err := decodeVarint(blob, offset)
			if err != nil {
				return err
			}
			offset = nextOffset
		} else {
			return fmt.Errorf("unsupported sub-wire type: %d", wireType)
		}
	}
	return nil
}

func decodeModelUsageStats(blob []byte, usage *AntigravityProtoUsage) error {
	offset := 0
	for offset < len(blob) {
		tagKey, nextOffset, err := decodeVarint(blob, offset)
		if err != nil {
			return err
		}
		offset = nextOffset
		fieldNum := int(tagKey >> 3)
		wireType := int(tagKey & 0x07)

		if wireType == 0 {
			val, nextOffset, err := decodeVarint(blob, offset)
			if err != nil {
				return err
			}
			offset = nextOffset

			v := val
			switch fieldNum {
			case 1:
				usage.InputTokens = &v
			case 2:
				usage.CacheReadTokens = &v
			case 3:
				usage.OutputTokens = &v
			case 5:
				usage.TotalTokens = &v
			case 6:
				usage.ThinkingOutputTokens = &v
			}
		} else {
			return fmt.Errorf("unexpected wire type in ModelUsageStats: %d", wireType)
		}
	}
	return nil
}

// AntigravityRawFields is the closed five-field vocabulary for Antigravity raw_usage.
var AntigravityRawFields = []string{
	"cache_read_tokens",
	"input_tokens",
	"output_tokens",
	"thinking_output_tokens",
	"total_tokens",
}

// AntigravityReceiptFields is the receipt allowlist: native SQLite step index idx.
var AntigravityReceiptFields = []string{"idx"}

// AntigravityAllowlists returns the closed records allowlist for Antigravity.
func AntigravityAllowlists() records.Allowlists {
	raw := append([]string(nil), AntigravityRawFields...)
	sort.Strings(raw)
	return records.Allowlists{
		RawUsageFields: raw,
		ReceiptFields:  append([]string(nil), AntigravityReceiptFields...),
	}
}

// AntigravitySQLiteRow is one row from gen_metadata.
type AntigravitySQLiteRow struct {
	Idx     int    `json:"idx"`
	Size    int    `json:"size"`
	Data    []byte `json:"-"`
	DataHex string `json:"data_hex,omitempty"`
}

// AntigravityTranscriptLine is one line from transcript.jsonl.
type AntigravityTranscriptLine struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Status    string `json:"status"`
}

// AntigravityBinding is the owner's explicit execution-origin statement.
type AntigravityBinding struct {
	Friend string
	Bench  string
	ID     string
}

// Supplied reports whether friend and bench were named.
func (b AntigravityBinding) Supplied() bool {
	return b.Friend != "" && b.Bench != ""
}

// AntigravityWorkspace binds the active workspace repository.
type AntigravityWorkspace struct {
	ID       string
	PolicyID *string
	Touched  []string
}

// Supplied reports whether a workspace repository was named.
func (w AntigravityWorkspace) Supplied() bool {
	return w.ID != ""
}

// AntigravityOptions holds mapping declarations.
type AntigravityOptions struct {
	MappingID       string
	ProducerVersion *string
}

// AntigravityRecord is one sealed observation and its provenance.
type AntigravityRecord struct {
	Idx         int
	EventKey    []string
	ID          string
	Envelope    []byte
	Observation records.Observation
}

// DecodeAntigravitySession joins SQLite gen_metadata rows with transcript lines and seals
// each turn into a canonical nova.tokens.observation/2 envelope.
func DecodeAntigravitySession(
	sessionID string,
	sqliteRows []AntigravitySQLiteRow,
	transcriptLines []AntigravityTranscriptLine,
	opts AntigravityOptions,
	binding AntigravityBinding,
	ws AntigravityWorkspace,
) ([]AntigravityRecord, error) {
	if sessionID == "" {
		return nil, errors.New("antigravity: session_id is required")
	}
	if !antigravityIsContentID(opts.MappingID) {
		return nil, fmt.Errorf("antigravity: mapping_id must be a canonical sha256 content ID: %q", opts.MappingID)
	}

	// 1:1 join check: duplicate transcript step_index is fatal
	transcriptMap := make(map[int]AntigravityTranscriptLine, len(transcriptLines))
	for _, tl := range transcriptLines {
		if _, exists := transcriptMap[tl.StepIndex]; exists {
			return nil, fmt.Errorf("duplicate step_index %d in transcript: 1:1 join requires unique keys", tl.StepIndex)
		}
		transcriptMap[tl.StepIndex] = tl
	}

	// 1:1 join check: duplicate sqlite idx is fatal
	seenIdx := make(map[int]bool, len(sqliteRows))
	for _, row := range sqliteRows {
		if seenIdx[row.Idx] {
			return nil, fmt.Errorf("duplicate idx %d in sqlite rows: 1:1 join requires unique keys", row.Idx)
		}
		seenIdx[row.Idx] = true
	}

	// Origin
	var origin records.Origin
	if binding.Supplied() {
		var bindingID *string
		if binding.ID != "" {
			bid := binding.ID
			bindingID = &bid
		}
		friend := binding.Friend
		bench := binding.Bench
		origin = records.Origin{
			Friend:    &friend,
			Bench:     &bench,
			Basis:     antigravityOriginBound,
			BindingID: bindingID,
		}
	} else {
		origin = records.Origin{
			Basis: antigravityOriginUnknown,
		}
	}

	// Repository
	var repo records.Repository
	if ws.Supplied() {
		wsID := ws.ID
		touched := ws.Touched
		if touched == nil {
			touched = []string{}
		}
		repo = records.Repository{
			ID:       &wsID,
			Basis:    antigravityRepoBound,
			PolicyID: ws.PolicyID,
			Touched:  touched,
		}
	} else {
		repo = records.Repository{
			Basis:   antigravityRepoUnattributed,
			Touched: []string{},
		}
	}

	v := records.NewValidator(AntigravityAllowlists())
	recordsOut := make([]AntigravityRecord, 0, len(sqliteRows))

	for _, row := range sqliteRows {
		blob := row.Data
		if len(blob) == 0 && row.DataHex != "" {
			var err error
			blob, err = hex.DecodeString(row.DataHex)
			if err != nil {
				return nil, fmt.Errorf("turn %d invalid hex payload: %w", row.Idx, err)
			}
		}
		if row.Size > 0 && len(blob) != row.Size {
			return nil, fmt.Errorf("turn %d payload size mismatch: got %d, expected %d", row.Idx, len(blob), row.Size)
		}

		usage, err := DecodeAntigravityProto(blob)
		if err != nil {
			return nil, fmt.Errorf("turn %d failed to decode proto: %w", row.Idx, err)
		}

		idxStr := strconv.Itoa(row.Idx)
		eventKey := []string{sessionID, idxStr}

		// Time
		var occurredAt *string
		var timeBasis string
		if tl, ok := transcriptMap[row.Idx]; ok {
			ts := tl.CreatedAt
			occurredAt = &ts
			timeBasis = antigravityTimeBasisResponse
		} else {
			occurredAt = nil
			timeBasis = antigravityTimeBasisUnknown
		}

		// Model
		var model records.Model
		if usage.Model != "" {
			mID := usage.Model
			model = records.Model{
				ID:    &mID,
				Basis: antigravityModelBasisHarness,
			}
		} else {
			model = records.Model{
				Basis: antigravityModelBasisUnknown,
			}
		}

		// RawUsage: closed 5 fields
		omittedReason := antigravityReasonOmitted
		makeField := func(val *uint64) records.RawField {
			if val != nil {
				s := strconv.FormatUint(*val, 10)
				return records.RawField{
					Presence:   "present",
					Value:      &s,
					NumberKind: "integer",
					Unit:       "tokens",
					Reason:     nil,
				}
			}
			return records.RawField{
				Presence:   "absent",
				Value:      nil,
				NumberKind: "integer",
				Unit:       "tokens",
				Reason:     &omittedReason,
			}
		}

		rawUsage := map[string]records.RawField{
			"input_tokens":           makeField(usage.InputTokens),
			"cache_read_tokens":      makeField(usage.CacheReadTokens),
			"output_tokens":          makeField(usage.OutputTokens),
			"total_tokens":           makeField(usage.TotalTokens),
			"thinking_output_tokens": makeField(usage.ThinkingOutputTokens),
		}

		obs := records.Observation{
			Schema: records.SchemaObservation,
			Source: records.Source{
				Kind:            AntigravitySourceKind,
				ProducerVersion: opts.ProducerVersion,
				Namespace:       AntigravityNamespace,
				SessionID:       sessionID,
				EventKey:        eventKey,
			},
			Kind: AntigravityObservationKind,
			Revision: records.Revision{
				Native:     nil,
				Supersedes: []string{},
				Basis:      antigravityRevisionBasis,
			},
			Time: records.Times{
				OccurredAt: occurredAt,
				Start:      nil,
				End:        nil,
				Basis:      timeBasis,
			},
			Origin:     origin,
			Model:      model,
			Repository: repo,
			RawUsage:   rawUsage,
			ModelUsage: []records.ModelUsage{},
			MappingID:  opts.MappingID,
			Receipt:    map[string]string{"idx": idxStr},
		}

		env, id, err := v.SealObservation(obs)
		if err != nil {
			return nil, fmt.Errorf("turn %d SealObservation failed: %w", row.Idx, err)
		}

		read, err := v.ValidateEnvelope(env)
		if err != nil {
			return nil, fmt.Errorf("turn %d ValidateEnvelope read-back failed: %w", row.Idx, err)
		}

		recordsOut = append(recordsOut, AntigravityRecord{
			Idx:         row.Idx,
			EventKey:    eventKey,
			ID:          id,
			Envelope:    env,
			Observation: *read.Observation,
		})
	}

	return recordsOut, nil
}

// DecodeAntigravityFromJSON decodes from JSON representations of sqlite_rows.json and transcript.jsonl.
func DecodeAntigravityFromJSON(
	sessionID string,
	sqliteJSON []byte,
	transcriptJSONL []byte,
	opts AntigravityOptions,
	binding AntigravityBinding,
	ws AntigravityWorkspace,
) ([]AntigravityRecord, error) {
	var rawRows []json.RawMessage
	trimmed := bytes.TrimSpace(sqliteJSON)
	if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(sqliteJSON, &rawRows) != nil {
		return nil, errors.New("antigravity: sqlite rows must be one JSON array")
	}
	sqliteRows := make([]AntigravitySQLiteRow, 0, len(rawRows))
	for _, raw := range rawRows {
		var row AntigravitySQLiteRow
		if err := antigravityJSONRow(raw, "idx", &row, "idx", "size", "data_hex"); err != nil {
			return nil, err
		}
		sqliteRows = append(sqliteRows, row)
	}

	var transcriptLines []AntigravityTranscriptLine
	for line := range bytes.Lines(transcriptJSONL) {
		lineBytes := bytes.TrimSpace(line)
		if len(lineBytes) == 0 {
			continue
		}
		var tl AntigravityTranscriptLine
		if err := antigravityJSONRow(lineBytes, "step_index", &tl,
			"step_index", "source", "type", "created_at", "status"); err != nil {
			return nil, err
		}
		transcriptLines = append(transcriptLines, tl)
	}

	return DecodeAntigravitySession(sessionID, sqliteRows, transcriptLines, opts, binding, ws)
}

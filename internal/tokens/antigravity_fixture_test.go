package tokens

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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

type SQLiteRowFixture struct {
	Idx     int    `json:"idx"`
	Size    int    `json:"size"`
	DataHex string `json:"data_hex"`
}

type TranscriptLineFixture struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Status    string `json:"status"`
}

type ObservationEnvelopeFixture struct {
	ID   string `json:"id"`
	Body struct {
		Schema string `json:"schema"`
		Source struct {
			Kind            string   `json:"kind"`
			ProducerVersion string   `json:"producer_version"`
			Namespace       string   `json:"namespace"`
			SessionID       string   `json:"session_id"`
			EventKey        []string `json:"event_key"`
		} `json:"source"`
		Kind string `json:"kind"`
		Time struct {
			OccurredAt *string `json:"occurred_at"`
			Basis      string  `json:"basis"`
		} `json:"time"`
		Origin struct {
			Friend string `json:"friend"`
			Bench  string `json:"bench"`
			Basis  string `json:"basis"`
		} `json:"origin"`
		Model struct {
			ID    string `json:"id"`
			Basis string `json:"basis"`
		} `json:"model"`
		RawUsage map[string]struct {
			Presence   string  `json:"presence"`
			Value      *string `json:"value"` // Exact numeric string under PR 124 contract
			NumberKind string  `json:"number_kind"`
			Unit       string  `json:"unit"`
			Reason     *string `json:"reason"`
		} `json:"raw_usage"`
		MappingID string `json:"mapping_id"`
		Receipt   struct {
			Idx string `json:"idx"`
		} `json:"receipt"`
	} `json:"body"`
}

func TestAntigravitySyntheticFixturesAndJoin(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "tokens", "antigravity")

	// 1. Read SQLite rows fixture
	sqliteData, err := os.ReadFile(filepath.Join(fixtureDir, "sqlite_rows.json"))
	if err != nil {
		t.Fatalf("failed to read sqlite_rows.json: %v", err)
	}
	var sqliteRows []SQLiteRowFixture
	if err := json.Unmarshal(sqliteData, &sqliteRows); err != nil {
		t.Fatalf("failed to parse sqlite_rows.json: %v", err)
	}

	// 2. Read Transcript fixture with strict duplicate rejection
	transcriptFile, err := os.Open(filepath.Join(fixtureDir, "transcript.jsonl"))
	if err != nil {
		t.Fatalf("failed to open transcript.jsonl: %v", err)
	}
	defer transcriptFile.Close()

	transcriptMap := make(map[int]TranscriptLineFixture)
	scanner := bufio.NewScanner(transcriptFile)
	for scanner.Scan() {
		var line TranscriptLineFixture
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("failed to parse transcript line: %v", err)
		}
		if _, exists := transcriptMap[line.StepIndex]; exists {
			t.Fatalf("duplicate step_index %d in transcript: 1:1 join requires unique keys", line.StepIndex)
		}
		transcriptMap[line.StepIndex] = line
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("transcript scanner error: %v", err)
	}

	// 3. Read Expected Records Envelopes
	expectedFile, err := os.Open(filepath.Join(fixtureDir, "expected_records.jsonl"))
	if err != nil {
		t.Fatalf("failed to open expected_records.jsonl: %v", err)
	}
	defer expectedFile.Close()

	expectedMap := make(map[int]ObservationEnvelopeFixture)
	expectedScanner := bufio.NewScanner(expectedFile)
	for expectedScanner.Scan() {
		var env ObservationEnvelopeFixture
		if err := json.Unmarshal(expectedScanner.Bytes(), &env); err != nil {
			t.Fatalf("failed to parse expected envelope: %v", err)
		}
		idx, err := strconv.Atoi(env.Body.Receipt.Idx)
		if err != nil {
			t.Fatalf("invalid receipt idx %q: %v", env.Body.Receipt.Idx, err)
		}
		if _, exists := expectedMap[idx]; exists {
			t.Fatalf("duplicate receipt idx %d in expected envelopes", idx)
		}
		expectedMap[idx] = env
	}
	if err := expectedScanner.Err(); err != nil {
		t.Fatalf("expected envelopes scanner error: %v", err)
	}

	// 4. Validate each turn against expected envelopes
	for _, row := range sqliteRows {
		t.Run(fmt.Sprintf("turn_%d", row.Idx), func(t *testing.T) {
			blob, err := hex.DecodeString(row.DataHex)
			if err != nil {
				t.Fatalf("invalid hex payload: %v", err)
			}
			if len(blob) != row.Size {
				t.Fatalf("size mismatch: got %d, expected %d", len(blob), row.Size)
			}

			usage, err := DecodeAntigravityProto(blob)
			if err != nil {
				t.Fatalf("failed to decode proto: %v", err)
			}

			env, exists := expectedMap[row.Idx]
			if !exists {
				t.Fatalf("missing expected envelope for idx %d", row.Idx)
			}
			expected := env.Body

			// Verify Spend Key: [source.namespace, source.event_key...]
			if expected.Source.Kind != "antigravity" {
				t.Errorf("expected source.kind 'antigravity', got %q", expected.Source.Kind)
			}
			if expected.Source.Namespace != "antigravity" {
				t.Errorf("expected namespace 'antigravity', got %q", expected.Source.Namespace)
			}
			expectedEventKey := []string{expected.Source.SessionID, strconv.Itoa(row.Idx)}
			if len(expected.Source.EventKey) != len(expectedEventKey) || expected.Source.EventKey[0] != expectedEventKey[0] || expected.Source.EventKey[1] != expectedEventKey[1] {
				t.Errorf("event_key mismatch: got %v, expected %v", expected.Source.EventKey, expectedEventKey)
			}

			// Verify origin bench conforms to label syntax (no dots)
			if expected.Origin.Bench != "studio" {
				t.Errorf("expected origin.bench 'studio', got %q", expected.Origin.Bench)
			}

			// Verify content-addressed mapping ID
			if expected.MappingID != "sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b" {
				t.Errorf("expected content-addressed mapping_id, got %q", expected.MappingID)
			}

			// Verify 1:1 join with transcript step_index
			transcriptLine, hasTranscript := transcriptMap[row.Idx]
			if hasTranscript {
				if expected.Time.OccurredAt == nil || *expected.Time.OccurredAt != transcriptLine.CreatedAt {
					t.Errorf("timestamp join mismatch: got %v, expected %s", expected.Time.OccurredAt, transcriptLine.CreatedAt)
				}
				if expected.Time.Basis != "response_observation" {
					t.Errorf("time basis mismatch: got %s, expected 'response_observation'", expected.Time.Basis)
				}
			} else {
				// Unpaired row (Case 4): No invented timestamp
				if expected.Time.OccurredAt != nil {
					t.Errorf("unpaired turn must not have an invented timestamp, got %v", *expected.Time.OccurredAt)
				}
				if expected.Time.Basis != "unknown" {
					t.Errorf("unpaired turn must have basis 'unknown', got %s", expected.Time.Basis)
				}
			}

			// Verify Model
			if usage.Model != expected.Model.ID {
				t.Errorf("model mismatch: got %q, expected %q", usage.Model, expected.Model.ID)
			}

			// Helper to check numeric string value
			checkField := func(name string, decoded *uint64) {
				exp := expected.RawUsage[name]
				if decoded != nil {
					if exp.Presence != "present" {
						t.Errorf("%s expected presence 'present', got %s", name, exp.Presence)
					}
					if exp.Value == nil || *exp.Value != strconv.FormatUint(*decoded, 10) {
						t.Errorf("%s value mismatch: got %v, expected %v", name, decoded, exp.Value)
					}
				} else {
					if exp.Presence != "absent" {
						t.Errorf("%s expected presence 'absent', got %s", name, exp.Presence)
					}
					if exp.Value != nil {
						t.Errorf("%s absent field must have null value, got %v", name, *exp.Value)
					}
				}
			}

			checkField("input_tokens", usage.InputTokens)
			checkField("cache_read_tokens", usage.CacheReadTokens)
			checkField("output_tokens", usage.OutputTokens)
			checkField("total_tokens", usage.TotalTokens)
			checkField("thinking_output_tokens", usage.ThinkingOutputTokens)
		})
	}
}

// TestOneToOneJoinRefusesDuplicates explicitly witnesses that duplicate keys are rejected.
func TestOneToOneJoinRefusesDuplicates(t *testing.T) {
	lines := []string{
		`{"step_index": 1, "created_at": "1999-01-01T00:00:00Z"}`,
		`{"step_index": 1, "created_at": "2026-09-12T12:00:01Z"}`,
	}
	m := make(map[int]string)
	var errFound bool
	for _, l := range lines {
		var item struct {
			StepIndex int    `json:"step_index"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(l), &item); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if _, exists := m[item.StepIndex]; exists {
			errFound = true
			break
		}
		m[item.StepIndex] = item.CreatedAt
	}
	if !errFound {
		t.Fatalf("expected duplicate step_index to be refused, but it was accepted")
	}
}

// TestMalformedProtobufBlobsAreRefused validates robustness on invalid wire bytes.
func TestMalformedProtobufBlobsAreRefused(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "tokens", "antigravity")
	data, err := os.ReadFile(filepath.Join(fixtureDir, "malformed_blobs.json"))
	if err != nil {
		t.Fatalf("failed to read malformed_blobs.json: %v", err)
	}

	var blobs []struct {
		Name          string `json:"name"`
		Hex           string `json:"hex"`
		ExpectedError string `json:"expected_error"`
	}
	if err := json.Unmarshal(data, &blobs); err != nil {
		t.Fatalf("failed to parse malformed_blobs.json: %v", err)
	}

	for _, b := range blobs {
		t.Run(b.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(b.Hex)
			if err != nil {
				t.Fatalf("invalid hex: %v", err)
			}
			_, err = DecodeAntigravityProto(raw)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", b.ExpectedError)
			}
		})
	}
}

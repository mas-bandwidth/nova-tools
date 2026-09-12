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
	Model               string
	InputTokens         *uint64
	CacheReadTokens     *uint64
	OutputTokens        *uint64
	TotalTokens         *uint64 // Context window total (non-spend)
	ThinkingOutputTokens *uint64
}

// DecodeVarint reads a varint from wire bytes starting at offset.
func decodeVarint(data []byte, offset int) (uint64, int, error) {
	var val uint64
	var shift uint
	for i := offset; i < len(data); i++ {
		b := data[i]
		val |= uint64(b&0x7F) << shift
		if (b & 0x80) == 0 {
			return val, i + 1, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
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
			if offset+int(length) > len(blob) {
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
			if offset+int(length) > len(blob) {
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

type ObservationRecordFixture struct {
	Schema string `json:"schema"`
	Source struct {
		Kind            string `json:"kind"`
		ProducerVersion string `json:"producer_version"`
		Namespace       string `json:"namespace"`
		SessionID       string `json:"session_id"`
		EventKey        string `json:"event_key"`
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
		Value      *uint64 `json:"value"`
		NumberKind string  `json:"number_kind"`
		Unit       string  `json:"unit"`
		Reason     *string `json:"reason"`
	} `json:"raw_usage"`
	Receipt struct {
		Idx int `json:"idx"`
	} `json:"receipt"`
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

	// 2. Read Transcript fixture
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
		transcriptMap[line.StepIndex] = line
	}

	// 3. Read Expected Records
	expectedFile, err := os.Open(filepath.Join(fixtureDir, "expected_records.jsonl"))
	if err != nil {
		t.Fatalf("failed to open expected_records.jsonl: %v", err)
	}
	defer expectedFile.Close()

	expectedMap := make(map[int]ObservationRecordFixture)
	expectedScanner := bufio.NewScanner(expectedFile)
	for expectedScanner.Scan() {
		var rec ObservationRecordFixture
		if err := json.Unmarshal(expectedScanner.Bytes(), &rec); err != nil {
			t.Fatalf("failed to parse expected record: %v", err)
		}
		expectedMap[rec.Receipt.Idx] = rec
	}

	// 4. Validate each turn against expected records
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

			expected, exists := expectedMap[row.Idx]
			if !exists {
				t.Fatalf("missing expected record for idx %d", row.Idx)
			}

			// Verify Spend Key: ["antigravity", session_id, event_key]
			if expected.Source.Namespace != "antigravity" {
				t.Errorf("expected namespace 'antigravity', got %q", expected.Source.Namespace)
			}
			if expected.Source.EventKey != strconv.Itoa(row.Idx) {
				t.Errorf("event_key mismatch: got %q, expected %d", expected.Source.EventKey, row.Idx)
			}

			// Verify 1:1 join with transcript step_index
			transcriptLine, hasTranscript := transcriptMap[row.Idx]
			if hasTranscript {
				if expected.Time.OccurredAt == nil || *expected.Time.OccurredAt != transcriptLine.CreatedAt {
					t.Errorf("timestamp join mismatch: got %v, expected %s", expected.Time.OccurredAt, transcriptLine.CreatedAt)
				}
				if expected.Time.Basis != "turn_completion" {
					t.Errorf("time basis mismatch: got %s, expected 'turn_completion'", expected.Time.Basis)
				}
			} else {
				// Unpaired row (Case 4)
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

			// Verify Presence Semantics for Wire Tags
			// Tag 1 (input_tokens)
			expInput := expected.RawUsage["input_tokens"]
			if usage.InputTokens != nil {
				if expInput.Presence != "present" || *usage.InputTokens != *expInput.Value {
					t.Errorf("input_tokens mismatch: got %v, expected %v", usage.InputTokens, expInput.Value)
				}
			} else {
				if expInput.Presence != "absent" {
					t.Errorf("input_tokens expected absent, got %v", usage.InputTokens)
				}
			}

			// Tag 2 (cache_read_tokens)
			expCache := expected.RawUsage["cache_read_tokens"]
			if usage.CacheReadTokens != nil {
				if expCache.Presence != "present" || *usage.CacheReadTokens != *expCache.Value {
					t.Errorf("cache_read_tokens mismatch: got %v, expected %v", usage.CacheReadTokens, expCache.Value)
				}
			} else {
				if expCache.Presence != "absent" {
					t.Errorf("cache_read_tokens expected absent on wire, got %v", usage.CacheReadTokens)
				}
			}

			// Tag 3 (output_tokens)
			expOutput := expected.RawUsage["output_tokens"]
			if usage.OutputTokens != nil {
				if expOutput.Presence != "present" || *usage.OutputTokens != *expOutput.Value {
					t.Errorf("output_tokens mismatch: got %v, expected %v", usage.OutputTokens, expOutput.Value)
				}
			} else {
				if expOutput.Presence != "absent" {
					t.Errorf("output_tokens expected absent on wire, got %v", usage.OutputTokens)
				}
			}

			// Tag 5 (total_tokens - context window evidence, non-spend)
			expTotal := expected.RawUsage["total_tokens"]
			if usage.TotalTokens != nil {
				if expTotal.Presence != "present" || *usage.TotalTokens != *expTotal.Value {
					t.Errorf("total_tokens mismatch: got %v, expected %v", usage.TotalTokens, expTotal.Value)
				}
			} else {
				if expTotal.Presence != "absent" {
					t.Errorf("total_tokens expected absent on wire, got %v", usage.TotalTokens)
				}
			}

			// Tag 6 (thinking_output_tokens - subset of output tokens)
			expThinking := expected.RawUsage["thinking_output_tokens"]
			if usage.ThinkingOutputTokens != nil {
				if expThinking.Presence != "present" || *usage.ThinkingOutputTokens != *expThinking.Value {
					t.Errorf("thinking_output_tokens mismatch: got %v, expected %v", usage.ThinkingOutputTokens, expThinking.Value)
				}
				// Special check for Case 3 (Present-zero): value must be 0 and presence "present"
				if *usage.ThinkingOutputTokens == 0 && expThinking.Presence != "present" {
					t.Errorf("explicit wire zero must have presence 'present'")
				}
			} else {
				if expThinking.Presence != "absent" {
					t.Errorf("thinking_output_tokens expected absent on wire, got %v", usage.ThinkingOutputTokens)
				}
			}

			// Invariant Check: Context total (Tag 5) is non-spend.
			// Spend = input_tokens + output_tokens.
			if usage.InputTokens != nil && usage.OutputTokens != nil {
				spend := *usage.InputTokens + *usage.OutputTokens
				if usage.TotalTokens != nil && *usage.TotalTokens > 0 {
					if spend == *usage.TotalTokens || spend+*usage.TotalTokens == spend {
						t.Errorf("context window size must not be confused with spend")
					}
				}
			}
		})
	}
}

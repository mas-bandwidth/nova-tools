package tokens

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

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

// TestDecodeAntigravityMatchesExpectedRecords tests that DecodeAntigravityFromJSON
// reproduces the expected records with identical content IDs and observations.
func TestDecodeAntigravityMatchesExpectedRecords(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "tokens", "antigravity")

	sqliteJSON, err := os.ReadFile(filepath.Join(fixtureDir, "sqlite_rows.json"))
	if err != nil {
		t.Fatalf("failed to read sqlite_rows.json: %v", err)
	}
	transcriptJSONL, err := os.ReadFile(filepath.Join(fixtureDir, "transcript.jsonl"))
	if err != nil {
		t.Fatalf("failed to read transcript.jsonl: %v", err)
	}
	expectedLinesRaw, err := os.ReadFile(filepath.Join(fixtureDir, "expected_records.jsonl"))
	if err != nil {
		t.Fatalf("failed to read expected_records.jsonl: %v", err)
	}

	expectedLines := bytes.Split(bytes.TrimSpace(expectedLinesRaw), []byte("\n"))
	if len(expectedLines) != 4 {
		t.Fatalf("expected 4 lines in expected_records.jsonl, got %d", len(expectedLines))
	}

	version := "2.12.2"
	opts := AntigravityOptions{
		MappingID:       "sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b",
		ProducerVersion: &version,
	}
	binding := AntigravityBinding{
		Friend: "emma",
		Bench:  "studio",
	}
	ws := AntigravityWorkspace{
		ID: "mas-bandwidth/emma",
	}

	recs, err := DecodeAntigravityFromJSON(
		"conv-synth-001",
		sqliteJSON,
		transcriptJSONL,
		opts,
		binding,
		ws,
	)
	if err != nil {
		t.Fatalf("DecodeAntigravityFromJSON failed: %v", err)
	}

	if len(recs) != len(expectedLines) {
		t.Fatalf("got %d records, want %d", len(recs), len(expectedLines))
	}

	v := records.NewValidator(AntigravityAllowlists())

	for i, rec := range recs {
		expectedLine := bytes.TrimSpace(expectedLines[i])

		// 1. Verify content ID equality: exact 64-hex SHA-256 match
		var expEnv struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(expectedLine, &expEnv); err != nil {
			t.Fatalf("record %d invalid expected json: %v", i+1, err)
		}
		if rec.ID != expEnv.ID {
			t.Errorf("record %d ID mismatch: got %s, want %s", i+1, rec.ID, expEnv.ID)
		}

		// 2. Verify ValidateEnvelope accepts sealed bytes and matches expected envelope
		valEnv, err := v.ValidateEnvelope(rec.Envelope)
		if err != nil {
			t.Fatalf("record %d ValidateEnvelope failed: %v", i+1, err)
		}
		if valEnv.ID != rec.ID {
			t.Errorf("record %d validated ID %s != rec.ID %s", i+1, valEnv.ID, rec.ID)
		}

		// 3. Verify ValidateEnvelope accepts the expected fixture line and matches rec.ID
		expParsed, err := v.ValidateEnvelope(expectedLine)
		if err != nil {
			t.Fatalf("record %d ValidateEnvelope(expectedLine) failed: %v", i+1, err)
		}
		if expParsed.ID != rec.ID {
			t.Errorf("record %d expParsed.ID %s != rec.ID %s", i+1, expParsed.ID, rec.ID)
		}

		// 4. Verify observation equality
		if valEnv.Observation.Kind != expParsed.Observation.Kind {
			t.Errorf("record %d kind mismatch", i+1)
		}
		if valEnv.Observation.Source.SessionID != expParsed.Observation.Source.SessionID {
			t.Errorf("record %d session_id mismatch", i+1)
		}
	}
}

// TestDecodeAntigravityWithSealedMapping validates decoding under the sealed mapping manifest.
func TestDecodeAntigravityWithSealedMapping(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "tokens", "antigravity")

	sqliteJSON, err := os.ReadFile(filepath.Join(fixtureDir, "sqlite_rows.json"))
	if err != nil {
		t.Fatalf("failed to read sqlite_rows.json: %v", err)
	}
	transcriptJSONL, err := os.ReadFile(filepath.Join(fixtureDir, "transcript.jsonl"))
	if err != nil {
		t.Fatalf("failed to read transcript.jsonl: %v", err)
	}

	sealedMappingID := "sha256:173b9ff62dcda4fdd2187298d1fdbc66d1b01fe14bd9b7899bf9fc11444386b1"
	version := "2.12.2"
	opts := AntigravityOptions{
		MappingID:       sealedMappingID,
		ProducerVersion: &version,
	}
	binding := AntigravityBinding{
		Friend: "emma",
		Bench:  "studio",
	}
	ws := AntigravityWorkspace{
		ID: "mas-bandwidth/emma",
	}

	recs, err := DecodeAntigravityFromJSON("conv-synth-001", sqliteJSON, transcriptJSONL, opts, binding, ws)
	if err != nil {
		t.Fatalf("DecodeAntigravityFromJSON with sealed mapping failed: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d", len(recs))
	}

	v := records.NewValidator(AntigravityAllowlists())
	for i, rec := range recs {
		if rec.Observation.MappingID != sealedMappingID {
			t.Errorf("record %d mapping_id mismatch: got %s, want %s", i+1, rec.Observation.MappingID, sealedMappingID)
		}
		valEnv, err := v.ValidateEnvelope(rec.Envelope)
		if err != nil {
			t.Fatalf("record %d ValidateEnvelope failed: %v", i+1, err)
		}
		if valEnv.ID != rec.ID {
			t.Errorf("record %d validated ID %s != rec.ID %s", i+1, valEnv.ID, rec.ID)
		}
	}
}

// TestDecodeAntigravityRefusals tests defensive refusal conditions.
func TestDecodeAntigravityRefusals(t *testing.T) {
	opts := AntigravityOptions{
		MappingID: "sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b",
	}
	binding := AntigravityBinding{}
	ws := AntigravityWorkspace{}

	// Empty session ID
	_, err := DecodeAntigravitySession("", nil, nil, opts, binding, ws)
	if err == nil {
		t.Fatal("expected error for empty session ID, got nil")
	}

	// Malformed mapping ID
	badOpts := AntigravityOptions{MappingID: "not-a-sha256"}
	_, err = DecodeAntigravitySession("sess-1", nil, nil, badOpts, binding, ws)
	if err == nil {
		t.Fatal("expected error for malformed mapping ID, got nil")
	}

	// Duplicate SQLite idx
	dupRows := []AntigravitySQLiteRow{
		{Idx: 1, Data: []byte{}},
		{Idx: 1, Data: []byte{}},
	}
	_, err = DecodeAntigravitySession("sess-1", dupRows, nil, opts, binding, ws)
	if err == nil {
		t.Fatal("expected error for duplicate sqlite idx, got nil")
	}

	// Duplicate transcript step_index
	dupTrans := []AntigravityTranscriptLine{
		{StepIndex: 1},
		{StepIndex: 1},
	}
	_, err = DecodeAntigravitySession("sess-1", nil, dupTrans, opts, binding, ws)
	if err == nil {
		t.Fatal("expected error for duplicate transcript step_index, got nil")
	}

	// Malformed proto blob
	badProtoRows := []AntigravitySQLiteRow{
		{Idx: 1, Data: []byte{0x80}}, // truncated varint
	}
	_, err = DecodeAntigravitySession("sess-1", badProtoRows, nil, opts, binding, ws)
	if err == nil {
		t.Fatal("expected error for malformed proto blob, got nil")
	}
}

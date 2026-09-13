package tokens

import (
	"strings"
	"testing"
)

const identityMapping = "sha256:173b9ff62dcda4fdd2187298d1fdbc66d1b01fe14bd9b7899bf9fc11444386b1"

func decodeIdentityJSON(sqlite, transcript string) ([]AntigravityRecord, error) {
	return DecodeAntigravityFromJSON("native-session", []byte(sqlite), []byte(transcript),
		AntigravityOptions{MappingID: identityMapping}, AntigravityBinding{}, AntigravityWorkspace{})
}

func TestAntigravityJSONRequiresExplicitNativeIdentity(t *testing.T) {
	for name, src := range map[string]string{
		"null_export":           `null`,
		"non_array":             `{}`,
		"missing_idx":           `[{"size":6,"data_hex":"0a0422020801"}]`,
		"null_idx":              `[{"idx":null,"size":6,"data_hex":"0a0422020801"}]`,
		"null_row":              `[null]`,
		"non_object_row":        `[0]`,
		"bool_idx":              `[{"idx":false}]`,
		"string_idx":            `[{"idx":"0"}]`,
		"fraction_idx":          `[{"idx":0.5}]`,
		"exponent_idx":          `[{"idx":1e0}]`,
		"overflow_idx":          `[{"idx":18446744073709551616}]`,
		"alias_only":            `[{"IDX":0}]`,
		"duplicate_idx":         `[{"idx":0,"idx":1}]`,
		"escaped_duplicate_idx": `[{"idx":0,"\u0069dx":1}]`,
		"duplicate_size":        `[{"idx":0,"size":0,"size":1}]`,
		"duplicate_hex":         `[{"idx":0,"data_hex":"","data_hex":"00"}]`,
		"trailing_document":     `[{"idx":0}] []`,
		"partial_export":        `[{"idx":0,"size":6,"data_hex":"0a0422020801"},{"idx":null}]`,
		"private_scalar":        `[{"idx":0,"size":"PRIVATE-SENTINEL"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := decodeIdentityJSON(src, "")
			if err == nil || len(out) != 0 {
				t.Fatalf("invalid native identity must refuse without observations: count=%d err=%v", len(out), err)
			}
			if strings.Contains(err.Error(), "PRIVATE-SENTINEL") {
				t.Fatal("source value leaked into diagnostic")
			}
		})
	}
}

func TestAntigravityJSONCannotInventTranscriptJoin(t *testing.T) {
	const sqlite = `[{"idx":0,"size":6,"data_hex":"0a0422020801"}]`
	for name, line := range map[string]string{
		"missing_step":           `{"created_at":"2026-09-12T12:00:00Z"}`,
		"null_step":              `{"step_index":null,"created_at":"2026-09-12T12:00:00Z"}`,
		"null_row":               `null`,
		"alias_only":             `{"STEP_INDEX":0,"created_at":"2026-09-12T12:00:00Z"}`,
		"string_step":            `{"step_index":"0"}`,
		"duplicate_step":         `{"step_index":0,"step_index":1}`,
		"escaped_duplicate_step": `{"step_index":0,"step_\u0069ndex":1}`,
		"duplicate_time":         `{"step_index":0,"created_at":"2026-09-12T12:00:00Z","created_at":"2026-09-13T12:00:00Z"}`,
		"trailing_document":      `{"step_index":0} {}`,
		"private_scalar":         `{"step_index":0,"created_at":{"PRIVATE-SENTINEL":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := decodeIdentityJSON(sqlite, line)
			if err == nil || len(out) != 0 {
				t.Fatalf("invalid native join must refuse without observations: count=%d err=%v", len(out), err)
			}
			if strings.Contains(err.Error(), "PRIVATE-SENTINEL") {
				t.Fatal("source value leaked into diagnostic")
			}
		})
	}
}

func TestAntigravityJSONNativeZeroAndUnknownMaterial(t *testing.T) {
	const sqlite = `[{"idx":0,"IDX":9,"size":6,"SIZE":1,"data_hex":"0a0422020801","DATA_HEX":"invalid","private":{"idx":3,"idx":4,"text":"PRIVATE-SENTINEL"}}]`
	for name, transcript := range map[string]string{
		"unpaired": "",
		"paired":   `{"step_index":0,"STEP_INDEX":9,"created_at":"2026-09-12T12:00:00Z","CREATED_AT":"2026-09-13T12:00:00Z","text":"PRIVATE-SENTINEL"}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := decodeIdentityJSON(sqlite, transcript)
			if err != nil || len(out) != 1 {
				t.Fatalf("explicit native zero must remain valid: count=%d err=%v", len(out), err)
			}
			if len(out[0].EventKey) != 2 || out[0].EventKey[0] != "native-session" || out[0].EventKey[1] != "0" {
				t.Fatalf("native identity changed: %v", out[0].EventKey)
			}
			at := out[0].Observation.Time.OccurredAt
			if name == "unpaired" && at != nil {
				t.Fatalf("unpaired row invented a timestamp: %s", *at)
			}
			if name == "paired" && (at == nil || *at != "2026-09-12T12:00:00Z") {
				t.Fatalf("native timestamp join changed: %v", at)
			}
			if strings.Contains(string(out[0].Envelope), "PRIVATE-SENTINEL") {
				t.Fatal("unknown source material entered retained output")
			}
		})
	}
	if out, err := decodeIdentityJSON("[]", ""); err != nil || len(out) != 0 {
		t.Fatalf("empty array should remain an empty source: count=%d err=%v", len(out), err)
	}
}

// The API already receives the source bytes. Ignored transcript content must not
// impose bufio.Scanner's unrelated 64KiB default on valid native metadata.
func TestAntigravityJSONLargeIgnoredTranscriptField(t *testing.T) {
	const sqlite = `[{"idx":0,"size":6,"data_hex":"0a0422020801"}]`
	line := `{"step_index":0,"created_at":"2026-09-12T12:00:00Z","text":"` + strings.Repeat("private-text-", 8192) + `"}`
	out, err := decodeIdentityJSON(sqlite, line)
	if err != nil || len(out) != 1 || out[0].Observation.Time.OccurredAt == nil {
		t.Fatalf("valid metadata alongside ignored content must retain its native join: count=%d err=%v", len(out), err)
	}
	if strings.Contains(string(out[0].Envelope), "private-text-") {
		t.Fatal("ignored transcript content entered retained output")
	}
}
